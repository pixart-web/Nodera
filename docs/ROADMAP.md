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

**Phase 19** — this pass:
- [x] RBAC/settings management: new backend surface in `internal/rbac`
      (`ListRoles`, `ListMembers`, `AssignRole`, `RevokeRole`), all gated by
      `organization.manage`, plus 4 new HTTP routes (`GET /roles`,
      `GET /organization/members`, `POST`/`DELETE
      /organization/members/{userID}/roles[/{roleID}]`) and
      `web/app/(org)/settings/page.tsx` to drive them — a Roles table
      (name, description, permission set) and a Members table showing each
      member's current roles as removable badges, with an "Assign role"
      control offering only roles they don't already hold
- [x] `rbac.New` now takes an `AuditRecorder` (role assign/revoke are
      audited — `rbac.role.assigned`/`rbac.role.revoked`), so every call
      site (`cmd/server/main.go`, `harness_test.go`,
      `internal/integration_test.go`) was reordered to construct `audit`
      before `rbac`
- [x] `AssignRole` validates both the target user is actually a member of
      the calling org and the role is actually available to it (system
      role or that org's own) before writing anything — `NOT_FOUND` on
      either, not a silent no-op or a raw FK constraint error
- [x] 5 new integration tests, all passing under `-race`: system roles are
      returned with full permission sets, a plain member is forbidden from
      listing roles (`organization.manage` gate), member listing reflects
      real assigned roles including a freshly-added member with none yet,
      assign+revoke round-trips correctly (assigning an already-held role
      is a no-op; revoking a non-held one is `NOT_FOUND`), and both
      invalid-target cases (non-member user, unknown role) are rejected
- [x] OpenAPI additions (`Role`/`Member`/`MemberRole` schemas, 4 paths
      under the existing `organizations` tag — not a new "organization"
      tag, corrected after an initial mismatch caught by `@redocly/cli
      lint`'s tag-reference check before commit); `lib/api-types.generated.ts`
      regenerated
- [x] Verified live end to end: assigned the `admin` role to a real member
      alongside their existing `owner` role, watched both badges appear;
      revoked `admin`, watched it disappear; separately signed up a second
      real user, added them as a plain `member` via direct DB insert (no
      "add member" endpoint exists yet — see Not yet implemented below),
      logged in as them, and confirmed the Settings page renders two
      graceful `FORBIDDEN` error banners rather than a blank or crashed
      page. Checked the browser console on a fresh tab afterward — zero
      errors
- [x] Full backend (`gofmt`/`vet`/`build`/`test -race`) and frontend
      (`tsc`/`next build`) clean
- [x] Docs (`API.md`, `FRONTEND.md`, `README.md`) updated to match

**Phase 20** — this pass:
- [x] The "add member" gap Phase 19 surfaced (no API path from "user has
      an account" to "user is a member of this org") — `POST
      /api/v1/organization/members` (`internal/tenancy.AddMember`), gated
      by `organization.manage`. Adds an *existing* account to the calling
      org with the system `member` role; never creates an account (that
      stays `identity.SignUp`'s job) and sends no invite email — there's
      no email delivery in this phase at all, so this is "join an
      existing user," not an invite-by-email flow
- [x] `internal/identity.FindByEmail`: new exported lookup, the first
      thing `internal/tenancy` has ever needed from `internal/identity` —
      added as a narrow method on the concrete `*identity.Service` (same
      pattern `internal/agents` already uses for `*ai.Service`/
      `*tools.Registry`, per ADR-002) rather than giving `tenancy` any
      broader access to user records
- [x] `tenancy.New` and `rbac.New` both now take an `AuditRecorder`
      (member-added and role-assign/revoke are all audited), so every
      call site (`cmd/server/main.go`, `harness_test.go`,
      `internal/integration_test.go`) was reordered to construct `audit`
      and `identity` before `tenancy`
- [x] `AddMember` validates the email resolves to a real account
      (`NOT_FOUND` if not) and that they aren't already a member
      (`CONFLICT` if so) before writing anything
- [x] 4 new integration tests, all passing under `-race`: a real add
      grants exactly the `member` role and shows up in `ListMembers`, a
      plain member is forbidden from adding anyone, an unknown email is
      rejected, and adding an already-added member is rejected
- [x] `web/app/(org)/settings/page.tsx`: an "Add member" form (email in)
      above the Members table. OpenAPI additions (`AddedMember` schema, 1
      path) validated with `@redocly/cli lint`; `lib/api-types.generated.ts`
      regenerated
- [x] Verified live end to end: signed up a fresh real account, added it
      to the org through the actual UI form, watched it appear in the
      Members table with the `member` role; resubmitted the same email
      and saw the real `CONFLICT` ("user is already a member of this
      organization") render inline; submitted an email with no account
      and saw the real `NOT_FOUND` ("user not found") render inline.
      Checked the browser console on a fresh tab afterward — zero errors
- [x] Full backend and frontend verification clean
- [x] Docs (`API.md`, `FRONTEND.md`, `README.md`) updated; removed the
      now-fulfilled "add member endpoint" bullet from Next up

**Phase 21** — this pass:
- [x] Custom role creation/editing, resolving the last remaining bullet
      from the original RBAC/settings phase. `internal/rbac.CreateRole`/
      `UpdateRolePermissions`/`DeleteRole`, all `organization.manage`-gated,
      using schema that already existed (`roles.organization_id` for
      org-scoped custom roles) — no migration needed, purely additive Go
      code
- [x] `CreateRole`/`UpdateRolePermissions` enforce the same
      no-privilege-escalation rule as API token scopes and agent
      `permission_scope`: requested permissions must be a subset of the
      caller's own, and must each be a real key in the `permissions`
      catalog — validated in that order (unknown-key check first) after a
      test caught the alternate order producing a misleading `FORBIDDEN`
      for a typo'd permission instead of the more useful
      `VALIDATION_ERROR`
- [x] `getCustomRole` (used by both update and delete) refuses any system
      role or another org's role with the same `NOT_FOUND` a nonexistent
      ID would produce — a system role's fixed permission set can only
      ever change via a migration, never through this path
- [x] `DeleteRole` refuses (`CONFLICT`) a role still held by any member —
      the caller must revoke every assignment first, so deleting a role
      never silently changes what a member can do as a side effect
- [x] 4 new integration tests, all passing under `-race`: create + assign
      a custom role and see it reflected in both `ListRoles` and
      `ListMembers`, both escalation/unknown-key rejections, replacing a
      custom role's permission set while a system role is refused, and
      delete-blocked-while-assigned then succeeding after revoke
- [x] 3 new HTTP routes (`POST /roles`, `PUT /roles/{id}/permissions`,
      `DELETE /roles/{id}`) and OpenAPI additions, validated with
      `@redocly/cli lint`; `lib/api-types.generated.ts` regenerated
- [x] `web/app/(org)/settings/page.tsx`: a "Create role" form above the
      Roles table and a "Delete" control per custom role (system roles
      show `system` instead, never delete)
- [x] Verified live end to end through the real UI: created a custom
      `auditor` role (`audit.read`, `infrastructure.read`), assigned it to
      a real member alongside their existing `member` role, attempted
      delete and saw the real `CONFLICT` render inline, revoked it from
      the member, and confirmed delete then succeeded and the role
      disappeared from the table. Checked the browser console on a fresh
      tab afterward — zero errors
- [x] Full backend and frontend verification clean
- [x] Docs (`API.md`, `FRONTEND.md`, `README.md`) updated; removed the
      now-fulfilled "custom role creation/editing" bullet from Next up

**Phase 22** — this pass:
- [x] `internal/ai/providers/openai`: a second cloud provider adapter, for
      the OpenAI Chat Completions API — proving the Anthropic-established
      pattern (registry row vs. Go adapter are separate; `kind: cloud`
      correctly refused by `restricted`-privacy profiles) generalizes to
      more than one cloud provider, not just Anthropic specifically
- [x] Structurally simpler than the Anthropic adapter: OpenAI accepts a
      `"system"`-role message directly inside the `messages` array, so
      `Chat` passes every message through unchanged — no extraction/join
      step like Anthropic's separate top-level `system` field needs
- [x] `NODERA_OPENAI_API_KEY` (optional; unset → adapter not registered,
      `UNAVAILABLE` rather than a crash — same pattern as
      `NODERA_ANTHROPIC_API_KEY`/`NODERA_OLLAMA_BASE_URL`)
- [x] Tests: 5 new unit tests for the adapter (success, server error,
      malformed response, an empty-`choices` response correctly treated
      as an error rather than a fabricated empty reply, context
      cancellation) plus 2 new integration tests (full pipeline through a
      mock server; a `RESTRICTED` profile refusing to route to the
      now-registered `openai` adapter) — all passing under `-race`
- [x] Verified live that both the optional-config fail-closed path and
      the registration path work: booted with `NODERA_OPENAI_API_KEY`
      unset (no registration log line, `/health`/`/ready` unaffected),
      then booted with a fake (non-live) key set and confirmed the
      `openai` row appeared in `GET /api/v1/ai/providers` with
      `kind: cloud` — did not fabricate a live chat response, since no
      real credential was available (rule 36, same as the Anthropic pass)
- [x] `govulncheck` clean
- [x] Docs (`AI_ARCHITECTURE.md`, `README.md`, `.env.example`) updated

**Phase 23** — this pass:
- [x] Rate limiting extended to `POST /api/v1/organizations` (10/hour per
      user) — the only remaining mutation reachable by any freshly-signed-up
      user with no permission gate at all (every other unlimited endpoint
      is already `organization.manage`-gated, which meaningfully narrows
      who can even attempt abuse — see `docs/SECURITY.md`)
- [x] Keyed by the calling user's ID, not IP — unlike login/signup, this
      endpoint is only reachable once authenticated, so the actor is
      already known and stable; an IP key would be both weaker (shared
      IPs behind NAT) and unnecessary (no pre-auth anonymity to account
      for, unlike signup)
- [x] `newRateLimiters` extended to a 4th limiter, following the exact
      same in-process/Redis-backed selection as the other three; no new
      pattern introduced
- [x] Verified live end to end against a real Redis-backed limiter: signed
      up a fresh user, created 10 organizations successfully, confirmed
      the 11th returned a real `429 RATE_LIMITED`, and confirmed the
      counter key (`ratelimit:create_org:<user_id>`) existed in Redis
- [x] Full backend verification clean (no router-level Go test added,
      matching the existing pattern — login/signup rate limiting also has
      no router-level test, only the underlying `Limiter`/`RedisLimiter`
      behavior is unit-tested; this endpoint's limiting was verified live
      instead, same as those)
- [x] Docs (`SECURITY.md`, `API.md`, `README.md`) updated; also fixed a
      stale claim in `API.md` left over from an early phase (it said the
      agent execution loop and cloud AI provider adapters had "no HTTP
      surface yet" — both have had one for many phases)

**Phase 24** — this pass:
- [x] Renaming/editing a custom role's name or description, resolving the
      last remaining bullet from the RBAC/custom-roles work.
      `internal/rbac.UpdateRoleDetails` — name/description only, the
      permission set is untouched (that stays `UpdateRolePermissions`'s
      job, a deliberately separate call so a details-only edit can never
      accidentally change what a role grants)
- [x] Reuses `getCustomRole` (refuses any system role or another org's
      role with `NOT_FOUND`) and a new `loadRolePermissions` helper to
      fill in the response's `Permissions` field after the `UPDATE`,
      since that statement only touches `name`/`description`
- [x] 1 new integration test, passing under `-race`: a details update
      changes name/description while leaving the permission set intact,
      and renaming a system role is refused with `NOT_FOUND`
- [x] 1 new HTTP route (`PUT /roles/{id}`, distinct from the existing
      `PUT /roles/{id}/permissions`) and an OpenAPI addition, validated
      with `@redocly/cli lint`; `lib/api-types.generated.ts` regenerated
- [x] `web/app/(org)/settings/page.tsx`: an "Edit" control per custom role
      (alongside "Delete") opening an inline form pre-filled with the
      role's current name/description
- [x] Verified live end to end through the real UI: created a custom
      role, edited its name and description through the form, confirmed
      the table updated and its permission set (`audit.read`) was
      unaffected by the details-only edit, then deleted it. Checked the
      browser console on a fresh tab afterward — zero errors
- [x] Full backend and frontend verification clean
- [x] Docs (`API.md`, `FRONTEND.md`) updated; removed the now-fulfilled
      bullet from Next up

**Phase 25** — this pass:
- [x] Real-time updates via polling, for the two pages whose data changes
      independently of anything the viewer does on that page.
      `lib/useApi.ts` takes an optional `{ pollMs }`: on that interval it
      re-fetches in the background, updating `data` on success and doing
      nothing on a failed tick (a transient hiccup degrades to
      briefly-stale data, not a flashing spinner or a blanked page) —
      `reload()`, still called by every mutating action, keeps its
      original behavior of showing the loading state and surfacing real
      errors; only the background tick is silent
- [x] Applied to `jobs/page.tsx` (5s — job status changes as the worker
      processes it) and the Approvals section of `tools/page.tsx` (7s — a
      pending approval can be created, expire, or be decided by someone
      else entirely). Each page's own copy says "Refreshes automatically
      every Ns" rather than silently refreshing with no indication
      anything is happening
- [x] Verified live: enqueued a job via a direct API call (not through
      the page) and watched it appear with a real `failed` status
      (`no handler registered for job type` — the genuine worker outcome,
      not fabricated) once the worker picked it up, with no reload or
      navigation; separately, triggered a `restart_container` approval
      via a direct API call and watched it appear in the pending list,
      again with no reload. Checked the browser console on a fresh tab
      afterward — zero errors
- [x] Full frontend typecheck + production build clean; backend
      unaffected (no Go changes this pass, sanity-checked anyway)
- [x] Docs (`FRONTEND.md`, `README.md`) updated; removed the now-fulfilled
      "real-time updates" bullet from Next up

**Phase 26** — this pass:
- [x] Self-service account management, found by auditing `internal/identity`
      for gaps rather than from a pre-listed Next-up item: there was no
      way for a user to change their own password or display name after
      signup at all. `UpdateProfile` (display name only — email isn't
      editable here, since changing it would need re-verification email
      delivery that doesn't exist in this phase) and `ChangePassword`
      (current password required, rotates the hash, revokes every other
      active session)
- [x] Both take a raw `userID` rather than an `authctx.AuthContext` —
      matching `CreateOrganization`'s existing pattern, since these are
      pre-organization identity operations: a session token alone
      resolves a user ID; the organization and permissions aren't known
      yet at this point in the request pipeline
      (`cmd/server/middleware.go`: `requireSession` vs.
      `requireOrganization`)
- [x] `ChangePassword` resolves the calling session's own ID (from the
      raw token, empty for an API-token caller) so it can be excluded
      from the mass session-revoke — changing your password doesn't log
      you out of the session that made the request, only every other one
- [x] 5 new integration tests, all passing under `-race`: profile update
      changes the display name (email unaffected), an empty display name
      is rejected, an incorrect current password is rejected, changing
      the password revokes every other session but leaves the current
      one valid, and the new password actually works for a future login
      while the old one no longer does
- [x] 2 new HTTP routes (`PUT /account/profile`, `POST
      /account/password`, deliberately outside any organization-scoped
      route group) and OpenAPI additions, validated with `@redocly/cli
      lint`; `lib/api-types.generated.ts` regenerated
- [x] `web/app/(org)/account/page.tsx`: a Profile form and a Change
      Password form, linked from the sidebar footer (the user's own
      email is now a link to this page). On a successful profile save,
      the new display name is written back to `lib/session.ts`'s stored
      user so the sidebar reflects it without a full reload
- [x] Verified live end to end through the real UI: renamed the display
      name and confirmed it persisted across a reload; submitted the
      wrong current password and saw the real `current password is
      incorrect` error; changed the password with the correct one, saw
      the real success message, and confirmed by navigating to another
      page that the current session was genuinely still valid (not just
      claimed to be) — then reset the password back to the original test
      value so later manual verification sessions keep working. Checked
      the browser console on a fresh tab afterward — zero errors
- [x] Full backend and frontend verification clean
- [x] Docs (`API.md`, `FRONTEND.md`, `SECURITY.md`, `README.md`) updated

**Phase 27** — this pass:
- [x] Self-service session management — "log out other devices," a
      natural follow-on from Phase 26's `ChangePassword` (which already
      had to solve "resolve the current session's own ID to exclude it
      from a mass revoke"; this generalizes the same underlying data to a
      user-facing list). `internal/identity.ListSessions` (every active
      session for an account, most recent first, with `IsCurrent` marking
      the one behind the request) and `RevokeSession` (revoke one by ID,
      scoped to the calling user so nobody can revoke another user's
      session by guessing/enumerating an ID)
- [x] No new columns needed — `sessions` already had `id`, `created_at`,
      `expires_at`, `ip_address`, `user_agent` from the original identity
      migration; this phase only added read/revoke access to data that
      already existed
- [x] 2 new integration tests, passing under `-race`: listing correctly
      marks exactly the session that made the request as current (not by
      list-ordering coincidence — resolved from the actual token), and
      revoking a session only ever affects that session and only for its
      owner (a second user attempting to revoke the first user's session
      by ID gets `NOT_FOUND`, not silently ignored or, worse, successful)
- [x] 2 new HTTP routes (`GET /account/sessions`,
      `DELETE /account/sessions/{id}`) and OpenAPI additions (`Session`
      schema), validated with `@redocly/cli lint`;
      `lib/api-types.generated.ts` regenerated
- [x] `web/app/(org)/account/page.tsx`: a third "Active sessions" section
      — a table with device/IP, created/expires, and a "this session"
      badge on the current one (which gets no "Log out" button; the
      sidebar's own "Sign out" already covers that case directly)
- [x] Verified live end to end: logged in a second time via a direct API
      call (simulating another device) without touching the page,
      reloaded, and saw both sessions listed with exactly one correctly
      marked "this session"; clicked "Log out" on the other and watched
      it disappear from the table. Checked the browser console on a
      fresh tab afterward — zero errors
- [x] Full backend and frontend verification clean
- [x] Docs (`API.md`, `FRONTEND.md`, `SECURITY.md`, `README.md`) updated

**Phase 28** — this pass:
- [x] Node update/status-report/decommission — found by auditing
      `internal/infrastructure` for gaps: nodes could only ever be
      registered and read, never edited, never reported as online/offline,
      never retired. Migration `0014` adds a terminal `decommissioned`
      value to `nodes.status`'s CHECK constraint (the only schema change
      needed — every other field this phase touches already existed)
- [x] `UpdateNode` edits a node's editable inventory (hostname, role,
      environment, operating_system, cpu/memory/storage, capabilities) —
      everything except identity fields fixed at registration (`provider`,
      `provider_resource_id`) and `status`, which is reported separately.
      Each field is a pointer (nil = leave unchanged) except
      `Capabilities`, whose zero value can't distinguish "no change" from
      "clear it" for a slice — callers pass an explicit empty slice to
      clear it
- [x] `UpdateNodeStatus` is what a future Node Agent heartbeat would call
      (`docs/INFRASTRUCTURE.md`; no such agent exists yet, so this is only
      reachable via a direct API call today, not an automatic process) —
      sets `status` and stamps `last_seen_at` in the same call, since a
      status report is itself evidence the node was just reachable
- [x] `DecommissionNode` is a terminal, one-way action — the row is kept,
      not deleted (both existing FKs, `applications.node_id` and
      `ai_models.node_id`, use `ON DELETE SET NULL`, so a hard delete
      would only null those references and silently discard "this
      application used to run on that node" history). Idempotent to call
      again; every other mutation on a decommissioned node is refused
      with `CONFLICT`
- [x] 5 new integration tests, all passing under `-race`: a partial
      update touches only the provided fields, an empty hostname is
      rejected, a status report stamps `last_seen_at`, an unrecognized
      status value is rejected, and decommissioning is terminal (idempotent
      to repeat, but blocks further updates/status-reports, and the node
      still appears in `List` afterward)
- [x] 3 new HTTP routes (`PUT /infrastructure/nodes/{id}`,
      `POST .../status`, `POST .../decommission`) and OpenAPI additions,
      validated with `@redocly/cli lint`; `lib/api-types.generated.ts`
      regenerated
- [x] `web/app/(org)/infrastructure/page.tsx`: a "Set status…" select and
      a "Decommission" button per row, both hidden once a node is
      decommissioned; `StatusBadge` gained a `decommissioned` color
- [x] Verified live end to end: registered a real node, set its status to
      `online` through the dropdown and watched the badge update to a
      genuine round-tripped value, decommissioned it and watched both
      controls disappear while the row stayed in the table, then
      confirmed via a direct API call that a further status update
      correctly returns a real `409 CONFLICT`. Checked the browser
      console on a fresh tab afterward — zero errors
- [x] Full backend and frontend verification clean, including confirming
      migration `0014` applies cleanly against both the dev database and
      a fresh test database
- [x] Docs (`API.md`, `INFRASTRUCTURE.md`, `FRONTEND.md`, `README.md`)
      updated — `INFRASTRUCTURE.md` also had a stale "nothing updates
      last_seen_at yet" bullet from an earlier phase, corrected here

## Next up

1. **Concrete job types**: the worker dispatcher is real but nothing
   enqueues a `backup.create` or `deploy.application` job yet — those
   arrive with the backups/deployment domains.
2. **More tool handlers**: `get_container_logs` needs a container domain
   that doesn't exist yet; `create_backup`/`verify_backup` need the jobs
   system wired to an actual backup mechanism.
3. **Generate the OpenAPI spec from code** instead of hand-maintaining it,
   and swap `web/`'s hand-written `lib/types.ts` over to the generated
   `lib/api-types.generated.ts`.
4. **A "platform secrets" mechanism** for cloud provider credentials
   (`docs/AI_ARCHITECTURE.md` Credential handling) — the
   org-scoped-secrets-vs-platform-wide-provider mismatch is unaffected by
   having two cloud adapters now instead of one.
5. **Actual invite-by-email** (as opposed to Phase 20's "add an existing
   account") — needs outbound email delivery, which doesn't exist in this
   phase at all.
6. **Rate limiting on further endpoints** beyond the four covered now, if
   a concrete abuse case surfaces (most mutations remain unlimited but are
   `organization.manage`-gated, which is a meaningfully different risk
   profile than the four already covered).
7. **Real-time updates via websockets/SSE**, if polling's ~5-7s latency
   ever proves insufficient — polling now covers the two pages where it
   mattered most; every other page still loads once.
8. **Polling for more pages** if a concrete need surfaces (e.g. the
   Agents page while a `Run`/`ExecuteTool` call an agent makes is
   in flight) — not added speculatively.

## Explicitly not started (rule 38 — deferred by design)

Kubernetes support, multi-region orchestration, GPU auto-provisioning,
ML-based AI routing, model fine-tuning, unrestricted autonomous agents,
complete CyberAudit/Web Content Flow/Kiko functionality.
