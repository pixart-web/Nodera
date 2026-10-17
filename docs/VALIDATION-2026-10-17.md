# NODERA / SERVON — Production Validation Evidence

Date: 2026-10-17
Host: nodera-prod-01

## Validated

- Production compose rendered successfully; all six declared services satisfy compose invariants.
- API, web, agent, PostgreSQL and Redis containers are healthy.
- `/ready` reports database, Redis, job queue and Agent healthy.
- Public HTTPS endpoint `nodera.pixart.pt` returns HTTP 200 for `/login` and uses a valid Let's Encrypt certificate.
- HTTP redirects to HTTPS with 308.
- Public API rejects unauthenticated access.
- Full staging security smoke suite passed: authentication, cookie flags, CSRF, tenant isolation, scoped bearer tokens, audit events, platform-role separation and logout/session revocation.
- Node Agent self-test passed all signed-request negative checks: replay, tampering, stale/future timestamps, wrong key, unknown agent and unsigned requests.
- Real production Agent is online and linked to the `pixart` organization.
- Real provisioning test created and started a disposable WordPress workload through the Agent; the workload was subsequently removed through the approval-gated deletion path.
- Approval-gated destructive operation was executed and recorded successfully.
- API, web, Agent, Redis, PostgreSQL and Traefik restart/recovery checks passed.
- Database post-migration protection script passed all 10 checks.
- Go test suite passed in a Go 1.27 build container with `-p 1`.
- Go race suite was executed in a cgo-enabled build container; no source-level race failures were observed in the reported packages.
- Web typecheck/lint/build passed; production web image built successfully.
- Web production image deployed as `nodera-web:servon-7dc717f` and health check passed.
- Static security/audit policy checks passed; `npm audit --omit=dev --audit-level=high` reported 0 vulnerabilities. One accepted high advisory is documented by the project audit policy until its stated expiry.
- Control-plane local encrypted backup was generated and read-back verification passed. The restore verifier was corrected to avoid a SIGPIPE/exit-141 caused by piping `pg_restore` through `head` under `pipefail`.
- No test users or test organizations remain from the validation run.

## Not yet GREEN / external dependencies

- Off-host disaster-recovery copy is not configured: `NODERA_BACKUP_DESTINATION=local` and the production SFTP/Storage Box key is empty.
- The validated backup is therefore only a local encrypted copy and is not a disaster-recovery copy.
- Host-level firewall/routing policy could not be independently changed from the non-root Commander session; this remains an operator-level verification item.
- GitHub CI/release publication is not complete for the local Servon branding commit because the server's Git HTTPS push is not authenticated. The production web image is currently a local immutable tag, not a published registry artifact.
- Provider adapters for Git and node provisioning remain explicitly `not_configured`/`local_dev` where shown by `/ready`; these are future integration capabilities, not silently treated as production features.
- Full host reboot was not performed because the Hetzner machine also carries unrelated customer workloads; a controlled maintenance-window reboot remains required before a hard GREEN disaster-recovery claim.

## Status

YELLOW — production operationally healthy and extensively validated, but not yet eligible for a truthful `GREEN — PRODUCTION VALIDATED` declaration until off-host backup/DR, release publication, and the remaining operator-level infrastructure checks are completed.
