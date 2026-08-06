-- +goose Up
-- +goose StatementBegin
SELECT 'up SQL query';

-- Re-key the Kaufmann tenant so fleet-lite and kaufmann-oracle agree on one uuid
-- for one company.
--
--   kaufmann_oracle.tenants "Kaufmann"  7be1ab9e-9286-4a8f-b45f-15f25ee4da77  (kept)
--   fleets_lite.tenants     "Kaufmann"  9708b213-21fe-41da-bded-c3026d16b85c  (re-keyed)
--
-- Exactly one tenant exists in both databases; the other 15 keep their uuids
-- untouched, which is what lets the tenancy-service backfill reuse them and
-- leaves the Tenant-Id header working unchanged.
--
-- kaufmann's uuid wins because its row is the operator: it carries the DIMO
-- developer license, the signer keypair and the Kore credentials, and is
-- referenced by the whole onboarding pipeline (vins, commands, kore_sims,
-- access_tenants, reports). fleet-lite's row is a derived view of the same
-- company, and its vehicles table repopulates from identity-api, so this is
-- both the smaller and the more recoverable side to move.
--
-- WHY THIS RUNS BEFORE THE GROUP-ID MIGRATION (20260805180000): that migration
-- rewrites fleet_groups.id to <tenant-uuid>_<slug>. Re-keying first means group
-- ids are written once, with their final uuid, and the attestation republish
-- happens once. Reversing the order would require rewriting every group id and
-- every TEXT[] scope column a second time.
--
-- Users see one cosmetic effect: fleet-lite routes on #/<tenantId>/ and caches
-- selectedTenant in localStorage, so anyone logged in re-selects their fleet
-- once. No data is lost.

-- Let the re-key cascade. Every one of these is ON DELETE CASCADE with the
-- default ON UPDATE NO ACTION, which would REJECT an update to tenants.id while
-- child rows exist. vehicle_fleet_groups and vehicle_geofences reference
-- vehicles (tenant_id, token_id) rather than tenants directly, so they follow
-- transitively — a two-level cascade.
ALTER TABLE tenant_users DROP CONSTRAINT tenant_users_tenant_id_fkey;
ALTER TABLE tenant_users ADD CONSTRAINT tenant_users_tenant_id_fkey
    FOREIGN KEY (tenant_id) REFERENCES tenants (id) ON DELETE CASCADE ON UPDATE CASCADE;

ALTER TABLE vehicles DROP CONSTRAINT vehicles_tenant_id_fkey;
ALTER TABLE vehicles ADD CONSTRAINT vehicles_tenant_id_fkey
    FOREIGN KEY (tenant_id) REFERENCES tenants (id) ON DELETE CASCADE ON UPDATE CASCADE;

ALTER TABLE fleet_groups DROP CONSTRAINT fleet_groups_tenant_id_fkey;
ALTER TABLE fleet_groups ADD CONSTRAINT fleet_groups_tenant_id_fkey
    FOREIGN KEY (tenant_id) REFERENCES tenants (id) ON DELETE CASCADE ON UPDATE CASCADE;

ALTER TABLE vehicle_favorites DROP CONSTRAINT vehicle_favorites_tenant_id_fkey;
ALTER TABLE vehicle_favorites ADD CONSTRAINT vehicle_favorites_tenant_id_fkey
    FOREIGN KEY (tenant_id) REFERENCES tenants (id) ON DELETE CASCADE ON UPDATE CASCADE;

ALTER TABLE invitations DROP CONSTRAINT invitations_tenant_id_fkey;
ALTER TABLE invitations ADD CONSTRAINT invitations_tenant_id_fkey
    FOREIGN KEY (tenant_id) REFERENCES tenants (id) ON DELETE CASCADE ON UPDATE CASCADE;

ALTER TABLE geofences DROP CONSTRAINT geofences_tenant_id_fkey;
ALTER TABLE geofences ADD CONSTRAINT geofences_tenant_id_fkey
    FOREIGN KEY (tenant_id) REFERENCES tenants (id) ON DELETE CASCADE ON UPDATE CASCADE;

ALTER TABLE vehicle_tco_settings DROP CONSTRAINT vehicle_tco_settings_tenant_id_fkey;
ALTER TABLE vehicle_tco_settings ADD CONSTRAINT vehicle_tco_settings_tenant_id_fkey
    FOREIGN KEY (tenant_id) REFERENCES tenants (id) ON DELETE CASCADE ON UPDATE CASCADE;

ALTER TABLE vehicle_fleet_groups DROP CONSTRAINT vehicle_fleet_groups_tenant_id_token_id_fkey;
ALTER TABLE vehicle_fleet_groups ADD CONSTRAINT vehicle_fleet_groups_tenant_id_token_id_fkey
    FOREIGN KEY (tenant_id, token_id) REFERENCES vehicles (tenant_id, token_id)
    ON DELETE CASCADE ON UPDATE CASCADE;

ALTER TABLE vehicle_geofences DROP CONSTRAINT vehicle_geofences_tenant_id_token_id_fkey;
ALTER TABLE vehicle_geofences ADD CONSTRAINT vehicle_geofences_tenant_id_token_id_fkey
    FOREIGN KEY (tenant_id, token_id) REFERENCES vehicles (tenant_id, token_id)
    ON DELETE CASCADE ON UPDATE CASCADE;

-- The re-key itself, guarded. Skips cleanly where the tenant doesn't exist (dev,
-- local, a fresh database) and refuses rather than corrupting if the target uuid
-- is somehow already taken by a different tenant.
DO $$
DECLARE
    old_id CONSTANT UUID := '9708b213-21fe-41da-bded-c3026d16b85c';
    new_id CONSTANT UUID := '7be1ab9e-9286-4a8f-b45f-15f25ee4da77';
BEGIN
    IF NOT EXISTS (SELECT 1 FROM tenants WHERE id = old_id) THEN
        RAISE NOTICE 'Kaufmann tenant % not present; nothing to re-key', old_id;
        RETURN;
    END IF;

    IF EXISTS (SELECT 1 FROM tenants WHERE id = new_id) THEN
        RAISE EXCEPTION 'target tenant uuid % already exists — refusing to merge two tenants', new_id;
    END IF;

    -- FK'd tables follow via ON UPDATE CASCADE, including the two that reference
    -- vehicles rather than tenants.
    UPDATE tenants SET id = new_id, updated_at = NOW() WHERE id = old_id;

    -- These two carry tenant_id with NO foreign key, so nothing cascades to them
    -- and a miss would leave rows pointing at a tenant that no longer exists —
    -- silently, since there is no constraint to complain. Update explicitly.
    UPDATE geofence_passes        SET tenant_id = new_id WHERE tenant_id = old_id;
    UPDATE geofence_scan_coverage SET tenant_id = new_id WHERE tenant_id = old_id;

    RAISE NOTICE 'Kaufmann tenant re-keyed % -> %', old_id, new_id;
END $$;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 'down SQL query';

DO $$
DECLARE
    old_id CONSTANT UUID := '7be1ab9e-9286-4a8f-b45f-15f25ee4da77';
    new_id CONSTANT UUID := '9708b213-21fe-41da-bded-c3026d16b85c';
BEGIN
    IF NOT EXISTS (SELECT 1 FROM tenants WHERE id = old_id) THEN
        RAISE NOTICE 'tenant % not present; nothing to revert', old_id;
        RETURN;
    END IF;
    IF EXISTS (SELECT 1 FROM tenants WHERE id = new_id) THEN
        RAISE EXCEPTION 'original uuid % is taken — refusing to merge two tenants', new_id;
    END IF;

    UPDATE tenants SET id = new_id, updated_at = NOW() WHERE id = old_id;
    UPDATE geofence_passes        SET tenant_id = new_id WHERE tenant_id = old_id;
    UPDATE geofence_scan_coverage SET tenant_id = new_id WHERE tenant_id = old_id;
END $$;

-- ON UPDATE CASCADE is deliberately left in place. It was missing by omission
-- rather than by design, and restoring the omission would only make a future
-- re-key fail the same way this one would have.

-- +goose StatementEnd
