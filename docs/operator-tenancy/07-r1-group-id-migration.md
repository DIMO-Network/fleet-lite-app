# R1 — fleet-group id migration, execution plan

Turning `fleet_groups.id` from a bare `slug(name)` into `<tenant-uuid>_<slug>`
in both `fleet-lite-app` and `kaufmann-oracle`.

Blocks Phase 2. Also fixes a bug that is live today. See
[05-risks-and-open-questions.md](05-risks-and-open-questions.md) R1 for why, and
[02-target-architecture.md](02-target-architecture.md#fleet-group-ids-carry-their-tenant)
for the format rationale.

## The acceptance rule

**A group CloudEvent is accepted if its id prefix matches the tenant currently
being viewed. Otherwise it's dropped.**

That's the whole rule. Note what it is *not*: it says nothing about which app
produced the CE. Attribution is by **tenant**, not by producer — which is why it
keeps working as the system changes:

- **Viewing a customer tenant** → only that customer's groups. Operator groups
  never appear on a customer's fleet, matching D3/Q9 (the operator assigns
  vehicles, the customer owns their groups).
- **Viewing an operator tenant with `fleet_lite_enabled`** → groups created in
  b2b appear, because they carry that same operator tenant's prefix. Cross-app
  group visibility falls out for free.
- **Future: groups viewable from both tools** → same rule, no change.

### It switches on by itself when tenant ids unify

Today kaufmann and fleet-lite have separate `tenants` tables with unrelated
uuids, so no kaufmann-produced group can ever match a fleet-lite tenant prefix —
cross-app flow is effectively **off**.

Once Phase 0's backfill reuses the existing UUIDs
([04-migration-plan.md](04-migration-plan.md)), the operator's tenant id is the
same on both sides. At that point b2b-created groups start appearing in the
operator's fleet-lite view **with no code change** — the rule already says yes.

Worth writing down because it looks like a gap now and is actually a
dependency that resolves itself.

## The regression this creates, and why it changes the rollout

fleet-lite currently accepts kaufmann's groups *by accident*: `desiredGroups`
unions every producer's CEs and `ensureGroup` auto-creates unrecognised ids. So
some tenants may have groups and memberships that exist **only** because
kaufmann published them.

Those are about to stop being in `desired`. And `reconcile`
(`group_sync.go:224`) removes any local membership absent from `desired`
whenever `allowRemove` is open — which `removalAllowed` opens when
`groups_updated_at IS NULL`, i.e. when nobody ever edited the group locally.
**That is exactly the sync-created population.**

So a naive rollout deletes real data on the first sync after deploy.

### Measured in production (2026-08-05)

An earlier draft proposed detecting auto-created groups by their `#808080`
default colour. **That query is wrong and returns zero.** `ensureGroup` only
falls back to grey when the CloudEvent carries no name/colour — and kaufmann's
CEs carry both, so synced groups are indistinguishable from hand-made ones by
colour.

The correct test is comparing id sets across the two databases. Run against
prod, read-only:

```sql
-- fleet-lite
SELECT id FROM fleet_groups ORDER BY 1;
-- kaufmann
SELECT id FROM kaufmann_oracle.fleet_groups ORDER BY 1;
```

`comm -12` the two lists. What that actually returned:

| Measure | Value |
|---|---|
| fleet-lite groups | 82 |
| kaufmann groups | 79 |
| **ids present in both** | **76** |
| fleet-lite-only ids | 6 |
| memberships on shared-id groups | **370 of 378 (98%)** |
| grouped vehicles | 287 |
| …ever edited locally in fleet-lite (`groups_updated_at`) | **0** |
| …ever synced from a CE (`last_group_sync_at`) | **287** |

**fleet-lite's entire production group structure came from kaufmann's
CloudEvents.** Not one group has ever been mutated in fleet-lite, so
`AttestVehicleGroups` has never fired for these vehicles and fleet-lite's own
producer has published nothing. Everything on screen is kaufmann's assertions.

And because no vehicle has `groups_updated_at` set, `removalAllowed` is **open
for all 287** — there is no freshness gate standing in the way.

So enabling `dropForeign` without a republish would delete 370 of 378
memberships: the production fleet's entire group structure, on the next sync.

## The sequencing constraint this creates

The two "Kaufmann" tenants are **different uuids for the same company**:

| | tenant id |
|---|---|
| `kaufmann_oracle.tenants` "Kaufmann" | `7be1ab9e-9286-4a8f-b45f-15f25ee4da77` |
| `fleets_lite.tenants` "Kaufmann" | `9708b213-21fe-41da-bded-c3026d16b85c` |

This is precisely the collision case [04-migration-plan.md](04-migration-plan.md)
flagged ("a company that exists as a tenant in *both* databases"), and it lands
on the main production tenant.

After R1, kaufmann publishes `7be1ab9e-…_east` while fleet-lite's tenant is
`9708b213-…`. The prefixes don't match, so tenant-matching drops everything.

> **`dropForeign` must not be enabled until the two tenants share one uuid.**

That unification is Phase 0's backfill. Until then the flag stays off and the
transitional adopt-into-own-namespace branch keeps today's behaviour exactly.

This also clarifies what fleet-lite's "Kaufmann" tenant *is*: not a customer, but
**the operator viewing its own fleet in fleet-lite** — the `fleet_lite_enabled`
case from D6. Groups flowing from b2b into that view is correct and desirable,
which is exactly what tenant-matching delivers once the ids are one. The design
holds; only the sequencing was wrong.

### Revised dependency

R1's migration, tolerance and republish ship standalone and are safe.
**Step 6 (enforce tenant-matching) moves out of R1 and into Phase 0**, gated on
tenant-uuid unification. R1 no longer blocks on it, and Phase 2 gets the
attribution it needs from the id format alone.

## 1. Full reference map

Every place a group id is stored or crosses a boundary. Anything not listed here
was checked and doesn't hold group ids.

### fleet-lite-app

| Location | Kind | Cascades on PK update? |
|---|---|---|
| `fleet_groups.id` | `TEXT PRIMARY KEY` | — |
| `vehicle_fleet_groups.fleet_group_id` | FK, `ON DELETE CASCADE` | **No** — see §2 |
| `tenant_users.allowed_group_ids` | `TEXT[]`, no FK | No |
| `invitations.allowed_group_ids` | `TEXT[]`, no FK | No |
| `geofences.group_ids` | `TEXT[]`, no FK | No |
| `data.groups[].id` in `dimo.document.vehicle.groups` | external, published | No |
| `?group=<id>` deep link (`fleet-list-view.ts:196`) | external, bookmarks | No |

### kaufmann-oracle

| Location | Kind | Cascades? |
|---|---|---|
| `fleet_groups.id` | `TEXT PRIMARY KEY` | — |
| `fleet_groups.name` | `TEXT NOT NULL UNIQUE` | globally unique — same bug, fix while here |
| `vin_fleet_groups.fleet_group_id` | FK, `ON DELETE CASCADE` | **No** |
| `access_fleet_groups.fleet_group_id` | FK, `ON DELETE CASCADE` | **No** |
| `data.groups[].id` | external, published | No |
| `/api/v1/fleet-groups` response | external, public API | No |

The three `TEXT[]` columns in fleet-lite are the easy ones to miss — no foreign
key means no error, just silently stale ids that stop matching anything. All
three are member/geofence *scoping*, so a miss degrades quietly into "this
member sees nothing" or "this geofence targets nothing".

## 2. The Postgres detail that shapes the migration

`vehicle_fleet_groups.fleet_group_id` has `ON DELETE CASCADE` but **not**
`ON UPDATE CASCADE`. The default is `ON UPDATE NO ACTION`, so
`UPDATE fleet_groups SET id = ...` **fails** while child rows exist.

Two ways out. Add `ON UPDATE CASCADE` first and let children follow, or
insert-new / repoint / delete-old. The first is far less code and less risk:

1. Drop and recreate each FK with `ON UPDATE CASCADE`.
2. One `UPDATE` on `fleet_groups`. Children follow automatically.
3. Hand-update the `TEXT[]` columns, which have no FK.

Leave `ON UPDATE CASCADE` in place afterwards — it's harmless and makes any
future id change tractable.

No collision risk in the single `UPDATE`: old ids are globally unique, new ids
are `<uuid>_<slug>` which cannot collide with a bare slug, and
`UNIQUE (tenant_id, name)` guarantees uniqueness within a tenant.

## 3. Migrations

Both are idempotent — the `strpos(id, '_') = 0` guard makes re-runs no-ops.
That matters because slugs can never contain `_`: `slugNonAlphanum`
(`[^a-z0-9]+`) collapses everything non-alphanumeric to `-`, and uuids contain
`-` but not `_`. So exactly one `_` in a new id, zero in a legacy one — the
format is self-detecting.

### 3.1 fleet-lite-app

```sql
-- +goose Up
-- +goose StatementBegin

-- Let PK rewrites cascade to the join table.
ALTER TABLE vehicle_fleet_groups
    DROP CONSTRAINT vehicle_fleet_groups_fleet_group_id_fkey;
ALTER TABLE vehicle_fleet_groups
    ADD CONSTRAINT vehicle_fleet_groups_fleet_group_id_fkey
    FOREIGN KEY (fleet_group_id) REFERENCES fleet_groups (id)
    ON DELETE CASCADE ON UPDATE CASCADE;

-- Rewrite ids. vehicle_fleet_groups follows via ON UPDATE CASCADE.
UPDATE fleet_groups
   SET id = tenant_id::text || '_' || id
 WHERE strpos(id, '_') = 0;

-- Soft references: no FK, so update by hand. Per-element guard keeps it
-- idempotent; ARRAY(SELECT ...) preserves element order.
UPDATE tenant_users
   SET allowed_group_ids = ARRAY(
       SELECT CASE WHEN strpos(g, '_') = 0 THEN tenant_id::text || '_' || g ELSE g END
       FROM unnest(allowed_group_ids) AS g)
 WHERE allowed_group_ids IS NOT NULL;

UPDATE invitations
   SET allowed_group_ids = ARRAY(
       SELECT CASE WHEN strpos(g, '_') = 0 THEN tenant_id::text || '_' || g ELSE g END
       FROM unnest(allowed_group_ids) AS g)
 WHERE allowed_group_ids IS NOT NULL;

-- group_ids is NOT NULL DEFAULT '{}', so guard on cardinality instead.
UPDATE geofences
   SET group_ids = ARRAY(
       SELECT CASE WHEN strpos(g, '_') = 0 THEN tenant_id::text || '_' || g ELSE g END
       FROM unnest(group_ids) AS g)
 WHERE cardinality(group_ids) > 0;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

UPDATE geofences
   SET group_ids = ARRAY(SELECT split_part(g, '_', 2) FROM unnest(group_ids) AS g)
 WHERE cardinality(group_ids) > 0;
UPDATE invitations
   SET allowed_group_ids = ARRAY(SELECT split_part(g, '_', 2) FROM unnest(allowed_group_ids) AS g)
 WHERE allowed_group_ids IS NOT NULL;
UPDATE tenant_users
   SET allowed_group_ids = ARRAY(SELECT split_part(g, '_', 2) FROM unnest(allowed_group_ids) AS g)
 WHERE allowed_group_ids IS NOT NULL;
UPDATE fleet_groups SET id = split_part(id, '_', 2) WHERE strpos(id, '_') > 0;

-- +goose StatementEnd
```

⚠️ The down migration reintroduces the collision, so it will fail if two tenants
hold the same slug by then. That's correct — it should fail rather than silently
merge two tenants' groups. Down is for a fast rollback in the first hours, not a
long-term escape hatch.

### 3.2 kaufmann-oracle

Same shape, two FKs, plus the globally-unique name constraint:

```sql
-- +goose Up
-- +goose StatementBegin

ALTER TABLE kaufmann_oracle.vin_fleet_groups
    DROP CONSTRAINT vin_fleet_groups_fleet_group_id_fkey;
ALTER TABLE kaufmann_oracle.vin_fleet_groups
    ADD CONSTRAINT vin_fleet_groups_fleet_group_id_fkey
    FOREIGN KEY (fleet_group_id) REFERENCES kaufmann_oracle.fleet_groups (id)
    ON DELETE CASCADE ON UPDATE CASCADE;

ALTER TABLE kaufmann_oracle.access_fleet_groups
    DROP CONSTRAINT access_fleet_groups_fleet_group_id_fkey;
ALTER TABLE kaufmann_oracle.access_fleet_groups
    ADD CONSTRAINT access_fleet_groups_fleet_group_id_fkey
    FOREIGN KEY (fleet_group_id) REFERENCES kaufmann_oracle.fleet_groups (id)
    ON DELETE CASCADE ON UPDATE CASCADE;

UPDATE kaufmann_oracle.fleet_groups
   SET id = tenant_id::text || '_' || id
 WHERE strpos(id, '_') = 0;

-- Names were globally unique — the same bug wearing a different hat.
ALTER TABLE kaufmann_oracle.fleet_groups DROP CONSTRAINT fleet_groups_name_key;
ALTER TABLE kaufmann_oracle.fleet_groups
    ADD CONSTRAINT fleet_groups_tenant_name_key UNIQUE (tenant_id, name);

-- +goose StatementEnd
```

Verify the real constraint names first — `fleet_groups_name_key` and the FK
names above are Postgres defaults and should be confirmed against the live
schema with `\d kaufmann_oracle.fleet_groups`.

## 4. Code changes

### 4.1 Id construction

**fleet-lite** — `internal/service/fleet_group.go:138`:

```go
// groupID builds a tenant-scoped, attestation-safe group id. The tenant uuid
// prefix makes ids globally unique and self-attributing: with a shared operator
// developer license, the id is the only field in the group CloudEvent that can
// tell two tenants' groups apart.
func groupID(tenantID, name string) string { return tenantID + "_" + slug(name) }
```

`CreateGroup` uses it; `UpdateGroup` still never re-derives the id on rename, so
published attestations stay valid. Keep that.

**kaufmann** — `internal/controllers/fleet_vehicles.go:509`, same change against
`slugify`.

### 4.2 Read-side acceptance (ships *before* the migration — see §6)

`internal/service/group_sync.go`, applied to each incoming CE group id in
`desiredGroups` (or immediately after, before `reconcile`):

```go
// normaliseGroupID maps a CloudEvent group id into the tenant being reconciled.
//
// The rule is tenant-matching, not producer-matching: a group belongs to the
// tenant whose uuid prefixes its id, whoever published the CE. So an operator
// tenant viewed in fleet-lite sees the groups b2b created for it, while a
// customer tenant never sees the operator's.
//
// Legacy ids (published before the tenant-prefix migration) are bare slugs with
// no tenant. Safe to adopt: reconcile always runs for one known vehicle in one
// known tenant, so a bare slug is unambiguous in that context.
//
// dropForeign is flag-gated. Until fleet-lite has republished its own groups,
// dropping foreign ids would remove memberships that exist only because another
// producer's CE created them — see §"The regression this creates".
func normaliseGroupID(tenantID, id string, dropForeign bool) (string, bool) {
    i := strings.IndexByte(id, '_')
    if i < 0 {
        return tenantID + "_" + id, true // legacy bare slug
    }
    if id[:i] == tenantID {
        return id, true // ours
    }
    if dropForeign {
        return "", false
    }
    return tenantID + "_" + id[i+1:], true // transitional: adopt into our namespace
}
```

`dropForeign` starts **false** — preserving today's accidental behaviour exactly
— and flips to true after republish. The transitional branch is deleted along
with the flag once rollout completes.

Log every drop with tenant and id at debug level. During rollout that line is
the live answer to "was cross-app group flow actually in use", and it's cheaper
to read than the `#808080` query.

### 4.3 A latent bug this fixes for free

`ensureGroup` (`group_sync.go:306`) checks existence by
`ID = g.ID AND tenant_id = ?`, then inserts with `ID: g.ID`. If tenant A already
owns group id `vans`, tenant B's sync sees `exists = false` and the insert dies
on a primary-key violation. The comment claims this surfaces as a
`(tenant_id, name)` collision; it's actually a PK collision.

Tenant-prefixed ids make the existence check and the PK agree, so this stops
happening. Worth a regression test rather than leaving it implicit.

### 4.4 Frontend

`?group=<id>` deep links (`fleet-list-view.ts:196`) carry the old bare slug.
Existing bookmarks silently fall through to "no filter" after the migration.

Acceptable — the ids are opaque and links are short-lived. If it matters, the
same normalise rule can be applied client-side for one release.

## 5. Republish

Both apps publish group CEs for the same vehicles, so both need a republish pass
or the fetch-api union keeps serving stale bare-slug ids until the next organic
group edit.

**fleet-lite's republish is a correctness gate, not just convergence.** It's what
puts locally-held groups into fleet-lite's own CE so that enforcing
tenant-matching (§6 step 6) doesn't remove them. Don't treat it as optional
cleanup.

**kaufmann** — enqueue the existing River job per vehicle; `AttestGroupsArgs` is
already unique-by-args so duplicates coalesce:

```sql
SELECT DISTINCT v.tenant_id, v.vehicle_token_id
FROM kaufmann_oracle.vin_fleet_groups vfg
JOIN kaufmann_oracle.vins v ON v.imei = vfg.imei
WHERE v.vehicle_token_id IS NOT NULL;
```

**fleet-lite** — no equivalent job runner, so add a CLI subcommand alongside
`import-group-attestations` (same `google/subcommands` pattern, same Helm
Job shape):

```
republish-group-attestations [-tenant-id X] [-dry-run]
```

Iterate distinct `(tenant_id, token_id)` in `vehicle_fleet_groups`, load groups,
call `AttestVehicleGroups`. Throttle it — one signed CE per vehicle, and
`GROUP_SYNC.md` already flags fetch/attest fan-out as needing a concurrency cap.

Run `-dry-run` first and reconcile the count against the SQL above.

## 6. Rollout order

Two constraints drive the ordering, and both are about never destroying data:

1. **Tolerance ships before ids change** — so no live CE is ever unreadable.
2. **Republish ships before foreign-drop** — so no membership is removed for
   being absent from a CE that hasn't been written yet.

| # | Step | Why here |
|---|---|---|
| 1 | Deploy §4.2 with `dropForeign = false`, fleet-lite | Both id formats readable. A no-op today — every id is legacy — and foreign ids still adopted, so behaviour is unchanged |
| 2 | Run migrations (§3), both repos | Ids rewritten; readers already cope |
| 3 | Deploy §4.1 id construction, both repos | New groups get the new format |
| 4 | **fleet-lite republish (§5)** | Every locally-held group now appears in fleet-lite's own CE. This is the step that makes step 6 safe |
| 5 | kaufmann republish (§5) | Operator CEs converge on the new format |
| 6 | *Phase 0, not here:* flip `dropForeign = true` | **Blocked until the two Kaufmann tenants share one uuid** — see the sequencing constraint above. Also requires step 4 to have landed |
| 7 | *Later:* delete the flag and the legacy branch | Once no bare-slug CE is inside the retention window |

Steps 2 and 3 can share a deploy per repo — migrations run at startup, before
traffic. Steps 1, 4 and 6 must be distinct, and **step 6 must not ship in the
same release as step 4**: verify the republish landed before enforcing.

Verify before step 6 by confirming fleet-lite's own producer now asserts every
shared-id group for every one of the 287 grouped vehicles. Any vehicle the
republish missed is a vehicle step 6 would strip. Given 0 of 287 have ever been
locally edited, assume nothing is published until the republish proves otherwise.

Repo order doesn't matter. A mixed fleet — one repo migrated, one not — is
exactly the legacy case tolerance was written for.

## 7. Testing

- **Migration:** testcontainers, seed two tenants with a same-named group each,
  plus `vehicle_fleet_groups` / `allowed_group_ids` / `geofences.group_ids`
  rows. Assert every reference is rewritten and nothing is orphaned. Run the
  migration **twice** — idempotency is a claim, so test it.
- **The bug this fixes:** two tenants both create "Vans". Fails today with
  `ErrGroupNameExists`; must succeed after.
- **`normaliseGroupID`:** table-driven over both flag states — legacy bare slug,
  own-tenant prefixed, foreign-tenant prefixed (adopted when
  `dropForeign=false`, dropped when true), empty, slug containing `-`.
- **The removal regression:** seed a group that exists locally but is absent
  from fleet-lite's own CE while present in a foreign-tenant CE. Assert it
  survives with `dropForeign=false`, and assert it survives *after* a republish
  with `dropForeign=true`. This is the test that would have caught the data loss.
- **Reconcile with a mixed CE stream:** one legacy CE and one new-format CE for
  the same vehicle, assert they resolve to one group and not two.
- **`ensureGroup` cross-tenant:** the §4.3 regression.
- **Down migration:** verify it restores bare slugs, and verify it *fails loudly*
  when two tenants hold the same slug.

## 8. Sibling: geofence ids have the identical bug

`geofences.id` is also `slug(name)` as a global `TEXT PRIMARY KEY` with
`UNIQUE (tenant_id, name)` — `internal/service/geofence.go:189`,
`internal/db/migrations/20260624230000_geofences.sql`. Geofence ids also go into
attestations. Same bug, same fix, same blast radius.

**Not in this change.** The group migration also carries the CE tolerance and
republish work; bundling geofences doubles the surface of a migration that
touches five tables. Do it immediately after as a near-copy — smaller, because
`vehicle_geofences.geofence_id` is the only FK and nothing else stores a
geofence id.

Worth filing now so it doesn't get lost once groups are fixed.

## 9. Estimate and sequencing

Roughly, assuming no surprises in the constraint names:

| Piece | Notes |
|---|---|
| Migrations (both repos) | Mechanical once constraint names are confirmed |
| `normaliseGroupID` + wiring | Small, but it's the correctness core — worth the most care |
| Id construction (both repos) | Two lines plus tests |
| fleet-lite republish CLI | Largest single piece; mirror `import-group-attestations` |
| Tests | Comparable to the implementation, mostly migration fixtures |

Independent of everything else in the tenancy programme — no dependency on
`fleet-tenancy-api` existing. It can start immediately and ship on its own.
