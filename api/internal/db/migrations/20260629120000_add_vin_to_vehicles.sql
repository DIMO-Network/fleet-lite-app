-- +goose Up
-- +goose StatementBegin
SELECT 'up SQL query';

-- VIN, cached from the vehicle's latest dimo.document.vehicle.registration
-- attestation that carries a vin field (the same document the license_plate is
-- read from; see LicensePlateSyncService / kaufmann-oracle ADR 0004). Read-only
-- here: fleet-lite-app pulls it during the import-group-attestations cron's
-- per-vehicle loop and never writes it back to the attestation network.
-- Nullable: NULL means "no vin document found yet".
ALTER TABLE IF EXISTS vehicles
    ADD COLUMN IF NOT EXISTS vin TEXT;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 'down SQL query';

ALTER TABLE IF EXISTS vehicles
    DROP COLUMN IF EXISTS vin;

-- +goose StatementEnd
