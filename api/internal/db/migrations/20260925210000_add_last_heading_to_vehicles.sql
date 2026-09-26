-- +goose Up
-- +goose StatementBegin
SELECT 'up SQL query';

-- Heading (degrees clockwise from true north, 0-360) of the vehicle's most
-- recent GPS fix, written through alongside last_lat/last_lon by the telemetry
-- fan-out. Lets the map draw direction markers on the instant DB-seeded first
-- paint. NULL when the vehicle doesn't report currentLocationHeading.
ALTER TABLE IF EXISTS vehicles
    ADD COLUMN IF NOT EXISTS last_heading DOUBLE PRECISION;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 'down SQL query';

ALTER TABLE IF EXISTS vehicles
    DROP COLUMN IF EXISTS last_heading;

-- +goose StatementEnd
