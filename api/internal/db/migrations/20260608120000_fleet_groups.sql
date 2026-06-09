-- +goose Up
-- +goose StatementBegin
SELECT 'up SQL query';

-- A fleet group is a tenant-scoped, named, colored bucket of vehicles. The id
-- is a slug derived from the name at creation time; it is stable and is used as
-- the group id inside the per-vehicle group-membership attestation. Names are
-- unique per tenant (not globally) so different tenants may reuse the same name.
CREATE TABLE IF NOT EXISTS fleet_groups (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    color      VARCHAR(7) NOT NULL, -- HTML hex color like #FF5733
    tenant_id  UUID NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, name)
);

CREATE INDEX IF NOT EXISTS idx_fleet_groups_tenant_id ON fleet_groups (tenant_id);

-- Join table between a tenant's vehicles (keyed by on-chain token_id) and the
-- tenant's fleet groups. Composite FK to vehicles (tenant_id, token_id) keeps
-- membership consistent with the vehicle's tenant.
CREATE TABLE IF NOT EXISTS vehicle_fleet_groups (
    tenant_id      UUID   NOT NULL,
    token_id       BIGINT NOT NULL,
    fleet_group_id TEXT   NOT NULL REFERENCES fleet_groups (id) ON DELETE CASCADE,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, token_id, fleet_group_id),
    FOREIGN KEY (tenant_id, token_id) REFERENCES vehicles (tenant_id, token_id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_vehicle_fleet_groups_group ON vehicle_fleet_groups (fleet_group_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 'down SQL query';

DROP TABLE IF EXISTS vehicle_fleet_groups;
DROP TABLE IF EXISTS fleet_groups;

-- +goose StatementEnd
