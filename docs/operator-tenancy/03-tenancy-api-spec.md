# `fleet-tenancy-api` — proposed spec

Draft. Names and shapes are a starting point for review, not a contract.

## Stack

Mirror what both apps already do so the code is portable between them:

- Go, Fiber v2, zerolog
- goose migrations, sqlboiler models, `stretchr/testify`, `go.uber.tenant/mock`,
  testcontainers-go for DB-dependent tests
- `github.com/DIMO-Network/shared/pkg/db`, `.../dimoauth`,
  `.../middleware/metrics`
- Layout: `api/internal/{app,config,controllers,gateway,models,service,db}`
- Helm chart under `charts/`, Prometheus on the monitoring port

## Data model

### `tenants`

```sql
CREATE TABLE tenants (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name          TEXT NOT NULL,
    kind          TEXT NOT NULL,            -- 'operator' | 'customer'
    parent_tenant_id UUID REFERENCES tenants (id) ON DELETE RESTRICT,
    status        TEXT NOT NULL DEFAULT 'active',   -- active | suspended
    managed       BOOLEAN NOT NULL DEFAULT FALSE,   -- operator-managed
    -- 'implicit' = everything the effective dev license is privileged on
    --              (operator tenants, self-serve tenants)
    -- 'explicit' = the vehicle_entitlements rows written by the operator
    entitlement_mode TEXT NOT NULL DEFAULT 'explicit',
    -- Does this tenant appear as a selectable fleet in fleet-lite-app? Operators
    -- with thousands of vehicles turn this off; fleet-lite targets sub-500.
    fleet_lite_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    external_ref  TEXT,                     -- operator's own customer id, if any
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT operator_has_no_parent CHECK (kind <> 'operator' OR parent_tenant_id IS NULL)
);
CREATE INDEX idx_tenants_parent ON tenants (parent_tenant_id);
CREATE UNIQUE INDEX idx_tenants_name_per_parent
    ON tenants (parent_tenant_id, lower(name)) WHERE parent_tenant_id IS NOT NULL;
```

Name uniqueness is **per operator**, not global — kaufmann's current
`name TEXT NOT NULL UNIQUE` is too strict once many operators exist. Unparented
tenants are exempt (customers may legitimately share a name).

`managed` is separate from `parent_tenant_id IS NOT NULL` so an operator can hand a
customer full self-management later without breaking the reporting hierarchy.

`entitlement_mode` defaults to `explicit` but is set to `implicit` when the tenant
is created as an operator or as an unparented self-serve tenant. Storing it rather
than deriving it from `kind` leaves room for an operator that wants an explicitly
curated fleet-lite view of its own fleet.

`fleet_lite_enabled` is only meaningful for tenants a user could plausibly open in
fleet-lite. It defaults on, which is right for a small operator and wrong for a
large one — the console should surface it. Note that a **finer lever already
exists**: `memberships.scope_group_ids` can restrict an operator user to a few
fleet groups inside fleet-lite without hiding the tenant entirely.

### `tenant_credentials`

```sql
CREATE TABLE tenant_credentials (
    tenant_id           UUID PRIMARY KEY REFERENCES tenants (id) ON DELETE CASCADE,
    dimo_client_id   TEXT,
    dimo_api_key_enc TEXT,            -- AES-256-GCM, key = sha256(TENANT_SECRET_ENC_KEY)
    signer_address   VARCHAR(43),
    signer_key_enc   TEXT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX idx_tenant_credentials_client_id
    ON tenant_credentials (lower(dimo_client_id)) WHERE dimo_client_id IS NOT NULL;
```

Same encryption scheme both apps already use, so backfill is a straight copy of
ciphertext when `TENANT_SECRET_ENC_KEY` is shared — no decrypt/re-encrypt step.

The unique index on `dimo_client_id` is what makes developer-license →
tenant resolution unambiguous. kaufmann's current resolver takes
`qm.Limit(1)` and comments that duplicates "shouldn't happen, but the data model
allows it"; here it can't.

Signer material lives with the credential because it's the same
"identity this tenant acts as" concept, and kaufmann's shared-account flow already
pairs them.

### `users`

```sql
CREATE TABLE users (
    wallet        VARCHAR(43) PRIMARY KEY,   -- EIP-55 checksummed
    email         TEXT,
    first_name    TEXT,
    last_name     TEXT,
    phone         TEXT,
    business_name TEXT,
    shared_account_signer_address VARCHAR(43),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_users_email ON users (lower(email));
```

Superset of kaufmann's `user_profiles` minus the government-id columns, which
should stay in the oracle — they're KYC data for a specific onboarding
programme, not general identity, and there's no reason to widen their blast
radius.

Wallets are stored checksummed. `kaufmann-oracle/internal/service/access.go`
carries a large in-line repair routine for historically non-checksummed rows;
the backfill normalises once and a `CHECK`/trigger keeps it that way, so that
code doesn't get inherited.

### `memberships`

```sql
CREATE TABLE memberships (
    tenant_id            UUID NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    wallet            VARCHAR(43) NOT NULL REFERENCES users (wallet) ON DELETE CASCADE,
    role              TEXT NOT NULL DEFAULT 'member',   -- owner | admin | member
    permissions       JSONB NOT NULL DEFAULT '[]'::jsonb,
    scope_group_ids   TEXT[],                -- NULL = unrestricted within the tenant
    granted_by_tenant_id UUID REFERENCES tenants (id),
    granted_by_wallet VARCHAR(43),
    last_login_at     TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, wallet)
);
CREATE INDEX idx_memberships_wallet ON memberships (wallet);
```

**`permissions` is authoritative; `role` is a label and a preset.** Every
authorization check reads `permissions`. `role` exists so a members list can say
"Owner" and so adding a member can fill capabilities from a template — it is
never checked directly. See
[02-target-architecture.md](02-target-architecture.md#one-authz-model-shared).

Capability strings: `manage_members`, `manage_settings`, `onboard_vehicles`,
`reports`. App-specific ones are fine — fleet-lite never checks
`onboard_vehicles`.

**`view_all_fleets` is deliberately absent.** It duplicates
`scope_group_ids IS NULL`, and storing one fact twice has no defined resolution
when they disagree. `scope_group_ids` is the single mechanism and is strictly
more expressive; kaufmann's migration maps `view_all_fleets` →
`scope_group_ids = NULL` and derives the capability if it needs to report it.

`granted_by_tenant_id` is the audit trail for "the operator added this user", and
lets the customer-facing UI distinguish members they manage from members the
operator manages.

`last_login_at` moves here from fleet-lite's `tenant_users`; the group-sync cron
tiering of the retired GROUP_SYNC design depends on it, so tenancy-api needs to expose it.

### `tenant_delegations`

```sql
CREATE TABLE tenant_delegations (
    operator_tenant_id UUID NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    customer_tenant_id UUID NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    scopes          TEXT[] NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by_wallet VARCHAR(43),
    PRIMARY KEY (operator_tenant_id, customer_tenant_id)
);
```

Scopes: `manage_members`, `manage_vehicles`, `manage_settings`.

**Management only — a delegation never grants a fleet-lite session.** Operator
staff are b2b-only; there are no impersonation scopes. See
[02-target-architecture.md](02-target-architecture.md#no-impersonation-operator-staff-are-b2b-only).

A row is created automatically when an operator provisions a customer, carrying
all three scopes. Authorization always checks the row, never `parent_tenant_id`
directly — so revoking a delegation is a single delete, and future
operator-of-operator arrangements need no schema change.

### `vehicle_entitlements`

```sql
CREATE TABLE vehicle_entitlements (
    tenant_id            UUID NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    vehicle_token_id  BIGINT NOT NULL,
    source            TEXT NOT NULL,       -- operator | sacd | import
    source_group_id   TEXT,                -- provenance for group bulk-assign
    granted_by_tenant_id UUID REFERENCES tenants (id),
    granted_by_wallet VARCHAR(43),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    revoked_at        TIMESTAMPTZ,
    PRIMARY KEY (tenant_id, vehicle_token_id)
);
CREATE INDEX idx_vehicle_entitlements_token ON vehicle_entitlements (vehicle_token_id);
```

Rows exist **only for `explicit`-mode tenants**. Operator and self-serve tenants
resolve their fleet from the license's privileged set and have no rows at all —
writing one row per vehicle for an operator with thousands of them would be
pure overhead.

`source_group_id` refers to an **operator-side** fleet group (kaufmann's
`fleet_groups`), used purely to select vehicles at assign time. It is provenance,
not a cross-tenant link; the customer's own groups are separate and theirs.

**Exclusivity invariant:** a vehicle token may have at most one active
entitlement among the **explicit-mode** tenants of a given operator. Enforced in the
service layer (it needs the parent lookup, so a partial unique index won't
express it), and covered by a test. This is what makes fleet-lite's per-tenant
`vehicles` rows safe. The operator implicitly sees every vehicle including
assigned ones — that's intended, not a violation.

Only **minted** vehicles can be entitled: entitlement is keyed by
`vehicle_token_id`, which an unminted VIN doesn't have. The console's assign
picker must filter accordingly.

`revoked_at` rather than a hard delete: knowing a vehicle *used to* belong to a
customer matters for support and for cleaning up their cached rows.

### `invitations`

Ported from `fleet-lite-app/internal/db/migrations/20260617120000_invitations.sql`
plus `20260708190000_invitation_email_tracking.sql`, with `tenant_id` → `tenant_id`
and an added `created_by_tenant_id` so operator-sent invites are distinguishable.
Behaviour — single-use, SHA-256-hashed token, wallet unknown until accept,
Postmark delivery tracking — unchanged.

## HTTP surface

Two audiences, two auth schemes.

### Service-to-service (`/v1/...`)

Callers: `fleet-lite-app` api, `kaufmann-oracle`, `b2b-fleet-mgr-app` proxy.
Auth: DIMO developer-license JWT verified against DIMO JWKS, matched to an
`tenant_credentials.dimo_client_id` — the same pattern as
`kaufmann-oracle/internal/app/api_auth.go`. Cluster-internal only.

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/v1/authz?wallet=&tenant_id=` | **The hot path.** Role, permissions, scope, `via: direct\|delegation` |
| `GET` | `/v1/tenants?wallet=&surface=` | Tenants a wallet can act in, direct + delegated, each flagged. `surface=fleet_lite` filters out tenants with `fleet_lite_enabled = false`; `surface=b2b` returns operator tenants only |
| `GET` | `/v1/tenants/{id}` | Tenant detail incl. `kind`, `parent_tenant_id`, `managed`, `entitlement_mode`, `fleet_lite_enabled` |
| `GET` | `/v1/tenants/{id}/dimo-token` | Short-lived DIMO developer JWT for the tenant's **effective** credential |
| `GET` | `/v1/tenants/{id}/entitlements` | Active entitled vehicle token ids (paged; `?since=` for deltas). For implicit-mode tenants returns `{"mode":"implicit"}` and no list — the caller enumerates from the license instead |
| `GET` | `/v1/operators/{id}/children` | Customer tenants under an operator, with counts. Backs the console list and the per-operator sync pass |
| `GET` | `/v1/tenants/{id}/members` | Membership list |
| `POST` | `/v1/tenants/{id}/members/{wallet}/login` | Touch `last_login_at`, capture email |
| `GET` | `/v1/resolve/client-id/{clientId}` | Developer license → tenant (replaces kaufmann's resolver) |

### User-facing (`/user/v1/...`)

Callers: browsers, via the b2b proxy or fleet-lite's api.
Auth: the end user's DIMO JWT (same JWKS), authorized against memberships and
delegations.

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/user/v1/tenants?surface=` | The caller's tenants. `surface=fleet_lite` returns direct memberships only; `surface=b2b` includes tenants reachable by delegation |
| `POST` | `/user/v1/tenants` | Create a customer tenant (operator: under self; self-serve: unparented) |
| `PATCH` | `/user/v1/tenants/{id}` | Rename, suspend, resume, toggle `fleet_lite_enabled` |
| `PUT` | `/user/v1/tenants/{id}/credentials` | Set/replace a DIMO license (validated by minting a JWT before persisting, as `TenantsController.CreateTenant` does today) |
| `GET`/`POST`/`DELETE` | `/user/v1/tenants/{id}/members[/{wallet}]` | Membership CRUD |
| `POST` | `/user/v1/tenants/{id}/members/provision` | **Operator on-behalf provisioning**: `{email, role, scopeGroupIds}` → accounts-api lookup-or-create → user + membership |
| `GET`/`POST`/`DELETE` | `/user/v1/tenants/{id}/invitations[/{id}]` | Invitation CRUD |
| `POST` | `/user/v1/invitations/accept` | Accept (JWT-authenticated, token-authorized, no tenant in path) |
| `GET` | `/user/v1/tenants/{id}/vehicles` | Entitlements with provenance |
| `POST` | `/user/v1/tenants/{id}/vehicles` | Assign: `{tokenIds[]}` or `{fromGroupId, tokenIds[]}` |
| `DELETE` | `/user/v1/tenants/{id}/vehicles/{tokenId}` | Revoke |
| `GET` | `/user/v1/tenants/{id}/vehicles/drift` | Vehicles added to a previously-assigned group since assignment |
| `POST` | `/user/v1/tenants/{id}/vehicles/reapply-group` | Re-expand a group assignment |

Group *expansion* happens in the caller (b2b resolves group → token ids against
the oracle, fleet-lite against its own tables) and arrives here as a token id
list plus `fromGroupId`. tenancy-api records provenance but stays free of
fleet-domain concepts.

## `/v1/authz` response

```json
{
  "tenantId": "0f3c…",
  "wallet": "0xAbC…",
  "member": true,
  "role": "member",
  "permissions": ["onboard_vehicles", "reports"],
  "scopeGroupIds": ["vans", "north-region"],
  "via": "direct",
  "operatorTenantId": null,
  "tenantStatus": "active",
  "cacheTtlSeconds": 60
}
```

`scopeGroupIds: null` means unrestricted — matching fleet-lite's existing
convention where a NULL `allowed_group_ids` is full access.

When `via: "delegation"`, `operatorTenantId` names the operator and `permissions`
is derived from the delegation scopes, not from a membership row. This is
meaningful to b2b (the operator is managing that customer) and **fleet-lite
should refuse it outright** — a delegation is never a fleet session.

## Non-functional

- **Latency budget.** `/v1/authz` is called on every request in two apps. Target
  p99 < 10ms with an in-process cache in each caller (30–60s) plus a
  service-side cache. It is a two-table lookup; it should be a single query.
- **Availability.** tenancy-api down means both apps are down unless callers
  serve stale. Callers keep the last good authz answer for a bounded staleness
  window (5 min) and log loudly. Revocation is therefore eventually consistent
  by up to that window — acceptable, and it should be written down where
  operators can see it.
- **Audit.** Every write records actor wallet + actor tenant. Operator actions on a
  customer tenant are the ones that will get questioned; they need a trail from day
  one, not retrofitted.
- **Metrics.** Prometheus on the monitoring port. Beyond the golden signals:
  authz cache hit rate, delegated-vs-direct authz ratio, entitlement grants and
  revocations by operator.
