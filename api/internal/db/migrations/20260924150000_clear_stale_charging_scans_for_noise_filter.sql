-- +goose Up
-- +goose StatementBegin
SELECT 'up SQL query';

-- The recharge-native-segments migration (charging_detection.go) dropped
-- the zero-energy/short-duration noise filter, wrongly assuming
-- telemetry-api's own `recharge` segmentation already excluded connector
-- self-check / relay-click blips -- confirmed live in production that it
-- does not. The filter was reinstated in the commit that added this
-- migration, but every row either table holds today was persisted before
-- that fix landed, and charging_scan_coverage marks those windows as
-- already scanned -- exactly the same trap as
-- 20260921120000_clear_stale_charging_scans.sql, so it needs the same
-- remedy: clear both tables so the corrected filter actually takes effect
-- on next load instead of reading back stale noise forever.
TRUNCATE TABLE charging_sessions;
TRUNCATE TABLE charging_scan_coverage;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 'down SQL query';

-- Not reversible: the truncated rows were noise the fixed code would have
-- discarded anyway, so there is nothing worth restoring.

-- +goose StatementEnd
