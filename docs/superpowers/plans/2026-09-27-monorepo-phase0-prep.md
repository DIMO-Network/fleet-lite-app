# Monorepo Consolidation — Phase 0 Prep Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Finish the two prep items the spec's Migration §Phase 0 calls for — move kaufmann-oracle to Go 1.26, and write down today's deployment shape for all three apps — without touching any app's runtime behavior or CI trigger logic.

**Architecture:** Two independent, unrelated changes done as two separate PRs against kaufmann-oracle and fleet-lite-app respectively. Task 1 is a version bump in kaufmann-oracle's own repo (`go.mod`, two workflow files, the Dockerfile base image), verified by the repo's existing build/vet/test/lint. Task 2 is a new markdown doc in fleet-lite-app recording the ArgoCD facts already gathered plus the still-open items, so later phases don't have to re-derive them.

**Tech Stack:** Go 1.26 toolchain, GitHub Actions, Docker, Markdown.

**Spec:** `docs/superpowers/specs/2026-09-25-monorepo-consolidation-design.md`

## Global Constraints

- Go moves to 1.26 everywhere (spec, CI/CD section) — this plan does that for kaufmann-oracle; fleet-lite-app and parentos are already on 1.26.
- Every step leaves all apps green and deployable (spec, Migration intro) — this plan changes no app's runtime behavior, only its build toolchain version and a doc.
- The monorepo does not exist yet — this plan makes no change to fleet-lite-app's or parentos' structure, and does not create `dimo-monorepo`.
- All three repos are being actively developed by the user concurrently (established in conversation) — re-pull each repo immediately before starting its task, don't trust a snapshot from earlier in this session.

## Review Focus

- **kaufmann-oracle's `go.mod` says `go 1.25.7`, but the installed toolchain is 1.26.3 and the repo already builds clean under it** — a bump to `go 1.26` in `go.mod` must not be the only change; `golangci-lint` and `go vet` must both be run and pass, not just `go build`, since lint/vet can surface toolchain-version-specific findings that a bare build won't.
- **Two workflow files pin `go-version: 1.25.x` independently** (`lint.yml`, `test.yml`) — bumping only `go.mod` and missing one of these leaves CI running a different Go minor version than local builds, which is exactly the kind of drift this whole project exists to prevent. Task 1 checks both by name.
- **The Dockerfile's `FROM golang:1.25` is a third, separate pin** — easy to miss because it's not a `.go` file or a workflow; a docker build after the `go.mod` bump would still compile with 1.25's stdlib unless this line changes too.
- **The deployment inventory doc must record what's confirmed vs. assumed, not present speculation as fact** — e.g., kaufmann's *dev* ArgoCD Application name/path was never independently confirmed (only prod was pulled from the UI); the doc must say so explicitly rather than assuming it mirrors the prod entry.
- **This plan must not touch `.github/workflows/buildpushdev.yml` or `buildpushtagged.yml`'s trigger/path logic** — the spec's open item about bump commits retriggering builds is explicitly deferred to phase 1 verification, not phase 0. A worker tempted to "fix" that here should not.

---

## File Structure

**kaufmann-oracle repo** (Task 1):
- Modify: `go.mod:3` — `go 1.25.7` → `go 1.26.0`
- Modify: `.github/workflows/lint.yml:17` — `go-version: 1.25.x` → `go-version: 1.26.x`
- Modify: `.github/workflows/test.yml:15` — `go-version: 1.25.x` → `go-version: 1.26.x`
- Modify: `resources/docker/Dockerfile:1` — `FROM golang:1.25 AS build` → `FROM golang:1.26 AS build`

**fleet-lite-app repo** (Task 2):
- Create: `docs/superpowers/plans/2026-09-27-deployment-inventory.md`

## Task 1: Bump kaufmann-oracle to Go 1.26

**Files:**
- Modify: `go.mod`
- Modify: `.github/workflows/lint.yml`
- Modify: `.github/workflows/test.yml`
- Modify: `resources/docker/Dockerfile`

**Interfaces:**
- Consumes: nothing from another task.
- Produces: kaufmann-oracle building, vetting, linting and testing green on Go 1.26, matching fleet-lite-app and parentos. Later phases (Go core extraction) can assume all three apps compile under one Go version.

- [ ] **Step 1: Re-pull kaufmann-oracle and confirm a clean starting point**

```bash
cd /Users/jamesli/DIMO/kaufmann-oracle
git checkout main
git pull --ff-only origin main
git status -sb
```

Expected: `git status -sb` shows `## main...origin/main` with no `ahead`/`behind` and no uncommitted changes. If it shows anything else, stop and report — do not proceed on a dirty or diverged tree.

- [ ] **Step 2: Create the branch**

```bash
git checkout -b chore/go-1.26
```

- [ ] **Step 3: Bump `go.mod`**

Change line 3 of `go.mod` from:
```
go 1.25.7
```
to:
```
go 1.26.0
```

- [ ] **Step 4: Run `go mod tidy` and confirm no unrelated diff**

```bash
go mod tidy
git diff go.mod go.sum
```

Expected: `go.mod`'s `go` line shows the 1.25.7 → 1.26.0 change; `go.sum` may pick up incidental line reordering but should show no dependency version changes. If `go.sum` shows dependency version bumps, stop and report before continuing — that's a second unrelated change hiding inside this bump.

- [ ] **Step 5: Bump the two workflow files**

In `.github/workflows/lint.yml`, change:
```yaml
          go-version: 1.25.x
```
to:
```yaml
          go-version: 1.26.x
```

In `.github/workflows/test.yml`, change the same line the same way.

- [ ] **Step 6: Verify no other `go-version` reference was missed**

```bash
grep -rn "go-version.*1\.25\|golang:1\.25\|go 1\.25" . --include="*.yml" --include="*.yaml" --include="Dockerfile" --include="go.mod"
```

Expected: no output. If anything prints, add it to this task's file list and fix it before continuing.

- [ ] **Step 7: Bump the Dockerfile base image**

Change `resources/docker/Dockerfile:1` from:
```
FROM golang:1.25 AS build
```
to:
```
FROM golang:1.26 AS build
```

- [ ] **Step 8: Build, vet, and test locally**

```bash
go build ./...
go vet ./...
go test ./... -timeout 3m
```

Expected: all three exit 0. `go test` may skip DB-backed tests if no local Postgres/Docker is running (matches the project's existing convention per `AGENTS.md`-equivalent testing notes) — a skip is fine, a failure is not.

- [ ] **Step 9: Run the linter**

```bash
golangci-lint run
```

Expected: exit 0, no new findings introduced by the version bump. If golangci-lint isn't installed locally, note that CI will run it (`lint.yml`) and this step is a local best-effort check, not a hard gate for this step alone — but do not skip Step 10.

- [ ] **Step 10: Commit**

```bash
git add go.mod go.sum .github/workflows/lint.yml .github/workflows/test.yml resources/docker/Dockerfile
git commit -m "chore: move to Go 1.26, matching fleet-lite-app and parentos

Part of the dimo-monorepo consolidation prep (phase 0). All three
apps now build on the same Go minor version so the shared core
work in later phases doesn't hit version skew.

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

- [ ] **Step 11: Push and open the PR**

```bash
git push -u origin chore/go-1.26
gh pr create --repo DIMO-Network/kaufmann-oracle \
  --title "chore: move to Go 1.26" \
  --body "Matches fleet-lite-app and parentos ahead of the dimo-monorepo consolidation. No behavior change — go.mod, lint.yml/test.yml go-version, and the Docker build image all move 1.25 → 1.26.

🤖 Generated with [Claude Code](https://claude.com/claude-code)"
```

Expected: PR opens against `main`. Confirm CI (`lint.yml`, `test.yml`, `helmlint.yml`) runs and passes before this task is considered done — report the PR URL and CI status, don't just report that the PR was created.

## Task 2: Write the deployment inventory doc

**Files:**
- Create: `docs/superpowers/plans/2026-09-27-deployment-inventory.md` (in fleet-lite-app, alongside the spec and this plan)

**Interfaces:**
- Consumes: the ArgoCD facts already gathered in conversation (repo URL / path / target revision / values file per app, prod only) and the corrected findings from the spec (kaufmann's dev/prod split, the tenancy status correction, the styling baseline decision).
- Produces: a single doc that phase 1 (monorepo creation and deployment cutover) and future readers can treat as the source of truth for "how does each app deploy today", instead of re-deriving it. Explicitly marks what is confirmed vs. still open, per the Review Focus item above.

- [ ] **Step 1: Create the file with confirmed facts**

Write `docs/superpowers/plans/2026-09-27-deployment-inventory.md`:

```markdown
# Deployment inventory — fleet-lite-app, parentos, kaufmann-oracle

Snapshot taken 2026-09-27, ahead of the dimo-monorepo consolidation
(see `docs/superpowers/specs/2026-09-25-monorepo-consolidation-design.md`).
This is a point-in-time record for phase 1 planning — re-verify before
acting on it if much time has passed, since all three repos are under
active development.

## Confirmed from the ArgoCD UI (prod Applications)

| App | Repo | Path | Target revision | Values file |
|---|---|---|---|---|
| fleet-lite-app | `git@github.com:DIMO-Network/fleet-lite-app.git` | `charts/fleet-lite-app` | `HEAD` | `values-prod.yaml` |
| parentos | `git@github.com:DIMO-Network/parentos.git` | `charts/parentos` | `HEAD` | `values-prod.yaml` |
| kaufmann-oracle | `git@github.com:DIMO-Network/kaufmann-oracle.git` | `charts/kaufmann-oracle` | `HEAD` | `values-prod.yaml` |

All three prod Applications track `HEAD` on `main` at a fixed values
file, not a tag ref — the tag only decides which image gets *written
into* that file (see below), not which git ref ArgoCD syncs from.

## Confirmed from each repo's GitHub workflows

All three apps share the same two-workflow shape:

| App | Dev trigger | Dev writes | Prod trigger | Prod writes |
|---|---|---|---|---|
| fleet-lite-app | push to `main` | `charts/fleet-lite-app/values.yaml` | push tag `v*` | `charts/fleet-lite-app/values-prod.yaml` |
| parentos | push to `main` | `charts/parentos/values.yaml` | push tag `v*` | `charts/parentos/values-prod.yaml` |
| kaufmann-oracle | push to `main` | `charts/kaufmann-oracle/values.yaml` | push tag `v*`, cut via a GitHub Release | `charts/kaufmann-oracle/values-prod.yaml` |

kaufmann-oracle's prod tag is created by cutting a GitHub Release
(confirmed by the user), not by pushing a tag directly with git — the
practical trigger (`push: tags: v*`) is the same either way.

## Still open — not blocking code work, blocks phase 1 cutover only

- **ArgoCD *dev* Application names/paths were not independently pulled.**
  Only the prod entries above came from the ArgoCD UI. It is reasonable
  to assume a mirrored dev Application per app (same repo/path, reading
  `values.yaml` instead of `values-prod.yaml`), but this has not been
  confirmed and should not be treated as fact until someone checks the
  ArgoCD UI for a `-dev` (or equivalently named) Application per app.
- **Bump-commit self-trigger loop.** Every dev/prod workflow above ends
  by committing an `image.tag` change back to `main` via
  `fjogeleit/yaml-update-action`. Whether that commit re-triggers
  `buildpushdev.yml` (which also fires on push to `main`) has not been
  checked in any of the three repos. This must be verified before the
  reusable `build-app.yml` workflow (spec, CI/CD section) is written,
  since a self-triggering loop in the shared workflow would hit three
  apps at once instead of one.

## Corrections carried over from the design spec

These were wrong in earlier passes and are recorded here so phase 1
doesn't reintroduce them:

- **kaufmann-oracle's dev/prod split is real and current**, not a gap.
  An earlier check of a stale local clone (533 commits behind) showed
  only one `values.yaml` with no `-prod` counterpart; re-pulling showed
  the same two-file split as the other two apps, as reflected in the
  table above.
- **Operator tenancy is much further along than
  `docs/operator-tenancy/README.md`'s "design / pre-implementation"
  status line says.** Both fleet-lite-app and kaufmann-oracle have
  executed through migration phase P5b: `access_tenants` and
  `access_fleet_groups` are dropped in both repos, and the local
  authorization path has been deleted, not just flagged off. Updating
  that doc's status line is tracked as separate housekeeping, not part
  of this migration.
- **The shared web styling baseline is fleet-lite-app's post-refresh
  code** (`04c7bae`, PR #174, `docs/DESIGN.md`), not parentos' current
  look. The "400 of 444 web files identical" figure in the spec's
  Findings table predates that refresh and must be re-measured at the
  start of the web-core extraction phase.
```

- [ ] **Step 2: Re-pull fleet-lite-app and confirm a clean starting point**

```bash
cd /Users/jamesli/DIMO/fleet-lite-app
git checkout main
git pull --ff-only origin main
git status -sb
```

Expected: `## main...origin/main`, clean except for the new untracked file from Step 1.

- [ ] **Step 3: Create the branch and commit**

```bash
git checkout -b docs/deployment-inventory
git add docs/superpowers/plans/2026-09-27-deployment-inventory.md
git commit -m "docs: record today's deployment shape for all three apps

Phase 0 of the dimo-monorepo consolidation (see the design spec).
Captures the ArgoCD and workflow facts gathered while planning the
migration, and what's still unconfirmed, so phase 1 doesn't have to
re-derive them.

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

- [ ] **Step 4: Push and open the PR**

```bash
git push -u origin docs/deployment-inventory
gh pr create --repo DIMO-Network/fleet-lite-app \
  --title "docs: deployment inventory for the monorepo consolidation" \
  --body "Phase 0 prep for the dimo-monorepo consolidation (see \`docs/superpowers/specs/2026-09-25-monorepo-consolidation-design.md\`). No code change.

🤖 Generated with [Claude Code](https://claude.com/claude-code)"
```

Expected: PR opens. Report the PR URL.

---

## Self-Review

**Spec coverage:** Migration §Phase 0 lists exactly two items — "Move kaufmann to Go 1.26 as its own PR" (Task 1) and "Write down how each app is deployed today" (Task 2). Both are covered. No other spec section describes phase-0-scoped work; everything else (monorepo creation, core extraction, web core, base chart, kaufmann import, scaffolding) is out of scope for this plan by the spec's own phasing.

**Placeholder scan:** No TBD/TODO; every step has literal commands or literal file content, not descriptions of what to do.

**Type consistency:** N/A — no shared code interfaces between the two tasks; they're independent.

**Review Focus:** all five items above have an owning step: the lint/vet item is Steps 8–9 of Task 1, the two-workflow-files item is Step 5–6 of Task 1, the Dockerfile item is Step 7 of Task 1, the confirmed-vs-assumed doc item is the "Still open" section of Task 2 Step 1, and the no-scope-creep item is enforced by this plan simply not containing a Task 3.
