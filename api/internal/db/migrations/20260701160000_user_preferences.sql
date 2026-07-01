-- +goose Up
-- +goose StatementBegin
SELECT 'up SQL query';

-- Per-user UI preferences (units, locale, trip-detection mechanism, …), keyed by
-- wallet so a user's choices follow them across browsers/devices instead of
-- living only in one browser's localStorage. Personal to the wallet, NOT the
-- tenant: a wallet in multiple tenants gets one preferences row, not divergent
-- settings per tenant — so this is keyed by wallet alone, not (tenant_id,
-- wallet) like tenant_users. prefs is a JSONB blob ({ units, locale,
-- tripMechanism, … }) so new preferences need no migration; the backend
-- whitelists known keys/values. See docs/USER_PREFERENCES_PLAN.md.
CREATE TABLE IF NOT EXISTS user_preferences (
    wallet     VARCHAR(43) PRIMARY KEY,        -- lowercased 0x address, matches tenant_users.wallet
    prefs      JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 'down SQL query';

DROP TABLE IF EXISTS user_preferences;

-- +goose StatementEnd
