# Roadmap

Reflects the priority order in the product brief §37, adjusted for what's
now actually done. Not a committed schedule — a prioritized punch list.
See git history for the detailed breakdown of each completed phase; not
duplicated here so it can't drift out of sync.

## Done

**Phase 1**: application skeleton, database, identity/auth, tenancy, RBAC,
audit, infrastructure (nodes) domain.

**Phase 2**: applications domain, user-owned scope-limited API tokens, a
real Postgres-backed jobs worker, and the AI Gateway HTTP surface with a
deterministic, privacy-policy-enforcing router.

**Phase 3**: secrets module (AES-256-GCM at rest), login rate limiting,
secure response headers, and a CI pipeline (gofmt/vet/build/`test -race`).

**Phase 4**: frontend skeleton (`web/`, Next.js + TypeScript + Tailwind)
and CORS support on the API.

**Phase 5**: a real Ollama AI provider adapter and API-managed AI
provider/model registry.

**Phase 6**: the Tool Gateway execution backend and human approval
workflow, with one real tool handler (`get_server_metrics`).

**Phase 7** — this pass:
- [x] Approval expiration — `defaultApprovalTTL` (24h), enforced lazily at
      the top of `ListApprovals`/`DecideApproval` (no new worker
      infrastructure needed); `TestTools_ExpiredApprovalCannotBeDecided`
- [x] `check_ssl` — a second real tool handler
      (`internal/tools/handlers/checkssl.go`): a genuine TLS handshake
      reporting the real leaf certificate's validity, days remaining, and
      trust status. Verified live against `github.com` and by unit tests
      against a real local TLS listener, not a fabricated response
- [x] Signup rate limiting (3/hour per IP), alongside the existing login
      limiter — verified live (`429` on the 4th rapid signup)
- [x] Dependency vulnerability scanning in CI: `govulncheck` for the API
      (fails on reachable vulnerabilities), `npm audit --audit-level=critical`
      for `web/` (fails only on critical, keeping the known tracked
      high-severity PostCSS finding visible without blocking every PR)
- [x] Docs (`AGENTS.md`, `API.md`, `SECURITY.md`, `DATABASE.md`,
      `README.md`) updated to match

## Next up

1. **API token target scoping**: today a user can only revoke their own
   tokens and only user-owned tokens exist — service-account-issued tokens
   and org-admin management of other users' tokens are deferred (see
   `internal/identity/apitoken.go`).
2. **Concrete job types**: the worker dispatcher is real but nothing
   enqueues a `backup.create` or `deploy.application` job yet — those
   arrive with the backups/deployment domains.
3. **More tool handlers**: `get_container_logs` needs a container domain
   that doesn't exist yet; `create_backup`/`verify_backup` need the jobs
   system wired to an actual backup mechanism.
4. **A background approval-expiry sweep** independent of `ListApprovals`/
   `DecideApproval` being called (today's lazy-check approach is
   sufficient for a human-facing queue, but a truly idle organization's
   stale approvals won't flip to `expired` until someone looks).
5. **Rate limiting beyond `/auth/login` and `/auth/signup`**, and a
   Redis-backed limiter for multi-instance deployments.
6. **OpenAPI/Swagger generation** and list-endpoint pagination — `web/`'s
   hand-written `lib/types.ts` is the thing to replace once this lands.
7. **A cloud AI provider adapter** (OpenAI or Anthropic) now that the
   provider/adapter separation pattern is proven with Ollama — will need
   the secrets module for credential storage.
8. **Agent execution loop**: read `agents.system_instructions`, drive the
   AI gateway, enforce `allowed_tool_keys` when an agent (not a human) is
   the caller of `tools.Execute`.
9. **Frontend follow-ups**: AI profile/chat UI, provider/model registry
   UI, a tools/approvals UI (real backend now exists for this one),
   RBAC/settings management UI, real-time updates (polling or websockets)
   instead of load-once pages.

## Explicitly not started (rule 38 — deferred by design)

Kubernetes support, multi-region orchestration, GPU auto-provisioning,
ML-based AI routing, model fine-tuning, unrestricted autonomous agents,
complete CyberAudit/Web Content Flow/Kiko functionality.
