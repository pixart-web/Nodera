# Releases, branches and rollback

## Flow

```
feature branch ─▶ pull request ─▶ required CI ─▶ merge to main ─▶ tag vX.Y.Z ─▶ Release workflow ─▶ immutable images ─▶ staging ─▶ production
```

Nothing is deployed from a branch, from `main`, or from a laptop. Staging and production run exactly the images a tag produced.

## Cutting a release

1. `main` has a green CI run for the commit you want (`.github/workflows/ci.yml`: API fmt/vet/build/test/race/govulncheck/OpenAPI lint, database-roles job, web lint/typecheck/test/build/audit policy, and the three image builds + production-compose invariants).
2. `git tag -a v0.1.0 -m "…" && git push origin v0.1.0`.
3. `.github/workflows/release.yml` first checks that the tagged commit has a **successful** CI run, then builds and pushes
   `ghcr.io/pixart-web/nodera-api`, `…/nodera-agent`, `…/nodera-web` tagged `0.1.0` and `sha-<commit>`, with provenance and SBOM attestations.
   The web image is built with an **empty** API URL (same-origin), so one image works on any domain.
4. The run summary lists each image's **digest** (`ghcr.io/pixart-web/nodera-api@sha256:…`). Prefer digests in `/etc/nodera/nodera.env` for production; tags are acceptable for staging.
5. Make the GHCR packages readable by the host (public, or a read-only token with `docker login ghcr.io` on the host).

`docker-compose.prod.yml` refuses to start without `NODERA_API_IMAGE`, `NODERA_WEB_IMAGE`, `NODERA_AGENT_IMAGE`, and `scripts/check-compose-prod.py` (in CI) fails on `:latest` or an untagged image.
The release workflow has **not been executed yet** (it needs a tag push); its first run is part of the staging deployment.

## Deploying a release

```bash
sudo scripts/backup-nodera.sh                       # control-plane backup FIRST: it is the rollback point for a migration
# edit NODERA_*_IMAGE in /etc/nodera/nodera.env to the new tag/digest, then:
cd /opt/nodera
docker compose --env-file /etc/nodera/nodera.env -f docker-compose.prod.yml pull
docker compose --env-file /etc/nodera/nodera.env -f docker-compose.prod.yml up -d    # the migrate job runs first; the API waits for it
sudo scripts/post-migrate.sh                        # re-applies + proves the audit-log protections (idempotent; "FAILED" = do not take traffic)
docker exec nodera-cp-api wget -qO- http://127.0.0.1:8080/ready
```

## Rollback

* **No schema change in the release (or backward-compatible):** set the previous image tags in `nodera.env` and `up -d`. The previous images are still in the registry and on the host; nothing is rebuilt.
* **The release migrated the schema:** the old API refuses to start against a *newer* schema only if you removed something it needs; migrations are forward-only. If the old version cannot run on the new schema, restore the control-plane package taken immediately before the release
  (`scripts/restore-nodera.sh apply …`), then start the previous images. You lose control-plane changes made since that backup (audit entries, operations, new projects) — which is why the backup is the first step.
* Customer workloads keep running throughout (they are plain containers); only management is interrupted.

## Branch protection (repository owner — cannot be set from the repo)

Settings → Branches → rule for `main`:

* **Require a pull request before merging** (≥ 1 approval; dismiss stale approvals on new commits)
* **Require status checks to pass**, and require the branch to be up to date: `API (fmt, vet, build, test, race, vuln, openapi)`, `Production database roles (owner migrates, app role is restricted)`, `Web (lint, typecheck, test, build, audit policy)`, `Docker images (api, web, agent) and compose files`
* **Block force pushes** and **block deletions**; **do not allow bypassing** the rules (including administrators)
* Require linear history (optional), require signed commits (optional)
* Settings → Tags: protect `v*` so only maintainers can create release tags
* Settings → Actions: default workflow permissions **read-only** (the workflows request what they need); require approval for workflows from outside collaborators
* Enable Dependabot alerts/security updates and secret scanning with push protection

Actions in the workflows are referenced by major-version tag (`actions/checkout@v4` …). Pinning them to full commit SHAs is a further hardening step left to the owner (a Dependabot rule can keep the SHAs current).
