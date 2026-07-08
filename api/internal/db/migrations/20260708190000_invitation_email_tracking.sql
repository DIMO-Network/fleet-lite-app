-- +goose Up
-- +goose StatementBegin
SELECT 'up SQL query';

-- Email-delivery tracking for invitations, fed by Postmark: the send call
-- stamps postmark_message_id + email_status='sent'; the webhook (phase 2, see
-- docs/POSTMARK_WEBHOOK_PLAN.md) upgrades the status to delivered/opened or
-- bounced. Status only ever upgrades (sent < delivered < opened; bounced wins)
-- because webhook events can arrive out of order / twice.
ALTER TABLE invitations
    ADD COLUMN IF NOT EXISTS postmark_message_id TEXT,
    ADD COLUMN IF NOT EXISTS email_status        TEXT,         -- sent | delivered | opened | bounced
    ADD COLUMN IF NOT EXISTS email_status_at     TIMESTAMPTZ,  -- when that status was reached
    ADD COLUMN IF NOT EXISTS email_status_detail TEXT;         -- e.g. bounce description

-- Webhook fallback lookup when metadata is missing (resolves by MessageID).
CREATE INDEX IF NOT EXISTS invitations_postmark_message_id_idx
    ON invitations (postmark_message_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 'down SQL query';

DROP INDEX IF EXISTS invitations_postmark_message_id_idx;
ALTER TABLE invitations
    DROP COLUMN IF EXISTS postmark_message_id,
    DROP COLUMN IF EXISTS email_status,
    DROP COLUMN IF EXISTS email_status_at,
    DROP COLUMN IF EXISTS email_status_detail;

-- +goose StatementEnd
