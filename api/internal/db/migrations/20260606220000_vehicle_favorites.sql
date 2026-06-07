-- +goose Up
-- +goose StatementBegin
SELECT 'up SQL query';

-- Vehicles a tenant has marked as favorites, pinned to the top of the vehicle
-- list in the UI. Scoped by tenant (= "account"), shared across its members,
-- mirroring the `vehicles` table's (tenant_id, token_id) scoping.
CREATE TABLE IF NOT EXISTS vehicle_favorites (
    tenant_id  UUID NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    token_id   BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, token_id)
);

CREATE INDEX IF NOT EXISTS idx_vehicle_favorites_tenant_id ON vehicle_favorites (tenant_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 'down SQL query';

DROP TABLE IF EXISTS vehicle_favorites;

-- +goose StatementEnd
