# fleet-lite-app — Implementation Plan

**Goal:** Stand up a new repo `fleet-lite-app` modeled on the architecture of
`rental-fleets-app`, with the UI driven by the Stitch designs in
`/Users/jreate/Source/stitch_fleet-lite-dimo/`, served locally over HTTPS at
`https://local-fleet-lite.dimo.org:3009`.

Created: 2026-05-27

---

## Source material

| Source | Role |
|---|---|
| `/Users/jreate/Source/rental-fleets-app/` | Architectural template (repo layout, web/ Vite+Lit stack, mkcert pattern, dev-host pattern) |
| `/Users/jreate/Source/stitch_fleet-lite-dimo/` | Design source — 5 HTML/CSS Stitch screens + the Obsidian Telemetry DESIGN.md token spec |

### Stitch screens to port

1. `fleet_overview/` — sidebar nav + full-bleed map + bottom/right "Your cars" glass panel
2. `vehicle_details/` — single-vehicle drill-in
3. `glovebox/` — documents
4. `account_settings/`
5. `obsidian_telemetry/DESIGN.md` — token reference (colors, typography, spacing, radii, component conventions)

The Stitch HTML uses Tailwind via CDN and an embedded `tailwind.config` block. We will **not** ship Tailwind. Instead we extract the tokens into CSS custom properties and convert utility classes into Lit `static styles` scoped per element, matching the rental-fleets-app pattern.

---

## Target architecture (mirrors rental-fleets-app)

```
fleet-lite-app/
├── api/                 # Go backend (scaffold only for now)
├── web/                 # Vite + Lit + TypeScript
│   ├── .mkcert/         # generated on first `npm run dev`
│   ├── public/
│   ├── src/
│   │   ├── elements/    # reusable Lit elements (app-root, side-nav, etc.)
│   │   ├── views/       # routed views (fleet-overview, vehicle-details, …)
│   │   ├── services/
│   │   ├── types/
│   │   ├── utils/
│   │   ├── assets/
│   │   ├── context.ts
│   │   ├── global-styles.ts   # design tokens
│   │   └── index.ts
│   ├── index.html
│   ├── vite.config.js
│   ├── tsconfig.json
│   ├── eslint.config.js
│   ├── lit-localize.json
│   └── package.json
├── charts/              # Helm (placeholder)
├── docs/
│   └── PLAN.md          # this file
├── scripts/
├── .github/workflows/
├── .gitignore
├── AGENTS.md
├── Dockerfile
└── README.md
```

### Web stack choices (kept identical to rental-fleets-app where possible)

- **Framework:** Lit 3 + `@lit-labs/router` + `@lit/context` + `@lit/localize`
- **Build:** Vite 8 + TypeScript 5
- **Dev TLS:** `vite-plugin-mkcert` (auto-generates CA + cert into `web/.mkcert/`)
- **Lint:** ESLint 9 + `eslint-plugin-lit` + `typescript-eslint`
- **Fonts:** Inter + JetBrains Mono + Material Symbols Outlined (loaded from Google Fonts CDN, same as rental-fleets-app)

### Dropped (vs rental-fleets-app)

These are kept out of the initial scaffold to stay lean — add back when the corresponding feature lands:

- `@dimo-network/transactions`, `@turnkey/*`, `@zerodev/*` (passkey signing flow)
- `leaflet`, `@types/leaflet` (map is a static background in the Stitch design; revisit when we wire real telemetry)
- `marked`, `dompurify` (chat surface — not in scope)

---

## Local-dev networking

- **Hostname:** `local-fleet-lite.dimo.org` → `127.0.0.1`
- **Port:** `3009` (rental-fleets-app uses 3008, b2b-fleet-mgr-app and others sit nearby — 3009 is free)
- **Why a `*.dimo.org` subdomain:** WebAuthn Relying Party ID rules require the dev origin to share a registrable suffix with `dimo.org` for passkey flows to work once we add signing. Following the same convention as `local-fleets.dimo.org`, `localdev.dimo.org`, `localtesla.dimo.org`.

`/etc/hosts` line to add:

```
127.0.0.1 local-fleet-lite.dimo.org
```

Then flush mDNS: `sudo killall -HUP mDNSResponder`.

`vite-plugin-mkcert` will, on first run, generate `web/.mkcert/cert.pem` + `key.pem` covering localhost, 127.0.0.1, and the new hostname, and install a root CA in the macOS keychain (requires one-time sudo prompt).

---

## To-Do (execution order)

> Live status is also tracked via TaskCreate/TaskUpdate during the session. This list is the durable copy.

- [x] **1. Scaffold repo skeleton** — directories, `.gitignore`, `git init`
- [x] **2. Write this PLAN.md** to `docs/`
- [ ] **3. Scaffold `web/`** — port `package.json`, `tsconfig.json`, `eslint.config.js`, `lit-localize.json`, `Makefile`, `index.html`, `src/index.ts`, empty `src/elements/index.ts` + `src/views/index.ts`, `src/context.ts`, `public/`, `src/assets/`. Drop rental-specific deps. Rename project, title, app-root tag.
- [ ] **4. Configure `vite.config.js`** — host=`local-fleet-lite.dimo.org`, port=3009, https, mkcert plugin → `.mkcert/`, eslintPlugin, viteStaticCopy for assets. Single `main` entry (no login/signup for now).
- [ ] **5. Add `/etc/hosts` entry** for `local-fleet-lite.dimo.org` → `127.0.0.1` and flush mDNS.
- [ ] **6. Extract design tokens** from `stitch_fleet-lite-dimo/obsidian_telemetry/DESIGN.md` and the tailwind-config block in `fleet_overview/code.html` into `web/src/global-styles.ts` as CSS custom properties + a shared `sharedStyles` Lit `css` export.
- [ ] **7. Build app shell** — `<app-root>` element with side nav (Vehicles / Stats / Glovebox / Settings + Support/Sign Out), top-bar slot, router outlet. Mirror rental-fleets-app/web/src/elements/app-root-v2.ts pattern but stripped to the Stitch design.
- [ ] **8. Port views** — for each Stitch screen, create a Lit view that renders the markup using tokens (Tailwind utility classes converted to scoped CSS):
  - `views/fleet-overview.ts`
  - `views/vehicle-details.ts`
  - `views/glovebox.ts`
  - `views/account-settings.ts`
- [ ] **9. Wire routes** — `@lit-labs/router` with `/`, `/vehicles/:tokenId`, `/glovebox`, `/settings`.
- [ ] **10. First boot** — `npm install`, `npm run dev`, hit `https://local-fleet-lite.dimo.org:3009`, confirm cert is trusted and Fleet Overview renders.
- [x] **11. Initial commit** on `main`. → Pushed to `DIMO-Network/fleet-lite-app` (public).
- [x] **12. Scaffold placeholders** — AGENTS.md + api/README.md + charts/README.md committed. Dockerfile deferred until api/ has a buildable binary (covered in §"API expansion" below).

---

## API expansion (2026-05-27)

Scope was widened mid-build: the Go API and helm chart move from out-of-scope to **in-scope** at *Skeleton + Auth + Vehicles* depth. Rationale: enables the frontend to hit a real backend for the logged-in user's cars, replacing mock data, with the minimum set of subsystems needed.

### What lands in this round

- `api/` Go service mirroring `rental-fleets-app/api/` directory layout
- JWT auth via DIMO JWKS (`github.com/gofiber/contrib/jwt`)
- `/vehicles` endpoint: returns `identity-api` vehicles owned by the JWT's wallet
- `/identity/*` public proxy endpoints used by the frontend
- `/health` + `/version` + Prometheus `/metrics` on monitoring port
- Fiber static-serving of `../web/dist` so the same binary serves the SPA in prod
- `goose` migrations runner (no migrations yet — `/vehicles` is read-only against identity-api)
- Real multi-stage `Dockerfile` that builds web + api in one image
- `charts/fleet-lite-app/` helm chart cloned from rental-fleets-app and trimmed

### Deliberately dropped from rental-fleets-app/api

These subsystems exist in rental-fleets-app but have no place in fleet-lite-app's surface — every one of them adds dependency weight without serving a frontend feature:

- **Tenant model** — fleet-lite is per-user, not multi-tenant. No `tenants` table, no `Tenant-Id` header middleware, no `tenant-selector` flow.
- **Chat agent + Langfuse** — no AI surface in this app.
- **River job queue** — no async report generation yet.
- **Google Calendar integration** — out of scope.
- **Webhooks (inbound email, telemetry)** — not wired.
- **Alerts WS + REST** — not wired.
- **Ledger + Vendors + Vehicle Costs + Maintenance + Reports** — fleet-operator-only features.
- **Rental Sessions + Guests** — Turo-specific.
- **Kore Wireless gateway** — SIM management out of scope.
- **Documents (extract / attest / fetch)** — glovebox is frontend-only mocks until a separate phase.
- **Pending vehicles / IMEI claiming** — onboarding flow not in scope.
- **Account management** — `accounts-api` proxy not wired yet.

If/when these come back, mirror the rental-fleets-app shape rather than reinventing.

### API to-do (execution order)

- [ ] **A1. Scaffold api root** — `go.mod` (module `github.com/DIMO-Network/fleet-lite-app`), `.golangci.yml`, `.gitignore`, `Makefile`, `settings.sample.yaml`, `sqlboiler.toml`.
- [ ] **A2. `cmd/fleet-lite-app/`** — `main.go` (trimmed: zerolog + signals + db + fiber + dimoauth + identity service + vehicles controller), `migrate.go` (goose subcommand).
- [ ] **A3. `internal/config`** — trimmed Settings struct.
- [ ] **A4. `internal/core`** — `errors.go`, `permissions.go` (copy as-is).
- [ ] **A5. `internal/gateway/identity_api*`** — typed `FetchVehiclesByWalletAddress` + `FetchVehicleByTokenID` + cache.
- [ ] **A6. `internal/service/identity_api`** — raw-bytes proxy for `/identity/*` endpoints.
- [ ] **A7. `internal/models/models.go`** — trimmed (Vehicle, Definition, SyntheticDevice, PageInfo, paged structs, GraphQlData).
- [ ] **A8. `internal/controllers/{common,identity,vehicles}.go`** — wallet extraction from JWT; identity proxy handlers; `GetVehicles` for current user.
- [ ] **A9. `internal/app/app.go`** — fiber app builder with middleware, routes, static serving.
- [ ] **A10. `internal/db/`** — placeholder migrations + models dirs.
- [ ] **A11. `go mod tidy` + `go build ./...`** — smoke test.
- [ ] **A12. Dockerfile** — multi-stage (Go build + npm build → busybox runtime).
- [ ] **A13. Helm chart** — `charts/fleet-lite-app/` cloned from rental-fleets-app, trimmed envs.
- [ ] **A14. Commit + push** — all of the above to `DIMO-Network/fleet-lite-app`.

### Conventions chosen for fleet-lite api

- DB name: `fleet_lite_app` (snake_case; matches `rental_fleets_app` convention).
- Binary name: `fleet-lite-app`.
- Module path: `github.com/DIMO-Network/fleet-lite-app`.
- API port: **8084** (rental-fleets uses 8082, b2b-fleet-mgr uses 8083 territory — leaving room).
- Monitoring port: **8085**.
- HTTPS dev origin: `https://local-fleet-lite.dimo.org:8084` when api is hit directly, but normal dev hits the frontend at `:3009` which proxies to `:8084` via Vite (when wired) or has the api serve the frontend's `dist/` (production-mimicking mode).

## Out of scope (still)

- CI workflows beyond a copy of the rental-fleets template
- Auth / passkey signing — design surface only, no working flow
- Live map tile provider — placeholder background image, as in the Stitch design
- Tests
- Frontend wiring to the new api (frontend still uses mock data; switch happens in a follow-up phase)

---

## Conventions to preserve from rental-fleets-app

- Lit `static styles = [sharedStyles, css\`…\`]` pattern; no Tailwind runtime.
- One element/view per file; explicit `customElements.define()` at the bottom.
- Side-effect-only barrel imports from `elements/index.ts` and `views/index.ts` so a single `import './elements/index.ts'` registers every tag.
- Dev cert lives in `web/.mkcert/` and is **not** committed (`*.pem` is gitignored).
- The Go API, when added, reads the same `.mkcert/` directory when `USE_DEV_CERTS: true`.

---

## Design token mapping (from Stitch → CSS vars)

Source: `stitch_fleet-lite-dimo/fleet_overview/code.html` tailwind config + `obsidian_telemetry/DESIGN.md`.

Naming follows Material Design 3 surface roles, which is what the Stitch designs already use.

```
--surface:                  #131313
--surface-dim:              #131313
--surface-bright:           #393939
--surface-container-lowest: #0e0e0e
--surface-container-low:    #1c1b1b
--surface-container:        #201f1f
--surface-container-high:   #2a2a2a
--surface-container-highest:#353534
--on-surface:               #e5e2e1
--on-surface-variant:       #c4c7c8
--outline:                  #8e9192
--outline-variant:          #444748
--primary:                  #ffffff
--on-primary:               #2f3131
--secondary:                #ffb691     /* accent orange */
--secondary-container:      #ea6b18
--tertiary-container:       #86f8c8     /* accent green */
--error:                    #ffb4ab
--background:               #131313

/* type scale */
--font-headline:   'Inter', sans-serif;
--font-mono:       'JetBrains Mono', monospace;
/* sizes per DESIGN.md typography block */

/* spacing */
--sidebar-width:  280px;
--gutter:         24px;
--container-max:  1440px;
```

The DESIGN.md "Brand & Style" section calls for a `#000000` background, but the tailwind config in the actual Stitch HTML uses `#131313`. We follow the HTML (which is what the screens actually render against) since the screenshots are the visual source of truth.
