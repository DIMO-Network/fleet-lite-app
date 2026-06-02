# Agent Guidelines — fleet-lite-app

This repo follows the same conventions as
[`rental-fleets-app`](../rental-fleets-app). When in doubt, mirror that repo's
behaviour. The notes below capture the **deltas** that are specific to
fleet-lite-app, and the rules that are most likely to bite an agent working
here.

See [`docs/PLAN.md`](docs/PLAN.md) for the implementation plan and what's been
built so far.

## Layout

```
api/      Go backend — Fiber + zerolog + goose migrations. JWT auth +
          /vehicles, /telemetry, /documents (glovebox), and identity-api
          proxy endpoints. Mirrors rental-fleets-app/api/ layout (cmd/,
          internal/{app,config,core,controllers,gateway,models,service,db}).
web/      Vite + Lit 3 + TypeScript. Production-ready stack, all four
          designed views ported from ../stitch_fleet-lite-dimo.
charts/   Helm chart in charts/fleet-lite-app/, cloned from rental-fleets-app
          and trimmed.
docs/     PLAN.md and future docs.
```

## API endpoints (current)

Public:
- `GET  /health` — DB ping
- `GET  /version` — commit hash
- `GET  /public/settings` — client-safe config (Login With DIMO, chain, addresses)
- `GET  /identity/vehicle/:tokenID`, `GET /identity/definition/:id`,
  `GET /identity/owner/:owner`, `POST /identity/proxy` — DIMO identity-api proxies

Authenticated (DIMO JWT in `Authorization: Bearer ...`):
- `GET /vehicles` — vehicles owned by the JWT's `ethereum_address`
- `GET /vehicles/:tokenID` — single vehicle by tokenID
- `GET /telemetry/:tokenID/latest`, `GET /telemetry/:tokenID/timeseries` — DIMO telemetry-api
- `POST /documents/extract`, `GET /documents/vin-lookup`, `POST /documents/attest`,
  `GET /documents/list`, `GET /documents/download` — glovebox (see docs/GLOVEBOX.md)

Monitoring (separate port, no auth):
- `GET /metrics` on `MONITORING_PORT` (default `8085`) — Prometheus

## Web conventions

These differ slightly from rental-fleets-app and are easy to forget:

- **No Tailwind at runtime.** The Stitch source HTML uses Tailwind via CDN; we
  do *not* ship it. Utility classes are converted into scoped CSS inside each
  Lit element's `static styles`. Tokens live in `web/src/global-styles.ts` as
  CSS custom properties and are exposed via `sharedStyles`.
- **Material Symbols redeclaration.** Each Shadow DOM needs its own
  `.material-symbols-outlined` font-family declaration. `sharedStyles` already
  includes it — pull in `sharedStyles` and you get it.
- **Active nav fill.** The active sidebar item uses
  `font-variation-settings: 'FILL' 1` on its Material Symbol. Don't swap icon
  names for filled variants; just toggle the variation setting via a CSS class.

## Running locally

See [`README.md`](README.md). TL;DR:

```sh
echo '127.0.0.1 local-fleet-lite.dimo.org' | sudo tee -a /etc/hosts
sudo killall -HUP mDNSResponder
cd web && npm install && npm run dev
# → https://local-fleet-lite.dimo.org:3009
```

mkcert generates `web/.mkcert/cert.pem` and `key.pem` on first run.

## Ports

- `3009` — Vite dev server (rental-fleets-app uses 3008; don't collide).

## Future api/ standards

When the Go backend lands, follow the same standards as
`rental-fleets-app/AGENTS.md`:

- `stretchr/testify` for assertions
- `go.uber.org/mock` for interface mocking
- goose for migrations
- sqlboiler for ORM
- testcontainers-go for DB-dependent integration tests
- migrations identified by wallet address, not user id
- migrations get `created_at` / `updated_at` `TIMESTAMPTZ NOT NULL DEFAULT NOW()` by default

## What's intentionally **not** in this repo yet

Don't add these unless asked — `docs/PLAN.md` lists them and the rationale:

- Passkey signing flow (zerodev / turnkey / @dimo-network/transactions)
- Leaflet / real map tiles
- Chat surface (marked / dompurify) and the chat agent backend
- Tenant model, alerts, ledger, maintenance, reports, rental sessions,
  guests, kore, google-calendar (all stripped from the API — see PLAN.md §"Deliberately dropped from rental-fleets-app/api")
- River job queue + Langfuse observability
- Tests

Already landed (no longer "not yet" — the frontend is wired to the real api,
not mock data): `/vehicles`, telemetry charts, the glovebox documents flow
(extract / attest / list / download), and CI workflows (`.github/workflows/`).
