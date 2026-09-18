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

**Phase 6** — this pass:
- [x] Tool Gateway execution backend (`internal/tools`): permission check →
      risk-tier check → approval gate (privileged/critical) → handler →
      audit, all real and tested. `get_server_metrics` has a genuine
      handler (wrapping `infrastructure.Service.Get`, returning labeled
      inventory data, not fabricated live telemetry); every other seeded
      tool honestly reports `NOT_IMPLEMENTED` rather than faking success
- [x] Human approval workflow: `POST /api/v1/tools/{key}/execute` on a
      privileged/critical tool returns `202` + an approval id instead of
      running; `POST /api/v1/approvals/{id}/decide` records the decision
      and, if approved, attempts real execution — recording an honest
      `NOT_IMPLEMENTED` outcome when no handler exists rather than treating
      "approved" and "executed successfully" as the same fact
- [x] `GET /api/v1/tools`, `GET /api/v1/approvals` for visibility
- [x] Tests: 6 integration tests covering the read-tool happy path,
      permission denial, privileged-tool approval creation, approve→execute
      (both the NOT_IMPLEMENTED and the real-handler cases), and
      reject-never-executes — plus a live end-to-end smoke test through the
      real HTTP API with a full audit trail
- [x] `docs/AGENTS.md` rewritten to state precisely what's real (the
      pipeline, one handler) vs. not (the agent execution loop, most tool
      handlers, sandboxing, approval expiry)

## Next up

1. **API token target scoping**: today a user can only revoke their own
   tokens and only user-owned tokens exist — service-account-issued tokens
   and org-admin management of other users' tokens are deferred (see
   `internal/identity/apitoken.go`).
2. **Concrete job types**: the worker dispatcher is real but nothing
   enqueues a `backup.create` or `deploy.application` job yet — those
   arrive with the backups/deployment domains.
3. **More tool handlers**: `get_container_logs` and `check_ssl` are natural
   next read-risk tools now the pattern (wrap a domain service, register in
   `cmd/server/main.go`) is proven.
4. **Approval expiration**: `approvals.expires_at` exists in the schema;
   nothing reads or enforces it yet.
5. **Rate limiting beyond `/auth/login`**, and a Redis-backed limiter for
   multi-instance deployments (today's limiter is in-process only).
6. **Dependency vulnerability scanning** in CI (`govulncheck` for the API;
   an equivalent for `web/`, or the Next.js 16 upgrade that resolves the
   current PostCSS advisories — `docs/SECURITY.md`).
7. **OpenAPI/Swagger generation** and list-endpoint pagination — `web/`'s
   hand-written `lib/types.ts` is the thing to replace once this lands.
8. **A cloud AI provider adapter** (OpenAI or Anthropic) now that the
   provider/adapter separation pattern is proven with Ollama — will need
   the secrets module for credential storage.
9. **Agent execution loop**: read `agents.system_instructions`, drive the
   AI gateway, enforce `allowed_tool_keys` when an agent (not a human) is
   the caller of `tools.Execute`.
10. **Frontend follow-ups**: AI profile/chat UI, provider/model registry
    UI, a tools/approvals UI (real backend now exists for this one),
    RBAC/settings management UI, real-time updates (polling or websockets)
    instead of load-once pages.

## Explicitly not started (rule 38 — deferred by design)

Kubernetes support, multi-region orchestration, GPU auto-provisioning,
ML-based AI routing, model fine-tuning, unrestricted autonomous agents,
complete CyberAudit/Web Content Flow/Kiko functionality.
