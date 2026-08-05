# Risks and open questions

## Prerequisites

### R1 — Fleet-group ids collide across tenants — *fix chosen*

**Blocks Phase 2.**

`fleet_groups.id` is `slug(name)` and a **global** `TEXT PRIMARY KEY`
(`internal/db/migrations/20260608120000_fleet_groups.sql`), while the intended
uniqueness is `UNIQUE (tenant_id, name)`. `FleetGroupService.CreateGroup`
(`internal/service/fleet_group.go:134`) inserts `ID: slug(name)` and maps the
resulting unique violation to `ErrGroupNameExists`.

Already wrong today: the second tenant to create "Vans" is told the name is
taken, by a group in another tenant they can't see. Masked only by low tenant
count. kaufmann has the same shape (`name TEXT NOT NULL UNIQUE`, slug PK).

It gets worse under a shared operator license, because `data.groups[].id` in the
`dimo.document.vehicle.groups` CloudEvent is the only field that can distinguish
customers — `source` is the operator's client id for everyone, and `producer` is
a per-app constant. Colliding ids would let `group_sync.go`'s reconcile merge two
customers' groups, and let one customer's removal drop the other's membership.

**Fix: embed the tenant uuid in the id — `<tenant-uuid>_<slug>`.** Solves the
primary-key collision and the attestation attribution together, and needs **no
CloudEvent schema change**. Full detail, format notes and the migration path
(including legacy bare-slug tolerance on read) in
[02-target-architecture.md](02-target-architecture.md#fleet-group-ids-carry-their-tenant).

**Execution plan: [07-r1-group-id-migration.md](07-r1-group-id-migration.md)** —
migrations for both repos, the FK `ON UPDATE CASCADE` detail, read-side
tolerance, republish, and the rollout order that keeps every CE readable
throughout.

The acceptance rule is **tenant-matching, not producer-matching**: a group
belongs to the tenant whose uuid prefixes its id, whoever published it. So an
operator tenant viewed in fleet-lite sees its b2b-created groups, a customer
tenant never sees the operator's, and cross-app visibility switches on by itself
once Phase 0 unifies tenant ids. Planning it exposed a data-loss path — enforcing
the rule before fleet-lite republishes would delete memberships that exist only
because another producer's CE created them — so enforcement is flag-gated behind
the republish.

Scope: this concerns **fleet-lite groups across customer tenants**. Operator-side
groups are used only to select vehicles at assign time and never propagate into a
customer tenant.

## Accepted risks

Consequences of the locked decisions. Listed so they're deliberate.

### R3 — Tenant isolation is enforced by our code, not the chain

Direct consequence of **D2** and **D5**. Every customer's vehicle data is
reachable with the operator's developer JWT. One controller that forgets an
entitlement check leaks one customer's telemetry to another.

This is the **intended architecture**, not a reluctant trade — see
[06-onchain-surface.md](06-onchain-surface.md). Access control that changes
weekly shouldn't need a passkey prompt and a transaction. On-chain does two
things (ownership, one SACD grant for enumeration); web2 decides who may look at
what.

That makes the mitigations below load-bearing rather than defensive. They are
the isolation mechanism, so they get built with the feature, not after it.

1. A single `AssertEntitled(ctx, tenantID, tokenID)` choke point; no gateway call
   takes a token id that didn't come from an entitlement-filtered query.
2. A table-driven cross-tenant isolation test over the full route list, so new
   routes are covered by default rather than by remembering.
3. kaufmann's `/api/v1` public surface must take an explicit tenant scope — under
   a shared license, resolving license → tenant is no longer unambiguous and
   would otherwise hand a caller the operator's entire fleet.
4. Entitlement denials logged and alerted on; a spike means a bug, not an attack.

### R4 — Snapshot group assignment drifts

Direct consequence of **D3**. Assigning group *Vans* to a customer expands to the
vehicles in it at that moment. Vehicles added later don't appear.

Traded for: no implicit grants. Auto-following would mean an operator adding a
vehicle to an internal group silently exposes it to a customer.

**Mitigation:** `source_group_id` provenance on every entitlement row, a drift
endpoint, and a console banner with one-click re-apply.

### R5 — Availability coupling

Both apps depend on tenancy-api for every request. It becomes a single point of
failure for two products.

**Mitigation:** in-process authz cache (30–60s) plus bounded stale-serve (5 min)
with loud logging. The cost: **membership revocation is eventually consistent by
up to the staleness window**, which needs documenting somewhere an operator will
read — "I removed them and they could still get in" is a support call otherwise.

Both apps already cache tenants for 24h, so this isn't a new class of problem,
just a more visible one.

### R6 — fleet-lite holds the whole operator fleet regardless of the toggle

`fleet_lite_enabled = false` stops fleet-lite **serving** an operator's thousands
of vehicles. It does not stop it **holding** them — every customer's slice is
computed from the operator's privileged set, so sync, the group-sync cron fan-out
and location refresh all scale with the total fleet, not the displayed one.

Easy to mis-assume the toggle solves this. It solves the UI half.

**Mitigation:** the master-pass + tiering design in
[02-target-architecture.md](02-target-architecture.md) — sync once per operator,
batch the upserts, and give expensive per-vehicle work only to vehicles entitled
to a fleet-lite-visible tenant. Unassigned inventory stays cheap.

Worth a load test against a realistic operator fleet before Phase 2 ships.

## Resolved

### Q1 — Does the operator's developer license scale? — **yes**

Pooling every customer's calls through one developer license is acceptable, and
the license can be adjusted to support the load.

Watch item, not a risk: if a single heavy customer degrades others, the
architecture already supports giving them their own license (a tenant with its
own `tenant_credentials` row resolves to itself instead of its parent).

### Q2 — Usage attribution and billing — **pooled by design**

Pooled credentials with no per-customer boundary is the intent. Operators bill
their customers themselves; **manual invoicing for now**, tooling later as
customer count grows.

Worth knowing: adding per-tenant accounting at the DIMO call sites is much
cheaper to do while those call sites are being touched in Phase 2 than to
retrofit later. Not a reason to build it now — a reason to leave a seam.

### Q3 — The operator's signer persists — **intended**

Accounts created on a customer's behalf register the operator's signer as
`providedSignerAddress`, letting the operator sign for those users indefinitely.

That's the point: it's what makes transferring vehicles *between* an operator's
customers easy, and if a car sells out of network the record is deleted.

**Later improvement:** a transfer path that doesn't depend on
`providedSignerAddress` — a different contract or a parameter change. Documented
here so it isn't lost; not scheduled.

### Q4 — Customer offboarding — **web2 revocation**

Vehicles are always minted from b2b with on-chain ownership held by the operator
(`kaufmann-oracle/internal/onboarding/onboard.go:286`). Customers never hold the
asset, so offboarding is: revoke entitlements (web2), or use the existing
`/vehicle/transfer` flows if they're taking vehicles with them. No per-vehicle
re-SACD exercise. Detail in [06-onchain-surface.md](06-onchain-surface.md).

### Q8 — Where does fleet-lite degrade? — **~400+, known and accepted**

No customer currently exceeds 500 vehicles and the target market is below that.
A ~400-vehicle fleet has been tested: noticeable frontend performance issues,
still usable.

**Not investing in optimisation now.** Set the console's `fleet_lite_enabled`
nudge around 500 as a soft warning. Revisit if the target market moves upward.

Note this is a *per-customer* limit and is unrelated to R6, which is about the
backend holding the operator's whole fleet.

### Q9 — Can the operator pre-create a customer's fleet groups? — **no**

The operator sets vehicle assignments only. Groups inside a customer tenant are
created and managed by the customer in fleet-lite. Keeps the boundary clean and
removes a chunk of console surface.

### Q10 — Can a vehicle be entitled to more than one tenant? — **no**

The exclusivity invariant holds: at most one explicit-mode tenant per operator.
No expected need for shared-pool or sub-leased vehicles. This is what keeps
fleet-lite's per-tenant `vehicles` rows safe.

### Q11 — Do operator staff use fleet-lite? — **no, b2b-only**

Operator staff work entirely in `b2b-fleet-mgr-app`. This removed impersonation
from the design — no `impersonate_*` scopes, no delegated fleet-lite sessions, no
"viewing as operator" banner, and `GET /tenants` returns direct memberships only.
See [02-target-architecture.md](02-target-architecture.md#no-impersonation-operator-staff-are-b2b-only).

Delegation survives for *management* only.

### Q5 — Adopt the capability-string permission model? — **yes, it fits**

Audited every role gate in fleet-lite. There are five, and they collapse to two
capabilities:

| fleet-lite gate | Capability |
|---|---|
| `AddMember`, `RemoveMember`, `UpdateMemberAccess` (`internal/controllers/tenants.go:227,257,289`) | `manage_members` |
| all invitation operations (`internal/controllers/invitations.go:46`) | `manage_members` |
| `UpdateSettings` — DIMO credentials (`internal/controllers/tenants.go:330`) | `manage_settings` |

Everything else in fleet-lite is gated on membership alone, not role. So the
"much simpler permissioning" turns out to be a two-capability subset of the
model kaufmann already has — a clean fit, not a forced one.

**Shared set:** `manage_members` (kaufmann's `manage_admin_users`, renamed),
`manage_settings` (new — kaufmann gated this on `is_admin` only),
`onboard_vehicles` and `reports` (kaufmann; fleet-lite's TCO could adopt
`reports` later). App-specific capabilities are fine; fleet-lite just never
checks `onboard_vehicles`.

**`role` stops being an authorization input** — it becomes a display label and a
preset for filling capabilities when adding a member. `permissions[]` is what
gets checked. Keeping both authoritative would be two sources of truth for one
decision.

**Two findings worth calling out:**

1. **`view_all_fleets` must not be a stored capability.** It means exactly what
   fleet-lite's `allowed_group_ids IS NULL` means. Store it both ways and there
   is no defined answer for a member holding `view_all_fleets` *and*
   `scope_group_ids = ['vans']`. `scope_group_ids` wins — it's also strictly
   more expressive, since a boolean can't say "these three groups". kaufmann's
   migration maps `view_all_fleets` → `scope_group_ids = NULL`, and the
   capability becomes derived, never stored.

2. **kaufmann gains a capability it doesn't have.** Tenant settings there are
   gated on `is_admin` alone, so `manage_settings` is new on that side — a
   tightening, and worth checking nobody depended on any admin being able to
   change credentials.

### Q6 — Service name — **`fleet-tenancy-api`**

DIMO's Go services in this tree overwhelmingly use `<domain>-api`
(`identity-api`, `telemetry-api`, `attestation-api`, `token-exchange-api`,
`devices-api`, `users-api`, `profiles-api`, `vehicle-events-api`,
`vehicle-triggers-api`). There is no `-service` suffix anywhere, so
`fleet-tenant-service` would have been the odd one out. "Tenancy" also covers
tenants + memberships + entitlements in a way "tenants" alone undersells.

Rejected: `fleet-accounts-api` — collides with DIMO's `accounts` and
`profiles-api` (whose README still says "accounts-api" at the top).

**And one word throughout.** The schema says `tenants` / `parent_tenant_id`,
matching both codebases and the `Tenant-Id` header. An earlier draft of these
docs said *organization* to disambiguate during the survey; it's been renamed
away.

### Q7 — Doc locations — **decided, needs doing**

Docs move into the new service's repo when it exists, and each of the three
existing repos keeps a pointer — both for humans and so agents entering a repo
via `AGENTS.md` find the tenancy model without hunting.

**Claude Code reads `CLAUDE.md`, not `AGENTS.md`** — confirmed against the docs.
So the `AGENTS.md` files in fleet-lite-app and kaufmann-oracle were never being
loaded by anyone using Claude Code, tenancy content or not.

Fixed by adding a one-line `CLAUDE.md` to each repo containing `@AGENTS.md`,
which is the documented import syntax. Both tools now read the same file with no
duplication, and Claude-specific instructions can be appended below the import
if ever needed. (A `ln -s AGENTS.md CLAUDE.md` symlink also works but breaks on
Windows without Developer Mode.)

Done so far: pointer docs in all three repos, `AGENTS.md` tenancy sections in
fleet-lite-app and kaufmann-oracle, a new `AGENTS.md` for b2b-fleet-mgr-app
(which had none), and `CLAUDE.md` importers in all three.

Anyone can verify with `/context` — `CLAUDE.md` should appear under **Memory
files**.

⚠️ **`kaufmann-oracle/.gitignore` line 35 ignores `CLAUDE.md`.** The importer
exists locally but won't be committed, so coworkers there still won't load
`AGENTS.md`. Someone added that line deliberately — presumably to keep personal
notes out of the repo — but a one-line `@AGENTS.md` importer is shared team
config, not personal. Either drop the ignore rule (and use `CLAUDE.local.md`,
which is the documented spot for personal notes, if that was the concern), or
accept that kaufmann's `AGENTS.md` stays unread by Claude Code for everyone but
you. Needs a call from whoever added it.

Remaining: move the doc set into the new repo at Phase 0 and update the three
pointers to reference it.

## Open

**Nothing blocking.** Every question raised during design has been answered.

What remains is execution risk, tracked above as R1 (the group-id fix, which
must land before Phase 2) and R3–R6 (accepted consequences of the locked
decisions, each with mitigations that are part of the build rather than
follow-ups).

Two items are deliberately deferred rather than open:

- **Per-tenant usage accounting** (Q2) — not needed while invoicing is manual,
  but leave a seam at the DIMO call sites in Phase 2 rather than retrofitting.
- **Transfer without `providedSignerAddress`** (Q3) — a later contract-level
  improvement, unscheduled.
