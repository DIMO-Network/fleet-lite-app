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

All three apps share the same trigger shape (dev on push to `main`,
prod on push tag `v*`) and the same values-file split, but **prod does
not build the same way in all three** — this is the one difference
`build-app.yml` (spec, CI/CD section) most needs to model as a config
knob, not assume away:

| App | Dev trigger | Dev writes | Prod trigger | Prod builds? | Prod image tag source | Prod writes |
|---|---|---|---|---|---|---|
| fleet-lite-app | push to `main` | `charts/fleet-lite-app/values.yaml` | push tag `v*` | **No.** Polls for the image `buildpushdev` already built for the tagged commit (up to 30 min), fails loudly if it never appears. Never runs a second build. | The dev-build SHA of the commit behind the tag (found via `git log --first-parent`, skipping bot bump commits) | `charts/fleet-lite-app/values-prod.yaml` |
| parentos | push to `main` | `charts/parentos/values.yaml` | push tag `v*` | **Yes**, a fresh `docker/build-push-action` run. | Commit SHA slug (`steps.slug.outputs.BUILD_TAG`) | `charts/parentos/values-prod.yaml` |
| kaufmann-oracle | push to `main` | `charts/kaufmann-oracle/values.yaml` | push tag `v*` | **Yes**, a fresh `docker/build-push-action` run. | The **git tag itself**, stripped of `v` (`dawidd6/action-get-tag`) — not the commit SHA | `charts/kaufmann-oracle/values-prod.yaml` |

**Release vs. tag causality is opposite in fleet-lite-app vs. the other
two.** In fleet-lite-app, *pushing the tag* is what creates the GitHub
Release — `buildpushprod.yml`'s last step calls
`github.rest.repos.createRelease` for the pushed tag if one doesn't
already exist. In kaufmann-oracle, it runs the other way: *cutting a
GitHub Release* is the trigger, since a Release creates a `v*` tag that
`buildpushtagged.yml` listens for (confirmed by the user). parentos'
tag-creation mechanism (direct `git push --tags` vs. a Release) has not
been independently confirmed. A phase-1 reader relying only on this doc
should not assume the same tag/Release relationship holds across apps.

## Still open — not blocking code work, blocks phase 1 cutover only

- **ArgoCD *dev* Application names/paths were not independently pulled.**
  Only the prod entries above came from the ArgoCD UI. It is reasonable
  to assume a mirrored dev Application per app (same repo/path, reading
  `values.yaml` instead of `values-prod.yaml`), but this has not been
  confirmed and should not be treated as fact until someone checks the
  ArgoCD UI for a `-dev` (or equivalently named) Application per app.
- **Bump-commit self-trigger loop.** Every dev/prod workflow above
  writes an `image.tag` change back to `main` via
  `fjogeleit/yaml-update-action` (fleet-lite-app's prod workflow does
  this and then also creates a GitHub Release — see the table note
  above; the bump commit itself is the same shape everywhere). Whether
  that commit re-triggers `buildpushdev.yml` (which also fires on push
  to `main`) has not been checked in any of the three repos. This must
  be verified before the
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
