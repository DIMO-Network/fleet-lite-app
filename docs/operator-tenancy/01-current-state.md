# Current state

A survey of `fleet-lite-app`, `b2b-fleet-mgr-app` and `kaufmann-oracle` as of
2026-08-04, focused on tenancy, users and vehicle access.

## The headline

**There are two complete, independent multi-tenant systems with the same name
and no runtime connection to each other.** A grep for cross-repo references
turns up nothing but comments noting that the two sides derive trip distance the
same way. The only things they actually share are the DIMO platform underneath
(identity-api, telemetry-api, attest/fetch-api, accounts-api) and a set of
conventions that were deliberately kept parallel.

## Side by side

|  | `kaufmann-oracle` (operator side, fronted by `b2b-fleet-mgr-app`) | `fleet-lite-app` (customer side) |
|---|---|---|
| Tenant table | `kaufmann_oracle.tenants` | `tenants` |
| Tenant columns | `name` (globally unique), `dimo_client_id`, `dimo_secret_enc`, `kore_client_id`, `kore_secret_enc`, `command_password`, `signer_address`, `signer_key_enc` | `name`, `dimo_client_id`, `dimo_api_key_enc` |
| User table | `user_profiles` (wallet PK; email, first/last name, phone, business name, government id, `shared_account_signer_address`) — renamed from `access` in `20260306145205` | none; identity is just the wallet on `tenant_users`, plus an `email` captured at login |
| Membership | `access_tenants (tenant_id, wallet)` with `permissions JSONB`, `is_admin`, `original_claim` | `tenant_users (tenant_id, wallet)` with `role` (`owner`/`member`), `allowed_group_ids TEXT[]`, `last_login_at`, `email` |
| Authorization model | capability strings — `manage_admin_users`, `view_all_fleets`, `onboard_vehicles`, `reports` (`internal/core/permissions.go`) | coarse role + per-member fleet-group scope (`docs/GROUP_ACCESS_PLAN.md`) |
| Tenant resolution | `Tenant-Id` header → `Access.ResolveTenantAccess` requires an `access_tenants` row with `is_admin = true` (`internal/app/access.go`, `internal/service/access.go`) | `Tenant-Id` header → `TenantService.GetMembership` (`internal/app/tenant.go`) |
| Vehicles | `vins`, keyed by IMEI, `tenant_id` nullable FK. Populated by the onboarding/mint pipeline | `vehicles`, PK `(tenant_id, token_id)`. Populated by `SyncVehicles` → `identity-api vehicles(filterBy: {privileged: <clientID>})` |
| Fleet groups | `fleet_groups` (slug PK, **globally unique name**) × `vin_fleet_groups` keyed by IMEI | `fleet_groups` (slug PK, name unique **per tenant**) × `vehicle_fleet_groups` keyed by token id |
| Secrets at rest | AES-256-GCM, key = `sha256(TENANT_SECRET_ENC_KEY)` | identical scheme, same env var name |
| Invitations | none — operator grants access directly | `invitations` table, single-use SHA-256-hashed token emailed via Postmark, wallet unknown until accept |

The encryption scheme, the `Tenant-Id` header, the `GetTenantByID` +
`patrickmn/go-cache` shape, and the AES helpers are near-identical copies in both
repos. That's convenient for consolidation.

## `b2b-fleet-mgr-app` is a BFF, not a backend

It has **no database and no migrations**. `api/internal/app/app.go` is one large
routing table where nearly everything is
`oracleApp.<verb>(path, genericProxyCtrl.Proxy)` under
`/oracle/:oracleID/*`, with `oracleIDMiddleware` validating the oracle id
against a configured list. The handful of non-proxy handlers exist to build
payloads for passkey signing (mint / transfer / disconnect / delete) and to talk
to accounts-api for OTP login.

So: **all operator-side tenant state lives in the oracle**, and the b2b app is
the UI plus a signing helper. Adding a second upstream (the tenancy service)
alongside the oracles is a natural extension of the pattern it already has.

The frontend keeps oracle + tenant selection in `localStorage` via
`web/src/services/oracle-tenant-service.ts`, and `.planning/PROJECT.md` shows an
in-flight milestone to harden exactly that flow (force a tenant selection before
the app shell, remove hardcoded Kaufmann defaults).

## How the two sides are coupled today (indirectly)

### 1. SACD grants chosen at mint time

`b2b-fleet-mgr-app/web/src/elements/add-vin-element.ts` builds the `SacdInput[]`
submitted with a mint. The available grantees come from the operator tenant's
own `dimo_client_id`, plus a free-text "use below" field. Permissions are a
bitmask over the standard DIMO privilege set, expiry is hardcoded to 40 years.

Whatever client id ends up as a grantee is exactly what a fleet-lite tenant
would later see, because fleet-lite's sync is
`FetchPrivilegedVehicles(tenant.ClientID)` →
`vehicles(filterBy: {privileged: clientID})`. Today the only way to get vehicles
into a customer's fleet-lite tenant is for someone to have typed that customer's
client id into that box at mint time (or to have SACD-shared afterwards).

`kaufmann-oracle` also has the reverse check: `POST /v1/fleet/vehicles/r1/sync`
(`internal/controllers/fleet_vehicles.go:1801+`) validates via identity-api that
a vehicle carries an unexpired SACD grant to the tenant's client id before
importing it into `vins`.

### 2. Group-membership attestations

Both sides publish per-vehicle `dimo.document.vehicle.groups` CloudEvents whose
`data.groups` is a list of `{id, name, color}` (`models.GroupRef`), and both read
them back from fetch-api.

- kaufmann: `internal/groupattest/worker.go`, a River job enqueued after any
  group mutation, coalesced per `(tenant, vehicle)`. ADR
  `docs/adr/0001-fleet-group-attestations.md`.
- fleet-lite: `internal/service/group_sync.go` plus the
  `import-group-attestations` CLI, run as daily-warm / weekly-full CronJobs. The
  reconcile takes the union of the latest CE **per producer** and applies
  removals only behind a freshness gate. See `docs/GROUP_SYNC.md`.

fleet-lite stamps `producer: "fleet-lite-app"` on its own writes. kaufmann's
writes carry the shared `dimo_client_id` as `source` and (historically) an empty
producer.

This is the closest thing to a real integration: an operator putting a vehicle
into a group in the b2b app will, within a day or a week, show up as that group
on the vehicle in fleet-lite — *provided* both are looking at the same vehicle
and the group ids happen to match.

## What already exists that the operator model needs

Three capabilities are already built and are the reason this is a smaller
project than it looks.

### Creating DIMO accounts on a user's behalf

`kaufmann-oracle/internal/gateway/accounts_api.go`:

- `GetAccount(email, walletAddress, developerJWT)` — presenting the tenant's
  developer JWT gets the extended response including `walletAddress`.
- `CreateAccount(email, providedSignerAddress, developerJWT)` — creates the
  account and registers the **tenant's signer wallet** as its provided signer.

`AccountController.GrantAdminAccess` (`internal/controllers/account.go:313`)
chains these: look the account up by email, create it if missing, upsert
`user_profiles`, upsert `access_tenants` with the requested permissions. That is
already "operator creates a user on the customer's behalf", just pointed at the
operator's own tenant.

### Proving the operator may act for that user

`internal/service/shared_signer.go` — `AssertTenantMaySignFor` checks that
`account.ProvidedSignerAddress == tenant.SignerAddress` before any irreversible
on-chain operation, and is re-asserted inside the River workers, not just at the
HTTP edge. Each tenant gets a freshly generated signer keypair at creation
(`TenantsController.Create`), stored encrypted.

### Machine-to-machine auth by developer license

`kaufmann-oracle/internal/app/api_auth.go` —
`NewDeveloperLicenseTenantResolver` verifies a DIMO developer-license JWT (same
issuer as user JWTs, so the existing JWKS middleware covers it) and resolves the
tenant by matching the `ethereum_address` claim against
`tenants.dimo_client_id`. It backs the public `/api/v1` surface
(`/vehicles`, `/fleet-groups`) with generated Swagger.

That's the exact pattern a service-to-service call into a shared tenancy service
should reuse.

## What's missing

- **No hierarchy anywhere.** No parent/child, no operator concept, no
  cross-tenant delegation. Every tenant in both systems is a peer.
- **No provisioning API between the apps.** Nothing in b2b or kaufmann can
  create or configure a fleet-lite tenant.
- **No operator view of customer users.** kaufmann's admin-accounts screens
  manage users *of the operator's own tenant*.
- **No vehicle→customer assignment**, other than choosing SACD grantees at mint.
- **fleet-lite tenant creation is self-serve only.** `onboard-tenant.ts` asks
  the user for a DIMO client id + API key, offering a popup that runs DIMO
  login's `PROVISION_DEVELOPER_LICENSE` entry state to generate one. The backend
  validates the credentials by minting a developer JWT before persisting
  (`TenantsController.CreateTenant`).

## Two latent bugs found while surveying

Both become materially worse under an operator with many customers, so they're
listed here as preconditions rather than incidental findings.

### fleet-lite fleet-group ids collide across tenants

`fleet_groups.id` is `slug(name)` and is a **global** `TEXT PRIMARY KEY`
(`internal/db/migrations/20260608120000_fleet_groups.sql`), but the uniqueness
constraint the code intends is `UNIQUE (tenant_id, name)`.
`FleetGroupService.CreateGroup` (`internal/service/fleet_group.go:134`) inserts
with `ID: slug(name)` and maps any unique violation to `ErrGroupNameExists`.

So the second tenant to create a group called "Vans" gets told the name is
already taken — by a group they cannot see, in another tenant. Under one
operator with fifty customers, common names ("vans", "trucks", "north") are
exhausted almost immediately.

### Group ids are the join key in attestations

`data.groups[].id` in the CloudEvent is that same slug. With a **shared operator
developer license** (decision D2) every customer's attestations carry the same
`source`, and fleet-lite's own writes carry the same `producer`. Two customers
with a "Vans" group would be indistinguishable in the attestation stream, and
the reconcile in `group_sync.go` would happily merge them.

Namespacing group ids per tenant is therefore a **prerequisite** for D2,
not a nice-to-have. See [05-risks-and-open-questions.md](05-risks-and-open-questions.md).
