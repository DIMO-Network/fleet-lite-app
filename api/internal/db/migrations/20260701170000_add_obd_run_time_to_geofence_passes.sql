-- +goose Up
-- +goose StatementBegin
SELECT 'up SQL query';

-- Engine-on time accrued during a geofence pass, in seconds: obdRunTime (OBD
-- "run time since engine start") at the last in-geofence sample minus the first.
-- Distinct from dwell_s, which is wall-clock time inside the polygon whether the
-- engine was running or the vehicle was parked. Nullable: absent when the
-- vehicle doesn't report obdRunTime, or when the value is unreliable (engine
-- restarted mid-pass, resetting the counter). See docs/GEOFENCES_PLAN.md.
-- Existing cached passes keep NULL and backfill on the next fresh scan.
ALTER TABLE IF EXISTS geofence_passes
    ADD COLUMN IF NOT EXISTS obd_run_time_s DOUBLE PRECISION;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 'down SQL query';

ALTER TABLE IF EXISTS geofence_passes
    DROP COLUMN IF EXISTS obd_run_time_s;

-- +goose StatementEnd
