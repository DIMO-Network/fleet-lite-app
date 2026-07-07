-- +goose Up
-- +goose StatementBegin
SELECT 'up SQL query';

-- Optional per-vehicle acquisition/depreciation inputs for the TCO report.
-- Scoped by tenant like vehicle_favorites. All purchase fields are nullable —
-- a vehicle with no row (or nulls) just shows operating costs, no
-- acquisition/depreciation line. See
-- docs/superpowers/specs/2026-07-06-tco-reporting-design.md.
CREATE TABLE IF NOT EXISTS vehicle_tco_settings (
    tenant_id         UUID NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    token_id          BIGINT NOT NULL,
    purchase_price    NUMERIC,
    purchase_date     DATE,
    useful_life_years INTEGER,
    currency          TEXT NOT NULL DEFAULT 'USD',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, token_id)
);

CREATE INDEX IF NOT EXISTS idx_vehicle_tco_settings_tenant_id ON vehicle_tco_settings (tenant_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 'down SQL query';

DROP TABLE IF EXISTS vehicle_tco_settings;

-- +goose StatementEnd
