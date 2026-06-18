-- +goose Up
-- +goose StatementBegin
SELECT 'up SQL query';

-- Email-based member invitations. An owner invites by email; we email a
-- single-use accept link carrying a high-entropy token (only its SHA-256 hash
-- is stored). The invitee logs in with their own DIMO passkey and accepts,
-- at which point their JWT wallet is added to tenant_users with `role`.
-- The wallet is unknown at invite time (DIMO has no email->wallet lookup here),
-- so invitee_wallet is null until acceptance.
CREATE TABLE IF NOT EXISTS invitations (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id         UUID NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    email             TEXT NOT NULL,
    role              TEXT NOT NULL DEFAULT 'member',
    token_hash        TEXT NOT NULL,
    status            TEXT NOT NULL DEFAULT 'pending', -- pending | accepted | revoked
    invited_by_wallet VARCHAR(43) NOT NULL,
    invitee_wallet    VARCHAR(43),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at        TIMESTAMPTZ NOT NULL,
    accepted_at       TIMESTAMPTZ
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_invitations_token_hash ON invitations (token_hash);
CREATE INDEX IF NOT EXISTS idx_invitations_tenant_id ON invitations (tenant_id);
CREATE INDEX IF NOT EXISTS idx_invitations_status ON invitations (status);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 'down SQL query';

DROP TABLE IF EXISTS invitations;

-- +goose StatementEnd
