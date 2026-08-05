# Target architecture

## Shape

```
                    ┌────────────────────────────────┐
                    │       fleet-tenancy-api        │
   operator &       │  tenants · users · memberships │
   customer  ──────▶│   delegations · entitlements   │◀─────  customer
   identity         │   invitations · DIMO tokens    │        identity
                    └───────┬────────────────┬───────┘
                            │                │
              authz + provisioning       authz + entitlements
                            │                │
      ┌─────────────────────┴───┐        ┌───┴──────────────────┐
      │   b2b-fleet-mgr-app     │        │    fleet-lite-app    │
      │   (operator console)    │        │  (customer product)  │
      └───────────┬─────────────┘        └───────────┬──────────┘
                  │ /oracle/:id/* proxy              │
      ┌───────────┴─────────────┐                    │
      │     kaufmann-oracle     │                    │
      │  vins · onboarding ·    │                    │
      │  kore · signer · attest │                    │
      └───────────┬─────────────┘                    │
                  │                                  │
                  └────────────┬─────────────────────┘
                               ▼
              DIMO platform: identity · telemetry · fetch
              attest · accounts — all called with the
              **operator's** developer JWT
```

## Why a separate service

The alternative was to make one of the existing apps the source of truth.

- fleet-lite owning it makes the *customer* product responsible for provisioning
  itself, and gives the operator console a dependency on the customer app.
- kaufmann owning it binds fleet-lite to one specific oracle. b2b-fleet-mgr-app
  is deliberately multi-oracle (motorq, tesla, flespi all exist in this tree);
  customer tenancy is not an oracle concern.

A third service is more up-front work, but tenancy is genuinely shared domain:
both apps already implement the same tenant table, the same encryption, the same
`Tenant-Id` header and the same cache. The duplication is the smell that
justifies the extraction.

**It is not a new DIMO platform service.** `accounts` / `profiles-api` /
`users-api` deal with DIMO *end-user accounts and wallets*. This service deals
with *application tenancy for our fleet products* — which tenants exist, who
belongs to them, and what vehicles they may see. It is a consumer of
accounts-api, not a replacement for it.

## Tenants

One table, two kinds, one self-referencing parent link.

- **Operator tenant** — no parent. Holds the DIMO developer license used for all
  data access, plus the signer wallet used for on-behalf-of operations. Its
  members are the operator's staff, working in `b2b-fleet-mgr-app`.
- **Customer tenant** — `parent_tenant_id` points at the operator. Holds no
  credentials of its own by default. Its members are the end customer's users,
  working in `fleet-lite-app`.
- **Self-serve tenant** — a customer tenant with `parent_tenant_id = NULL` and its own
  credentials. This is what every existing fleet-lite tenant becomes, and it is
  how the current self-serve flow keeps working. Everything below degrades
  gracefully for these: no delegation, entitlements derived from their own
  privileged set rather than assigned by an operator.

`kind` is stored explicitly rather than inferred from `parent_tenant_id`, because a
self-serve customer and an operator both have a null parent but behave nothing
alike.

## Two apps, two surfaces

The apps are not two views of the same thing, and conflating them causes
trouble. The distinction:

| | `b2b-fleet-mgr-app` | `fleet-lite-app` |
|---|---|---|
| You operate **as** | the operator tenant | any tenant you can act in |
| Vehicle scope | **every vehicle** in the operator's fleet | one tenant's slice |
| Sub-tenants are | configuration objects you manage | the thing you're logged into |
| Built for | thousands of vehicles — paged, searchable, filterable tables | **sub-500 vehicle fleets**, map-first, operational visualisation |
| Vehicle source | the oracle's `vins` table (already paged and filtered) | `vehicles`, synced from identity-api |

So **you don't "switch into" a sub-tenant in b2b.** You stay the operator and
configure the customer's fleet from the outside. And operator staff don't go
into fleet-lite to see a customer's view either — see
[No impersonation](#no-impersonation-operator-staff-are-b2b-only) below. That
keeps b2b's tenant-selection story simple — it selects an operator — which
matters because that flow is already being hardened in
`b2b-fleet-mgr-app/.planning/PROJECT.md`.

### Operator fleet visibility in fleet-lite

An operator tenant can also appear in fleet-lite as a fleet in its own right —
showing everything its license is privileged on. That's useful for a small
operator and actively harmful for a large one, because fleet-lite is tuned for
a few hundred vehicles, not several thousand.

So the operator tenant carries a flag, set from the b2b console:

**`fleet_lite_enabled`** — default **true**, can be turned off.

When off, the operator tenant simply isn't listed as a selectable tenant in fleet-lite,
and operator staff work entirely in b2b — which is the better tool for operator
work anyway.

**A softer lever already exists.** Membership rows carry `scope_group_ids`, so
an operator user can be scoped to a handful of fleet groups in fleet-lite
instead of the whole fleet. That's a better answer than the boolean for
"the operator wants *some* fleet-lite visibility at scale", and it needs no new
mechanism. The boolean is the blunt instrument; group scope is the scalpel.

Worth a product touch: when an operator crosses the vehicle count where
fleet-lite degrades, the console should suggest turning it off (or scoping by
group) rather than waiting for someone to notice the map is slow. See Q8 in
[05-risks-and-open-questions.md](05-risks-and-open-questions.md) — we don't yet
know the real number.

## Authorization: one question, one endpoint

Both apps today do the same thing at the edge with different code:
`fleet-lite-app/internal/app/tenant.go` and
`kaufmann-oracle/internal/app/access.go`. Both collapse into a single call:

> **"What may wallet W do in tenant O?"**

`GET /v1/authz?wallet=0x…&tenant_id=…` returns role, capability list, group scope,
and crucially **how** the answer was reached:

- `via: "direct"` — W has a membership row in O.
- `via: "delegation"` — W is a member of an operator tenant holding a delegation
  over O. This is what lets an operator **manage** a customer — members,
  vehicles, settings — without being a member of that customer tenant. It is
  never a fleet session: fleet-lite refuses delegated access outright (D7).

Callers cache the answer for 30–60s, exactly as both apps already cache tenants
today. A stale-serve fallback keeps them up if tenancy-api is briefly down.

### One authz model, shared

fleet-lite has `role` + `allowed_group_ids`; kaufmann has `permissions[]` +
`is_admin`. **Both converge on capability strings** — an audit showed the fit is
clean (see Q5 in
[05-risks-and-open-questions.md](05-risks-and-open-questions.md)).

Every owner-only gate in fleet-lite today is one of five operations —
`AddMember`, `RemoveMember`, `UpdateMemberAccess`, all invitation operations, and
`UpdateSettings` — which collapse to exactly **two** capabilities.

**Shared capability set:**

| Capability | Meaning | Used by |
|---|---|---|
| `manage_members` | add/remove members, change their access, manage invitations | both (kaufmann's `manage_admin_users` renamed) |
| `manage_settings` | change tenant settings and DIMO credentials | both (new for kaufmann, which only gated on `is_admin`) |
| `onboard_vehicles` | mint / onboard | kaufmann |
| `reports` | run reports | kaufmann; fleet-lite's TCO could adopt |

Capabilities are additive and app-specific ones are fine — fleet-lite simply
never checks `onboard_vehicles`.

**`role` becomes a label and a preset, not an authorization input.**
`permissions[]` is what gets checked, everywhere. `role` stays because "Owner" /
"Member" is the right thing to show in a members list and the right way to fill
in capabilities when adding someone. Two sources of truth for the same decision
is how authorization bugs happen, so only one of them is authoritative.

**Group scope is data, not a capability.** kaufmann's `view_all_fleets` is
deliberately *not* in the set above: it means the same thing as fleet-lite's
`allowed_group_ids IS NULL`, and encoding one fact two ways guarantees drift —
what would a member with `view_all_fleets` *and*
`scope_group_ids = ['vans']` resolve to?

So `scope_group_ids` is the single mechanism (`NULL` = unrestricted), and
`view_all_fleets` becomes **derived, never stored**. It's also strictly more
expressive: a boolean can't say "these three groups". kaufmann's migration maps
`view_all_fleets` → `scope_group_ids = NULL`.

## Credentials: minted, never handed out

Decision D2 puts all data access under the operator's developer license. The
naive implementation is for tenancy-api to return the decrypted API key to
whichever service asks. Don't do that — it turns one service into a
credential-distribution endpoint and puts plaintext keys on the wire on every
cache miss.

Instead, **tenancy-api mints the DIMO developer JWT itself**:

`GET /v1/tenants/{id}/dimo-token` → `{ token, expiresAt }`

The private key never leaves the service. This is not new work — kaufmann's
`TenantService.GetDeveloperJWT` already does exactly this, wrapping
`shared/pkg/dimoauth.AuthService` with a per-tenant cache that refreshes on
expiry. That code moves into tenancy-api roughly as-is.

**Resolution rule:** an tenant's *effective* credential is its own if it has one,
otherwise its parent's. So customer tenants under an operator resolve to the
operator's license; self-serve tenants resolve to their own. Callers don't need to
know which — they ask for the tenant's token and get the right one.

An escape hatch (`GET /v1/tenants/{id}/credentials`, cluster-internal, mutually
authenticated) is worth keeping for cases the token endpoint can't cover, but
should start out unimplemented.

## Vehicle access

### Two entitlement modes

Not every tenant enumerates its vehicles the same way, and forcing them to would
mean writing entitlement rows for every vehicle an operator owns.

| Mode | Who | Resolution |
|---|---|---|
| **implicit** | operator tenants, self-serve tenants | everything the tenant's *effective* developer license is privileged on |
| **explicit** | managed customer tenants | the `vehicle_entitlements` rows written by their operator |

Stored as `entitlement_mode` on the tenant, defaulted from `kind` and overridable.

This is what makes the model coherent: the operator's fleet is defined by SACD
grants on chain (implicit), and each customer's slice is carved out of it in
web2 (explicit). A customer never needs a SACD grant, which is the whole point
of D2/D5.

It also means the **exclusivity invariant applies to explicit tenants only** — a
vehicle may be entitled to at most one customer tenant under a given operator, but
the operator itself implicitly sees every vehicle including ones assigned to
customers. That's intended; the operator manages them.

### The entitlement table

Decision D3: the model is **per vehicle**, keyed by token id, with provenance.

Assigning a fleet group to a customer expands to one row per vehicle, each
stamped with `source_group_id`. That gives the best of both:

- Read paths are simple — a flat set of token ids per tenant.
- The operator UI can still work in groups.
- Because provenance is recorded, the console can detect drift ("4 vehicles have
  been added to *Vans* since you assigned it") and offer a one-click re-apply,
  rather than silently diverging or silently auto-granting.

**Whose groups?** The operator's. When an operator bulk-assigns "group Vans" to
customer Acme, they're using an **operator-side** group (kaufmann's
`fleet_groups` × `vin_fleet_groups`) purely as a way to *select vehicles*. The
group does not propagate into Acme's tenant — Acme has their own groups in
fleet-lite, managed by them. `source_group_id` therefore refers to an operator
group id and is provenance only, never a cross-tenant link.

This also means the group-id collision problem (R1/R2) is confined to
*fleet-lite* groups across customer tenants. Operator groups live in a separate
namespace and aren't implicated.

Auto-following group membership (live rather than snapshot semantics) is
deliberately **not** in v1. It means a vehicle added to a group inside the
operator's own tooling instantly becomes visible to a customer, which is exactly
the kind of implicit grant that causes a leak. Revisit once the drift UI shows
whether anyone actually wants it.

### How fleet-lite's sync changes

Today (`internal/service/vehicle.go`):

```
SyncVehicles(tenant) → FetchPrivilegedVehicles(tenant.ClientID)
                     → upsert vehicles (tenant_id, token_id)
```

Target:

```
1. per operator tenant:  FetchPrivilegedVehicles(operator.ClientID)  → the full privileged set
2. per customer tenant:  entitled token ids from tenancy-api
3. materialise vehicles rows for (customer_tenant_id, token_id) ∈ entitlements
```

Implicit-mode tenants (operator, self-serve) skip step 2 — their set is "everything
the effective license is privileged on", which for self-serve is precisely
today's behaviour.

### The scale trap: the toggle fixes the UI, not the backend

Worth being blunt about, because it's easy to assume `fleet_lite_enabled = false`
makes the scale problem go away. It doesn't.

Even with operator visibility off, **fleet-lite still has to sync the operator's
entire privileged set**, because every customer's slice is computed from it.
Turning the toggle off stops fleet-lite *serving* thousands of vehicles; it
doesn't stop it *holding* them.

Concretely, at operator scale:

- `SyncVehicles` upserts row-by-row in a loop
  (`internal/service/vehicle.go`) — thousands of individual statements per pass.
- The group-sync cron's weekly full pass is **one fetch-api call per vehicle**,
  each needing an asset JWT. `docs/GROUP_SYNC.md` already flags this as needing
  throttle/batch; at operator scale it stops being optional.
- Location refresh and telemetry fan-out scale with the held set, not the
  displayed set.

**Design response — tier the work by whether anyone can see the vehicle:**

1. **Sync the operator's privileged set once per operator**, not once per
   customer. One master pass, then cheap per-customer materialisation from
   entitlements.
2. **Only vehicles entitled to at least one fleet-lite-visible tenant get the
   expensive treatment** — location refresh, group sync, telemetry. Unassigned
   inventory sitting in the operator's pool is synced as identity metadata only
   and costs almost nothing.
3. **Batch the upserts.** A row-at-a-time loop is fine at 50 vehicles and not at
   5,000.

Point 2 is the important one: in a mature operator, most vehicles are either
unassigned inventory or belong to a customer nobody has logged into this week.
The existing warm/cold tiering in `GROUP_SYNC.md` (tenant activity by
`last_login_at`) already has the right shape — it just needs to extend to the
entitlement dimension as well as the activity one.

**Keep one `vehicles` row per (tenant, token).** The alternative — a single row per
token joined through entitlements — is cleaner on paper but touches every
downstream query and foreign key in fleet-lite: `vehicle_fleet_groups`,
`vehicle_favorites`, geofences, geofence passes, TCO settings, all keyed by
`(tenant_id, token_id)`. Duplication is not a real concern because tenancy-api
enforces that a vehicle is entitled to **at most one customer tenant per operator**.

### Ownership sits with the operator

Vehicles are always minted from `b2b-fleet-mgr-app`, and on-chain ownership is
held by the **operator's** user account — this is already how it works today
(`kaufmann-oracle/internal/onboarding/onboard.go:286` mints with
`Owner: args.Owner`, the operator staff member signing with their passkey).
Customer tenants never hold the asset.

The SACD grant attached at mint exists so the operator's developer license can
**enumerate the fleet** from identity-api — `vehicles(filterBy: {privileged: …})`
is driven by grants, not ownership. It is not a per-customer authorization
mechanism and never changes after mint.

That's the whole on-chain surface: ownership plus one grant. Which customer sees
a vehicle, and when that stops, is a database decision. See
[06-onchain-surface.md](06-onchain-surface.md) for the full boundary, customer
offboarding, and the proposal to record entitlements as signed attestations.

### The security property this creates

Under D2/D5 the tenant boundary is **not** enforced by the chain — deliberately.
Every customer's data is reachable with the operator's developer JWT. A single
missing check in a single controller leaks one customer's telemetry to another.

Since this is the intended mechanism rather than a gap, it has to be designed
for, not hoped for:

1. **One choke point.** Every path that turns a token id into a DIMO call goes
   through `AssertEntitled(ctx, tenantID, tokenID)`. No gateway method is called
   with a token id that didn't come out of an entitlement-filtered query.
2. **The public API too.** kaufmann's `/api/v1` resolves a tenant from a
   developer license. Under a shared license that resolution becomes ambiguous —
   it must resolve to the operator and then require an explicit tenant scope, or it
   will hand a customer the operator's whole fleet.
3. **A test that fails loudly.** An integration test that stands up two customer
   tenants under one operator and asserts every tenant-scoped endpoint 403s or
   404s on the other's token id. Table-driven over the route list, so new routes
   are covered by default.

This is the cost of D2/D5, accepted in exchange for access control that changes
without a passkey prompt and a transaction.

## Fleet group ids carry their tenant

**Decision: a fleet group id is `<tenant-uuid>_<slug>`.** Both apps, same format.

This replaces today's bare `slug(name)` global primary key, and it fixes two
problems at once:

- **The collision.** `fleet_groups.id` is a global `TEXT PRIMARY KEY` while the
  intended uniqueness is per-tenant, so the second tenant to create "Vans" is
  told the name is taken by a group they can't see. Embedding the tenant uuid
  makes ids globally unique by construction.
- **The attribution.** Under a shared operator license every customer's
  attestations carry the same `source` and `producer`, so `data.groups[].id` is
  the only thing that can distinguish them. A self-describing id means **no
  CloudEvent schema change at all** — the existing `{id, name, color}` shape
  keeps working and becomes unambiguous.

The tenant in the id is whichever tenant created the group: the **operator**
tenant for groups created in `b2b-fleet-mgr-app`, the **customer** tenant for
groups created in `fleet-lite-app`.

**Readers accept a group when its prefix matches the tenant being viewed** —
tenant-matching, not producer-matching. Nothing keys off which app published the
CloudEvent. That single rule covers every case: a customer tenant sees only its
own groups; an operator tenant with `fleet_lite_enabled` sees the groups b2b
created for it; and if groups should later be viewable from both tools, the rule
already permits it. It also means cross-app visibility switches on by itself
once Phase 0's UUID reuse makes the operator's tenant id the same on both
sides — no code change at that point.

### Format notes

- Separator `_`. Slugs are `[a-z0-9-]+` (`slugNonAlphanum` collapses everything
  else to `-`) and uuids contain `-`, so `_` is unambiguous — split on the first
  one. Avoid `:` since Fiber uses it for route params.
- Full uuid, not a truncated prefix. These ids are rarely typed by hand, and a
  short prefix trades a real collision risk for cosmetics.
- Ids stay immutable. `UpdateGroup` already renames without re-deriving the id,
  so a rename doesn't orphan attestations. That stays true.

### Migration

Not just a column change — the id is a foreign key in several places and a join
key in published attestations.

1. Rewrite `fleet_groups.id` and cascade: `vehicle_fleet_groups.fleet_group_id`,
   `tenant_users.allowed_group_ids[]` in fleet-lite; `vin_fleet_groups` and
   `access_fleet_groups` in kaufmann.
2. **Tolerate legacy bare slugs on read.** Already-published CloudEvents carry
   bare slugs, and `group_sync.go` auto-creates unknown groups — so without
   tolerance, an old CE would spawn a phantom group. The fix is easy: reconcile
   always runs *for a known vehicle in a known tenant*, so a bare slug resolves
   unambiguously to `<that tenant>_<slug>`. Keep that mapping for a release or
   two.
3. Republish attestations for active vehicles so the stream converges on the new
   format, then drop the legacy tolerance.

### The alternative we didn't take

Keeping `id` as a bare slug and making the primary key `(tenant_id, id)` is
arguably cleaner relational design — no string munging. But the CloudEvent join
key is the group id alone, so that route *still* needs a `tenantId` field added
to the document, plus a schema decision coordinated with
`kaufmann-oracle/docs/adr/0001-fleet-group-attestations.md`.

Embedding avoids the CE change entirely. Given the attestation path is the
harder half of the problem, that's the better trade.

## User provisioning

Decision D4 keeps both paths.

**Operator-provisioned (new).** From the customer detail screen in b2b:

1. Operator enters an email and picks a role + optional group scope.
2. tenancy-api calls accounts-api `GetAccount(email, …)`; if absent,
   `CreateAccount(email, operatorSignerAddress, operatorDeveloperJWT)`.
3. The returned wallet is upserted into `users`, and a membership row is written
   for the customer tenant, stamped `granted_by_tenant_id = <operator>`.
4. The customer logs into fleet-lite with that email and lands directly in their
   tenant — no invitation to accept.

Steps 2–3 are `GrantAdminAccess` (`kaufmann-oracle/internal/controllers/account.go:313`)
generalised to write into an arbitrary tenant rather than the caller's own.

**Invitation (existing).** fleet-lite's `invitations` table, tokens and Postmark
templates move to tenancy-api unchanged in behaviour, so an operator can *also*
send an invite from b2b and have the customer accept it in fleet-lite. Keeping
this matters because it's the only path that works when we don't want to create
a wallet on someone's behalf.

## No impersonation: operator staff are b2b-only

An earlier draft had operator staff opening a customer's fleet inside fleet-lite
via a delegated read-only session, with a "viewing as operator" banner.

**Dropped.** Operator staff work in `b2b-fleet-mgr-app`, full stop. That app
already shows every vehicle in the operator's fleet with paging, search and
filters — better suited to operator work than fleet-lite's map-first view, and
it scales.

This is a real simplification. It removes:

- `impersonate_read` / `impersonate_write` scopes
- the delegated-session UX and its banner
- delegated tenants from fleet-lite's tenant list — **`GET /tenants` returns direct
  memberships only**
- an entire class of "whose data am I looking at" confusion

**Delegation still exists**, but only for *management*: an operator acts on a
customer tenant through the console (`manage_members`, `manage_vehicles`,
`manage_settings`). It never grants a fleet-lite session. So `via: "delegation"`
in the authz response is meaningful to b2b and is something fleet-lite can
simply refuse.

If an operator genuinely needs to see a customer's exact view for support, the
honest path is a support login into that customer tenant — a membership, visible in
the customer's member list — rather than an invisible back door.

## What each repo has to do

### New: `fleet-tenancy-api`

Go + Fiber + zerolog + goose + sqlboiler + testcontainers, following the
existing `api/internal/{app,config,controllers,gateway,models,service,db}`
layout that both apps already use. Own Postgres schema. See
[03-tenancy-api-spec.md](03-tenancy-api-spec.md).

### `fleet-lite-app`

- `TenantService` becomes a tenancy-api client; keep the `go-cache` layer.
- `NewTenantMiddleware` calls `/v1/authz` instead of `GetMembership`.
- `GET /tenants` lists **direct memberships only** — no delegated tenants — and is
  filtered to fleet-lite-visible tenants (an operator tenant with
  `fleet_lite_enabled = false` never appears).
- `SyncVehicles` becomes entitlement-driven, with the master-pass +
  materialisation split and the tiering described above.
- Batch the vehicle upserts.
- Tenant/member/invitation tables become read caches, then get dropped.
- `POST /tenants` self-serve creation stays, creating an unparented tenant.
- **Namespace fleet-group ids per tenant** — prerequisite, see
  [05-risks-and-open-questions.md](05-risks-and-open-questions.md).

### `b2b-fleet-mgr-app`

- A second upstream alongside `/oracle/:oracleID/*` — say `/tenancy/*` — proxied
  the same way, authenticated with the user's DIMO JWT.
- New **Customers** section: list, create, detail (Users / Vehicles / Settings
  tabs), open-in-fleet-lite.
- Vehicle assignment picker driven by the existing paged/filtered
  `GetFleetVehicles` view, **restricted to minted vehicles** — an unminted VIN
  has no token id and can't be entitled.
- A **fleet-lite visibility toggle** on the operator's own settings.
- SACD grantee defaults to the operator's client id; picker moves behind an
  Advanced expander (see [06-onchain-surface.md](06-onchain-surface.md)).
- Extend `oracle-tenant-service.ts` (or a sibling) with the operator tenant and the
  customer tenant currently being configured — note this is *configuration
  context*, not a tenant switch. Lands on top of the in-flight tenant flow work
  in `.planning/PROJECT.md`; sequence after it.

### `kaufmann-oracle`

- `kaufmann_oracle.tenants` keeps **only oracle-specific columns** (Kore
  credentials, command password, signer keypair). Identity and DIMO credentials
  move to tenancy-api. **No new FK column is needed** — because the migration
  reuses the existing UUIDs, `kaufmann_oracle.tenants.id` *is* the shared tenant
  id.
- `access` / `access_tenants` reads become `/v1/authz` calls.
- `user_profiles` identity fields fold into tenancy `users`.
- `NewDeveloperLicenseTenantResolver` resolves license → tenant via tenancy-api,
  and the `/api/v1` surface takes an explicit tenant scope.
