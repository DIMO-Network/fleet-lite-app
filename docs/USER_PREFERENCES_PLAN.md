# User Preferences — Backend Persistence Plan

Persist per-user UI preferences (units, locale, trip-detection mechanism) on the
backend, keyed by **wallet**, so a user's choices follow them across browsers,
devices, and sessions instead of living only in one browser's `localStorage`.

Starts with the three preferences that exist today; the storage is a JSONB blob
so future preferences need no new migration.

Created: 2026-07-01
Status: design + first implementation (this PR).

---

## Problem

All user preferences live in `localStorage` via `PrefsService`
(`web/src/services/prefs-service.ts`):

| Preference | Key | Default |
|---|---|---|
| Units | `fleet-lite:units` | `metric` (was `imperial`) |
| Locale | `fleet-lite:locale` | browser language → `es`/`en` |
| Trip mechanism | `fleet-lite:trip-mechanism` | `auto` |

Because it's browser-local, a user who sets metric on their laptop sees the
default again on their phone or in a new browser. There is **no** backend record
of a user's preferences today.

## Identity model (reuse — don't rebuild)

- A user is a **wallet address**, read from the DIMO JWT via
  `GetWalletAddressFromJWT(c)` (the `ethereum_address` claim). The JWT carries
  no name/email.
- `tenant_users (tenant_id, wallet)` already tracks per-user `email` /
  `last_login_at`, written by the existing `POST /tenants/:id/login`
  (`TouchLogin`) touch endpoint.
- Preferences are **personal to the wallet, not the tenant** — a wallet in
  multiple tenants should not get divergent units. So preferences are keyed by
  **wallet alone**, in their own table, rather than as columns on the
  `(tenant_id, wallet)` membership row.

## Design

### 1. Table — `user_preferences`

```sql
CREATE TABLE IF NOT EXISTS user_preferences (
    wallet     VARCHAR(43) PRIMARY KEY,        -- lowercased 0x address
    prefs      JSONB NOT NULL DEFAULT '{}',    -- { units, locale, tripMechanism, ... }
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

- **Wallet-keyed**, lowercased for consistency with `tenant_users.wallet`.
- **JSONB blob** so adding a preference later is a one-line change to the
  backend whitelist — no migration. This is the "leverage for future" part.
- No FK to `tenant_users`: preferences outlive/precede any single membership and
  are wallet-global.

### 2. Backend

- `make sqlboiler` regenerates `internal/db/models/user_preferences.go`.
- `UserPrefsService` (needs only `logger` + `pdb`, constructed in `app.go`):
  - `Get(ctx, wallet) (map, error)` — returns the stored blob, or `{}`.
  - `Upsert(ctx, wallet, prefs map) error` — single-row upsert on `wallet`.
- `UserPrefsController`:
  - **`GET /me/preferences`** → `{ ...prefs }`.
  - **`PUT /me/preferences`** → full-replace; body is the preferences object.
  - Both authorize with `GetWalletAddressFromJWT` — the caller's own row, so
    they live under `authApp` (JWT only), **not** `tenantApp` (no tenant scope).
- **Sanitization:** the controller whitelists known keys to known enum values
  (`units ∈ {metric,imperial}`, `locale ∈ {en,es}`, `tripMechanism ∈ {…}`) and
  drops anything else, so the DB never holds arbitrary client JSON. Adding a
  preference = extend the whitelist.

### 3. Frontend — `PrefsService` becomes backend-backed, `localStorage` as cache

`localStorage` stays the **synchronous read path** so existing `getUnits()` /
`getLocale()` callers don't all become async. The backend is layered on:

- **Hydrate on boot** (`hydrateFromServer()`): `GET /me/preferences`, merge with
  precedence **server > localStorage**, write the merged values back to
  `localStorage`, and dispatch the existing `fleet-lite-prefs-changed` event so
  components re-render. Called once per session from `app-root` (guarded), right
  where `recordLogin` already fires — we know the caller is authed there.
- **First-sync seed / backfill:** if the merged result differs from what the
  server returned (i.e. the server was missing keys the browser had), `PUT` the
  merged blob up once. This migrates current users' existing choices instead of
  resetting them.
- **Write-through:** every `setUnits` / `setLocale` / `setTripMechanism` writes
  `localStorage` immediately (instant UI), then `PUT`s the full stored set,
  best-effort (catch-and-ignore, same detached pattern as the location
  write-through). Only pushes when a token is present.

### 4. Metric default

The client default flip (unset → `metric`) stays as the fallback used when
**neither** server nor `localStorage` has a value.

## Precedence summary

1. Explicit server value (another device already saved it) — wins.
2. Else explicit `localStorage` value (this browser) — used, and backfilled up.
3. Else the built-in default (metric / browser-locale / auto).

## Edge cases

- **Anonymous / pre-login:** no token → `PrefsService` behaves exactly as today
  (localStorage only); hydrate/push are no-ops.
- **Server unreachable:** hydrate and write-through are best-effort; the UI
  keeps working from `localStorage`.
- **Multi-tenant wallet:** one preferences row regardless of active tenant — by
  design.
- **Race:** a preference change during the async hydrate window can be
  overwritten by the server merge. Acceptable for v1 (hydrate runs early, once);
  revisit if it bites.

## Scope / phasing

Both land in **this PR**:

- **Backend:** migration + model + `UserPrefsService` + `/me/preferences`
  endpoints + wiring.
- **Frontend:** `hydrateFromServer`, write-through, first-sync seed in
  `PrefsService`; hook hydrate into `app-root`.

## Future

- New preferences (e.g. default map layer, table density, notification opts)
  drop into the same blob + whitelist — no migration.
- If preferences ever need to be tenant-scoped as well as user-scoped, add a
  nullable `tenant_id` and a composite key; the wallet-only row remains the
  personal default.
