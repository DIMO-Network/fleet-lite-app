# Monorepo consolidation: fleet-lite-app, parentos

Status: revised 2026-10-07 — kaufmann-oracle dropped from scope (was originally included as a third app; see "Decision: kaufmann-oracle excluded" below). Phases 0–2 (partial) implemented; see `docs/superpowers/plans/`.

## Goal

Put the shared core of two near-identical web apps in one repo so a core fix lands once and every app picks it up. Each app keeps only its own domain code and still deploys separately. Spinning up a third app (web, Go+Fiber shaped) should take hours, not days.

Success criteria:
- A change to `core/` is built and tested against all apps in one PR. No version drift by construction.
- Each app keeps its own image, chart, database, secrets, namespace, and prod release cadence.
- A new app is scaffolded from one command and deploys through the same pipeline.

## Non-goals

- Merging runtime state (databases, secrets, namespaces) between apps.
- rental-fleets-app. It is the common ancestor, but it is out of scope until these two are done.
- Rewriting app domain logic. Extraction moves code, it does not redesign features.
- **Including kaufmann-oracle in this repo.** It's a device-ingestion service (TCP listener, on-chain minting, no web frontend), not a web app, and shared almost nothing with fleet-lite-app/parentos even before accounting for shape (1 of 44 compared Go files identical). Folding it in made the monorepo's purpose — "the shared core of near-identical web apps" — awkward to explain and awkward to onboard a new web app into. It stays its own repo. Its Go 1.26 bump (this spec's old Phase 0 prep) already landed there independently and is harmless to keep regardless. See "Decision: kaufmann-oracle excluded" for what else that undoes.

## Findings

| | fleet-lite-app | parentos |
|---|---|---|
| Shape | `api/` Go + `web/` Lit/Vite + Helm | same |
| Go | 1.26 | 1.26 |
| Non-test Go LOC (excl. models) | ~27k | ~9k |
| Dockerfile | `Dockerfile` | `Dockerfile` |
| Values | `values.yaml` + `values-prod.yaml` | same |
| Prod release | tag `v*` builds and bumps `values-prod.yaml` | same |
| Chart | trimmed clone of rental-fleets-app | same |

Overlap measured by path, after normalizing module paths:
- Go, fleet-lite vs parentos: 8 files identical, 36 diverged at the time of this spec's first draft. Drift had already started; `core/config` and `core/server` (Phase 2) have since reclaimed the identical portion of it.
- Web, fleet-lite vs parentos: 400 of 444 files identical as of this spec's first draft — stale as of fleet-lite-app's visual refresh (see Web core below); needs re-measuring before Phase 3. **Re-measured 2026-10-07 in `dimo-monorepo`, first pass (399/443) was wrong in a way that mattered — corrected same day:** the first re-measure counted 399 of 443 common `web/src` paths as byte-identical and reported that as the Phase 3 `packages/web-core` candidate pool. **387 of those 399 are static files under `src/assets/` (fonts, logos, images)** — not code. The actual identical *source* pool is 12 files: `context.ts`, `elements/index.ts`, `generated/locale-codes.ts`, `index.ts`, `localization.ts`, `services/settings-service.ts`, `types/document.ts`, `types/group.ts`, `utils/{brand-logo,logo-color,units,utils}.ts`. Of the 56 common non-asset source files, 44 (79%) are diverged and only 12 (21%) are identical — the inverse of what "399/443 identical" implied. fleet-lite also has 42 web files parentos doesn't (glovebox, TCO, charging — features parentos lacks); parentos has 12 fleet-lite doesn't. **Practical effect:** Phase 3 is not "move ~400 identical files, triage 44" — it's much closer to the Go-core shape: a small identical core (12 files, plus the asset directory as a separate, easy win) and a much larger diverged set (44 files) needing the same per-file reconciliation-before-extraction discipline as `core/config`/`core/server`/`core/db` got. See Web core below.

kaufmann-oracle was compared here in earlier drafts (1 of 44 Go files identical, same layer names but mostly different code, Go-only device-ingestion shape with no web frontend) — see Non-goals for why it's excluded. That comparison is kept out of this table now since it's no longer a candidate app, not because the numbers changed.

## Target layout

```
apps/
  fleet-lite/     cmd/, internal/ (domain code, sqlboiler models, migrations), web/ entry, Dockerfile, chart values
  parentos/       same
core/             shared Go packages
packages/
  web-core/       shared frontend (npm workspace)
charts/
  base/           shared Helm library chart
.github/workflows/
  build-app.yml   reusable build/push/bump workflow
  ci.yml          path-filtered lint/test
scripts/
  new-app         scaffolds apps/<name>
go.mod            single module at the repo root
```

### Go core

**As actually built (Phases 1-2): `go.work` with a separate `core` module plus each app's own existing `go.mod`, not a single root `go.mod`.** This spec originally proposed a single root `go.mod` as the strongest anti-drift option, with `go.work` as a fallback "if kaufmann's heavier dependencies in the shared graph proves a real cost" — with kaufmann out of scope that specific cost never applied, but `go.work` was used anyway because each app's `go.mod` already existed with its own independent dependency set (sqlboiler, DIMO SDKs, etc.) before this migration started, and collapsing two live modules into one is a bigger, riskier step than adding a third small module (`core`) and wiring both apps to it via `replace` directives. `core/config` and `core/server` both shipped this way. Each new `core/` package's dependencies are pinned to match whatever version both apps already use (checked explicitly per package, e.g. `go-ethereum`, `fiber/v2`), so the anti-drift property is preserved without unifying the whole dependency graph.

Candidate `core/` packages, each reconciled from the diverged copies before extraction:
- `config`: settings loading and common fields (ports, DB, JWT key set URL, DIMO endpoints). **Done** — `core/config`.
- `server`: Fiber bootstrap, error handling, health/version endpoints, and DIMO JWT audience enforcement (folded `auth` in here rather than as its own package — the two apps' JWT wiring was small enough to live alongside the bootstrap it's attached to). **Done** — `core/server`.
- `db`: goose migration runner. **Done** — `core/db.MigrateCmd` (PR #14). Connection setup itself (`db.NewDbConnectionFromSettings`, `db.Store`) was already in the external `github.com/DIMO-Network/shared/pkg/db` package, not duplicated in either app — nothing to extract there. Migrations and sqlboiler models stay per app, as planned.
- `gateway`: identity-api, telemetry-api, device-definitions-api clients. **Checked, deliberately skipped for now** (see PR #14's description). `identity_api.go`/`fetch_api.go` are structurally close to identical (import-path-only diff) but coupled to each app's `models.Vehicle`/`models.Tenant`, which genuinely diverge mid-tenancy-migration — fleet-lite's `Vehicle` carries `CanShare`/`ShareBlocker`/`Groups`/`MetadataPending` (operator-tenancy fields), parentos' carries `Driver *FamilyMember`. Extracting now means designing a generics-based abstraction over that shape, which is real design work, not a mechanical move — revisit once tenancy work lands and the shape stabilizes. `document_types.go` has zero model coupling and is the easiest follow-up candidate; needs one parameter for fleet-lite's "fuel" doc-type extra.
- `errors`, `permissions`. **Checked, skipped.** Both apps' `internal/core/errors.go`/`permissions.go` are identical 4-8 line stubs ("empty for now" on permissions) — already effectively covered by `core/server`'s `ErrorHandler`/`ErrorRes`, and `permissions.go` is about to churn once `role` becomes a capability check (see Tenancy section of `AGENTS.md`). Not worth a package yet.

Extracted only after diffing, and only if the copies are the same concept: `tenants`, `reports/distance_travelled`, `attestation/vinvc`. fleet-lite's tenancy is being superseded (see its `docs/operator-tenancy/`), so `tenants` is not extracted until that lands.

Rule: an app package moves to `core/` only when at least two apps use it. Anything single-use stays in the app.

### Web core

`packages/web-core` holds the identical source files (12 — see Findings) plus `src/assets/` (fonts, logos, images — 387 files, identical and a trivially safe move since nothing imports them by TS path, only by URL/static reference). Everything else — shared elements, `global-styles.ts` tokens, services, utils that *look* shared but have diverged post-refresh — goes through the same one-package-at-a-time reconciliation the Go core work used, not a bulk move. Each app keeps its views, branding, and entry point. Lit, Vite, and TypeScript versions are pinned once at the workspace root.

**Styling baseline: fleet-lite-app's new visual language, not parentos' current one.** fleet-lite-app landed a visual refresh (`04c7bae`, PR #174, 2026-09-25) that replaces the old Stitch/Material export look with the DIMO Driver mobile app's design language — Euclid Circular A, blue-black surfaces, the sky→mint gradient reserved for brand moments, sentence-case labels, tonal surfaces over hairline borders. It's documented in `docs/DESIGN.md` (principles + token table) and implemented in `web/src/global-styles.ts`. This lands after fleet-lite/parentos were compared at "400 of 444 web files identical" — that comparison predates the refresh and needs redoing once `web-core` extraction starts, since the refresh touched ~25 shared elements (`side-nav`, `app-root`, every modal, `tenant-members`, `tenant-switcher`, login/onboarding pages) that were previously identical between the two apps.

Practical effect on phase 3 (web core extraction):
- `packages/web-core`'s tokens, fonts and shared elements are extracted from fleet-lite-app's post-refresh code, not parentos'.
- parentos adopts the new tokens as part of moving onto `web-core`, rather than web-core supporting two visual languages.
- `docs/DESIGN.md` moves to the repo root (or `packages/web-core/docs/`) as the one design doc for all apps, superseding the need for each app to restate it.
- Re-diff fleet-lite vs parentos web files at the start of phase 3 — the divergence count above is now stale. **Done, and corrected same day** — see Findings above. Real identical-source pool is 12 files, not ~400; the ~400 figure was almost entirely `src/assets/`. Extraction itself not yet started: the npm workspace doesn't exist yet (no root `package.json`), and the 12-file + assets move is a different, much smaller first PR than the 44-file reconciliation that follows it.

### Base Helm chart

**Revised now that kaufmann-oracle isn't imported.** The original plan derived `charts/base` from kaufmann-oracle's chart by importing it wholesale, since it had HPA/PDB/ServiceMonitor/PrometheusRule alert templates that fleet-lite's and parentos' trimmed-from-rental-fleets-app charts lacked. Without the import, build `charts/base` the same way but by hand: read kaufmann-oracle's chart as a reference (it's a public sibling repo, just not a dependency of this one) and port the templates this repo's two apps actually want — likely HPA and PDB at minimum, ServiceMonitor/PrometheusRule if either app wants real alerting, certificates only if needed. `charts/base` is a library chart: deployment, service, ingress, ExternalSecret, ServiceMonitor, and, behind flags, HPA, PDB, PrometheusRule alerts, and certificates. Each app chart depends on it and sets only host, env, secrets, resources, and flags.

## CI/CD

Same mechanism as today: push to `main` builds an image and commits `image.tag` into a values file; a tag does the same for prod. Only the file layout changes.

- One reusable `build-app.yml`. Each app calls it with a config block: Dockerfile path, chart path, tag format, single or split values files, platforms.
- Path filters: an app builds on `apps/<app>/**` and `core/**`. A `core/` change rebuilds and redeploys both apps to dev. It reaches prod only through each app's own tag.
- Prod tags are per app: `fleet-lite/v1.4.0`, `parentos/v0.9.2`.
- PRs touching `core/` run lint and test for every app. Slower, but it is the guard against breaking a sibling.
- The bump commit only touches values files. **Confirmed in Phase 2:** `dimo-monorepo` has no root `.github/workflows/` yet at all (this bump-commit-retrigger question is unresolved because CI isn't wired up yet, not because it was checked and found safe — still needs real verification once `build-app.yml` exists).
- Go moves to 1.26 everywhere — already true for both apps. Workflow action versions (checkout, golangci-lint) get pinned to current releases when `build-app.yml` is written; no longer sourced from kaufmann-oracle's workflows specifically, since it's not in this repo, but still a reasonable repo to check for patterns given it shares DIMO's general CI conventions.
- **Known blocker found during Phase 2 review, not yet resolved:** each app's `Dockerfile` builds by `COPY`ing only that app's own `api/` directory, so `core`'s `replace => ../../../core` module directive won't resolve inside that build context. Needs a decision (vendor `core/` into each app's build context, widen the Docker build context to the repo root, or make the Docker build workspace-aware) before `build-app.yml` can actually build an image that imports `core/`.

## Migration

Incremental, one reviewable step at a time. Every step leaves all apps green and deployable. Each phase is its own implementation plan.

0. **Done.** Prep, in the existing repos: kaufmann to Go 1.26 (harmless, kept even though kaufmann is no longer part of this migration), deployment inventory doc.
1. **Done.** Create the monorepo and import fleet-lite and parentos with full history under `apps/`. No code changes.
2. **Done.** Extract Go core from fleet-lite and parentos, one package per PR. `config`, `server`, `db` extracted; `gateway` and `errors`/`permissions` deliberately left in-app for now (see Go core above for why — not a gap, a reviewed decision).
3. **In progress.** Extract web core from the identical files first, then triage the diverged ones. Re-diff done (see Findings): 12 identical source files + 387 identical asset files, 44 diverged. Extraction itself not started.
4. **Not started, revised scope.** Base Helm chart, built by hand using kaufmann-oracle's chart as a reference rather than an import (see Base Helm chart above).
5. **Not started.** Scaffold and docs: `scripts/new-app`, a short "new app" doc, a root `AGENTS.md`, and the `build-app.yml` reusable workflow (which also needs the Docker-build-context question resolved first — see CI/CD above). Archive the old repos read-only once both apps are fully on the monorepo.

**Dropped:** importing kaufmann-oracle. See Non-goals.

## Risks

- **Reconciling diverged code.** Only 8 of 44 shared Go files are identical, so each extraction needs a decision about which behavior wins. Mitigation: one package per PR, and tests written against the behavior before code moves (the apps have almost no tests today).
- **Blast radius of core changes.** A bad `core/` change can break every app's dev deploy. Mitigation: all-app CI on `core/` PRs, and per-app prod tags.
- **Deployment cutover.** Argo tracks specific repos and paths today. Repointing is the riskiest non-code step; not yet attempted — Phases 0-2 only added code, no app has actually been repointed to deploy from `dimo-monorepo` yet.
- **Tag scheme change.** Per-app tags (`app/vX`) replace `vX`. Anything that reads the old tags (scripts, dashboards) needs updating.

## Testing and verification

- Phase 1: the image built from the monorepo boots and passes `/health` in dev, and its chart renders byte-identical values to the old repo's chart. (Not yet exercised — no CI/deploy pipeline wired up for `dimo-monorepo` yet, per Phase 5.)
- Phases 2-4: `go build ./...`, `go vet`, `go test ./...`, web lint and build, and `helm template` for every app, on every PR. This is the pattern Phase 2's `core/config` and `core/server` PRs already followed, plus a runtime smoke test per package (catches a dropped parameter a type-check alone wouldn't).
- Characterization tests for each `core/` package are written before extraction, from the current behavior of the more complete copy.

## Decisions and open questions

Decided:
- **Name:** the monorepo is `dimo-monorepo` (module `github.com/DIMO-Network/dimo-monorepo`).
- **Tenancy is further along than this spec first assumed — corrected below.**

**Correction (found while chasing the ArgoCD discrepancy, which turned out to be a stale local clone — see kaufmann note below):** `docs/operator-tenancy/README.md` still says "Status: design / pre-implementation," but that is stale. Both fleet-lite-app and kaufmann-oracle have already executed the migration through phase P5b: `access_tenants` and `access_fleet_groups` are dropped in both repos (0 remaining references in fleet-lite's `internal/`), group and membership reads/writes go through `fleet-tenancy-api`, and the local authorization path (`TENANCY_AUTHZ_ENABLED`) has been removed as dead code, not just flagged off. fleet-lite-app's `internal/service/tenant.go` (+ its test) is what remains — a thin client to the tenancy service, not a local tenant model. So:
- **`tenants` still stays out of `core/`,** but not because it's "being deleted" — because what's left (`tenant.go`) is already a fleet-tenancy-api client, and each app's client usage should be reviewed for extraction on its own merits once the Go core work reaches that package, rather than assumed out of scope.
- **Action:** update `docs/operator-tenancy/README.md`'s status line (and check whether `fleet-lite-app/AGENTS.md`'s tenancy warnings still describe the current code) as a small housekeeping PR, separate from this migration — the doc is actively misleading about how much of the risky work is already done.

Decided:
- **kaufmann prod trigger:** prod deploys when a GitHub Release is created (which pushes the `v*` tag `buildpushtagged.yml` listens for).

Confirmed from ArgoCD (prod apps, repo / path / target revision / values file):
- fleet-lite-app: `git@github.com:DIMO-Network/fleet-lite-app.git`, `charts/fleet-lite-app`, `HEAD`, `values-prod.yaml`
- parentos: `git@github.com:DIMO-Network/parentos.git`, `charts/parentos`, `HEAD`, `values-prod.yaml`
- kaufmann-oracle: `git@github.com:DIMO-Network/kaufmann-oracle.git`, `charts/kaufmann-oracle`, `HEAD`, `values-prod.yaml`

**Resolved — was a stale local clone, not a real discrepancy.** My local `kaufmann-oracle` checkout was 533 commits behind `origin/main`; re-pulling shows kaufmann-oracle has since gained the same dev/prod split fleet-lite and parentos already had: `buildpushdev.yml` (push to `main`) writes `charts/kaufmann-oracle/values.yaml`, and `buildpushtagged.yml` (push tag `v*`, i.e. a GitHub Release) writes `charts/kaufmann-oracle/values-prod.yaml`. That matches what ArgoCD reported. The earlier finding that kaufmann has "one `values.yaml` with `ENVIRONMENT: prod`" and no dev buffer is **stale and retracted** — treat all three apps as having the same dev/prod deploy shape.

**Lesson from this migration:** all repos involved are actively developed elsewhere while being planned around. Before acting on any finding in this spec, re-verify it against the live repo rather than trusting a snapshot — this bit the migration twice already (a stale local clone, and a merged-vs-open PR state mix-up during Phase 2).

### Decision: kaufmann-oracle excluded (2026-10-07)

kaufmann-oracle will not be imported into `dimo-monorepo`. Reasoning: it's a device-ingestion service with no web frontend, sharing almost no code with fleet-lite-app/parentos (1 of 44 compared Go files identical in the original survey), and folding it into a repo whose purpose is "shared core for near-identical web apps" made that purpose harder to explain and the repo harder to onboard a genuinely-similar future web app into. It stays its own repo, deployed and developed independently, same as before this migration started.

What this undoes from the original plan, and what's unaffected:
- **Undone:** Phase 5 (import kaufmann-oracle) is dropped entirely, not deferred.
- **Undone:** the Base Helm chart (Phase 4) is no longer built by importing kaufmann-oracle's chart — it's built by hand, using kaufmann-oracle's chart as a read-only reference (see Base Helm chart above).
- **Undone:** the "single root go.mod, fallback to go.work if kaufmann's dependencies are too heavy" framing — moot, since kaufmann was the only reason that tradeoff existed. (Separately, Phases 1-2 already used `go.work` regardless, for reasons unrelated to kaufmann — see Go core above.)
- **Kept, harmless either way:** kaufmann-oracle's Go 1.26 bump (Phase 0), done directly in its own repo, independent of this migration.
- **Kept, unaffected:** everything already built — `core/config`, `core/server`, the fleet-lite-app/parentos import, the parentos security/data-integrity fixes found along the way (`TENANT_SECRET_ENC_KEY` provisioning, JWT audience check). None of it referenced or depended on kaufmann-oracle.
- **Unaffected:** the scaffold script (Phase 5, was Phase 6) — still useful for a future web app, which is now explicitly the kind of app this repo is for.
