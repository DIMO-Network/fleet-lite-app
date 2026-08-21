# fleet-lite-app

A lighter-weight DIMO fleet companion app, modelled on
[`rental-fleets-app`](../rental-fleets-app) architecture with UI ported from the
Stitch design source in `../stitch_fleet-lite-dimo`.

## Stack

- **web/** — Vite + Lit 3 + TypeScript. Routed with `@lit-labs/router`.
  Design tokens live in `web/src/global-styles.ts` (CSS custom properties +
  shared Lit `css` exports). No Tailwind at runtime — utility classes from the
  Stitch HTML are converted to scoped CSS in each element.
- **api/** — Go backend (Fiber + zerolog). JWT auth + `/vehicles`,
  `/telemetry/*`, `/documents/*` (glovebox), and DIMO identity-api proxies.
  Serves the built `web/dist` SPA in production.
- **charts/** — Helm chart in `charts/fleet-lite-app/` (dev `values.yaml` +
  prod `values-prod.yaml`, host `fleets.dimo.co`).

## Local dev

### Quick start

```sh
make dev
```

`make dev` brings up the whole stack: it checks the dev host is in `/etc/hosts`,
ensures a local Postgres role + database exist, copies `settings.yaml`, installs
web deps, then runs the frontend (Vite) and backend (Go) together. Ctrl-C tears
both down. Run `make help` to see the individual targets.

Two one-time prerequisites it can't do for you:

1. **`/etc/hosts`** — needs a loopback entry (a `*.dimo.org` subdomain, so the
   WebAuthn relying-party-id rules stay satisfied). `make dev` checks for it and
   prints these commands if it's missing:

   ```sh
   echo '127.0.0.1 local-fleet-lite.dimo.org' | sudo tee -a /etc/hosts
   sudo killall -HUP mDNSResponder
   ```

2. **Postgres** — must be running locally. `make dev` auto-creates the `dimo`
   role and `fleet_lite_app` database, but can't install Postgres itself:

   ```sh
   brew install postgresql@16 && brew services start postgresql@16
   ```

On first run `vite-plugin-mkcert` generates `web/.mkcert/cert.pem` + `dev.pem`
and installs a root CA in the macOS keychain (one-time sudo prompt). The backend
reads those same certs for its TLS listener. The app is then reachable at:

> https://local-fleet-lite.dimo.org:3009

### Running pieces individually

```sh
make web   # frontend only (Vite dev server, generates the mkcert certs)
make api   # backend only  (settings + migrations + Go server; needs the certs)
```

## CLI commands

The API binary doubles as a CLI (google/subcommands). With no arguments it
starts the server; with a subcommand it runs the task and exits. Locally:
`go run ./cmd/fleet-lite-app <subcommand>` from `api/`; in a pod the binary
is `/fleet-lite-app`.

| Subcommand | What it does | Flags |
|---|---|---|
| `migrate` | Migrate the database | `-up`, `-down` |

## Design source

The UI was ported from `/Users/jreate/Source/stitch_fleet-lite-dimo/`:

| Stitch screen           | Lit view (web/src/views/)   |
|-------------------------|------------------------------|
| `fleet_overview/`       | `fleet-overview.ts`         |
| `vehicle_details/`      | `vehicle-details.ts`        |
| `glovebox/`             | `glovebox.ts`               |
| `account_settings/`     | `account-settings.ts`       |
| `obsidian_telemetry/`   | tokens in `global-styles.ts`|

See [docs/PLAN.md](docs/PLAN.md) for the full implementation plan and to-do
list.
