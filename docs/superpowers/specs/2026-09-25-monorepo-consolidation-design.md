# Monorepo consolidation: fleet-lite-app, parentos, kaufmann-oracle

Status: draft for review. Nothing in this spec has been implemented.

## Goal

Put the shared core of three near-identical apps in one repo so a core fix lands once and every app picks it up. Each app keeps only its own domain code and still deploys separately. Spinning up a fourth app should take hours, not days.

Success criteria:
- A change to `core/` is built and tested against all apps in one PR. No version drift by construction.
- Each app keeps its own image, chart, database, secrets, namespace, and prod release cadence.
- A new app is scaffolded from one command and deploys through the same pipeline.

## Non-goals

- Merging runtime state (databases, secrets, namespaces) between apps.
- rental-fleets-app. It is the common ancestor, but it is out of scope until the three are done.
- Rewriting app domain logic. Extraction moves code, it does not redesign features.
- Sharing the web frontend with kaufmann-oracle (it has none).

## Findings

| | fleet-lite-app | parentos | kaufmann-oracle |
|---|---|---|---|
| Shape | `api/` Go + `web/` Lit/Vite + Helm | same | Go only, `cmd/` + `internal/` at repo root |
| Go | 1.26 | 1.26 | 1.25 |
| Non-test Go LOC (excl. models) | ~27k | ~9k | ~14k |
| Dockerfile | `Dockerfile` | `Dockerfile` | `resources/docker/Dockerfile` |
| Values | `values.yaml` + `values-prod.yaml` | same | `values.yaml` only (`ENVIRONMENT: prod`) |
| Prod release | tag `v*` builds and bumps `values-prod.yaml` | same | tag `v*` builds (amd64) and bumps `values.yaml` |
| Chart | trimmed clone of rental-fleets-app | same | full: HPA, PDB, ServiceMonitor, alert rules, certs |

Overlap measured by path, after normalizing module paths:
- Go, fleet-lite vs parentos: 8 files identical, 36 diverged. Drift has already started.
- Web, fleet-lite vs parentos: 400 of 444 files identical, 44 diverged.
- Go, kaufmann vs fleet-lite: 1 file identical, 14 diverged. Same layer names, mostly different code.

kaufmann-oracle is a device-ingestion service (TCP listener for Smart5 devices, on-chain minting, eleva/Kore, background retry jobs). Its `oracle`, `onboarding`, `ruptela`, `kore_wireless` code and TCP server stay app-specific.

## Target layout

```
apps/
  fleet-lite/     cmd/, internal/ (domain code, sqlboiler models, migrations), web/ entry, Dockerfile, chart values
  parentos/       same
  kaufmann/       cmd/, internal/ (oracle, onboarding, ruptela, ...), Dockerfile, chart values
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

One root `go.mod` so all apps share dependency versions. This is the strongest anti-drift option. The cost is that kaufmann's heavier dependencies appear in the shared graph. Go only compiles imported packages, so binaries stay small. A `go.work` with a module per app is the fallback if that cost proves real.

Candidate `core/` packages, each reconciled from the diverged copies before extraction:
- `config`: settings loading and common fields (ports, DB, JWT key set URL, DIMO endpoints).
- `server`: Fiber bootstrap, zerolog wiring, health and version endpoints, monitoring port and Prometheus.
- `auth`: DIMO JWT middleware.
- `db`: connection setup and goose migration runner. Migrations and sqlboiler models stay per app.
- `gateway`: identity-api, telemetry-api, device-definitions-api clients.
- `errors`, `permissions`.

Extracted only after diffing, and only if the copies are the same concept: `tenants`, `reports/distance_travelled`, `attestation/vinvc`. fleet-lite's tenancy is being superseded (see its `docs/operator-tenancy/`), so `tenants` is not extracted until that lands.

Rule: an app package moves to `core/` only when at least two apps use it. Anything single-use stays in the app.

### Web core

`packages/web-core` holds the roughly 400 identical files: shared elements, `global-styles.ts` tokens, localization, services, utils. Each app keeps its views, branding, and entry point. The 44 diverged files are triaged one by one into "shared with a hook or option" or "stays in the app". Lit, Vite, and TypeScript versions are pinned once at the workspace root.

**Styling baseline: fleet-lite-app's new visual language, not parentos' current one.** fleet-lite-app landed a visual refresh (`04c7bae`, PR #174, 2026-09-25) that replaces the old Stitch/Material export look with the DIMO Driver mobile app's design language — Euclid Circular A, blue-black surfaces, the sky→mint gradient reserved for brand moments, sentence-case labels, tonal surfaces over hairline borders. It's documented in `docs/DESIGN.md` (principles + token table) and implemented in `web/src/global-styles.ts`. This lands after fleet-lite/parentos were compared at "400 of 444 web files identical" — that comparison predates the refresh and needs redoing once `web-core` extraction starts, since the refresh touched ~25 shared elements (`side-nav`, `app-root`, every modal, `tenant-members`, `tenant-switcher`, login/onboarding pages) that were previously identical between the two apps.

Practical effect on phase 3 (web core extraction):
- `packages/web-core`'s tokens, fonts and shared elements are extracted from fleet-lite-app's post-refresh code, not parentos'.
- parentos (and kaufmann-oracle, if it ever gets a UI) adopts the new tokens as part of moving onto `web-core`, rather than web-core supporting two visual languages.
- `docs/DESIGN.md` moves to the repo root (or `packages/web-core/docs/`) as the one design doc for all apps, superseding the need for each app to restate it.
- Re-diff fleet-lite vs parentos web files at the start of phase 3 — the divergence count above is now stale.

### Base Helm chart

`charts/base` is a library chart derived from kaufmann's chart. It includes deployment, service, ingress, ExternalSecret, ServiceMonitor, and, behind flags, HPA, PDB, PrometheusRule alerts, and certificates. Each app chart depends on it and sets only host, env, secrets, resources, and flags.

## CI/CD

Same mechanism as today: push to `main` builds an image and commits `image.tag` into a values file; a tag does the same for prod. Only the file layout changes.

- One reusable `build-app.yml`. Each app calls it with a config block: Dockerfile path, chart path, tag format, single or split values files, platforms.
- Path filters: an app builds on `apps/<app>/**` and `core/**`. A `core/` change rebuilds and redeploys all three to dev. It reaches prod only through each app's own tag.
- Prod tags are per app: `fleet-lite/v1.4.0`, `parentos/v0.9.2`, `kaufmann/v2.1.0`.
- PRs touching `core/` run lint and test for every app. Slower, but it is the guard against breaking a sibling.
- The bump commit only touches values files. Those paths are in `paths-ignore` so a bump never retriggers a build. This must be verified in phase 1, because I have not checked how the current repos avoid the loop.
- Go moves to 1.26 everywhere, with the newer action versions from kaufmann's workflows (checkout v6, golangci-lint v9). kaufmann's `test.yml` and `helmlint.yml` become shared.

## Migration

Incremental, one reviewable step at a time. Every step leaves all apps green and deployable. Each phase is its own implementation plan.

0. **Prep, in the existing repos.** Move kaufmann to Go 1.26 as its own PR. Write down how each app is deployed today (which repo and path Argo tracks, per environment).
1. **Create the monorepo and import fleet-lite and parentos** with history (`git filter-repo` or subtree) under `apps/`. No code changes. Add the reusable workflow and path filters. Verify a dev deploy matches what the old repo produced, then repoint deployment for one app at a time.
2. **Extract Go core** from fleet-lite and parentos, one package per PR, starting with `config` and `server`. Each PR reconciles the diverged copies and leaves both apps green.
3. **Extract web core** from the identical files first, then triage the 44 diverged ones.
4. **Base Helm chart.** Build it from kaufmann's chart, then convert fleet-lite and parentos onto it.
5. **Import kaufmann-oracle.** Import with history, then adopt in this order: CI and chart, then `core/` packages one at a time. It goes last because it ingests live device data and has the least code in common.
6. **Scaffold and docs.** `scripts/new-app`, a short "new app" doc, and a root `AGENTS.md`. Archive the old repos read-only.

## Risks

- **Reconciling diverged code.** Only 8 of 44 shared Go files are identical, so each extraction needs a decision about which behavior wins. Mitigation: one package per PR, and tests written against the behavior before code moves (the apps have almost no tests today).
- **Blast radius of core changes.** A bad `core/` change can break every app's dev deploy. Mitigation: all-app CI on `core/` PRs, and per-app prod tags.
- **Deployment cutover.** Argo tracks specific repos and paths today. Repointing is the riskiest non-code step, so it happens one app at a time in phase 1.
- **kaufmann prod.** Live TCP ingestion. Its cutover happens last, with a rollback path of keeping the old repo deployable until the new pipeline has shipped a release.
- **Tag scheme change.** Per-app tags (`app/vX`) replace `vX`. Anything that reads the old tags (scripts, dashboards) needs updating.

## Testing and verification

- Phase 1: the image built from the monorepo boots and passes `/health` in dev, and its chart renders byte-identical values to the old repo's chart.
- Phases 2-5: `go build ./...`, `go vet`, `go test ./...`, web lint and build, and `helm template` for every app, on every PR.
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

**Lesson for the rest of this migration:** all three repos are being actively developed elsewhere while this is planned. Before phase 0 starts, re-pull all three repos and re-verify the Findings table above, the Go version row in particular (kaufmann was already re-confirmed at `go 1.25.7`, still behind the others' 1.26) and the workflow/chart details, rather than trusting this spec's snapshot.
