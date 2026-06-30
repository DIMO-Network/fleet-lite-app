-- +goose Up
-- +goose StatementBegin
SELECT 'up SQL query';

-- When we last actually fetched this vehicle's location from telemetry-api
-- (a real fan-out query, NOT a cache serve). Distinct from last_seen, which is
-- the GPS fix's own timestamp: a parked vehicle's last_seen can be hours old
-- even though location_pulled_at is seconds old. Drives the freshness window —
-- the map skips re-pulling a vehicle fetched within the last few minutes and
-- renders it from the last_lat/last_lon cache instead. Nullable: NULL means we
-- have never attempted a pull for this vehicle. See docs/LOCATION_REFRESH_PLAN.md.
ALTER TABLE IF EXISTS vehicles
    ADD COLUMN IF NOT EXISTS location_pulled_at TIMESTAMPTZ;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 'down SQL query';

ALTER TABLE IF EXISTS vehicles
    DROP COLUMN IF EXISTS location_pulled_at;

-- +goose StatementEnd
