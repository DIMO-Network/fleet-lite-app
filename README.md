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
  prod `values-prod.yaml`, host `fleet-lite.dimo.co`).

## Local dev

### 1. /etc/hosts

Add a loopback entry so the WebAuthn relying-party-id rules stay satisfied
(needs to be a subdomain of `dimo.org`):

```sh
echo '127.0.0.1 local-fleet-lite.dimo.org' | sudo tee -a /etc/hosts
sudo killall -HUP mDNSResponder
```

### 2. Run the web app

```sh
cd web
npm install
npm run dev
```

`vite-plugin-mkcert` generates `web/.mkcert/cert.pem` + `key.pem` on first run
and installs a root CA in the macOS keychain (one-time sudo prompt). After that
the app is reachable at:

> https://local-fleet-lite.dimo.org:3009

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
