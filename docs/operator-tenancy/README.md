# Operator-Managed Multi-Tenancy

Status: **design / pre-implementation**. Written 2026-08-04.

## The end goal

Today `fleet-lite-app` and `kaufmann-oracle` each have their own, unrelated
multi-tenant system, and a customer sets up their own fleet-lite tenant with
their own DIMO developer license.

We want an **operator-managed** model instead:

- An **operator** (e.g. Kaufmann, or DIMO's own fleet ops) works in
  `b2b-fleet-mgr-app`.
- From there they **create and configure customer tenants**, which is what the
  customer sees when they log into `fleet-lite-app`.
- The operator **manages that customer's users on their behalf** — creating
  accounts, setting roles, scoping access.
- The operator **controls which vehicles** each customer tenant can see.
- `b2b-fleet-mgr-app` becomes the operator console; `fleet-lite-app` is the
  end-customer product.

## Documents

| Doc | What's in it |
|---|---|
| [01-current-state.md](01-current-state.md) | What exists today across the three repos, side by side, and the (very thin) coupling between them |
| [02-target-architecture.md](02-target-architecture.md) | The target shape: a shared tenancy service, the operator/customer hierarchy, how vehicle access and credentials work |
| [03-tenancy-api-spec.md](03-tenancy-api-spec.md) | Proposed data model and HTTP surface for the new service |
| [04-migration-plan.md](04-migration-plan.md) | Phased rollout from today to the target, with coexistence for existing self-serve tenants |
| [05-risks-and-open-questions.md](05-risks-and-open-questions.md) | What can bite us, what's still undecided |
| [06-onchain-surface.md](06-onchain-surface.md) | What stays on chain and why, the SACD grantee default, customer offboarding |
| [07-r1-group-id-migration.md](07-r1-group-id-migration.md) | **Execution plan** for the fleet-group id fix — migrations, FK cascades, republish, rollout order |

## Decisions locked

D1–D4 were decided up front; D5–D9 fell out of working through the
implications. The rest of the design follows from them.

| # | Decision | Consequence |
|---|---|---|
| **D1** | **A new shared tenancy service** is the source of truth for tenants, users and memberships — not fleet-lite, not kaufmann-oracle | Both apps become clients. Biggest up-front cost, but it's the only option that stays oracle-agnostic and doesn't make fleet-lite depend on one specific oracle |
| **D2** | **Shared operator developer license** + app-level vehicle scoping | Customer tenants read DIMO data under the operator's license. Which vehicles a customer sees is a database entitlement the operator controls, not an on-chain SACD change. Reassignment is cheap; **isolation is enforced by our code, not by the chain** |
| **D3** | Vehicles are assigned **per vehicle**, with **fleet groups as bulk shorthand** | The entitlement table is keyed by vehicle token id. Assigning a group expands to rows, with provenance recorded so drift can be re-applied |
| **D4** | Users get in **both ways**: operator provisions directly, and the existing email-invitation flow stays | Operator can pre-create an account + wallet via accounts-api and have the customer just log in; customers can still invite their own members |
| **D5** | **On-chain does two things: ownership and one SACD grant.** Everything else is web2 | Vehicles are always minted from b2b with on-chain ownership held by the **operator's** account (already true today). The SACD grant exists so the operator's license can enumerate its fleet from identity-api — not for per-customer authorization. **Sub-tenants get no SACD grants.** Customer access, revocation and offboarding are database operations |
| **D6** | **The two apps are different surfaces, not two views of one thing** | b2b shows the operator **every** vehicle and configures sub-tenants from the outside — you never "switch into" a sub-tenant there. fleet-lite shows one tenant's slice, map-first, tuned for sub-500 fleets. An operator tenant can appear in fleet-lite too, controlled by **`fleet_lite_enabled`** (default on, turned off when the fleet outgrows it) |
| **D7** | **Operator staff are b2b-only** — no impersonation | No delegated fleet-lite sessions, no `impersonate_*` scopes, no "viewing as operator" banner. Delegation exists for *management* only. A real simplification, and it removes a class of "whose data am I looking at" confusion |
| **D8** | **Fleet group ids embed their tenant**: `<tenant-uuid>_<slug>` | Fixes a live primary-key collision *and* makes group attestations attributable under a shared operator license — with no CloudEvent schema change. The tenant in the id is whoever created the group (operator in b2b, customer in fleet-lite), which is what makes cross-app group import possible later |
| **D9** | **One authz model: capability strings.** `permissions[]` is checked; `role` is a label + preset | fleet-lite's five owner-gates collapse to `manage_members` + `manage_settings`, a clean subset of what kaufmann already has. Group scope stays as `scope_group_ids` data — `view_all_fleets` is derived, never stored, since one fact stored twice has no defined resolution |

## The one-paragraph version

Stand up `fleet-tenancy-api`: it owns tenants (with an operator → customer
parent link), users, memberships, delegations and vehicle entitlements, and it
answers one hot question for both apps — *"what may this wallet do in this
tenant?"*. It also mints DIMO developer JWTs so credentials never leave it.
`fleet-lite-app` stops keeping its own tenants/members and syncs vehicles from
the **operator's** privileged set filtered by entitlements.
`b2b-fleet-mgr-app` grows a Customers console that calls the tenancy service.
`kaufmann-oracle` keeps its oracle-specific tenant columns but gets its identity
and authorization answers from the same place.

## Naming — decided

The service is **`fleet-tenancy-api`**, and the thing both existing systems call
a *tenant* stays a **tenant** here: table `tenants`, parent link
`parent_tenant_id`, with `kind` (`operator` | `customer`) carrying the
distinction. One word from repo name to schema to `Tenant-Id` header to
conversation.

(An earlier draft of these docs said *organization* to tell the two existing
`tenants` tables apart during the survey. Three vocabularies for one concept was
a tax; that draft has been renamed away.)

The ids don't move either: existing tenant UUIDs are reused as-is, so the
`Tenant-Id` header and every foreign key keep working — see
[04-migration-plan.md](04-migration-plan.md).
