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

**Phase 7**: approval expiration, a second real tool handler (`check_ssl`,
a genuine TLS check), signup rate limiting, and dependency vulnerability
scanning in CI (`govulncheck`, `npm audit`).

**Phase 8** — this pass:
- [x] Service accounts (`internal/identity/serviceaccount.go`):
      create/list/disable, gated by `organization.manage`. The
      `service_accounts` table existed since migration 0001 but nothing
      could create one until now
- [x] Service-account-issued API tokens
      (`CreateAPITokenForServiceAccount`) — same no-privilege-escalation
      rule as user-owned tokens (can't grant a scope the creator doesn't
      hold). Verified live: a service-account token authenticates as the
      service account with exactly its granted scopes, and is correctly
      forbidden from a tool call outside those scopes
- [x] Org-admin token management (`AdminListAPITokens`,
      `AdminRevokeAPIToken`, `organization.manage`): lists/revokes any
      token in the org regardless of owner — the counterpart to the
      existing self-scoped `ListAPITokens`/`RevokeAPIToken`. Verified live
      that a user's self-scoped list correctly excludes a service
      account's token while the admin listing includes it with correct
      owner attribution
- [x] New endpoints: `GET/POST /api/v1/service-accounts`,
      `DELETE /api/v1/service-accounts/{id}`,
      `POST /api/v1/service-accounts/{id}/api-tokens`,
      `GET/DELETE /api/v1/organization/api-tokens[/{id}]`
- [x] Tests: 5 new integration tests (create/list/disable, service-account
      token authentication + scope enforcement, cross-owner admin
      listing/revocation, permission denial for a plain 'member') — all
      passing under `-race`
- [x] Docs (`API.md`, `README.md`) updated to match

**Phase 9** — this pass:
- [x] Background approval-expiry sweep (`tools.Registry.RunExpirySweep`,
      run every 5 minutes across every organization by a goroutine in
      `cmd/server/main.go`) — an idle organization's stale approvals now
      flip to `expired` on schedule, not only when someone happens to call
      `ListApprovals`/`DecideApproval` for that org
- [x] Disabling a service account now atomically revokes all its
      outstanding tokens (previously it only blocked minting new ones,
      a narrower guarantee that was explicitly documented at the time and
      is now closed) — verified live (token works, `DELETE`, token
      immediately returns `401`)
- [x] AI chat rate limiting (60 requests/minute per organization) —
      protects against runaway provider cost or one tenant crowding out
      others on a shared local model, deliberately keyed by organization
      rather than IP since that's the actual risk boundary for this
      endpoint
- [x] Tests: 1 new integration test for the cross-org sweep, an existing
      test strengthened to prove token revocation on disable — all passing
      under `-race`
- [x] Docs (`AGENTS.md`, `API.md`, `SECURITY.md`, `README.md`) updated

## Next up

1. **Concrete job types**: the worker dispatcher is real but nothing
   enqueues a `backup.create` or `deploy.application` job yet — those
   arrive with the backups/deployment domains.
2. **More tool handlers**: `get_container_logs` needs a container domain
   that doesn't exist yet; `create_backup`/`verify_backup` need the jobs
   system wired to an actual backup mechanism.
3. **Rate limiting beyond `/auth/login`, `/auth/signup`, and `/ai/chat`**,
   and a Redis-backed limiter for multi-instance deployments.
4. **OpenAPI/Swagger generation** and list-endpoint pagination — `web/`'s
   hand-written `lib/types.ts` is the thing to replace once this lands.
5. **A cloud AI provider adapter** (OpenAI or Anthropic) now that the
   provider/adapter separation pattern is proven with Ollama — will need
   the secrets module for credential storage.
6. **Agent execution loop**: read `agents.system_instructions`, drive the
   AI gateway, enforce `allowed_tool_keys` when an agent (not a human) is
   the caller of `tools.Execute`.
7. **Per-tool/per-org-configurable approval TTL** (today's
   `defaultApprovalTTL` is a single global 24h constant).
8. **Frontend follow-ups**: service accounts/API tokens UI (real backend
   now exists for this), AI profile/chat UI, provider/model registry UI, a
   tools/approvals UI, RBAC/settings management UI, real-time updates
   (polling or websockets) instead of load-once pages.

## Explicitly not started (rule 38 — deferred by design)

Kubernetes support, multi-region orchestration, GPU auto-provisioning,
ML-based AI routing, model fine-tuning, unrestricted autonomous agents,
complete CyberAudit/Web Content Flow/Kiko functionality.
