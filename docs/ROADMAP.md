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

**Phase 12** — this pass:
- [x] `internal/ai/providers/anthropic`: a real adapter for the Anthropic
      Messages API — the first **cloud** provider (Ollama was local),
      proving the router's privacy-policy enforcement against an actual
      registered cloud adapter rather than a hypothetical one
- [x] Handles a real structural difference from Ollama: Anthropic takes the
      system prompt as a separate top-level field, not a `"system"`-role
      message — `Chat` extracts and joins any such messages before sending,
      rather than passing them through and having the API reject the call
- [x] `NODERA_ANTHROPIC_API_KEY` (optional; unset → adapter not registered,
      `UNAVAILABLE` rather than a crash, same pattern as
      `NODERA_OLLAMA_BASE_URL`) — but this one is a genuine secret, unlike
      Ollama's base URL, so `docs/AI_ARCHITECTURE.md` documents *why* it's
      an env var rather than `internal/secrets`: the registry is
      platform-wide, secrets are org-scoped, and that mismatch isn't
      resolved yet (tracked below, not silently worked around)
- [x] Tests: 6 new unit tests for the adapter (success — including
      asserting the system-message extraction actually happened on the
      wire, multi-system-message joining, default max_tokens, server
      error, malformed response, context cancellation) plus 2 new
      integration tests (full pipeline through a mock server; a
      RESTRICTED profile refusing to route to the now-real, now-registered
      cloud adapter) — all passing under `-race`
- [x] Verified live that the optional-config fail-closed path still works
      with no live Anthropic account available to test against: booted
      the server with `NODERA_ANTHROPIC_API_KEY` unset, confirmed no
      registration log line, confirmed `/health`/`/ready` unaffected —
      did not fabricate a live end-to-end AI response, since no real
      credential was available (rule 36)
- [x] Docs (`AI_ARCHITECTURE.md`, `README.md`, `.env.example`) updated

**Phase 13** — this pass:
- [x] `internal/agents`: agent identity + scoped execution, resolving the
      roadmap's "Agent execution loop" item against rule 38's prohibition
      on unrestricted autonomous agents — the LLM never picks its own
      tool; a caller always directs a specific `ExecuteTool` call. The
      agent's role is a bounded, pre-configured identity (its own
      `permission_scope` and `allowed_tool_keys`) that the call runs under
- [x] Migration `0012`: new `agents.manage` permission (seeded to
      owner/admin), distinct from the existing `agents.execute` —
      reusing `agents.execute` for both would let anyone who can run an
      agent also redefine what it's allowed to do
- [x] `authctx.ActorAgent`: a third actor type alongside user/service
      account, so an agent's own audit entries and permission checks are
      attributable to the agent, not the invoking caller
- [x] `CreateAgent` enforces no-privilege-escalation (`permission_scope`
      must be a subset of the caller's own held permissions, same rule as
      API token scopes) and validates every `allowed_tool_keys` entry
      against the real `tools` table
- [x] `Run(ctx, ac, id, message)`: builds the agent's own scoped
      `AuthContext` (`agentAuthContext`), prepends `system_instructions`
      as a system message, and calls the existing AI gateway under that
      scope — the agent's own scope, not the caller's `agents.execute`,
      must include `ai.use`
- [x] `ExecuteTool(ctx, ac, id, toolKey, input)`: checks `allowed_tool_keys`
      first, then reuses the existing Tool Gateway pipeline unchanged
      (`tools.Registry.Execute`) under the agent's scope, so permission/
      risk-tier/approval logic all apply correctly
- [x] 6 new integration tests, all passing under `-race`: creator-permission
      ceiling, unknown-tool-key rejection, full create→enable→run lifecycle
      (disabled agent can't run, enabled one gets a real `local-echo`
      response), agent-scope-missing-`ai.use` rejection, allowlist
      enforcement (a disallowed tool never reaches the Tool Gateway; an
      allowed-but-unimplemented tool correctly reports `NOT_IMPLEMENTED`,
      proving the call *did* reach the real pipeline), disabled-agent
      tool-execution rejection
- [x] Fixed a test-premise bug found during this work: the allowlist test
      originally used `check_ssl` as the "allowed but unimplemented" case,
      but `check_ssl.implemented=true` in the seed data (since Phase 7) —
      the registry correctly returned a more specific `INTERNAL_ERROR`
      ("marked implemented but has no registered handler in this
      process") instead of `NOT_IMPLEMENTED`. Switched the test to
      `get_container_logs`, which is genuinely `implemented=false`
- [x] 6 new HTTP routes wired in `cmd/server/router.go`
      (`GET/POST /agents`, `GET /agents/{id}`, `POST /agents/{id}/enable`,
      `POST /agents/{id}/disable`, `POST /agents/{id}/run`,
      `POST /agents/{id}/tools/{key}/execute`) and verified live: created
      an agent, ran it for a real (local-echo) chat response, executed a
      tool through it, and confirmed via `GET /api/v1/audit` that the
      management actions are attributed to the human caller while the
      tool-execution audit entry is attributed to the agent's own actor
      label — proving the dual-identity design works end to end, not just
      in unit tests
- [x] OpenAPI spec: `agents` tag, 6 new paths, `Agent`/`CreateAgentInput`
      schemas — validated with `@redocly/cli lint` (structurally valid;
      warning count rose proportionally to the new operations, same
      non-blocking style class as before) — and `web/lib/api-types.generated.ts`
      regenerated from it (not yet swapped in for the hand-written
      `lib/types.ts`, still tracked below)
- [x] Docs (`AGENTS.md`, `API.md`, `README.md`) updated to match

**Phase 14** — this pass:
- [x] `internal/platform/ratelimit.RedisLimiter`: a Redis-backed fixed-
      window limiter implementing the same `Allower` interface as the
      existing in-process `Limiter`, so `cmd/server` picks one at startup
      without the rest of the codebase (router, handlers) caring which —
      resolves the "multi-instance-safe limiter" half of this roadmap item
- [x] `cmd/server/main.go: newRateLimiters` — connects to Redis and
      verifies it with a startup `PING` when `NODERA_REDIS_URL` is set (a
      misconfigured URL fails startup loudly rather than silently falling
      back to per-instance limits); falls back to the in-process `Limiter`
      when unset, same behavior as before this change
- [x] Deliberate fail-open policy on a Redis error mid-request (logged,
      not silent) — a brief Redis outage degrades rate limiting rather
      than taking down login/signup/AI chat for every legitimate user;
      documented in `docs/SECURITY.md` and in code
- [x] 4 new integration tests against a real local Redis (`-race` clean):
      limit enforcement, key independence, window expiry, and — the actual
      point of this type over the in-process one — two independent Redis
      clients (standing in for two API process instances) sharing one
      counter, proving the "shared across instances" property directly
      rather than only asserting it in a comment
- [x] Verified live: booted the server with `NODERA_REDIS_URL` unset
      (in-process fallback, confirmed by the startup log line) and again
      with it set (confirmed the Redis-backed log line), tripped the real
      `/auth/signup` limiter over HTTP and confirmed the counter key
      (`ratelimit:signup:<ip>`) existed in Redis with the request that
      tripped it returning `429 RATE_LIMITED`
- [x] CI: added a `redis:7-alpine` service container and
      `NODERA_TEST_REDIS_URL` so the new tests run in the same pipeline as
      everything else, not just locally
- [x] `govulncheck` clean on the new `github.com/redis/go-redis/v9`
      dependency
- [x] Docs (`SECURITY.md`, `DEPLOYMENT.md`, `README.md`, `.env.example`)
      updated; also corrected two already-stale bullets in `SECURITY.md`'s
      "not yet implemented" list left over from earlier phases (signup
      rate limiting and CI vulnerability scanning were both already done,
      just not removed from that list at the time)

**Phase 15** — this pass:
- [x] Per-tool/per-org-configurable approval TTL, resolving the last
      remaining roadmap bullet from the original approvals-workflow phase.
      Migration `0013`: `organization_tool_settings` table (one row per
      configured `(organization_id, tool_key)` pair) and a new
      `tools.manage` permission (seeded to owner/admin), kept distinct
      from `approvals.decide` and every `tools.*` execution permission —
      configuring how long a tool's approvals stay open is an org-admin
      decision, not something every approver should be able to change
- [x] `internal/tools`: `SetApprovalTTL`/`ClearApprovalTTL`/
      `ListApprovalTTLOverrides`, bounded to `[5m, 30d]`
      (`minApprovalTTL`/`maxApprovalTTL`) — below 5 minutes a human
      realistically can't react in time; above 30 days a stale pending
      approval outlives the context behind the original request.
      `createApproval` resolves the effective TTL per call
      (`resolveApprovalTTL`: override if one exists, else the unchanged
      24h `defaultApprovalTTL`) — changing an override only affects
      approvals created afterward, never retroactively
- [x] 4 new integration tests (`-race` clean): default TTL with no
      override, an override actually changing a new approval's
      `expires_at` (and clearing it reverting to the default), out-of-range
      and unknown-tool-key rejection, and `tools.manage` permission
      enforcement against a plain member
- [x] 3 new HTTP routes (`GET /tools/approval-ttl`,
      `PUT`/`DELETE /tools/{key}/approval-ttl`) and OpenAPI additions
      (`OrganizationToolSetting` schema, validated with `@redocly/cli
      lint`); `web/lib/api-types.generated.ts` regenerated
- [x] Verified live end-to-end through the real HTTP API: set a 10-minute
      override, executed `restart_container`, confirmed the resulting
      approval's `expires_at` was exactly 10 minutes after `created_at`
      (not the 24h default); cleared the override and confirmed the next
      approval reverted to 24h; confirmed both the out-of-range-TTL and
      unknown-tool-key validation paths return `400`/`404` over real HTTP
- [x] Docs (`AGENTS.md`, `API.md`, `README.md`) updated to match

**Phase 16** — this pass:
- [x] `web/app/(org)/agents/page.tsx`: an agents management UI — list agent
      definitions, create one (name, AI profile key, system instructions,
      `allowed_tool_keys`/`permission_scope` as comma-separated inputs),
      enable/disable, and — once active — Run it (scoped chat) or Execute
      a tool through it (same resource type/id + JSON parameters shape as
      the Tools page, restricted to a `<select>` of that agent's own
      `allowed_tool_keys`). A new agent starts disabled, matching the API;
      Run/Execute are disabled in the UI rather than left to fail
      server-side. Added to the sidebar nav
- [x] `lib/types.ts`: `Agent` and `ChatResult` types, matching
      `internal/agents.Agent`/`internal/ai.ChatResult`'s real JSON tags
      (caught and fixed a first-draft mismatch: `preferred_model_ids` vs.
      a guessed `preferred_model_refs` field name, found live rather than
      assumed correct)
- [x] Verified live end to end: created an agent scoped to only `ai.use` +
      `tools.read`, confirmed executing `check_ssl` through it correctly
      failed with `FORBIDDEN` (`missing required permission:
      infrastructure.read`) — the agent's own scope, not the caller's,
      gates the call; created a second agent whose scope also included
      `infrastructure.read` and confirmed `check_ssl` against `github.com`
      returned genuine certificate data through it; confirmed `Run`
      against the `local-echo` provider returns real `echo: <message>`
      content. Checked the browser console on a fresh tab afterward —
      zero errors
- [x] Full frontend typecheck + production build clean; backend
      unaffected (no Go changes this pass, sanity-checked anyway)
- [x] Docs (`FRONTEND.md`, `README.md`) updated to match

**Phase 17** — this pass:
- [x] `web/app/(org)/ai/page.tsx`: an AI Gateway UI covering the whole
      surface — a Chat panel (pick a profile, send a message, see the real
      `ChatResult` including provider key, model, and token counts), a
      Profiles section (list + create), and Providers/Models sections
      (list + register, `ai.manage`-gated server-side). Added to the
      sidebar nav
- [x] `lib/types.ts`: `AIProvider`, `AIModel`, `ChatMessage` types matching
      `internal/ai`'s real JSON tags (`AIProfile` already existed and was
      already correct)
- [x] Caught a real bug during live verification, same class as Phase 16's:
      the first draft of the Profiles create form posted
      `preferred_model_refs`, but the API's actual field is
      `preferred_model_ids` — found by testing against the running API,
      fixed before commit
- [x] Verified live end to end: created a profile, sent a chat message
      through `local-echo` and got a real `echo: <message>` response with
      real token counts, registered a new model (`ollama/llama3.1`) and
      saw it appear in the table immediately. Checked the browser console
      on a fresh tab afterward — zero errors
- [x] Full frontend typecheck + production build clean; backend
      unaffected (no Go changes this pass, sanity-checked anyway)
- [x] Docs (`FRONTEND.md`, `README.md`) updated to match

**Phase 18** — this pass:
- [x] A tool-approval-TTL settings UI, added directly to
      `web/app/(org)/tools/page.tsx` rather than a separate page — an
      "Approval TTL" column, populated only for `privileged`/`critical`
      tools (a `read`/`safe` tool never has an approval, so it always
      shows `—`), showing the effective TTL (an org override, or
      `24h (default)`) with an inline "Edit" control to set or clear the
      org's override
- [x] `lib/types.ts`: `OrganizationToolSetting` type matching
      `internal/tools`'s real JSON tags
- [x] Verified live end to end: set `restart_container` to a 15-minute
      override through the UI, executed it, and confirmed via the real API
      that the resulting approval's `expires_at` was exactly 15 minutes
      after `created_at` (not the 24h default); cleared the override
      through the UI and confirmed the column reverted to `1d (default)`.
      Checked the browser console on a fresh tab afterward — zero errors
- [x] Full frontend typecheck + production build clean; backend
      unaffected (no Go changes this pass, sanity-checked anyway)
- [x] Docs (`FRONTEND.md`) updated; removed the now-stale
      "tool-approval-TTL settings UI" bullet from its not-built-yet list

## Next up

1. **Concrete job types**: the worker dispatcher is real but nothing
   enqueues a `backup.create` or `deploy.application` job yet — those
   arrive with the backups/deployment domains.
2. **More tool handlers**: `get_container_logs` needs a container domain
   that doesn't exist yet; `create_backup`/`verify_backup` need the jobs
   system wired to an actual backup mechanism.
3. **Rate limiting on endpoints beyond `/auth/login`, `/auth/signup`, and
   `/ai/chat`** — every other endpoint remains unlimited (the Redis-backed
   multi-instance limiter itself is now done, see Phase 14).
4. **Generate the OpenAPI spec from code** instead of hand-maintaining it,
   and swap `web/`'s hand-written `lib/types.ts` over to the generated
   `lib/api-types.generated.ts`.
5. **A "platform secrets" mechanism** for cloud provider credentials
   (`docs/AI_ARCHITECTURE.md` Credential handling), or a further cloud
   adapter (OpenAI) if that mismatch is deferred again.
6. **Remaining frontend follow-ups**: RBAC/settings management UI,
   real-time updates (polling or websockets) instead of load-once pages.

## Explicitly not started (rule 38 — deferred by design)

Kubernetes support, multi-region orchestration, GPU auto-provisioning,
ML-based AI routing, model fine-tuning, unrestricted autonomous agents,
complete CyberAudit/Web Content Flow/Kiko functionality.
