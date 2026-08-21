-- +goose Up
-- +goose StatementBegin
SELECT 'up SQL query';

-- P5b of fleet-tenancy-api's docs/plans/01-groups-into-tenancy.md — the end of
-- the groups move.
--
-- fleet-tenancy-api has owned every fleet group since the 2026-08-13 cutover:
-- both apps write groups there, read them from there, and it is the single
-- publisher of the group attestation. These tables have been a synchronously
-- maintained mirror since P4, kept only as the revert path while the cutover
-- soaked. The soak is done: reads on tenancy since 2026-08-13 with no revert,
-- and groups-diff clean on 2026-08-21 — differ=0, missing_remote=0,
-- unreachable=0 across all 7 tenants and 88 groups — meaning nothing exists
-- here that tenancy lacks. Dropping the tables is what removes the revert
-- path, on purpose.
--
-- THE ROWS ARE NOT RECOVERABLE FROM HERE once this runs. The down migration
-- restores the tables' SHAPE so a rollback boots, not their contents. The
-- authoritative copy is fleet-tenancy-api's fleet_groups /
-- vehicle_fleet_groups, which is also where these rows came to agree with
-- before the flip.
--
-- Order matters: vehicle_fleet_groups holds the only FK into fleet_groups, so
-- it goes first. Nothing else references either table — geofences.group_ids
-- and memberships' allowed_group_ids are TEXT[] soft references with no
-- constraint, and their integrity now lives in fleet-tenancy-api's own
-- reference rules.
DROP TABLE IF EXISTS vehicle_fleet_groups;
DROP TABLE IF EXISTS fleet_groups;

-- The group-sync bookkeeping columns were written by the attestation import
-- machinery deleted when this service stopped syncing groups from the wire.
-- Nothing has read or written them since; the models regenerate without them.
ALTER TABLE IF EXISTS vehicles
    DROP COLUMN IF EXISTS groups_updated_at,
    DROP COLUMN IF EXISTS last_group_sync_at;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 'down SQL query';

-- Recreates the tables as the three migrations that shaped them left them:
-- 20260608120000_fleet_groups (base), 20260805170000_unify_kaufmann_tenant_uuid
-- (tenant FKs rebuilt with ON UPDATE CASCADE) and
-- 20260805180000_tenant_scoped_group_ids (the group-id FK rebuilt with
-- ON UPDATE CASCADE so a re-keyed id carries to the join table). Reproducing
-- those clauses matters: without them a rollback would restore tables whose
-- FKs silently block the re-keys those migrations exist to allow. Empty,
-- deliberately — see the note above.
CREATE TABLE IF NOT EXISTS fleet_groups (
    id         TEXT PRIMARY KEY,       -- <tenant-uuid>_<slug>
    name       TEXT NOT NULL,
    color      VARCHAR(7) NOT NULL,    -- HTML hex color like #FF5733
    tenant_id  UUID NOT NULL REFERENCES tenants (id) ON DELETE CASCADE ON UPDATE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, name)
);

CREATE INDEX IF NOT EXISTS idx_fleet_groups_tenant_id ON fleet_groups (tenant_id);

CREATE TABLE IF NOT EXISTS vehicle_fleet_groups (
    tenant_id      UUID   NOT NULL,
    token_id       BIGINT NOT NULL,
    fleet_group_id TEXT   NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, token_id, fleet_group_id),
    CONSTRAINT vehicle_fleet_groups_fleet_group_id_fkey
        FOREIGN KEY (fleet_group_id) REFERENCES fleet_groups (id)
        ON DELETE CASCADE ON UPDATE CASCADE,
    CONSTRAINT vehicle_fleet_groups_tenant_id_token_id_fkey
        FOREIGN KEY (tenant_id, token_id) REFERENCES vehicles (tenant_id, token_id)
        ON DELETE CASCADE ON UPDATE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_vehicle_fleet_groups_group
    ON vehicle_fleet_groups (fleet_group_id);

ALTER TABLE IF EXISTS vehicles
    ADD COLUMN IF NOT EXISTS groups_updated_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS last_group_sync_at TIMESTAMPTZ;

-- +goose StatementEnd
