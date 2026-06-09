-- +goose Up
-- +goose StatementBegin
SELECT 'up SQL query';

-- Track member activity + identity. last_login_at drives the group-sync cron
-- tiering (a tenant is "warm" if any member logged in recently). email is the
-- human-readable identity shown in Settings -> Members (DIMO's JWT carries no
-- name/email; the frontend supplies the email from the OAuth redirect via the
-- login-touch endpoint). Both are nullable until the member's first login.
ALTER TABLE IF EXISTS tenant_users
    ADD COLUMN IF NOT EXISTS last_login_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS email         TEXT;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 'down SQL query';

ALTER TABLE IF EXISTS tenant_users
    DROP COLUMN IF EXISTS last_login_at,
    DROP COLUMN IF EXISTS email;

-- +goose StatementEnd
