# Group Sync Strategy (attestation-backed)

Status: design / pre-implementation. Tracks the work after PR #21.

## Objective

Vehicle group attestations (DIMO **Fetch API**, type `dimo.document.vehicle.groups`)
are the **authoritative source** of group membership. The local
`vehicle_fleet_groups` table is a **performance cache + search index** — we would
read attestations directly if not for (a) query latency and (b) no search.

**Hard constraint:** a freshly published attestation takes **~5–10s** to become
queryable from Fetch API. Any sync that runs inside that window sees *stale* data,
so it must never revert a recent local write.

## Authoritative model

> A vehicle's authoritative groups = **union of { latest `dimo.document.vehicle.groups`
> attestation, per producer }**.

Each app is authoritative over *its own* attestation. A group leaves the union when
the producer that asserted it drops it (and no other producer still asserts it).
This is why we key on **`producer`, not `source`/`dimo_client_id`** — an org runs
several apps under one developer credential, so source can't distinguish them.

> **VERIFIED (2026-06-09):** on our own CEs, `producer` comes back **empty** and
> `source` is the shared `dimo_client_id`. Our `signedCloudEvent` only sets
> `source`; DIMO stores exactly what we send. So **per-producer attribution does not
> work today** — sibling apps are indistinguishable. To make the authoritative model
> (and Phase-2 removals) viable we must **start stamping a stable `producer` on our
> own writes** (e.g. a constant app id like `fleet-lite-app`, or the app's own signer
> address). Then: **mirror our-own-producer** (removals propagate for groups *we*
> assert) and stay **additive for everything else** (empty/other producers). First
> confirm the Attest API accepts and persists a caller-set `producer`.

## Phasing

### Phase 1 — additive sync + triggers (this work)

- **Keep the current additive merge** (add-only, dedup by the
  `(tenant_id, token_id, fleet_group_id)` PK; never removes). Shipped in PR #21.
- **Lazy sync (frontend-initiated):** new tenant-scoped endpoint, e.g.
  `POST /fleet/vehicles/:tokenID/groups/sync`. The frontend calls it **when a
  vehicle is selected/viewed**; it pulls that one vehicle's group attestations,
  additively merges, and returns the vehicle's current groups for immediate display.
- **Cron, activity-tiered** (replaces the single hourly job):
  - **Daily** pass — the "warm" set only (recently viewed or recently group-changed).
  - **Weekly** pass — the **entire fleet** (catch-all so dormant vehicles, incl.
    foreign-app changes, converge within a week).
- Add bookkeeping columns to support tiering now and the Phase-2 guard later:
  `groups_updated_at` (last local group change) and `last_group_sync_at`
  (last time we pulled this vehicle).

Phase 1 is **safe against the 5–10s lag for free** — additive never removes, so a
stale sync can't revert anything.

### Phase 2 — guarded mirror / removals (later)

Producer is empty today (see Authoritative model), so Phase 2 starts by **making
our own attestations self-identifying**:

- **Write path:** stamp a stable `producer` on every CE we publish (pending
  confirmation the Attest API persists it).
- **Sync becomes hybrid:** **mirror** the latest CE from *our* producer (so removals
  we make propagate), and stay **additive** for all other producers (never remove a
  group a sibling/foreign app asserts).
- **CE-time write guard** for the lag: *adds* always apply; *removals* apply only
  when our producer's latest CE `time` ≥ the vehicle's `groups_updated_at` (our
  attestation has caught up). If it's behind, keep the optimistic local state.
- Optional: **post-write confirmation** — after publishing, poll Fetch until our CE
  appears, mark the row confirmed; also tells the guard the lag has cleared.

## Decisions (locked)

1. **Removals deferred to Phase 2** — ship additive cron + lazy first.
2. **Lazy = frontend-initiated on vehicle select**, via a dedicated backend endpoint.
3. **Cron cadence = daily warm pass + weekly full pass** (less-active tenants get
   the weekly pass only).
4. **Activity = tenant user login recency.** Add `last_login_at` to `tenant_users`
   (updated on login); a tenant is **warm** if any of its users logged in within
   **N days (default 7)** → daily pass, else weekly. (No per-vehicle telemetry
   signal is stored, and login is a better proxy for "is anyone using this fleet"
   anyway.)
5. **`tenant_users` also gets `name`** — captured so Settings → Members can show who
   each member is (today it shows only wallet addresses).

## Surface to build (Phase 1)

- **Migration (sync state):** `groups_updated_at`, `last_group_sync_at` (on
  `vehicles`, or a small `vehicle_group_sync_state` table).
- **Migration (`tenant_users`):** add `last_login_at TIMESTAMPTZ` and `name TEXT`.
- **Login hook:** stamp `tenant_users.last_login_at = now` for the authenticated
  wallet on login / first authenticated request of a session. Decide the source of
  `name` (DIMO JWT name claim if present, else the DIMO account/identity profile,
  else user-entered in Settings).
- **Gateway:** typed `ListByDIDAndType(did, type, limit)` (groups-only fetch) so the
  per-vehicle sync doesn't over-pull all CE types.
- **Endpoint:** `POST /fleet/vehicles/:tokenID/groups/sync` (tenant-scoped) →
  per-vehicle additive sync; debounced on the client; sets `last_group_sync_at`.
- **CLI/cron:** extend `import-group-attestations` with a tenant-activity selector
  (e.g. `-warm-only`, which restricts to tenants whose `max(last_login_at)` is within
  N days); keep the full run for weekly. Helm: two CronJobs (daily warm, weekly
  full), kept opt-in / dry-run-validated.
- **Write path:** on assign/remove, also stamp `groups_updated_at = now` (already
  writes DB + publishes attestation).
- **Frontend:** on vehicle select, call the sync endpoint, then refresh the groups
  shown in the panel / detail view. Settings → Members shows each member's `name`
  (and optionally last-login).

## Resolved

- **Producer identity** — verified empty on our CEs (see Authoritative model). Phase 2
  must stamp our own `producer`; mirror-our-own + additive-for-foreign.
- **"Warm"/active definition** — tenant user login recency (`tenant_users.last_login_at`),
  N = 7 days default (Decisions 4–5).

## Open questions / to verify

- **Attest API persists a caller-set `producer`?** Gates Phase 2. Verify the CE
  envelope accepts `producer` and Fetch returns it.
- **Source of `name`** for `tenant_users` — DIMO JWT name claim, DIMO identity/account
  profile, or user-entered in Settings.
- **Where to hook login** for `last_login_at` — there's no explicit login endpoint
  (JWT is validated per request); likely a lightweight "touch on first authenticated
  request per session/day" to avoid a write per request.
- **Cron scale/throttle:** weekly full = one Fetch call per vehicle (asset-JWT each).
  Throttle/batch; cap concurrency.
- **Lazy endpoint hardening:** debounce rapid selections; per-vehicle cooldown using
  `last_group_sync_at` so repeated views don't hammer Fetch API.
