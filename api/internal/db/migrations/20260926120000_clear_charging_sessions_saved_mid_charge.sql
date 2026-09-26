-- +goose Up
-- +goose StatementBegin
SELECT 'up SQL query';

-- A charge still under way when its vehicle's Charging tab loaded was stored
-- with its latest reading as its end, and its window marked scanned, so it
-- was never revisited: the rest of the charge came back as a second session,
-- and the Charging table's "Ended" column shows the fake end. 6f815c3 tried
-- to hold such sessions back using telemetry-api's isOngoing, which the
-- recharge detector never sets, so rows kept being stored that way.
-- charging_detection.go now holds back anything whose last reading is within
-- rechargeSettleMargin of now. Rows already stored include split and
-- truncated sessions, and coverage marks their windows as scanned -- the same
-- trap as 20260921120000 and 20260924150000, with the same remedy: clear both
-- tables so every vehicle's history is recomputed on its next load.
TRUNCATE TABLE charging_sessions;
TRUNCATE TABLE charging_scan_coverage;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 'down SQL query';

-- Not reversible: the truncated rows are recomputed from telemetry, which is
-- the source of truth; nothing is lost.

-- +goose StatementEnd
