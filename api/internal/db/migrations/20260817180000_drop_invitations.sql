-- +goose Up
-- +goose StatementBegin
SELECT 'up SQL query';

-- P4 of fleet-tenancy-api's docs/plans/04-invitations-into-tenancy.md.
--
-- fleet-tenancy-api has owned every invitation since the 2026-08-17 cutover:
-- it mints the token, sends the email, receives the delivery webhooks, and
-- writes the membership at accept, all in one transaction. This table has been
-- inert since then — the backfill copied its 14 rows there before the flip,
-- the fingerprint check proved the two sides agreed field for field, and the
-- application code that read it was deleted in the PR before this one.
--
-- THE ROWS ARE NOT RECOVERABLE FROM HERE once this runs. The down migration
-- restores the table's SHAPE so a rollback boots, not its contents. If the
-- rows are ever genuinely needed, they live in fleet-tenancy-api's own
-- invitations table — including these historical ones, which is the whole
-- point of having backfilled them by id before the cutover.
--
-- Nothing references this table: the only foreign key points outward
-- (tenant_id -> tenants), so the drop cannot orphan another table's rows.
DROP TABLE IF EXISTS invitations;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 'down SQL query';

-- Recreates the table as the four migrations that shaped it left it:
-- 20260617120000_invitations (base), 20260708190000_invitation_email_tracking
-- (the Postmark columns), 20260708220000_member_allowed_groups
-- (allowed_group_ids) and 20260805170000_unify_kaufmann_tenant_uuid, which
-- rebuilt the foreign key with ON UPDATE CASCADE so a re-keyed tenant uuid
-- carries. Reproducing that clause matters: without it a rollback would
-- restore a table whose FK silently blocks the very re-key that migration
-- exists to allow. Empty, deliberately — see the note above.
CREATE TABLE IF NOT EXISTS invitations (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id           UUID NOT NULL REFERENCES tenants (id) ON DELETE CASCADE ON UPDATE CASCADE,
    email               TEXT NOT NULL,
    role                TEXT NOT NULL DEFAULT 'member',
    token_hash          TEXT NOT NULL,
    status              TEXT NOT NULL DEFAULT 'pending', -- pending | accepted | revoked
    invited_by_wallet   VARCHAR(43) NOT NULL,
    invitee_wallet      VARCHAR(43),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at          TIMESTAMPTZ NOT NULL,
    accepted_at         TIMESTAMPTZ,
    postmark_message_id TEXT,
    email_status        TEXT,        -- sent | delivered | opened | bounced
    email_status_at     TIMESTAMPTZ, -- when that status was reached
    email_status_detail TEXT,        -- e.g. bounce description
    allowed_group_ids   TEXT[]
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_invitations_token_hash ON invitations (token_hash);
CREATE INDEX IF NOT EXISTS idx_invitations_tenant_id ON invitations (tenant_id);
CREATE INDEX IF NOT EXISTS idx_invitations_status ON invitations (status);
CREATE INDEX IF NOT EXISTS invitations_postmark_message_id_idx ON invitations (postmark_message_id);

-- +goose StatementEnd
