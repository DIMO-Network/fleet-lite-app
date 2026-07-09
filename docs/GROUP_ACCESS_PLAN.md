# Per-Member Group Access — Plan (fleet-lite-app)

Give owners fine-grained control over **which fleet groups a member can see**.
Today every member sees the whole tenant; after this, a member can be limited to
specific groups, and everything downstream (vehicle lists, per-vehicle data,
groups/geofences views) respects that boundary.

Decisions (confirmed 2026-07-08):
- **Ungrouped vehicles are invisible to limited members** — their world is
  exactly the union of their allowed groups.
- **Enforcement is at the API, on every vehicle endpoint** — not just list
  filtering. A crafted tokenId gets 404, the same as a vehicle in another tenant.
- **Groups + Geofences views are scoped** — a limited member sees only their
  groups (read-only) and geofence data restricted to their vehicles.
- **Only owners invite and manage users** (already true for create/revoke/resend
  + add/remove member; `GET /tenants/:id/invitations` tightens to owner-only).

## Semantics

One nullable string array, `allowed_group_ids`, on `tenant_users` (and mirrored
on `invitations` so the choice is made at invite time):

| role   | allowed_group_ids | access                                    |
|--------|-------------------|-------------------------------------------|
| owner  | (ignored, null)   | everything, always                        |
| member | NULL              | everything ("member with all groups")     |
| member | ['g1','g2']       | vehicles in g1 ∪ g2; groups g1, g2 only   |
| member | [] (empty)        | nothing (UI prevents; API treats as limited-to-none) |

- Group ids are `fleet_groups.id` slugs. A deleted group simply stops matching —
  the member silently loses that slice. (Optional hygiene: `array_remove` on
  group delete; not required for correctness.)
- **Limited members are read-only on management surfaces**: fleet-group CRUD and
  geofence CRUD return 403 for them. Full-access members keep today's abilities.

## Backend (PR A)

**Migration** (mirrors `20260609140000_tenant_user_activity.sql`):

```sql
ALTER TABLE tenant_users ADD COLUMN IF NOT EXISTS allowed_group_ids TEXT[];
ALTER TABLE invitations  ADD COLUMN IF NOT EXISTS allowed_group_ids TEXT[];
```

`make sqlboiler` regen (nullable `TEXT[]` → `types.StringArray`, nil when NULL).

**Membership → request context.** `TenantService.GetMembership` returns the
`tenant_users` row; `NewTenantMiddleware` (api/internal/app/tenant.go) stashes
role (already at `tenant_role`) plus `tenant_allowed_groups` — normalized to
**nil for owners and full-access members**, `[]string` only when limited.
Helpers in `controllers/common.go`: `GetAllowedGroups(c)` (nil = unrestricted).

**Enforcement points** (all keyed off `GetAllowedGroups(c)`):
- `GET /vehicles` — `ListVehicles` gains an allowed-groups filter:
  `token_id IN (SELECT token_id FROM vehicle_fleet_groups WHERE tenant_id=$1
  AND fleet_group_id = ANY($2))`.
- Per-vehicle endpoints (vehicle details, favorites, telemetry, trips,
  documents/glovebox) — shared guard `requireVehicleAccess(c, tokenID)` using
  `FleetGroupService.VehicleInGroups` (single EXISTS query); 404 on miss so
  outside-your-groups is indistinguishable from nonexistent.
- `GET /fleet/groups` — limited members get only their allowed groups.
- Fleet-group + geofence **mutations** — 403 for limited members.
- Geofence vehicle-resolution endpoints (`:id/vehicles`, passes, scan targets)
  — intersect resolved token ids with the member's accessible set.

**Invitations.** `POST /tenants/:id/invitations` accepts
`allowedGroupIds: string[] | null` (validated: only for role=member, ids must
exist in the tenant). Stored on the invitation, copied to `tenant_users` by
`Accept` → `AddMember`. Listed in `invitationJSON`.

**Members API.** `memberJSON` gains `allowedGroupIds` (null = all). New
owner-only `PUT /tenants/:id/members/:wallet/access` to change an existing
member's allowed groups (the "manage users with access" half). New
`GET /me/access` on the tenant app returning `{ role, allowedGroupIds }` so any
view can cheaply know the caller's own scope.

## Frontend (PR B)

- **Invite modal** (`invite-member-modal.ts`) replaces the inline invite form:
  email, role, and a group-access section — "All groups" vs "Selected groups"
  with a search box (frontend filter) over multi-select group checkboxes.
  Same modal reused for "Edit access" on an existing member row (owner-only).
- **Members list**: each member/invite row shows an access chip — "All groups"
  or "N groups" (title lists names).
- **Settings ("your access")**: non-owners see their level and either "Access to
  all groups" or "Limited access to the following groups: …" chips, driven by
  `GET /me/access` + the (already-filtered) groups list.
- **Groups view**: limited members see only their groups, with create/color/
  delete/manage controls hidden (API also rejects).
- **Geofences view**: unchanged UI; data arrives pre-filtered (vehicle overlays,
  counts, activity are limited to accessible vehicles). CRUD hidden for limited
  members.
- Vehicle map/list views need no changes — the API filter does the work.

## Rollout

1. PR A (backend): migration + enforcement + invitation plumbing + `/me/access`.
   Existing rows have NULL → **no behavior change for anyone until an owner
   limits someone.**
2. PR B (frontend): modal, chips, scoped views.
3. Release both together (single values-prod bump).
