# Migration plan

Six phases. Each is independently shippable and independently revertible; no
phase requires the next one to be useful.

## The key trick: reuse the existing UUIDs

Both `fleet_lite_app.tenants.id` and `kaufmann_oracle.tenants.id` are already
`UUID PRIMARY KEY DEFAULT gen_random_uuid()`. **Backfill them into the new
service's `tenants.id` unchanged.**

That means:

- No foreign key anywhere in either app has to be re-keyed. `vehicles`,
  `fleet_groups`, `vehicle_fleet_groups`, `invitations`, `geofences`,
  `tco_settings`, `vins`, `commands`, `kore_sims`, `reports` all keep pointing at
  the same UUIDs.
- The `Tenant-Id` header keeps working, with the same values, in both apps and
  both frontends. No client change, no dual-header period.
- Rollback at any phase is "stop calling tenancy-api", not "restore the ids".

The one collision case is a company that exists as a tenant in *both* databases
(an operator whose own tenant also has a fleet-lite tenant). Enumerate these
before starting — there are few enough to handle by hand — and pick one id,
mapping the other in the app that has to change.

## Phase 0 — Stand up the service, no cutover

**Ship:** `fleet-tenancy-api` repo with the schema from
[03-tenancy-api-spec.md](03-tenancy-api-spec.md), the `/v1/authz` endpoint, the
DIMO token minter, Helm chart, CI.

**Backfill, one-way, idempotent, re-runnable:**

| Source | Target |
|---|---|
| `fleet_lite_app.tenants` | `fleet_tenancy.tenants` (`kind='customer'`, `parent_tenant_id=NULL`, `managed=false`) + `tenant_credentials` |
| `fleet_lite_app.tenant_users` | `fleet_tenancy.users` (wallet + email) + `memberships` (`role`, `scope_group_ids` ← `allowed_group_ids`, `last_login_at`) |
| `fleet_lite_app.invitations` | `fleet_tenancy.invitations` |
| `kaufmann_oracle.tenants` | `fleet_tenancy.tenants` (`kind='operator'`) + `tenant_credentials` (incl. signer) |
| `kaufmann_oracle.user_profiles` | `fleet_tenancy.users` |
| `kaufmann_oracle.access_tenants` | `fleet_tenancy.memberships` (`permissions`, `role` ← `is_admin ? 'admin' : 'member'`) |

Ciphertext copies straight across if `TENANT_SECRET_ENC_KEY` is shared between
the deployments; confirm that first, and if not, decrypt-and-re-encrypt in the
backfill job rather than sharing the key.

**Verify before trusting it.** kaufmann already has the pattern —
`internal/gateway/dq_mirror.go` and `read_metrics.go` instrument primary reads to
compare against a mirror. Do the same here: both apps call `/v1/authz` in
parallel with their existing check, compare, emit a mismatch metric, and serve
the existing answer. Run until the mismatch rate is flat at zero.

**Exit:** backfill re-runs clean, shadow comparison shows zero mismatches over a
sustained window.

## Phase 1 — fleet-lite reads tenancy-api

**Ship:**

- `TenantService` becomes a tenancy-api client behind the same interface, keeping
  the `go-cache` layer.
- `NewTenantMiddleware` uses `/v1/authz`.
- Membership and invitation writes go to tenancy-api; local tables are
  dual-written as a fallback cache.
- Behind a feature flag, per environment.

**Not yet:** hierarchy, delegation, entitlements. Every tenant is still unparented
and syncs from its own license. Behaviour is identical from the outside.

**Exit:** flag on in prod, local tables demonstrably unread on the hot path.

## Phase 2 — hierarchy, delegation, entitlements

**Ship:**

- `parent_tenant_id`, `tenant_delegations`, `vehicle_entitlements` in active use.
- `entitlement_mode` (implicit for operator/self-serve, explicit for managed
  customers) and `fleet_lite_enabled`.
- fleet-lite's `SyncVehicles` splits into a **master pass per operator** plus
  cheap per-customer materialisation from entitlements, with batched upserts.
- Work tiering: only vehicles entitled to a fleet-lite-visible tenant get location
  refresh, group sync and telemetry. Unassigned inventory is identity-metadata
  only. Extends the existing warm/cold tiering in `docs/GROUP_SYNC.md` along the
  entitlement dimension.
- Effective-credential resolution (customer → parent) in the token endpoint.
- `AssertEntitled` choke point, plus the cross-tenant isolation test suite.
- **Prerequisite, must land first:** tenant-embedded fleet-group ids
  (`<tenant-uuid>_<slug>`) in both apps, with legacy bare-slug tolerance on read
  and an attestation republish pass. Without it, two customers under one operator
  collide on group slugs and their group attestations merge. See
  [02-target-architecture.md](02-target-architecture.md#fleet-group-ids-carry-their-tenant).

**Self-serve tenants are unaffected** — no parent means "entitled to everything my
own license is privileged on", which is exactly today's code path. This is the
coexistence guarantee, and it should have its own test.

**Exit:** a test operator tenant with two test customer tenants, each seeing only its
own vehicles, verified end to end.

## Phase 3 — the operator console

**Ship in `b2b-fleet-mgr-app`:**

- `/tenancy/*` upstream alongside `/oracle/:oracleID/*`, same proxy pattern.
- **Customers** list: name, status, vehicle count, user count, last activity.
- **Create customer**: name, optional own license.
- **Customer detail**:
  - *Users* — provision by email, set role + group scope, resend invite, revoke.
  - *Vehicles* — assign/unassign from the existing paged `GetFleetVehicles`
    view, **minted vehicles only**; bulk-assign by operator fleet group; drift
    banner with re-apply.
  - *Settings* — rename, suspend, credentials.
- **Operator settings** — `fleet_lite_enabled` toggle, with a nudge when the
  fleet outgrows fleet-lite.
- **SACD grantee default** in `add-vin-element.ts` — operator client id by
  default, picker behind an Advanced expander. Independently shippable and worth
  doing early; it's a correctness fix, not just tenancy plumbing.

Sequence this **after** the tenant-flow hardening milestone already in
`b2b-fleet-mgr-app/.planning/PROJECT.md`; both touch `oracle-tenant-service.ts`
and the app shell, and doing them concurrently will conflict.

**Exit:** an operator can create a customer, add a user, assign vehicles, and
that user can log into fleet-lite and see exactly those vehicles — without
anyone touching a database.

## Phase 4 — kaufmann cutover

**Ship:**

- `kaufmann_oracle.tenants` sheds its identity and DIMO credential columns; Kore
  credentials, command password and signer keypair stay (signer may move with the
  credential record — decide during the phase). **No FK column is added** — the
  UUID reuse above means `kaufmann_oracle.tenants.id` is already the shared
  tenant id.
- `NewAccessMiddleware` / `ResolveTenantAccess` call `/v1/authz`.
- `user_profiles` identity fields read from tenancy `users`; government-id
  columns stay put.
- `NewDeveloperLicenseTenantResolver` resolves via
  `/v1/resolve/client-id/{clientId}`, and `/api/v1` takes an explicit tenant scope
  so a shared operator license can't return the whole fleet.

**Exit:** kaufmann has no local authorization decisions left.

## Phase 5 — decommission

Drop `tenant_users`, `invitations`, `access_tenants` and the tenant identity
columns; remove dual-write; delete the shadow-comparison instrumentation.

Do this only after a full retention period has passed with no fallback reads —
the metric from Phase 0 is what tells you.

## Sequencing notes

- **Phases 1 and 4 are independent.** kaufmann can cut over before, after or
  alongside fleet-lite. If the operator console is the priority, Phase 4 can be
  deferred a long way.
- **Phase 2 is the only irreversible-feeling one**, because entitlement-driven
  sync changes what customers see. Keep the old sync path behind the flag for a
  full release cycle.
- **Phase 3 is where the value lands.** Phases 0–2 are plumbing that changes
  nothing an operator can see. Worth being honest about that in planning, and
  worth considering a thin vertical slice of Phase 3 earlier (one hardcoded
  operator, one customer) to validate the UX before building all the plumbing.
