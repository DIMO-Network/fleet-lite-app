-- +goose Up
-- +goose StatementBegin
SELECT 'up SQL query';

-- UUID generation (gen_random_uuid)
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- A tenant owns a DIMO developer license (client ID + encrypted API key) under
-- which all DIMO data calls (identity / telemetry / fetch / extract / attest)
-- are made. Secrets are AES-256-GCM encrypted with TENANT_SECRET_ENC_KEY.
CREATE TABLE IF NOT EXISTS fleet_lite_app.tenants (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name             TEXT NOT NULL,
    dimo_client_id   TEXT,
    dimo_api_key_enc TEXT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Many-to-many membership between wallets and tenants. A wallet can belong to
-- one or more tenants (created or invited). role is 'owner' or 'member'.
CREATE TABLE IF NOT EXISTS fleet_lite_app.tenant_users (
    tenant_id  UUID NOT NULL REFERENCES fleet_lite_app.tenants (id) ON DELETE CASCADE,
    wallet     VARCHAR(43) NOT NULL,
    role       TEXT NOT NULL DEFAULT 'member',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, wallet)
);

CREATE INDEX IF NOT EXISTS idx_tenant_users_wallet ON fleet_lite_app.tenant_users (wallet);

-- Vehicles synced from identity-api for a tenant: those the tenant's developer
-- license is privileged on (SACD-shared). Read by /vehicles, scoped by tenant.
CREATE TABLE IF NOT EXISTS fleet_lite_app.vehicles (
    tenant_id     UUID NOT NULL REFERENCES fleet_lite_app.tenants (id) ON DELETE CASCADE,
    token_id      BIGINT NOT NULL,
    owner_address TEXT,
    make          TEXT,
    model         TEXT,
    year          INT,
    definition_id TEXT,
    device_type   TEXT,
    imei          TEXT,
    serial        TEXT,
    minted_at     TIMESTAMPTZ,
    raw           JSONB,
    synced_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, token_id)
);

CREATE INDEX IF NOT EXISTS idx_vehicles_tenant_id ON fleet_lite_app.vehicles (tenant_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 'down SQL query';

DROP TABLE IF EXISTS fleet_lite_app.vehicles;
DROP TABLE IF EXISTS fleet_lite_app.tenant_users;
DROP TABLE IF EXISTS fleet_lite_app.tenants;

-- +goose StatementEnd
