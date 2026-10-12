# Final implementation audit

Self-audit of Nodera as an "Infrastructure Operating System / Control Plane", **reconciled against the code** at the Hetzner-readiness pass. It says what is real, what only runs against mocks or
local providers, and what waits for the real environment. Nothing is rounded up. **This is not a "production ready" claim.**

**Verdict: READY FOR HETZNER STAGING DEPLOYMENT (code side).** Next phase: real deployment → staging validation → disaster testing → production gate (`docs/PRODUCTION-GATE.md`) → pilot → migration. No feature work in between.

Measured: 32 migrations · 203 documented API operations (161 paths, drift-tested) · 96 Go source files (~27 k lines) · 323 Go test functions (`go test ./... -p 1`, and `-race`, against real Postgres and Redis) · 86 TypeScript files · 8 web unit tests ·
`govulncheck`: 0 reachable vulnerabilities (Go 1.27.2) · web dependency policy green with one documented, expiring exception.

## Status vocabulary

| Term | Meaning |
|---|---|
| IMPLEMENTED | code exists |
| LOCALLY TESTED | CI tests pass and/or it was exercised end to end on a developer machine (real Docker/MariaDB/Traefik/SFTP on a local Linux VM). **Not** the Hetzner host |
| STAGING VALIDATED | ran successfully on the real Hetzner environment with non-customer test workloads — *nothing is at this level* |
| PRODUCTION VALIDATED | the production gate and disaster tests passed — *nothing is at this level* |
| NOT IMPLEMENTED | no code |

## Per-component status

| Component | Status | Notes |
|---|---|---|
| Identity, RBAC, tenancy, audit, secrets, API tokens | IMPLEMENTED + LOCALLY TESTED | pre-existing, extended; real Postgres suites |
| Browser auth (HttpOnly/Secure cookie, CSRF), CORS | IMPLEMENTED + LOCALLY TESTED | also verified through Traefik with TLS termination (self-signed) |
| Operations engine (steps, rollback, cancel, idempotency, SSE) | IMPLEMENTED + LOCALLY TESTED | SSE progressive through Traefik; start-up marks restart-interrupted jobs failed (retry safe) |
| Approvals (dangerous operations) | IMPLEMENTED + LOCALLY TESTED | atomic single decision; 8-way race → 1×200, 7×409 |
| Projects / provisioning (WordPress) | IMPLEMENTED + LOCALLY TESTED | real Docker + MariaDB through the agent on a local VM; **REAL HETZNER VALIDATION PENDING** |
| Node Agent (protocol, allowlist, guard, DB ops) | IMPLEMENTED + LOCALLY TESTED | `docs/NODE-AGENT.md`; **REAL HETZNER VALIDATION PENDING** (Docker GID, socket, bind-mount ownership) |
| Provider mode `agent` (API without Docker socket) | IMPLEMENTED + LOCALLY TESTED | the production default; `docker` mode refused in production |
| Docker adapter | IMPLEMENTED + LOCALLY TESTED | real daemon on a local VM; Hetzner pending |
| MariaDB adapter | IMPLEMENTED — REAL MARIADB/DOCKER VALIDATION PENDING | exercised against a real MariaDB 11 container locally (ensure/dump/load/drop via the agent); injection, redaction, legacy-client and timeout tests in CI; the Hetzner MariaDB version/container/credentials are unknown |
| Backups: streaming create/verify/restore, safety snapshot, rollback | IMPLEMENTED + LOCALLY TESTED | bounded memory proven (384 MiB dump, ~1 MiB heap); temp files cleaned on failure/cancel/crash; ENOSPC → clean failure |
| Off-site backup (SFTP / Storage Box) | IMPLEMENTED — REAL HETZNER STORAGE BOX VALIDATION PENDING | pinned host key mandatory; hash-verified remote copy; tested vs an in-process SSH server (CI) and a real OpenSSH SFTP server (local) |
| Control-plane DR (`backup-nodera.sh`, `restore-nodera.sh`) | IMPLEMENTED + LOCALLY TESTED | age-encrypted, off-site, read-back verified, restore proven (marker row rolled back); full-server rebuild is disaster test 20, **not done** |
| Production compose (`docker-compose.prod.yml`) | IMPLEMENTED + LOCALLY TESTED | ran unmodified on a local VM; invariants enforced in CI; **never on Hetzner** |
| Database roles + audit immutability | IMPLEMENTED + LOCALLY TESTED | 10 checks as the runtime role; CI job; real-database verification pending |
| Migrations as a release step (`nodera-migrate`) | IMPLEMENTED + LOCALLY TESTED | advisory lock, `-status`, API verifies instead of migrating in production |
| Network: domains, DNS (local provider), SSL (local CA) | IMPLEMENTED + LOCALLY TESTED as **development** features | DNS is in-memory, certificates are a development CA; reported `local_dev`; **not production DNS/TLS** |
| Deployments (upload / mock git) | IMPLEMENTED + LOCALLY TESTED | GitHub provider, build runner, webhooks NOT IMPLEMENTED |
| Site migration (zip) | IMPLEMENTED + LOCALLY TESTED | FTP/SFTP/SSH/cPanel connectors NOT IMPLEMENTED |
| Monitoring, alerts, incidents, notifications, logs | IMPLEMENTED + LOCALLY TESTED | email channel NOT IMPLEMENTED; no self-paging |
| WordPress engine (clone/update/health) | IMPLEMENTED + LOCALLY TESTED (mock/local) | real update/clone on Hetzner pending |
| Feature flags, dashboard, search, AI plans | IMPLEMENTED + LOCALLY TESTED | AI only proposes; humans approve and run |
| Release pipeline (GHCR immutable images) | IMPLEMENTED — NOT YET EXECUTED | `release.yml` needs its first tag push |
| Hetzner Cloud provider, Cloudflare DNS, generic ACME, GitHub provider, SMTP, remote migration connectors, client portal | NOT IMPLEMENTED | prepared skeletons or schema only; not required for the first architecture |

## 1. What genuinely works today

Verified by tests against real PostgreSQL and the real HTTP router (and, for the UI, by driving the running app in a browser):

* **Operation framework** — persisted steps/logs, reverse-order rollback, cancellation, idempotent submit under concurrency, panic containment, correlation id, SSE progress, recovery of jobs interrupted by a restart.
* **Approvals for dangerous operations** — `project.delete`, `backup.restore`, `backup.delete`, `domain.remove`, `migration.cutover`, `deployment.production` cannot be submitted directly; nothing runs before a human decision.
* **Clients / projects / provisioning**, **backups** (real tar.gz provider, streaming, off-site SFTP), **domains/DNS/SSL** (local CA), **deployments**, **site migration** from zip archives, **WordPress** clone/update/health, **monitoring/alerts/incidents/logs** — as in the previous audit, unchanged in behaviour.
* **Node Agent** — enrolment, Ed25519 signed requests with replay table, signed agent-bound commands, allowlist twice, revocation, protected control-plane resources, database operations with file hand-off, replayed-command refusal, execution time limits.
* **Security regression suite** and OpenAPI drift test; production browser-security tests; configuration tests that make an unsafe production configuration impossible to start.

## 2. What only works with mocks (or local simulations)

| Area | Reality |
|---|---|
| Containers, databases, git | In `mock` mode in-memory. In `local` mode absent (engines refuse to run). In `docker`/`agent` modes real but only locally tested. Git: mock only. |
| DNS | A local in-process zone store (lost on restart). Not a registrar/Cloudflare. Reported `local_dev`. |
| Certificates | Real X.509 from a *local* CA — **not browser-trusted**. Reported `local_dev`. Nodera's own TLS comes from Traefik + Let's Encrypt. |
| Nodes | `NodeProvider` is mock-only. |
| Demo mode | Everything simulated, labelled in the UI and `/system/info`; refused when `NODERA_ENV=production`. |

## 3. What depends on Hetzner (cannot be proven until deployed)

Docker daemon integration and the **Docker GID**; the agent on the real host; workload bind-mount ownership (uid 10001) on the host filesystem; **MariaDB via real Docker** (version, container name, credentials); Traefik version/labels/network/TLS/HTTP→HTTPS and Let's Encrypt;
`NODERA_TRUSTED_PROXIES` correctness; production PostgreSQL roles on the real database; the **Storage Box** host key, port 23, key auth and throughput; `daemon.json` log defaults; firewall; **reboot recovery**; **disaster recovery of the whole server**; resource limits under real load; disk-pressure behaviour.
Each has an explicit test in `docs/SMOKE-TESTS.md` / `docs/DISASTER-TESTS.md`. Nothing is hardcoded to the host: names, networks, hosts and resolvers are configuration.

## 4. What depends on external services (NOT IMPLEMENTED — not blockers for the first architecture)

GitHub (provider, webhooks, build runner), Cloudflare DNS, generic ACME client, Hetzner Cloud API, SMTP/email/password reset/invitations, FTP/SFTP/SSH/cPanel migration sources, S3 backup target, client portal. AI providers (Ollama/Anthropic/OpenAI) exist and need credentials.

## 5. Destructive operations

`project.delete`, `backup.restore` (overwrites live data), `backup.delete`, `domain.remove`, `migration.cutover`, `deployment.production` (all **approval-gated**), plus `wordpress.update`, `migration.rollback`, `ssl.revoke`, agent `docker.remove`/`filesystem.remove` (allowlisted, signed, **never on a protected control-plane resource**),
client soft delete, retention sweeps and `agent revoke`. All require a permission and are audited.

## 6. Operations with rollback

| Operation | Rollback |
|---|---|
| `project.provision` | every step has an idempotent Undo; a database or container is only removed if *this run* created it |
| `backup.create` | a failed or cancelled run leaves no archive (atomic rename), no remote object, no temp file |
| `backup.restore` | mandatory safety snapshot, restored automatically on failure; `rollback_failed` names the snapshot |
| `deployment.*` | previous live tree restored automatically on any failure after DEPLOY; `deployment.rollback` |
| `migration.run` / `.cutover` | staging removed / safety backup restored; `migration.rollback` after completion |
| `wordpress.clone` / `.update` | full undo / old image + safety backup restored |
| `ssl.issue/renew` | order marked failed, secret removed |
| interrupted by an API restart | marked `failed` at start-up; retry is safe (steps are idempotent) |
| **no rollback by design** | `project.delete`, `backup.delete`, `domain.remove` (forward-only, approval-gated), retention sweeps |

## 7. Audited operations

Every state-changing service call writes an audit row (never secret values). **Not audited by design:** high-volume reads, log ingestion, heartbeats, notification read state. In production the runtime role cannot modify or delete audit rows.

## 8. Areas without a (complete) backend

Client portal, S3 backup target, SMTP, password reset, GitHub webhooks, build runner, remote migration connectors, persistent UI filters, server-side pagination in UI tables, WebSocket (SSE only), OpenAPI generated from code (hand-maintained + drift test),
`database.delete` / `credential.rotate` gateway tools (registered, no operation yet), automated rotation of the secrets key.

## 9. Tests

* Go: 323 test functions across operations, provisioning, backups (streaming memory bound, failure/cancel cleanup), off-site SFTP (host-key pinning, corruption, credentials), network, deployments, migrations, WordPress, monitoring, AI plans, node agent (protocol, executor, guard, replay, agent-mode end to end),
  production configuration, production browser security, trusted-proxy client IP, security suite, OpenAPI drift, rate limits (including Redis outage fallback), providers, redaction, netpolicy, plus the pre-existing identity/RBAC/tenancy/audit/secrets/tools suites.
  Run: `go test ./... -p 1` and `-race` (needs `NODERA_TEST_DATABASE_URL`, optionally `NODERA_TEST_REDIS_URL`).
* Web: Vitest, ESLint (zero warnings), `tsc`, `next build`; **no browser E2E suite**.
* Operational: `scripts/staging-smoke.py` (security, provisioning, approval race), `nodera-agent selftest`, `scripts/check-compose-prod.py`, `scripts/db/03-verify-roles.sql`.
* CI: see `docs/SECURITY.md` "CI". The Release workflow has not run yet.
* **Not tested anywhere:** the Hetzner host, real Let's Encrypt, a real Storage Box, reboot recovery, whole-server loss.

## 10. Remaining risks

1. **Unproven on the target** — everything in §3. The first deployment will find issues; the abstractions, failure paths and tests are in place, and local end-to-end testing already found and fixed real defects (workload ownership vs. restore, client-IP behind a proxy, request-body bound, agent auth on database failure, host-key algorithm selection).
2. **Workload file ownership.** Managed containers run as uid 10001 (`NODERA_WORKLOAD_USER`) so the control plane can back up/restore/delete their files; images that refuse to run as an arbitrary uid need a different strategy. WordPress (apache) was verified; others are not.
3. **One API instance only.** The job worker is single-threaded; in-process rate limits (API/dangerous/agent) are per instance and must move to shared state before scaling out; a long backup delays other operations.
4. **The agent is root-equivalent.** Allowlist + guard + no shell narrow what it can be asked to do, but a vulnerability in the agent, the Docker CLI or the signing key (agent-signing-key custody!) is a host compromise.
5. **Key loss is permanent**: `secrets-encryption-key` cannot be rotated by any tool and cannot be recovered; custody is a manual, human control (`docs/KEY-CUSTODY.md`).
6. **Nodera does not page anyone** about disk, failed backup timers, a degraded `/ready`, or an unreachable Storage Box. External alerting is a gate item.
7. **Local DNS/certificates are development implementations** — fine for development, wrong for customers' real domains. Cloudflare and ACME are NOT IMPLEMENTED.
8. **Forward-only migrations** — rolling back a release that migrated means restoring the pre-release control-plane backup.
9. **Web build-time advisory** `GHSA-vfj7-8cjw-p6xm` (`braces`, no fix exists) is accepted until 2027-01-31 with a documented exposure analysis (`docs/SECURITY.md`).
10. **Security review**: the suite is thorough but is not a substitute for an external penetration test before exposing the platform to untrusted tenants.

## Definition of done — honest checklist

Done and verified in code/CI/local runs: architecture audited · existing functionality preserved · multi-tenancy · RBAC · secrets · job engine · operation framework · node agent architecture + security (incl. protected resources) · project model ·
WordPress/deployment/migration/backup/restore/monitoring/alerting/incident engines · DNS and SSL abstractions · approvals · AI safety architecture · realtime (SSE) · API versioning · structured errors · rate limiting · health/readiness semantics ·
feature flags · dev + production compose · versioned release workflow · production DB role model · control-plane backup/restore · documentation · roadmap · tests · lint · typecheck · build · security tests · no secrets committed (the repo's `.env` is git-ignored and was never read).

**Partially met / not claimed:** "Infrastructure providers implemented" — interfaces, mock, local, Docker, MariaDB and SFTP adapters exist and are LOCALLY TESTED; Hetzner/Cloudflare/ACME/GitHub are skeletons. "Production Docker configuration" — prepared, validated in CI and run locally, **not deployed**.
"No fake production success states" — met: mock results are labelled, absent providers fail loudly, development DNS/TLS are labelled `local_dev`, email without SMTP reports failure.
