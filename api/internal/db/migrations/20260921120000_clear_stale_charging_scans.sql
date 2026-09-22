-- +goose Up
-- +goose StatementBegin
SELECT 'up SQL query';

-- The charging-session detection sweep shipped with a bug (fixed in the
-- commit that added this migration): it treated a missing telemetry sample
-- the same as an explicit "not charging" report, which fragmented every real
-- session into isolated single-sample runs that got discarded. Every row
-- either table holds today is a product of that bug -- "scanned this window,
-- found zero sessions" -- not a true absence of charging. None of it is
-- valid data worth preserving.
--
-- Clearing both tables lets the corrected detection code treat every
-- vehicle's window as unscanned and re-fetch/re-detect from telemetry on its
-- next Charging tab load, rather than reading back the bug's stale "nothing
-- found" results forever (charging_scan_coverage's whole point is to avoid
-- re-scanning an already-covered window, which is exactly what would
-- otherwise mask this fix from ever taking effect).
TRUNCATE TABLE charging_sessions;
TRUNCATE TABLE charging_scan_coverage;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 'down SQL query';

-- Not reversible: the truncated rows were never valid data, so there is
-- nothing to restore. Rolling back this migration is a no-op.

-- +goose StatementEnd
