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

**Phase 5** — this pass:
- [x] Real Ollama provider adapter (`internal/ai/providers/ollama`),
      tested against a mock HTTP server (rule 39 — no GPU/model download
      required for development or CI)
- [x] AI provider/model registry management (`internal/ai/registry.go`,
      `POST /api/v1/ai/providers`, `POST /api/v1/ai/models`) — the registry
      was previously migration-seed-only, which meant no new provider
      could actually be added without a code change; now an operator can
      register Ollama (or any future provider row) through the API. This
      is a platform-wide registry by design (no `organization_id`),
      documented as a deliberate phase-1 simplification in
      `docs/SECURITY.md` and `docs/API.md`
- [x] `cmd/server/main.go` auto-registers the `ollama` provider row when
      `NODERA_OLLAMA_BASE_URL` is set — registry and Go adapter are
      separate concerns on purpose (a row can exist with no adapter,
      correctly resolving to `UNAVAILABLE`, never a fabricated response —
      verified by `TestAIRegistryProviderWithNoAdapterIsUnavailable`)
- [x] Full pipeline integration test against a mock Ollama server proving
      profile → router → registry → real adapter → usage tracking all
      work together, plus a live smoke test of the fail-closed path

## Next up

1. **Tool Gateway execution backend** for at least one `read`-risk tool
   (`get_server_metrics`), including the approval flow for a `privileged`
   one — the `agents`/`tools`/`approvals` schema is ready, no Go domain
   package exists yet.
2. **API token target scoping**: today a user can only revoke their own
   tokens and only user-owned tokens exist — service-account-issued tokens
   and org-admin management of other users' tokens are deferred (see
   `internal/identity/apitoken.go`).
3. **Concrete job types**: the worker dispatcher is real but nothing
   enqueues a `backup.create` or `deploy.application` job yet — those
   arrive with the backups/deployment domains.
4. **Rate limiting beyond `/auth/login`**, and a Redis-backed limiter for
   multi-instance deployments (today's limiter is in-process only).
5. **Dependency vulnerability scanning** in CI (`govulncheck` for the API;
   an equivalent for `web/`, or the Next.js 16 upgrade that resolves the
   current PostCSS advisories — `docs/SECURITY.md`).
6. **OpenAPI/Swagger generation** and list-endpoint pagination — `web/`'s
   hand-written `lib/types.ts` is the thing to replace once this lands.
7. **A cloud AI provider adapter** (OpenAI or Anthropic) now that the
   provider/adapter separation pattern is proven with Ollama — will need
   the secrets module for credential storage.
8. **Frontend follow-ups**: AI profile/chat UI, provider/model registry
   UI, approvals UI (once its backend exists), RBAC/settings management
   UI, real-time updates (polling or websockets) instead of load-once
   pages.

## Explicitly not started (rule 38 — deferred by design)

Kubernetes support, multi-region orchestration, GPU auto-provisioning,
ML-based AI routing, model fine-tuning, unrestricted autonomous agents,
complete CyberAudit/Web Content Flow/Kiko functionality.
