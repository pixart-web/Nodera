# Final implementation audit

Self-audit of the "Infrastructure Operating System / Control Plane" implementation. It states what is real, what only runs
against mocks or local providers, and what is waiting for the real environment. Verified facts are marked **✔**; things that could
not be verified here are marked **✘ unverified**. Nothing below is rounded up: this is **not** a "100%" claim — the critical
production integrations (real Docker/MariaDB/Traefik on the Hetzner node, GitHub, Cloudflare, Let's Encrypt, Hetzner Cloud) have **not** run, only
their abstractions, workflows and tests have.

Measured at the audit commit: 31 migrations · 203 documented API operations (161 paths, drift-tested) · 89 Go source files (~25 k lines) ·
270 Go test functions (all pass with `-race -p 1`) · 86 TypeScript files · 8 web unit tests · `govulncheck`: 0 reachable vulnerabilities (Go 1.27.2).

## 1. What genuinely works today

Verified by tests against real PostgreSQL and the real HTTP router (and, for the UI, by driving the running app in a browser):

* **Operation framework** — persisted steps/logs, reverse-order rollback, cancellation, idempotent submit under concurrency, panic containment, correlation id, SSE progress.
* **Approvals for dangerous operations** — `project.delete`, `backup.restore`, `backup.delete`, `domain.remove`, `migration.cutover`, `deployment.production` cannot be submitted directly; nothing runs before a human decision (tested for each).
* **Clients / projects / provisioning** (create → provision → active, failure rolls back, retry works).
* **Backups** with the *real* tar.gz provider: SHA-256 verification, restore round trip, safety snapshot + rollback on failed restore, corruption detection, scheduled policies (idempotent per period), retention.
* **Domains / DNS / SSL** with the real local CA: record validation, provider-failure-leaves-no-phantom-row, propagation, issue/renew/revoke, expiry sweep, auto-renew (idempotent), private keys only encrypted in the secrets store (scanned for leaks).
* **Deployments** (upload and git-via-provider): stage tracking, automatic revert on failed health check, explicit rollback, production approval, path/ref/repository validation.
* **Site migration** from zip archives: preflight (PASS/WARNING/BLOCKER), zip-slip/zip-bomb-safe extraction, `wp-config.php` never migrated, serialized-data-safe URL rewrite (unit-tested incl. nested/escaped/multibyte), staging, health score, approved cutover with automatic restore, rollback after completion.
* **WordPress**: clone (files + DB + URL rewrite, full undo), in-place update (backup, image swap, restore old image on failure), health report.
* **Monitoring / alerts / incidents / notifications / logs**: SSRF-guarded checks (verified against the real local provider), one deduplicated incident per breach with auto-resolve, lifecycle + timeline, per-user read state, redacted logs, retention (audit log protected).
* **Node Agent**: enrolment (one-time hashed token, expiry, one agent per node), Ed25519 signed requests with replay table, signed agent-bound commands, allowlist enforced twice, revocation — exercised end to end with a real agent object against the real router.
* **Platform**: feature flags (gate engines per org), dashboard + global search + command palette (permission-aware), notification bell (SSE), AI plans, structured errors with `details`, per-actor and dangerous-request rate limits, `/health` + `/ready` (honest provider states).
* **Security regression suite** (SQLi, XSS, CSRF, IDOR/cross-tenant, privilege escalation, secret leakage, path traversal, command injection, replay/forged agents) and an OpenAPI drift test.
* **Docker**: API, agent and web images build; dev compose and the production template validate; the API image was started and exercised (demo mode, login, dashboard, certificates, system info). Demo mode + labelled seed work.
* **Frontend**: all pages read/write the real API (no mock data left), live operation panel over SSE, approvals feedback, DEVELOPMENT/mock banner driven by the server. `eslint --max-warnings 0`, `tsc`, `next build` and vitest pass.

## 2. What only works with mocks (or local simulations)

| Area | Reality |
|---|---|
| Containers, databases, git | In `mock` mode they are in-memory. In `local` mode they are **absent** and engines refuse to run (honest failure). Real: Docker CLI adapter, MariaDB-over-`docker exec` adapter — **✘ unverified against a real daemon/server** (tested through runner seams only). |
| DNS | A local, in-process zone store (lost on restart; `Sync` repairs it). Not a registrar/Cloudflare. |
| Certificates | Real X.509 from a *local* CA — valid structure, **not browser-trusted**. Mock certs are labelled `Nodera Mock CA` / `MOCK` in the UI. |
| Nodes | `NodeProvider` is mock-only. |
| Demo mode | Everything simulated, labelled MODO DEMO in the UI and `/system/info`; refused when `NODERA_ENV=production`. |
| Node Agent execution | Tested with the mock/in-memory providers inside the test process, not on a remote machine. |

## 3. What depends on Hetzner (or the real node)

Running `nodera-prod-01` with Docker + Traefik: the Docker adapter against the real daemon/socket (`NODERA_DOCKER_GID`), Traefik labels for your Traefik version and
resolver, the existing MariaDB container (adapter ready, `NODERA_MARIADB_*`), the Nodera PostgreSQL/Redis wiring, the production DB role without `UPDATE/DELETE` on `audit_log`,
the agent running on the node with the Docker socket, Hetzner Cloud node actions (adapter skeleton), firewall/network (`proxy-public`). Checklist: `docs/DEPLOYMENT.md`.
Nothing is hardcoded: node name, network, hosts, resolver and DB host are configuration.

## 4. What depends on external services

* **GitHub** — token/App; webhooks. `GitProvider` interface + mock; adapter is a skeleton that fails with `ErrUnavailable`.
* **Cloudflare DNS**, **Let's Encrypt/ACME** — credentials + clients (skeletons).
* **S3 / SFTP backup targets** — schema only.
* **SMTP** — email notification channel reports "SMTP is not configured"; no password-reset or invitations (no mail transport).
* **Remote migration sources** (FTP/SFTP/SSH/cPanel/remote WordPress) — `sitemig.Connector` interface only; preflight says so.
* **AI providers** — Ollama/Anthropic/OpenAI adapters pre-existing; plans need a configured profile.

## 5. Destructive operations

`project.delete`, `backup.restore` (overwrites live data), `backup.delete`, `domain.remove` (DNS + certificate), `migration.cutover` (replaces site + database),
`deployment.production`, plus `wordpress.update` (replaces the container image), `migration.rollback` (restores a snapshot), `ssl.revoke`, agent `docker.remove`/`filesystem.remove`
(allowlisted, signed), client soft delete, retention sweeps (delete old rows/artifacts) and `agent revoke`.
The first six require **approval**; all require a permission and are audited.

## 6. Operations with rollback

| Operation | Rollback |
|---|---|
| `project.provision` | every step has an idempotent Undo; a database or container is only removed if *this run* created it |
| `backup.restore` | mandatory safety snapshot, restored automatically on failure |
| `deployment.*` | previous live tree restored automatically on any failure after DEPLOY; `deployment.rollback` restores a release |
| `migration.run` | staging files/db removed, live site untouched |
| `migration.cutover` | safety backup restored automatically; `migration.rollback` after completion |
| `wordpress.clone` | full undo (project, db, container, files) |
| `wordpress.update` | old image recreated + safety backup restored |
| `ssl.issue/renew` | order marked failed, secret removed |
| **no rollback by design** | `project.delete`, `backup.delete`, `domain.remove` (forward-only; guarded by approval), retention sweeps |

## 7. Audited operations

Every state-changing service call writes an audit row (action, actor, resource, correlation id, resulting state — never secret values): operation submit/start/succeed/fail/cancel,
client/project/domain/record/certificate/backup/policy/deployment/migration changes, monitor/rule/channel/incident changes, feature-flag and retention changes, node-agent
registration/enrolment/revocation/command queueing, AI plan proposal/decision/step run, approvals (pre-existing), plus the pre-existing identity/RBAC/secrets/platform audit.
**Not audited by design:** high-volume reads, log ingestion, heartbeats (stored as metrics), notification read state.

## 8. Areas without a (complete) backend

Client portal (flag exists, off), S3/SFTP targets, SMTP/email, password reset, GitHub webhooks, build runner, remote migration connectors, persistent UI filters,
server-side pagination in the UI tables (API is paginated; tables page client-side over ≤200 fetched rows), WebSocket (SSE only), OpenAPI generated from code (hand-maintained + drift test),
`database.delete` / `credential.rotate` gateway tools (registered, no operation behind them yet).

## 9. Tests

* Go: 270 test functions — operations, provisioning, backups, network/SSL, deployments, migrations (+ rewrite unit tests), WordPress, monitoring/notifications/logs, AI plans, demo seed,
  node-agent protocol + end-to-end agent tests, security suite, OpenAPI drift, rate limits, SSE, providers (mock/local/docker/mariadb/external), redaction, netpolicy, plus the pre-existing identity/RBAC/tenancy/audit/secrets/tools suites.
  Run: `go test ./... -race -p 1` (needs `NODERA_TEST_DATABASE_URL`).
* Web: vitest (formatting, status mapping, SSE parser), ESLint, `tsc`, `next build`. **No browser E2E suite**: the main flows (login, create project, provision with live progress, domains/SSL/backups/monitoring/migration/deployment pages) were driven manually in a browser against the demo stack.
* CI: gofmt/vet/build/test -race/govulncheck, OpenAPI lint, web lint/typecheck/test/build/audit, Docker image builds, compose validation.

## 10. Remaining risks

1. **Unproven against production** — Docker/MariaDB/Traefik/Hetzner/GitHub/Cloudflare/ACME have never run. The first real deploy will find issues; the abstractions and failure paths are in place.
2. **Memory use on big sites** — the local backup provider builds archives in memory; deployments/migrations copy files through the FS provider (caps: 200 MB release, 64 MB upload, 512 MB extraction).
3. **Single-process background loops** (worker, sweepers, monitors) are idempotent and `SKIP LOCKED`-safe, but multi-instance behaviour has not been load-tested; the new rate limiters are per-instance.
4. **Local DNS store is volatile**; the DB is the source of truth and `Sync` repairs it, but a real provider is needed before DNS matters.
5. **Dev keys on disk** (`dev-secrets.key`, `dev-agent-signing.key`) are a development convenience; production refuses to start without explicit keys. Losing `NODERA_SECRETS_ENCRYPTION_KEY` makes stored secrets unrecoverable (documented).
6. **Audit immutability** relies on the production DB role (revoke `UPDATE/DELETE`), which is a deployment step, not code.
7. **AI plans** depend on model quality; safety does not (steps are vetted, humans approve and run), but a poor model yields empty plans.
8. **Forward-only migrations** (no down scripts) — a bad migration is fixed forward.
9. **UI**: no E2E regression suite; filters are not persisted; tables paginate client-side.
10. **Security reviews**: the suite is thorough but is not a substitute for an external penetration test before exposing the platform to untrusted tenants.

## Definition of done — honest checklist

Done and verified: architecture audited (this document, `docs/ARCHITECTURE.md` §9) · existing functionality preserved (all pre-existing suites pass) · multi-tenancy · RBAC (29 new granular permissions) ·
secrets · job engine · operation framework · node agent architecture + security · project model · WordPress/deployment/migration/backup/restore/monitoring/alerting/incident engines · DNS and SSL abstractions ·
GitHub integration *foundation* (interface + mock + prepared skeleton) · approvals · AI safety architecture · realtime (SSE) · command palette · notifications · API versioning (`/api/v1`) · structured errors · rate limiting ·
API tokens/service accounts (pre-existing hardening) · health endpoints · feature flags · Docker dev + prod template · local seed · demo mode · tests · lint · typecheck · build · security tests · documentation · roadmap · no critical TODO/FIXME ·
no secrets committed (the repo's `.env` is git-ignored and was never read).

**Partially met / not claimed:** "Infrastructure providers implemented" — interfaces, mock, local, Docker and MariaDB adapters exist; Hetzner/Cloudflare/ACME/GitHub are prepared skeletons. "Production Docker configuration prepared" — prepared and validated, **not deployed**.
"No fake production success states" — met: mock results are labelled `mock`/`MOCK`, absent providers fail loudly, email without SMTP reports failure.
