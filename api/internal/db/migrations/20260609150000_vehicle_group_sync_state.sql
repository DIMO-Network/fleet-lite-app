-- +goose Up
-- +goose StatementBegin
SELECT 'up SQL query';

-- Per-vehicle group-sync bookkeeping (Phase 1 of the attestation-backed group
-- sync — see docs/GROUP_SYNC.md).
--   groups_updated_at  — last time WE changed this vehicle's local group
--                        membership (assign/remove). Lets the cron prioritise
--                        recently-changed vehicles and is the Phase-2 write
--                        guard against the 5-10s Fetch-API lag.
--   last_group_sync_at — last time we pulled this vehicle's group attestations
--                        from Fetch API. Drives the lazy-endpoint cooldown
--                        (skip the pull if we synced within the cooldown window)
--                        and cron freshness selection.
-- Both nullable: NULL means "never changed / never synced".
ALTER TABLE IF EXISTS vehicles
    ADD COLUMN IF NOT EXISTS groups_updated_at  TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS last_group_sync_at TIMESTAMPTZ;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 'down SQL query';

ALTER TABLE IF EXISTS vehicles
    DROP COLUMN IF EXISTS groups_updated_at,
    DROP COLUMN IF EXISTS last_group_sync_at;

-- +goose StatementEnd
