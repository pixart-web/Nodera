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

**Phase 10** — this pass:
- [x] OpenAPI 3.0 spec (`api/openapi/openapi.json`, hand-maintained,
      validated against the real OpenAPI schema — not just hand-checked)
      covering all 32 implemented paths, embedded in the binary and served
      at `GET /openapi.json`; `GET /docs` serves Swagger UI against it
      (verified live in a browser, renders correctly, zero console errors)
- [x] `web/` can generate TypeScript types from the spec
      (`npm run gen:types` → `lib/api-types.generated.ts`, via
      `openapi-typescript`) — generated and committed as a foundation, not
      yet swapped in for the hand-written types
- [x] Pagination on the four list endpoints most likely to grow:
      `infrastructure/nodes`, `applications`, `jobs`, `audit`. A shared
      `internal/platform/httpserver.Page[T]`/`ParsePagination` — `?limit=`
      (default 50, capped 200) and `?offset=`, with `has_more` computed via
      a limit+1 over-fetch rather than a separate `COUNT` query. Each
      domain `List`/`Query` method got its own independent, higher safety
      ceiling so it stays safe to call directly from Go code that doesn't
      go through the HTTP layer
- [x] Updated `web/`'s 4 affected pages (plus the dashboard's stat cards,
      which now show "50+" rather than a count that looks precise but
      understates the true total once `has_more` is true — rule 36) and
      added a "Load more" button per page — verified live end-to-end in a
      browser against the real API with zero console errors
- [x] Tests: 7 new unit tests for the pagination helper — all passing
      under `-race`; full frontend typecheck + production build clean
- [x] Docs (`API.md`, `README.md`, `FRONTEND.md`) updated to match,
      including fixing a stale "no approvals backend" line in `FRONTEND.md`
      left over from before phase 6

**Phase 11** — this pass:
- [x] `web/app/(org)/tools/page.tsx`: tool registry table + an inline
      execute form per tool (resource type/id + JSON parameters) and an
      Approvals table with status filter and Approve/Reject. Verified live:
      `check_ssl` against `github.com` returns genuine certificate data
      rendered in the browser (matching the earlier curl test exactly);
      `deploy_application` correctly creates a pending approval instead of
      running, approving it flows through to the real Tool Gateway
- [x] `web/app/(org)/access/page.tsx`: the caller's own API tokens
      (create/revoke), service accounts (create/disable, issue a token
      owned by one), and an org-wide token listing that only renders when
      `GET /organization/api-tokens` doesn't come back `FORBIDDEN` — a
      member lacking `organization.manage` sees no section, not an error.
      Verified live end-to-end: created a service account, issued it a
      token, saw the raw-token-shown-once banner, and saw the new token
      immediately appear in the org-wide listing with correct owner
      attribution
- [x] Both pages added to the sidebar nav
- [x] Found and fixed a real bug during live verification (not caught by
      `tsc`/`next build`): both pages used the `<>...</>` Fragment
      shorthand for a `.map()` returning multiple elements per item, which
      can't carry a `key` — React's actual "missing key" console warning
      only surfaces at runtime. Switched to `<Fragment key={...}>`
- [x] Full frontend typecheck + production build clean; backend test suite
      unaffected (no Go changes this pass)
- [x] Docs (`FRONTEND.md`, `README.md`) updated to match

## Next up

1. **Concrete job types**: the worker dispatcher is real but nothing
   enqueues a `backup.create` or `deploy.application` job yet — those
   arrive with the backups/deployment domains.
2. **More tool handlers**: `get_container_logs` needs a container domain
   that doesn't exist yet; `create_backup`/`verify_backup` need the jobs
   system wired to an actual backup mechanism.
3. **Rate limiting beyond `/auth/login`, `/auth/signup`, and `/ai/chat`**,
   and a Redis-backed limiter for multi-instance deployments.
4. **Generate the OpenAPI spec from code** instead of hand-maintaining it,
   and swap `web/`'s hand-written `lib/types.ts` over to the generated
   `lib/api-types.generated.ts`.
5. **A cloud AI provider adapter** (OpenAI or Anthropic) now that the
   provider/adapter separation pattern is proven with Ollama — will need
   the secrets module for credential storage.
6. **Agent execution loop**: read `agents.system_instructions`, drive the
   AI gateway, enforce `allowed_tool_keys` when an agent (not a human) is
   the caller of `tools.Execute`.
7. **Per-tool/per-org-configurable approval TTL** (today's
   `defaultApprovalTTL` is a single global 24h constant).
8. **Remaining frontend follow-ups**: AI profile/chat UI, provider/model
   registry UI, RBAC/settings management UI, real-time updates (polling or
   websockets) instead of load-once pages.

## Explicitly not started (rule 38 — deferred by design)

Kubernetes support, multi-region orchestration, GPU auto-provisioning,
ML-based AI routing, model fine-tuning, unrestricted autonomous agents,
complete CyberAudit/Web Content Flow/Kiko functionality.
