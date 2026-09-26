# Releasing

How a change gets from a pull request to dev and prod. The workflows live in
`.github/workflows/`.

## Pipeline

| Step | Workflow | What it does |
| --- | --- | --- |
| Pull request | `golangci-lint`, `web-lint` | Checks `go-lint`, `go-test` (api) and `web` (eslint, typecheck + production build, vitest, localization drift). |
| Merge to `main` | `buildpushdev` | Builds `dimozone/fleet-lite-app:<sha7>` for the merge commit — the only image build — and commits `Update dev image version to <sha7>` to `charts/fleet-lite-app/values.yaml`. |
| Push a `v*` tag | `buildpushprod` | Builds nothing. Waits (up to 30 minutes) for the dev image of the tagged commit, commits `Update prod image version to <sha7> (<tag>)` to `values-prod.yaml`, and creates the GitHub Release with generated notes. |

Prod therefore runs exactly the image dev ran. `buildpushdev` runs one at a
time; a newer queued run replaces an older queued one, so a burst of merges
builds the first and the last. A commit whose run was replaced has no image
and cannot be released on its own — release a later commit.

Database migrations run in the deployment's `migrate` init container when the
new image rolls out. They are forward-only: rolling back an image does not
undo a migration.

## Cutting a release

1. Merge the pull request.
2. Wait for `buildpushdev` on the merge commit to go green, and check the change
   on dev.
3. Tag and push. Use an annotated tag:

   ```sh
   git fetch origin main --tags
   git tag -a v0.35.0 -m "v0.35.0" origin/main
   git push origin v0.35.0
   ```

   Tagging `origin/main` is fine when its tip is a bot image-bump commit:
   `buildpushprod` deploys the image of the newest commit behind the tag that
   changed anything besides the image tags.
4. Watch `buildpushprod`. It creates the release; edit the notes there if they
   need more than the pull request titles.

Versions are `vMAJOR.MINOR.PATCH`: bump the patch for fixes and the minor for
new features.

## Rolling back

Re-run the `buildpushprod` run of the previous release (Actions → the run →
Re-run all jobs). Its image already exists, so it only sets `values-prod.yaml`
back to that image. Runs older than 30 days cannot be re-run; open a pull
request that sets `image.tag` in `values-prod.yaml` instead.

## Protecting `main`

Merge only with `go-lint`, `go-test` and `web` green.

Protect `main` with a ruleset (Settings → Rules → Rulesets) that blocks force
pushes and deletion. Don't add "Require a pull request" or "Require status
checks" to it as things stand: both workflows commit their image bump straight
to `main` with the workflow's `GITHUB_TOKEN`, which cannot be put on a bypass
list, so those rules would reject every deploy. Requiring them first needs the
workflows to push with a GitHub App token whose app is on the ruleset's bypass
list.
