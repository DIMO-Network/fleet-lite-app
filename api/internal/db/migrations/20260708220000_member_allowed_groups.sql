-- +goose Up
-- +goose StatementBegin
SELECT 'up SQL query';

-- Per-member group access (see docs/GROUP_ACCESS_PLAN.md). NULL = full access
-- (all groups); an array limits a member to those fleet_groups ids. Owners
-- ignore the column entirely. Mirrored on invitations so the choice is made at
-- invite time and copied to tenant_users on accept.
ALTER TABLE tenant_users ADD COLUMN IF NOT EXISTS allowed_group_ids TEXT[];
ALTER TABLE invitations  ADD COLUMN IF NOT EXISTS allowed_group_ids TEXT[];

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 'down SQL query';

ALTER TABLE tenant_users DROP COLUMN IF EXISTS allowed_group_ids;
ALTER TABLE invitations  DROP COLUMN IF EXISTS allowed_group_ids;

-- +goose StatementEnd
