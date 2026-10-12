# Deployment

**Current stage: READY FOR HETZNER STAGING DEPLOYMENT (code side).** Nothing here has run on the real
Hetzner host. The next phase is *real deployment + staging validation + disaster testing*, not more features.

## Status vocabulary (used everywhere in these docs)

| Term | Meaning |
|---|---|
| **IMPLEMENTED** | The code exists. Says nothing about whether it works anywhere. |
| **LOCALLY TESTED** | Automated tests pass in CI, and/or it was exercised end to end on a developer machine (including a real Docker daemon, MariaDB, Traefik and an SFTP server on a local Linux VM). Not the Hetzner host. |
| **STAGING VALIDATED** | Ran successfully against the real Hetzner environment with non-customer test workloads. *Nothing is at this level yet.* |
| **PRODUCTION VALIDATED** | The production gate (`docs/PRODUCTION-GATE.md`) and the disaster tests passed. *Nothing is at this level yet.* |
| **NOT IMPLEMENTED** | No code. |

"Implemented" never means "production ready". See `docs/FINAL-IMPLEMENTATION-AUDIT.md` for the per-component table.

## What was exercised locally for this readiness pass

On a developer Mac with a Linux VM (Colima, Docker 29), using the **production images and `docker-compose.prod.yml` unchanged**:
Postgres with the owner/app role split, the one-shot migration job, Redis with a password, API (read-only root, all capabilities dropped,
no Docker socket), Web, Agent (the only container with the socket), a real **MariaDB 11** container, **Traefik v3.6** routing by host and path
(self-signed default certificate), a real **OpenSSH SFTP server** standing in for the Storage Box, and a WordPress container provisioned
through the agent. Verified: cookies/CSRF/tenant isolation through the proxy, SSE delivered progressively through Traefik, provisioning,
a 27 MB full backup (files + MariaDB dump) uploaded off-site and hash-verified, corruption detection, approval-gated restore with safety
snapshot, control-plane database backup → encrypted package → restore with a marker row rolled back. This is **LOCALLY TESTED**, not staging validated:
the real kernel, Docker GID, Hetzner network, Let's Encrypt, Storage Box host key, and reboot behaviour are all still unknown.

## Architecture and trust boundaries

```
Internet ──TLS──▶ Traefik ──▶ web   (Next.js; holds no secrets)
                      │
                      └──▶ api  (/api/, /agent/)  ──▶ postgres, redis     [internal-only network]
                              │
                              └─ signed, allowlisted commands ─▶ agent ──▶ Docker daemon ──▶ MariaDB, WordPress, …
```

* **Single public host** (`NODERA_HOST`): Traefik sends `/api/` and `/agent/` to the API and everything else to the UI. Same origin ⇒ the
  host-only session cookie and the JS-readable CSRF cookie work with no `Domain` attribute, CORS is not needed, and the web image has no
  baked-in domain (one immutable image for every environment). A split `app.` / `api.` host design would break the CSRF cookie
  (host-only cookies are not shared between subdomains), which is why it is not used.
* **The API does not mount the Docker socket.** `NODERA_PROVIDER_MODE=agent` routes container and MariaDB operations through the Node
  Agent as signed, expiring, allowlisted commands. `NODERA_PROVIDER_MODE=docker` (API holds the socket) still exists for development and is
  **refused in production** unless `NODERA_ALLOW_DIRECT_DOCKER=true` is set deliberately.
* **The agent is root-equivalent on the host** (Docker socket). It is constrained by: no shell exists (a fixed allowlist of operations with
  parameter validators, checked on the API *and* again on the agent), the protected-resource guard below, no listening port, runs as uid 10001,
  read-only root, all capabilities dropped.
* **Network separation.** `internal` (no external route): Postgres, Redis, migration job, agent. `nodera-edge` (dedicated, operator-created):
  Traefik, api, web only. Customer workloads sit on the shared `proxy-public` network and **have no route to the control plane**
  (verified locally: a WordPress container cannot reach `nodera-cp-api`).
* **Secrets are files**, mounted read-only into the single container that needs each one (`docs/KEY-CUSTODY.md`). They are not in the
  environment, `docker inspect`, the compose file or Git.

### Control-plane self-protection (`internal/nodeagent/guard`)

Managed operations travel the same agent path as everything else, so without a guard a project could name itself into the control plane and
have Nodera stop or remove it. Protected, on **both** the API (validation) and the agent (execution):

| Rule | Effect |
|---|---|
| Container name prefix `nodera-cp-` | start/stop/restart/remove/create refused |
| Label `nodera.role=control-plane` (every compose service carries it) | same, whatever the name |
| `NODERA_PROTECTED_CONTAINERS` (default `traefik,mariadb`) | same |
| Project slugs starting `cp-` | rejected at project creation |
| Workload volumes | must be absolute paths **inside the data root**; the Docker socket, `/`, `/etc`, the Postgres volume and named volumes are refused |
| Workload networks | `host` refused; workload user must be numeric and not root |

Read-only `inspect`/`logs` stay allowed. There is no allowlisted operation that removes volumes or networks at all.

**Break-glass is independent of Nodera.** If Nodera is down or wedged, recover with SSH + Docker, which never goes through the agent:
`docker compose -f /opt/nodera/docker-compose.prod.yml --env-file /etc/nodera/nodera.env ps|logs|up -d|restart api`, `docker exec nodera-cp-postgres psql …`,
and `scripts/restore-nodera.sh` for the database. Anyone with root on the host can do anything; that is the trust model — the guard stops *Nodera
operations* from damaging Nodera, not an operator with a shell.

## Artifacts

| File | Purpose |
|---|---|
| `api/Dockerfile` target `server` | API **and** `nodera-migrate` (same image, same embedded migrations); non-root uid 10001 |
| `api/Dockerfile` target `agent` | Node Agent; uid 10001 (same as the API so both share the data directory) |
| `web/Dockerfile` | Next standalone, non-root, no npm/yarn/TypeScript in the runtime image; build arg `NEXT_PUBLIC_NODERA_API_URL` is **empty for release images** (same-origin) |
| `docker-compose.yml` | local development only (published ports, dev password, mock/local providers) |
| `docker-compose.prod.yml` | production/staging stack (this document) |
| `deploy/nodera.env.example`, `deploy/backup.env.example` | non-secret configuration templates |
| `scripts/init-secrets.sh` | creates the secret files with strong random values (idempotent, never overwrites) |
| `scripts/db/01-roles.sql`, `02-post-migrate.sql`, `03-verify-roles.sql`, `scripts/post-migrate.sh` | production database role model, its verification, and the idempotent after-every-migration wrapper |
| `scripts/backup-nodera.sh`, `scripts/restore-nodera.sh` | control-plane (Nodera itself) backup and restore |
| `scripts/check-compose-prod.py` | CI-enforced security invariants of the rendered production compose |
| `scripts/staging-smoke.py` | drives the public API for the staging smoke checks |

## Releases and immutable images

Production references images by **version tag or `@sha256` digest, never `latest`** (compose refuses an unset value; CI fails on `:latest`).
`.github/workflows/release.yml` publishes `ghcr.io/pixart-web/nodera-{api,web,agent}:<version>` (plus `sha-<commit>`) when a `vX.Y.Z` tag is
pushed — only if that commit has a green CI run — and prints each image's digest in the run summary. Rollback is *deploy the previous tag*; it
never rebuilds an old commit. Details, branch policy and the exact GitHub settings: `docs/RELEASES.md`.

## Configuration

* `NODERA_ENV=production` makes the API **refuse to start** unless: CORS origins are explicit `https://` origins (no wildcard, no localhost),
  `NODERA_TRUSTED_PROXIES` is set, the secrets-encryption and agent-signing keys are supplied, the provider mode is not `mock`, direct Docker
  is not used without the override, no demo seed, no dev database password, and no `NODERA_MONITOR_ALLOW_INTERNAL` (`internal/platform/config`, tested).
* Any secret variable accepts `NAME_FILE=/path` instead of `NAME`; setting both is an error. Production compose uses the file form for the
  database URL, Redis URL, encryption key and agent signing key.
* `.env.example` documents development; `deploy/nodera.env.example` documents production.

## Migrations and database roles

Production does **not** migrate at API start. The release order is fixed:

```
pull images ─▶ migrate (owner role, one-shot) ─▶ 02-post-migrate.sql ─▶ api starts (verifies schema, runs as the app role)
```

* `nodera-migrate` takes a PostgreSQL advisory lock, applies pending forward-only migrations in separate transactions, and exits. `-status`
  lists pending ones (exit 3). In compose it is the `migrate` service and the API `depends_on` it with `service_completed_successfully`.
* The API in production (`NODERA_AUTO_MIGRATE=false`) only **checks** that no migration is pending and refuses to start otherwise.
* **Failure and rollback, honestly:** each migration is transactional, so a failed one leaves the schema at the previous version and the API stays down
  (the old API image is still running if you started `migrate` before replacing it — run it first). Migrations are forward-only: there are no
  down-migrations. Rolling back a release that has already migrated means restoring the pre-release control-plane backup
  (`scripts/backup-nodera.sh` before every release; `docs/RELEASES.md`). Write migrations to be backward compatible with the previous API
  version for one release when you can.
* **Roles** (`scripts/db/01-roles.sql`): `nodera_owner` owns the schema and is used only by `nodera-migrate`; `nodera_app` is the API's runtime role
  (DML on application tables, no DDL, no superuser). `02-post-migrate.sql` revokes `UPDATE/DELETE/TRUNCATE` on `audit_log` and write access to
  `schema_migrations` from the app role. The only deletion of audit rows is the retention sweep, which goes through the `SECURITY DEFINER` function
  `audit_log_apply_retention` (migration 0032) that refuses a cutoff younger than 90 days. `03-verify-roles.sql` proves all of it
  (10 checks, run in CI against a fresh Postgres, and in the runbook against the real one). No separate backup role: `pg_dump` runs as the
  superuser inside the container over the local socket at this scale.

## Health and readiness

| Endpoint | Question | Behaviour |
|---|---|---|
| `GET /health` | Is the process alive? | Always `200 {"status":"ok"}`, no dependencies. Used by the container healthchecks and Traefik. Not public through the proxy (the web router answers `/health`). |
| `GET /ready` | Can this instance safely take real requests? | `503` if PostgreSQL is unreachable or queued work is stuck >10 min. Otherwise `200` with per-dependency detail. |

Soft dependencies are reported but never make `/ready` fail: **Redis** (`down` ⇒ rate limiters fall back to per-instance limits, `status: degraded`),
**agent** (in `agent` mode: `not_configured | unknown | revoked | offline | ok` from the last heartbeat, 90 s threshold). Provider capabilities are listed
as `provider.*` with their true state (`real | agent | local | local_dev | offsite | mock | not_configured`); nothing claims Docker or MariaDB is ready
from a mock. `GET /api/v1/system/info` additionally returns plain-language `warnings` (in-memory DNS, development CA, same-host backups, mocks).
`/ready` is for operators and monitors, not for Traefik routing, so a degraded dependency cannot take the UI offline.

## Traefik contract

The stack assumes a Traefik **v3 that supports your Docker Engine's API version** (v3.1 failed against Docker 29 in the local test; v3.6 works —
pin a current v3 and check `docker logs traefik` for provider errors) with:

| Requirement | Value |
|---|---|
| Docker provider | `exposedByDefault: false`; Traefik attached to the `nodera-edge` network (`docker network create nodera-edge`; add it to Traefik's compose `networks:` too so it survives recreation) |
| Entrypoints | `websecure` (443) with TLS; `web` (80) redirecting to `websecure` (not provided by these labels; configure globally on Traefik) |
| Certificate resolver | named `letsencrypt` (or set `NODERA_CERT_RESOLVER`); HTTP/TLS challenge; DNS for `NODERA_HOST` must already point at the host |
| Routers (from labels) | `nodera-api`: `Host(NODERA_HOST) && (PathPrefix(/api/) \|\| PathPrefix(/agent/))`, priority 100; `nodera-web`: `Host(NODERA_HOST)`, priority 1 |
| Forwarded headers | Traefik sets `X-Forwarded-For/Proto/Host`. The API honours `X-Forwarded-For` **only** from `NODERA_TRUSTED_PROXIES` and takes the right-most untrusted entry, so a client cannot choose its rate-limit identity. Set `NODERA_TRUSTED_PROXIES` to the `nodera-edge` subnet. |
| SSE | Operation progress and `/api/v1/events` are `text/event-stream`. The API sends `X-Accel-Buffering: no` and the label `responseforwarding.flushinterval=1ms` forces immediate flush. Do **not** attach buffering/compression middleware to the API router. Keep Traefik's entrypoint `respondingTimeouts.idleTimeout` ≥ 180 s (the default); the API sends keep-alive comments. Verified progressive delivery through Traefik locally. |
| WebSockets | not used |
| Request bodies | the API bounds bodies itself: 2 MiB for JSON requests (`413`/validation error beyond that), 1 MiB for agent calls, the archive limit for site-migration uploads. No Traefik body limit is configured; do not add a small one on `/api/` or migration uploads break. |
| HTTP→HTTPS | global redirect on the `web` entrypoint |

A minimal static config that matches is in `docs/HETZNER-STAGING-RUNBOOK.md`. Nodera's own certificate for its UI/API comes from Traefik +
Let's Encrypt. Nodera's built-in certificate provider is a **development CA** (browsers do not trust it) and its DNS provider is **in-memory**
(`local_dev`): neither is production DNS/TLS for customer sites, and both are labelled so in `/ready`, `/system/info` and the UI.
Cloudflare and a generic ACME client are NOT IMPLEMENTED (`docs/ROADMAP.md`).

## Logging and disk

* Every compose service uses `json-file` with `max-size 10m`, `max-file 5` (50 MB/service), enforced by `check-compose-prod.py`.
* **Workload containers created by the agent do not inherit that.** Set the Docker daemon default in `/etc/docker/daemon.json`
  (`{"log-driver":"json-file","log-opts":{"max-size":"10m","max-file":"5"}}`, then restart Docker) — runbook step, verify with
  `docker inspect <workload> --format '{{.HostConfig.LogConfig}}'`.
* `/health` access logs are suppressed. Application logs are structured JSON; secrets are redacted (`internal/platform/redact`) in logs,
  operation logs and container-log ingestion; the audit log and error responses never carry credentials; SFTP/auth errors are scrubbed.
* Database growth: operation logs, metric samples, log entries and incidents have retention policies (defaults in `internal/logs`);
  the audit log is only pruned by an explicit per-organisation policy of ≥ 90 days. **Monitor disk** (alert at 80 %).

## Host sizing and resource limits

| | Minimum (staging) | Recommended (first customers) |
|---|---|---|
| CPU | 2 vCPU | 4 vCPU |
| RAM | 4 GB | 8 GB (Nodera control plane ≈ 2 GB at rest; the rest is for MariaDB + workloads) |
| Disk | 40 GB | 160 GB+ SSD, with `/var/lib/docker`, `/srv/nodera/data` and `/var/backups/nodera` sized for backups in flight |
| OS | Debian 12 / Ubuntu 22.04 or 24.04 LTS, Docker Engine ≥ 25 with the Compose plugin | same |

Compose memory limits (`deploy.resources.limits`): postgres 1 GB (`shared_buffers` 256 MB, `max_connections` 60), redis 192 MB (`maxmemory` 128 MB,
`volatile-ttl`, no persistence), api 768 MB, web 384 MB, agent 192 MB, migrate 256 MB — about 2.8 GB of ceilings. They are starting points, **not tuned**:
watch `docker stats` during the staging load and the first backups.

* **Backups use disk, not memory.** Archives are streamed; a database dump is staged as a temporary file (a tar entry needs its size up front), so
  budget **free disk ≥ the largest uncompressed database dump + the largest compressed site archive** under `NODERA_DATA_DIR/backups` (and `/tmp`
  inside the agent for nothing — dumps go to the shared data directory). A backup that cannot fit fails and cleans up; it does not corrupt anything.
  Temp files (`.tmp-backup-*`) are removed on success, failure and cancellation, and swept at start-up after a crash.
* **Concurrency:** the job worker runs operations strictly one at a time per API process, so a backup, a restore and a deploy never run in parallel — and a long backup delays every other queued operation until it finishes.
  Next.js has no cache volume (tmpfs); the web container is stateless.
* Docker log files, the Postgres volume and `/var/backups/nodera` are the things that fill the disk; all three are bounded or monitored above.

## Rate limiters: what is shared and what is not

| Limiter | Key | Budget | State |
|---|---|---|---|
| Login | client IP | 5 / 5 min | Redis; **falls back to a per-instance limiter** (not fully open) if Redis is down |
| Signup | client IP | 3 / h | Redis; same fallback |
| AI chat + agent run + AI plans | organisation | 60 / min | Redis; same fallback |
| Create organisation | user | 10 / h | Redis; same fallback |
| Change password | user | 5 / 5 min | Redis; same fallback |
| API budget | organisation + actor | 1200 / min | **in-process only** |
| Dangerous (approval-gated) requests | organisation + actor | 30 / h | **in-process only** |
| Agent requests / enrolment | agent id / IP | 600 / min, 10 / h | **in-process only** |

Fine for the first deployment (**exactly one API instance**). **Before running more than one API instance, every limit whose invariant must hold
deployment-wide must move to shared state** — the four in-process rows above — or each instance will grant its own full budget. Not implemented;
not needed until horizontal scaling exists (also blocked by the single-node Compose design).

## What is NOT verified until it runs on the real node

Docker GID and socket permissions; bind-mount ownership for workloads (they run as uid 10001; verified on a local VM, not on the host filesystem);
Traefik version/labels/Let's Encrypt/HTTP→HTTPS; the Storage Box host key, port 23 and the `ssh-ed25519` pin; the real MariaDB version
(`NODERA_MARIADB_LEGACY_CLIENT` for < 10.5), its container name and root password; firewall; `daemon.json` log defaults; reboot recovery
(`restart: unless-stopped` + healthcheck ordering); the shared data directory permissions (`2750`, uid 10001); behaviour under real disk pressure.
Each has a test in `docs/SMOKE-TESTS.md` or `docs/DISASTER-TESTS.md`.

## NOT IMPLEMENTED (does not block the first deployment)

Hetzner Cloud provider, Cloudflare DNS, generic ACME client, GitHub deployment provider/build runner/webhooks, FTP/SFTP/SSH/cPanel migration
connectors, SMTP/email/password recovery/invitations, client portal, HA / multiple API instances / multi-region. See `docs/ROADMAP.md`.
