-- +goose Up
-- +goose StatementBegin
SELECT 'up SQL query';

-- Display cache of the vehicle's most recent GPS fix. Written through whenever
-- the telemetry fan-out (TelemetryAPIService.FleetLocations) fetches a
-- vehicle's currentLocationCoordinates. Lets the map paint markers instantly
-- from Postgres on first load, before the live per-vehicle telemetry fan-out
-- reconciles them. last_seen is the timestamp of that GPS fix — used as the
-- "last seen" relative time in the vehicle list. All nullable: NULL means we
-- have never fetched a fix for this vehicle (no permission / no data / not yet
-- viewed).
ALTER TABLE IF EXISTS vehicles
    ADD COLUMN IF NOT EXISTS last_lat  DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS last_lon  DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS last_seen TIMESTAMPTZ;

-- Future-proofs the "list filtered to one tenant, ordered by recency" query:
-- a composite (tenant_id, last_seen DESC) lets Postgres satisfy the tenant
-- filter AND the ORDER BY from a single index scan (no separate sort step) as
-- per-tenant fleet sizes grow. NULLS LAST keeps never-seen vehicles at the
-- bottom of the list. (The bare tenant_id filter is already covered by the PK's
-- leading column; this index adds the ordered dimension.)
CREATE INDEX IF NOT EXISTS idx_vehicles_tenant_last_seen
    ON vehicles (tenant_id, last_seen DESC NULLS LAST);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 'down SQL query';

DROP INDEX IF EXISTS idx_vehicles_tenant_last_seen;
ALTER TABLE IF EXISTS vehicles
    DROP COLUMN IF EXISTS last_seen,
    DROP COLUMN IF EXISTS last_lon,
    DROP COLUMN IF EXISTS last_lat;

-- +goose StatementEnd
