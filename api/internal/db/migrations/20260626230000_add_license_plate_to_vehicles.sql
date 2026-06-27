-- +goose Up
-- +goose StatementBegin
SELECT 'up SQL query';

-- License plate, cached from the vehicle's latest
-- dimo.document.vehicle.registration attestation that carries a license_plate
-- field (published by the source-of-truth fleet system; see kaufmann-oracle
-- ADR 0004). Read-only here: fleet-lite-app pulls it during the
-- import-group-attestations cron's per-vehicle loop and never writes it back to
-- the attestation network. Nullable: NULL means "no plate document found yet".
ALTER TABLE IF EXISTS vehicles
    ADD COLUMN IF NOT EXISTS license_plate TEXT;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 'down SQL query';

ALTER TABLE IF EXISTS vehicles
    DROP COLUMN IF EXISTS license_plate;

-- +goose StatementEnd
