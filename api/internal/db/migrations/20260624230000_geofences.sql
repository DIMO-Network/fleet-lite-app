-- +goose Up
-- +goose StatementBegin
SELECT 'up SQL query';

-- A geofence is a tenant-scoped, named polygon (GeoJSON) with an optional speed
-- limit. Definitions are attested at the tenant client-id level; this table is
-- the local cache + query index (see docs/GEOFENCES_PLAN.md). The id is a slug
-- derived from the name at creation time; it is stable and is used as the
-- geofence id inside attestations. Names are unique per tenant.
--
-- scope controls which vehicles the geofence applies to:
--   'all'    — every vehicle in the tenant (no per-vehicle rows)
--   'group'  — vehicles in any of group_ids (derived from vehicle_fleet_groups)
--   'manual' — vehicles explicitly listed in vehicle_geofences
CREATE TABLE IF NOT EXISTS geofences (
    id              TEXT PRIMARY KEY,
    tenant_id       UUID NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    color           VARCHAR(7) NOT NULL,            -- HTML hex color like #FF5733
    geometry        JSONB NOT NULL,                 -- GeoJSON Polygon
    area_m2         DOUBLE PRECISION NOT NULL DEFAULT 0,
    speed_limit_kph INTEGER,                         -- nullable; optional per geofence
    scope           TEXT NOT NULL DEFAULT 'all',     -- all | group | manual
    group_ids       TEXT[] NOT NULL DEFAULT '{}',    -- target fleet group ids when scope = 'group'
    created_by      VARCHAR(43) NOT NULL,            -- wallet (0x…) of the creator
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, name)
);

CREATE INDEX IF NOT EXISTS idx_geofences_tenant_id ON geofences (tenant_id);

-- Join table for scope = 'manual' only: explicit vehicle↔geofence assignments,
-- keyed by on-chain token_id. Composite FK to vehicles (tenant_id, token_id)
-- keeps assignments consistent with the vehicle's tenant.
CREATE TABLE IF NOT EXISTS vehicle_geofences (
    tenant_id   UUID   NOT NULL,
    token_id    BIGINT NOT NULL,
    geofence_id TEXT   NOT NULL REFERENCES geofences (id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, token_id, geofence_id),
    FOREIGN KEY (tenant_id, token_id) REFERENCES vehicles (tenant_id, token_id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_vehicle_geofences_geofence ON vehicle_geofences (geofence_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 'down SQL query';

DROP TABLE IF EXISTS vehicle_geofences;
DROP TABLE IF EXISTS geofences;

-- +goose StatementEnd
