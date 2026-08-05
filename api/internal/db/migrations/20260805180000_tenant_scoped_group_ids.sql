-- +goose Up
-- +goose StatementBegin
SELECT 'up SQL query';

-- Fleet group ids become tenant-scoped: <tenant-uuid>_<slug>.
--
-- Two problems, one fix.
--
-- 1. `fleet_groups.id` is slug(name) as a GLOBAL primary key, while the intended
--    uniqueness is UNIQUE (tenant_id, name). The second tenant to create "Vans"
--    hits the PK and is told the name is taken — by a group in another tenant
--    they cannot see. Live bug, masked only by low tenant count.
--
-- 2. `data.groups[].id` in the dimo.document.vehicle.groups CloudEvent is the
--    only field that can tell two tenants' groups apart once a shared operator
--    developer license means every producer's CEs carry the same `source`.
--    A self-describing id fixes attribution with no CloudEvent schema change.
--
-- Format `<tenant-uuid>_<slug>` is self-detecting: slugs are [a-z0-9-]+
-- (slugNonAlphanum collapses everything else to '-') and uuids contain '-' but
-- never '_'. So a new id has exactly one '_' and a legacy id has none, which is
-- what makes every statement below idempotent and the read-side normalisation
-- unambiguous.

-- vehicle_fleet_groups_fleet_group_id_fkey is ON DELETE CASCADE but the default
-- ON UPDATE NO ACTION, so rewriting the parent PK would be REJECTED while child
-- rows exist. Recreate it with ON UPDATE CASCADE and the children follow the
-- rename automatically. Left in place afterwards: harmless, and it makes any
-- future id change tractable.
ALTER TABLE vehicle_fleet_groups
    DROP CONSTRAINT vehicle_fleet_groups_fleet_group_id_fkey;
ALTER TABLE vehicle_fleet_groups
    ADD CONSTRAINT vehicle_fleet_groups_fleet_group_id_fkey
    FOREIGN KEY (fleet_group_id) REFERENCES fleet_groups (id)
    ON DELETE CASCADE ON UPDATE CASCADE;

-- Rewrite the ids. vehicle_fleet_groups follows via ON UPDATE CASCADE.
-- No collision risk: old ids were globally unique, a '<uuid>_' prefix cannot
-- collide with a bare slug, and UNIQUE (tenant_id, name) keeps them unique
-- within a tenant.
UPDATE fleet_groups
   SET id = tenant_id::text || '_' || id
 WHERE strpos(id, '_') = 0;

-- Soft references: TEXT[] columns with no foreign key, so nothing cascades and
-- a miss fails silently rather than erroring. All three are scoping columns, so
-- a stale id degrades quietly into "this member sees nothing" or "this geofence
-- targets nothing" — which is why they are easy to overlook and worth naming:
--   tenant_users.allowed_group_ids  — per-member group scope
--   invitations.allowed_group_ids   — the same, chosen at invite time
--   geofences.group_ids             — geofence targeting when scope = 'group'
-- The per-element CASE keeps each idempotent; ARRAY(SELECT ...) preserves order.
UPDATE tenant_users
   SET allowed_group_ids = ARRAY(
       SELECT CASE WHEN strpos(g, '_') = 0 THEN tenant_id::text || '_' || g ELSE g END
       FROM unnest(allowed_group_ids) AS g)
 WHERE allowed_group_ids IS NOT NULL;

UPDATE invitations
   SET allowed_group_ids = ARRAY(
       SELECT CASE WHEN strpos(g, '_') = 0 THEN tenant_id::text || '_' || g ELSE g END
       FROM unnest(allowed_group_ids) AS g)
 WHERE allowed_group_ids IS NOT NULL;

-- group_ids is NOT NULL DEFAULT '{}', so guard on cardinality rather than NULL.
UPDATE geofences
   SET group_ids = ARRAY(
       SELECT CASE WHEN strpos(g, '_') = 0 THEN tenant_id::text || '_' || g ELSE g END
       FROM unnest(group_ids) AS g)
 WHERE cardinality(group_ids) > 0;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 'down SQL query';

-- Strips the tenant prefix. This REINTRODUCES the collision, so it fails if two
-- tenants have since created the same slug. That is correct — failing is better
-- than silently merging two tenants' groups into one row. Down is a fast
-- rollback for the first hours, not a lasting escape hatch.
UPDATE geofences
   SET group_ids = ARRAY(SELECT split_part(g, '_', 2) FROM unnest(group_ids) AS g)
 WHERE cardinality(group_ids) > 0;

UPDATE invitations
   SET allowed_group_ids = ARRAY(SELECT split_part(g, '_', 2) FROM unnest(allowed_group_ids) AS g)
 WHERE allowed_group_ids IS NOT NULL;

UPDATE tenant_users
   SET allowed_group_ids = ARRAY(SELECT split_part(g, '_', 2) FROM unnest(allowed_group_ids) AS g)
 WHERE allowed_group_ids IS NOT NULL;

UPDATE fleet_groups SET id = split_part(id, '_', 2) WHERE strpos(id, '_') > 0;

ALTER TABLE vehicle_fleet_groups
    DROP CONSTRAINT vehicle_fleet_groups_fleet_group_id_fkey;
ALTER TABLE vehicle_fleet_groups
    ADD CONSTRAINT vehicle_fleet_groups_fleet_group_id_fkey
    FOREIGN KEY (fleet_group_id) REFERENCES fleet_groups (id)
    ON DELETE CASCADE;

-- +goose StatementEnd
