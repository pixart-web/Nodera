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

**Phase 29** — this pass:
- [x] The same update/status-report/deregister trio Phase 28 added for
      nodes, mirrored onto `internal/applications` — its own package doc
      already said it deliberately mirrors `internal/infrastructure`'s
      shape, so this closes the gap between the two rather than leaving
      one domain ahead of the other. Migration `0015` adds a terminal
      `deregistered` value to `applications.status`'s CHECK constraint,
      the same pattern as `0014`
- [x] `Update` edits an application's editable fields (name, kind,
      node_id, environment, repository_url) — everything except `status`,
      reported separately. `node_id` needed one extra wrinkle nodes'
      `UpdateNode` didn't: it's already nullable (an app need not be
      pinned to a node), so a nil `*uuid.UUID` means "don't touch it" but
      an explicit pointer to `uuid.Nil` means "clear it" — a real
      three-state distinction a plain nullable field can't express in one
      pointer alone
- [x] `UpdateStatus` and `Deregister` are otherwise exact analogues of
      `UpdateNodeStatus`/`DecommissionNode` — same terminal-state
      `CONFLICT` guard, same idempotent-decommission behavior, same
      row-kept-not-deleted rationale (no FK cascade risk here either)
- [x] 5 new integration tests, all passing under `-race`, covering the
      same shapes as Phase 28's plus the extra node_id-clearing case
- [x] 3 new HTTP routes (`PUT /applications/{id}`, `POST .../status`,
      `POST .../deregister`) and OpenAPI additions, validated with
      `@redocly/cli lint`; `lib/api-types.generated.ts` regenerated
- [x] `web/app/(org)/applications/page.tsx`: the same "Set status…" select
      and "Deregister" button per row as Infrastructure's equivalent
      controls; `StatusBadge` gained a matching `deregistered` color
- [x] Verified live end to end: registered a real application, set its
      status to `running` through the dropdown and watched a genuine
      round-tripped green badge, deregistered it and watched both
      controls disappear while the row stayed in the table, then
      confirmed via a direct API call that a further status update
      correctly returns a real `409 CONFLICT`. Checked the browser
      console on a fresh tab afterward — zero errors
- [x] Full backend and frontend verification clean, including confirming
      migration `0015` applies cleanly against both the dev database and
      a fresh test database
- [x] Docs (`API.md`, `FRONTEND.md`, `README.md`) updated; also updated
      `internal/applications`'s own package doc comment, which had gone
      stale the moment this phase's methods were added

**Phase 30** — this pass:
- [x] `jobs.Retry` — found by auditing `internal/jobs` for gaps: a job
      that exhausts `max_attempts` reaches the terminal `failed` state
      with no way back except manually enqueuing a brand-new job with the
      same payload, losing the history linkage to the original request.
      `Retry` resets `attempts`/`progress`/`error`/`result`/
      `started_at`/`finished_at` and returns the *same row* (same ID) to
      `queued`, where the real worker claims it again on its normal poll
      — not a new job, so `idempotency_key` and any external reference to
      the original job ID stay valid
- [x] Only a `failed` job can be retried — `queued`/`running` hasn't
      finished failing yet, and `succeeded`/`cancelled` weren't failures
      to recover from. Mirrors `Cancel`'s existing convention of
      collapsing "wrong state" and "doesn't exist" into one `CONFLICT`
      message rather than leaking which case applies
- [x] 1 new integration test, passing under `-race`, that goes further
      than a status-flag check: enqueues a real job with no handler
      registered (genuinely fails, same mechanism as the existing
      unregistered-handler test), confirms `Cancel` is correctly refused
      post-failure, retries it, confirms the reset fields, confirms a
      second `Retry` call is refused (no longer `failed`), then registers
      the handler and lets a second real worker pass pick up the *same*
      job and succeed — proving `Retry` puts the job back somewhere a
      real worker will find it, not just that the row's status column
      changed
- [x] 1 new HTTP route (`POST /jobs/{id}/retry`) and an OpenAPI addition,
      validated with `@redocly/cli lint`; `lib/api-types.generated.ts`
      regenerated
- [x] `web/app/(org)/jobs/page.tsx`: a "Retry" button next to "Cancel,"
      shown only on a `failed` job (the counterpart to "Cancel" appearing
      only on `queued`)
- [x] Verified live using a genuinely failed job left over from an
      earlier phase's polling verification (not a fresh mock): clicked
      Retry, watched it reset to `queued` with `0/1` attempts and no
      error, then — without any reload — watched the existing 5s
      background polling pick up the real worker re-failing it the same
      honest way a few seconds later. Separately confirmed via a direct
      API call that retrying a `queued` job correctly returns a real
      `409 CONFLICT`. Checked the browser console on a fresh tab
      afterward — zero errors
- [x] Full backend and frontend verification clean
- [x] Docs (`API.md`, `FRONTEND.md`, `README.md`) updated

**Phase 31** — this pass:
- [x] `agents.Update`/`agents.Delete` — found by auditing `internal/agents`
      for gaps against the CRUD/lifecycle shape every other domain already
      has: only Create/List/Get/SetStatus existed, no way to edit an
      agent's configuration or remove one once genuinely retired
- [x] `Update` follows the established pointer-based partial-update
      pattern (`*string`/`*int` fields nil = don't touch; `AllowedToolKeys`
      and `PermissionScope` stay plain `[]string`, nil = don't touch,
      explicit `[]` = clear). `PermissionScope` is re-validated against the
      *caller's own currently-held* permissions on every update, not
      grandfathered from the agent's existing scope — the same
      no-privilege-escalation rule Create already enforces, now closed for
      the update path too
- [x] `Delete` is a hard delete, not a status flip — unlike nodes/
      applications (kept for operational history, FKs use
      `ON DELETE SET NULL`), an agent is judged more like an access-scoped
      config object (closer to an API token or role than physical
      infrastructure), and its only FK reference
      (`approvals.requesting_agent_id`) also uses `ON DELETE SET NULL`. It
      requires the agent to already be `disabled` — a real precondition
      check, not just a schema default — returning `CONFLICT` otherwise
- [x] 3 new integration tests, passing under `-race` alongside the 6
      pre-existing agent tests (9 total, 4.238s): partial-update-only-
      touches-provided-fields, a plain member rejected for attempting to
      grant a scope beyond their own permissions, and delete correctly
      refused while active (after an explicit enable, to prove the check
      is real and not just relying on the disabled-by-default schema
      value) then succeeding once disabled
- [x] 2 new HTTP routes (`PUT /agents/{id}`, `DELETE /agents/{id}`) and
      OpenAPI additions, validated with `@redocly/cli lint`;
      `lib/api-types.generated.ts` regenerated
- [x] `web/app/(org)/agents/page.tsx`: an inline "Edit" panel (mirroring
      Settings' `EditRoleForm` pattern) pre-filled with the agent's current
      values, and a "Delete" button that stays disabled (with an
      explanatory tooltip) unless the agent's status is already `disabled`
      — the UI enforces the same precondition the backend does rather than
      just surfacing the resulting error
- [x] Verified live end to end: created a real agent via the UI, edited
      only its description through the real form, reloaded on a fresh tab
      and confirmed the change round-tripped through the real backend
      while the name stayed untouched; enabled the agent and watched
      Delete grey itself out, then confirmed via a direct API call that
      deleting an active agent correctly returns a real `409 CONFLICT`;
      disabled it, deleted it through the real UI, and watched it
      disappear from the list. Checked the browser console on a
      completely fresh tab afterward — zero errors
- [x] Full backend test suite re-run clean (`go test ./... -race`)
- [x] Docs (`API.md`, `FRONTEND.md`, `AGENTS.md`, `README.md`) updated

**Phase 32** — this pass:
- [x] `ai.UpdateProfile`/`ai.DeleteProfile` — found by the same kind of
      CRUD-gap audit as Phase 31, applied to `internal/ai`: only
      Create/List existed for profiles, no way to edit routing/policy
      config after creation or remove a profile that's no longer needed
- [x] `Update` follows the established pointer-based partial-update
      pattern. `key` is deliberately excluded from what can change — it's
      the stable handle agents and callers reference a profile by
      (`agents.ai_profile_key` is free-form text, no FK), and renaming it
      out from under existing references would silently break them with
      no constraint to catch it
- [x] `Update`/`Delete` only reach org-owned profiles
      (`organization_id = caller's org`), never system-defined ones
      (`organization_id IS NULL`) — `ListProfiles`/`Chat` can see and use a
      system-defined profile, but an org's `ai.manage` can't mutate or
      remove it out from under every org. Verified with a profile row
      inserted directly with a NULL organization_id (no such row exists
      via the API today, but the schema supports it and the isolation
      needed a real test, not just an assumption)
- [x] 5 new integration tests, passing under `-race` alongside the 2
      pre-existing profile/chat tests: partial-update-only-touches-
      provided-fields (and confirms `key` never changes), a plain member
      without `ai.manage` rejected from both Update and Delete, delete-
      then-Chat-fails-NotFound (plus a second Delete on the same id also
      failing NotFound), and the system-defined-profile isolation case
      above for both Update and Delete
- [x] 2 new HTTP routes (`PUT /ai/profiles/{id}`, `DELETE
      /ai/profiles/{id}`) and OpenAPI additions, validated with
      `@redocly/cli lint`; `lib/api-types.generated.ts` regenerated
- [x] `web/app/(org)/ai/page.tsx`: an inline "Edit" panel per profile row
      (same expandable-row pattern as the Agents page) with `key` shown as
      a disabled input, and a "Delete" button
- [x] Verified live end to end: created a real profile through the UI,
      edited only its description through the real form, reloaded on a
      fresh tab and confirmed the change round-tripped through the real
      backend while `key` and every other untouched field stayed the
      same, then deleted it through the real UI and watched it disappear
      from both the Profiles table and the Chat panel's profile dropdown
      in the same render. Checked the browser console on a completely
      fresh tab afterward — zero errors
- [x] Full backend test suite re-run clean (`go test ./... -race`)
- [x] Docs (`API.md`, `FRONTEND.md`, `README.md`) updated

**Phase 33** — this pass:
- [x] `identity.EnableServiceAccount`/`UpdateServiceAccount` — found by the
      same CRUD-gap audit style as Phases 31-32, applied to
      `internal/identity/serviceaccount.go`: `DisableServiceAccount`
      existed but was one-way (no path back to `active`), and there was no
      way to rename an account or edit its description after creation
- [x] `EnableServiceAccount` reverses the status flip but deliberately does
      **not** restore any token `Disable` revoked — `Disable`'s own
      guarantee is that a caller never observes a disabled service account
      whose old tokens still authenticate, and that would be worthless if
      `Enable` quietly undid it. A re-enabled account mints fresh tokens
      the same way a newly created one does
- [x] `UpdateServiceAccount` follows the pointer-based partial-update
      pattern for `name`/`description` only — `status` stays Enable/
      Disable's job, since disabling also carries the token-revocation
      side effect a plain field update must never trigger
- [x] 3 new integration tests, passing under `-race` alongside the 4
      pre-existing service-account tests (7 total): enable reverses the
      status flip but the pre-disable token stays dead while a freshly
      minted post-enable token works, update touches only the provided
      field and never changes status, and a `member` without
      `organization.manage` is forbidden from both
- [x] 2 new HTTP routes (`PUT /service-accounts/{id}`, `POST
      /service-accounts/{id}/enable`) and OpenAPI additions, validated
      with `@redocly/cli lint`; `lib/api-types.generated.ts` regenerated
- [x] `web/app/(org)/access/page.tsx`: an inline "Edit" panel per service
      account row (name + description) and an "Enable"/"Disable" pair that
      swaps based on current status, mirroring "Issue token" also only
      showing while active
- [x] Verified live end to end: created a real service account through the
      UI, renamed it through the real edit form, disabled it and watched
      "Issue token" disappear and "Enable" appear, re-enabled it through
      the real UI, and confirmed on a completely fresh tab reload that
      both the rename and the re-enabled `active` status round-tripped
      through the real backend. Checked the browser console on that fresh
      tab — zero errors
- [x] Full backend test suite re-run clean (`go test ./... -race`)
- [x] Docs (`API.md`, `FRONTEND.md`, `README.md`) updated

**Phase 34** — this pass:
- [x] Closed a concrete rate-limiting bypass found during the same
      domain-audit pass as Phases 31-33: `POST /agents/{id}/run` drives
      the exact same `ai.Service.Chat` cost path `POST /ai/chat` does, but
      only `/ai/chat` checked `aiChatRate` — an agent's scoped chat could
      run unlimited AI-gateway calls against an org's budget with no
      throttling at all, unlike calling the gateway directly
- [x] `handleRunAgent` now checks the exact same `aiChatRate` limiter
      instance, keyed by `ac.OrganizationID` the same way `handleAIChat`
      already was — a shared per-organization budget between the two
      endpoints, not a second independent one an agent could exhaust
      separately from `/ai/chat`'s own 60/minute
- [x] No new domain logic, no migration, no OpenAPI schema change beyond
      documenting the existing `429`/`RATE_LIMITED` response on
      `POST /agents/{id}/run` (mirroring how `/ai/chat` already documents
      it); `lib/api-types.generated.ts` regenerated. `cmd/server` has no
      Go test harness for router-level HTTP wiring (rate limiting was
      previously verified live only, same as `/ai/chat`'s original
      rollout), so this was verified the same way, live, against the real
      running server rather than skipped for lack of a unit test
- [x] Verified live end to end: signed up a fresh account, created an org,
      an AI profile, and an agent scoped to `ai.use`, enabled it, then
      fired 65 real HTTP requests at `POST /agents/{id}/run` in a tight
      loop — the first 60 returned `200`, the 61st through 65th returned a
      real `429` with `RATE_LIMITED`. Then, without resetting anything,
      called `POST /ai/chat` for the same organization and confirmed it
      also came back `429` — proving the two endpoints draw from one
      shared budget, not two independent ones that happened to both be
      exhausted
- [x] Full backend test suite re-run clean (`go test ./... -race`)
- [x] Docs (`API.md`, `SECURITY.md`, `AGENTS.md`, `README.md`) updated —
      `SECURITY.md`'s rate-limiting section previously implied
      `agents/{id}/run` was unlimited; corrected

**Phase 35** — this pass:
- [x] `secrets.UpdateDescription` — found by the same CRUD-gap audit style
      as Phases 31-34, applied to `internal/secrets`: `Set` upserts by
      key, but changing only the description meant either resupplying the
      plaintext value (forcing an unintended rotation) or resupplying the
      empty string (which `Set` rejects outright, since a secret's value
      is never optional there)
- [x] `UpdateDescription` never touches the stored ciphertext, never
      re-encrypts anything, and never appears anywhere near the
      plaintext — it's a metadata-only `UPDATE ... SET description`
- [x] 2 new integration tests, passing under `-race` alongside the 3
      pre-existing secrets tests (5 total): update-description leaves the
      revealed value byte-for-byte unchanged (and rejects a nonexistent
      key with `NOT_FOUND`), and a member without `secrets.manage` is
      forbidden
- [x] 1 new HTTP route (`PATCH /secrets/{key}/description`) and an
      OpenAPI addition, validated with `@redocly/cli lint`;
      `lib/api-types.generated.ts` regenerated (`web/lib/api.ts` gained a
      `patch` method alongside its existing get/post/put/del)
- [x] `web/app/(org)/secrets/page.tsx`: an inline "Edit description" panel
      per row that only asks for the description, with copy explaining
      the value is neither shown nor resupplied by this form
- [x] **Caught a real bug during live verification**: the edit form's
      `PATCH` request failed in the browser with a raw `net::ERR_FAILED`
      — the CORS middleware's `Access-Control-Allow-Methods`
      (`internal/platform/httpserver/httpserver.go`) only listed
      `GET, POST, PUT, DELETE, OPTIONS`, so the browser's own preflight
      rejected `PATCH` before the request ever reached the handler.
      `curl` alone wouldn't have surfaced this, since it doesn't enforce
      CORS — this is exactly the class of bug the "verify live in a real
      browser" step in this loop exists to catch. Fixed by adding `PATCH`
      to the allow-list
- [x] Re-verified live end to end after the fix: generated a real
      `NODERA_SECRETS_ENCRYPTION_KEY`, created a real secret through the
      UI, edited only its description through the real form, confirmed
      it round-tripped through the real backend on a fresh tab reload,
      and confirmed via the corresponding integration test that the
      secret's underlying value is provably untouched by this path.
      Checked the browser console on the fresh tab — zero errors
- [x] Full backend test suite re-run clean (`go test ./... -race`)
- [x] Docs (`API.md`, `FRONTEND.md`, `README.md`) updated

**Phase 36** — this pass:
- [x] `tenancy.RemoveMember` — found by a fresh domain audit (the same
      style as Phases 31-35, now applied to `internal/tenancy`,
      `internal/rbac`, and `internal/identity`): `AddMember` existed with
      no counterpart to undo it, and `ListMembers`/`AssignRole`/
      `RevokeRole` in `internal/rbac` are otherwise a complete set —
      rbac's role CRUD and tools' TTL-override CRUD were both already
      fully closed, so this sweep's strongest finding was specifically
      the missing member-removal path
- [x] A single `DELETE FROM organization_members` is sufficient — the
      composite FK on `organization_member_roles` (migration 0002)
      cascades on delete, so removing a member also drops every role
      grant they held without a second query
- [x] Refuses to remove an organization's last remaining holder of the
      system `owner` role (`409 CONFLICT`) — an org with zero owners has
      no one left who can manage it, an unrecoverable state short of a
      database edit. The guard is specifically about the *last* owner:
      removing one of several owners is allowed
- [x] 6 new integration tests, passing under `-race` alongside the 4
      pre-existing tenancy tests (10 total): membership and role rows
      both drop (confirmed by a clean re-add), a plain member is
      forbidden, removing a nonexistent member is `NOT_FOUND`, the
      last-owner guard fires, and removing one of two owners succeeds
- [x] 1 new HTTP route (`DELETE /organization/members/{userID}`) and an
      OpenAPI addition, validated with `@redocly/cli lint`;
      `lib/api-types.generated.ts` regenerated
- [x] `web/app/(org)/settings/page.tsx`: a "Remove" button next to
      "Assign role" on each member row
- [x] Verified live end to end: attempted removing the sole owner of a
      real organization through the UI and watched the real `409
      CONFLICT` ("cannot remove the organization's last owner") render
      inline; added a second real account as a member, removed it
      through the UI, and confirmed it disappeared from the table.
      Checked the browser console on a fresh tab — zero errors
- [x] Full backend test suite re-run clean (`go test ./... -race`)
- [x] Docs (`API.md`, `FRONTEND.md`, `README.md`) updated

**Phase 37** — this pass:
- [x] `tenancy.UpdateOrganization` — the second finding from the Phase 36
      domain audit: `CreateOrganization` validates and inserts name/slug,
      but there was no way to rename an organization or fix a typo'd slug
      short of a database edit, even though both are shown read-only
      throughout the UI (dashboard header, org switcher)
- [x] Pointer-based partial update for `name`/`slug`, reusing
      `CreateOrganization`'s own validation (non-empty name, the same
      slug format regex) so the two paths can never disagree about what a
      valid name/slug looks like; a slug collision with another
      organization surfaces the same `409 CONFLICT` `CreateOrganization`
      already gives for a duplicate slug
- [x] 4 new integration tests, passing under `-race` alongside the 9
      pre-existing tenancy tests (13 total): partial-update-only-touches-
      provided-fields (name then slug, independently), a plain member
      forbidden, an invalid slug rejected, and renaming into another
      organization's existing slug correctly `CONFLICT`s
- [x] 1 new HTTP route (`PUT /organization`) and an OpenAPI addition,
      validated with `@redocly/cli lint`; `lib/api-types.generated.ts`
      regenerated
- [x] `web/app/(org)/settings/page.tsx`: a new "Organization" section at
      the top of the page — the first place in the UI this ever became
      editable rather than read-only
- [x] Verified live end to end: renamed a real organization through the
      form, confirmed the dashboard header and the audit log's
      `tenancy.organization.updated` entry both reflected the change
      immediately, and confirmed via a direct API call that renaming to
      another organization's slug returns a real `409 CONFLICT`. Checked
      the browser console on a fresh tab — zero errors
- [x] Full backend test suite re-run clean (`go test ./... -race`)
- [x] Docs (`API.md`, `FRONTEND.md`, `README.md`) updated

**Phase 38** — this pass:
- [x] `tenancy.LeaveOrganization` — the third Phase 36 audit finding: an
      admin (`organization.manage`) can add and now remove any member,
      but there was no *self-service* way for a member to remove their
      own membership — `AddMember` requires `organization.manage`, and
      RevokeSession/RevokeAPIToken's "act on your own resource" pattern
      had no equivalent here
- [x] Refactored `RemoveMember`'s body into a shared `removeMember(ctx,
      ac, userID, auditAction)` helper both `RemoveMember` and
      `LeaveOrganization` call — same last-owner guard, same cascade-via-
      FK delete, different audit action label (`tenancy.member.removed`
      vs `tenancy.member.left`) so the trail records which path was
      taken. `LeaveOrganization` requires no permission beyond being an
      authenticated member — deliberately not gated by
      `organization.manage`, since a member lacking it must still be able
      to remove *themselves*, just not anyone else
- [x] Subject to the identical last-owner guard as `RemoveMember`: a sole
      owner can't leave any more than they could remove themselves via
      the admin path
- [x] 2 new integration tests, passing under `-race` alongside the 13
      pre-existing tenancy tests (15 total): a plain member successfully
      removes themselves, and the last owner is refused
- [x] 1 new HTTP route (`POST /organization/leave`, in the same
      `requireOrganization`-only group as `GET`/`PUT /organization` —
      deliberately outside the `organization.manage`-gated group the
      member/role routes sit in) and an OpenAPI addition, validated with
      `@redocly/cli lint`; `lib/api-types.generated.ts` regenerated
- [x] `web/app/(org)/settings/page.tsx`: a "Leave this organization"
      control below the Organization form, independent of the
      `organization.manage` gate the Roles/Members sections below it
      need — a plain member sees it even though those sections show
      `FORBIDDEN`. Navigates to `/orgs` on success, same as "Switch
      organization"
- [x] Verified live end to end: as the sole owner, clicked Leave and saw
      the real `409 CONFLICT` render inline; added a second real
      account, logged in as them, confirmed the Organization form and
      Leave control render while Roles/Members correctly show
      `FORBIDDEN`, clicked Leave, and watched a genuine redirect to the
      org picker showing "you don't belong to any organization yet."
      Checked the console on a completely fresh tab afterward — zero
      errors
- [x] Full backend test suite re-run clean (`go test ./... -race`)
- [x] Docs (`API.md`, `FRONTEND.md`, `README.md`) updated

**Phase 39** — this pass:
- [x] `ai.DeleteProvider`/`DeleteModel` — found while widening the CRUD-gap
      audit beyond tenancy (Phases 36-38) to `internal/ai/registry.go`:
      `UpsertProvider`/`UpsertModel` can correct every other field of an
      existing row, but nothing could ever remove one, so a mis-typed key
      or a registration that's never going to be used stays in the
      registry forever
- [x] `DeleteProvider` cascades to every model registered under it
      (`ai_models.provider_id` is `ON DELETE CASCADE`, migration 0006);
      `DeleteModel` removes a single row. Neither touches
      `ai_profiles.preferred_model_ids`/`fallback_model_ids` — those are
      free-form `"provider_key/model_identifier"` text with no FK, so a
      profile referencing a deleted provider/model fails closed at
      resolve time (the router's existing behavior for any unresolvable
      reference) rather than via a cascading delete or a dangling FK
- [x] 5 new integration tests, passing under `-race` alongside the 2
      pre-existing registry tests (7 total): delete-then-relist for both
      a model and a provider (plus a second delete on each correctly
      `NOT_FOUND`), the provider-delete-cascades-to-its-models case, and
      a plain member without `ai.manage` forbidden from both
- [x] 2 new HTTP routes (`DELETE /ai/providers/{key}`, `DELETE
      /ai/providers/{providerKey}/models/{modelIdentifier}`) and OpenAPI
      additions, validated with `@redocly/cli lint`;
      `lib/api-types.generated.ts` regenerated
- [x] `web/app/(org)/ai/page.tsx`: a "Delete" button per row in both the
      Providers and Models tables; deleting a provider also reloads the
      Models list so a cascaded row disappears in the same render rather
      than needing a manual refresh
- [x] Verified live end to end: registered a real test provider and a
      model under it through the UI, deleted the model alone and
      confirmed only it vanished, re-registered a model, then deleted the
      provider and watched both the provider row and its model row
      disappear together in one reload. Checked the browser console on a
      fresh tab afterward — zero errors
- [x] Full backend test suite re-run clean (`go test ./... -race`)
- [x] Docs (`API.md`, `FRONTEND.md`, `README.md`) updated

**Phase 40** — this pass:
- [x] `ai.ListUsage` — the last finding from widening the CRUD-gap audit
      to the AI domain (Phase 39's sibling): every `Chat` call has always
      written a real `ai_usage_records` row via `recordUsage` (tokens,
      latency, status, classification), but there was no method, route,
      or UI to ever read one back — genuine cost/usage blindness despite
      the README already claiming "real usage tracking"
- [x] Tenant-scoped like every other list method (`ai_usage_records` does
      carry `organization_id`, unlike the provider/model registry), most
      recent first, paginated the same `limit`/`offset`/`has_more` way as
      `infrastructure/nodes`/`applications`/`jobs`/`audit` — the fifth
      endpoint to get that envelope
- [x] 3 new integration tests, passing under `-race` alongside every
      other AI test (20 total for the package): a real `Chat` call (both
      the success path and a resolve-failure path, which still records a
      row with empty provider/model and `status: error`) produces exactly
      the rows `ListUsage` then returns, in the right order, with the
      right fields; tenant isolation (org B sees zero of org A's usage);
      and a caller without `ai.use` is forbidden
- [x] 1 new HTTP route (`GET /ai/usage`) and OpenAPI additions (a new
      `AIUsageRecord`/`AIUsageRecordPage` schema pair, the fifth
      paginated endpoint documented in `docs/API.md`'s pagination
      section), validated with `@redocly/cli lint`;
      `lib/api-types.generated.ts` regenerated
- [x] `web/app/(org)/ai/page.tsx`: a new "Usage" section directly below
      Chat, paginated the same "Load more" way the Audit log page is.
      `ChatPanel` gained an `onSent` callback the page wires to
      `usage.reload()`, so sending a message refreshes Usage in the same
      render as the response rather than needing a manual reload
- [x] Verified live end to end: created a real profile, sent two real
      chat messages through it, and watched both appear in Usage
      immediately with the real token counts and `local` classification,
      most recent first; confirmed via a direct API call that the
      paginated envelope is correct. Checked the browser console on a
      fresh tab — zero errors
- [x] Full backend test suite re-run clean (`go test ./... -race`)
- [x] Docs (`API.md`, `FRONTEND.md`, `README.md`) updated

**Phase 41** — this pass:
- [x] `internal/identity` audit trail for API tokens and service
      accounts — found by a fresh domain-wide audit (widening beyond
      tenancy/AI, Phases 36-40) that noticed `identity.Service` had no
      `audit` field at all, unlike every other mutating domain (tenancy,
      rbac, ai, secrets, tools, agents, applications, infrastructure).
      The Audit page's own copy ("Every sensitive operation is recorded
      here") was, until now, false for this whole domain
- [x] Threaded an `AuditRecorder` into `identity.New` (now takes an
      `auditRecorder` param — 3 call sites updated: `main.go`,
      `harness_test.go`, `integration_test.go`) and wired it into
      `CreateAPIToken`, `CreateAPITokenForServiceAccount`,
      `RevokeAPIToken`, `AdminRevokeAPIToken` (`apitoken.go`) and
      `CreateServiceAccount`, `DisableServiceAccount`,
      `EnableServiceAccount`, `UpdateServiceAccount`
      (`serviceaccount.go`) — the full lifecycle of both, not a subset
- [x] **Deliberately did not audit `SignUp`/`Login`/`Logout`/
      `ChangePassword`/session management** — a real scope decision, not
      an oversight: those routes run before an organization is selected
      (`requireSession` only, not `requireOrganization` —
      `cmd/server/router.go`), and `audit.Query` always filters by
      `organization_id`. A NULL-org audit entry would be written but
      could never be read back through the existing Audit page or API —
      writing rows nothing could ever verify or display would have
      violated rule 36 in spirit even though the write itself would
      "succeed." Documented explicitly in `identity.New`'s doc comment
      and `docs/SECURITY.md` so a future phase doesn't have to
      rediscover this
- [x] The raw API token value is never included in what gets audited —
      `APIToken`'s JSON shape only ever carries `TokenPrefix`, never the
      secret itself, same guarantee `Set`/`UpdateDescription` already give
      secret values
- [x] 2 new integration tests, passing under `-race` alongside every
      existing identity/service-account/API-token test: one confirms
      create+revoke both produce real, queryable audit rows AND
      double-checks by querying `audit_log` directly that the raw token
      value never appears in `resulting_state`; the other confirms
      create/disable/enable/update on a service account each produce
      their own distinct audit action
- [x] No new HTTP routes, OpenAPI changes, or frontend code — existing
      endpoints simply now also write audit entries, and the existing
      Audit page already renders whatever `GET /audit` returns
- [x] Verified live end to end: created a real service account through
      the UI, disabled it, and watched `identity.service_account.created`
      then `identity.service_account.disabled` appear at the top of the
      real Audit page; separately created a real API token and watched
      `identity.api_token.created` appear too. Checked the browser
      console on a fresh tab — zero errors
- [x] Full backend test suite re-run clean (`go test ./... -race`)
- [x] Docs (`SECURITY.md`, `README.md`) updated

**Phase 42** — this pass:
- [x] `internal/jobs` audit trail — the second and smaller finding from
      the same sweep that produced Phase 41: `jobs.go` had no `audit`
      import at all, so `Enqueue`/`Cancel`/`Retry` (all real state
      changes) left no trail, unlike every sibling domain. Unlike
      Phase 41's identity methods, every jobs method already takes a
      fully-resolved `authctx.AuthContext` with a real organization — no
      pre-organization caveat needed here, so all three got audited
- [x] `jobs.New` now takes an `AuditRecorder` (2 call sites updated:
      `main.go`, and 3 local constructions in `jobs_integration_test.go`)
- [x] 1 new integration test, passing under `-race` alongside the 3
      pre-existing jobs tests: enqueue-then-cancel produces both
      `jobs.job.enqueued` and `jobs.job.cancelled` as real, queryable
      audit rows
- [x] No new HTTP routes, OpenAPI changes, or frontend code — existing
      endpoints simply now also write audit entries
- [x] Verified live end to end: enqueued a real job with no registered
      handler through the Jobs page (it failed visibly, the existing
      documented behavior), retried it, and watched both
      `jobs.job.enqueued` and `jobs.job.retried` appear at the top of the
      real Audit page. Checked the browser console on a fresh tab — zero
      errors
- [x] Full backend test suite re-run clean (`go test ./... -race`)
- [x] Docs (`README.md`) updated

**Phase 43** — this pass:
- [x] `identity.DeleteServiceAccount` — a genuine lifecycle gap found in
      the same domain audit that produced Phases 41-42:
      Create/List/Disable/Enable/Update all existed, but disabling is
      reversible and there was no way to permanently remove a service
      account that's genuinely retired, short of a database edit
- [x] `agents.Delete` (Phase 31) is the direct precedent: a hard delete
      (not the terminal-status-with-row-kept pattern nodes/applications
      use), gated on the account already being `disabled` — a real
      precondition check, not a schema default. `api_tokens.
      service_account_id` is `ON DELETE CASCADE` (migration 0001), so
      deleting also removes every token the account ever held, including
      already-revoked ones; `audit_log.actor_service_account_id` is `ON
      DELETE SET NULL`, so every audit entry the account's actions ever
      produced survives (with its `actor_label` snapshot intact) — only
      the FK back to the now-gone row clears
- [x] Reused the existing `DELETE /service-accounts/{id}` path for a
      different route: `DELETE /service-accounts/{id}` already means
      "disable" from an earlier phase, a naming decision this phase
      couldn't retroactively change without breaking existing callers, so
      the new hard-delete action lives at `DELETE
      /service-accounts/{id}/permanent` instead
- [x] 3 new integration tests, passing under `-race` alongside every
      existing service-account test: delete refuses an active account
      then succeeds once disabled (and a second delete on the same id is
      `NOT_FOUND`), the cascade genuinely removes every `api_tokens` row
      for the account (verified with a direct `SELECT count(*)`, not an
      assumption about the schema), and a member without
      `organization.manage` is forbidden
- [x] 1 new HTTP route and an OpenAPI addition, validated with
      `@redocly/cli lint`; `lib/api-types.generated.ts` regenerated
- [x] `web/app/(org)/access/page.tsx`: a "Delete" button that only
      appears once a service account is disabled, next to "Enable" —
      matching the "Issue token"/"Disable" pair's own active-only
      visibility rule
- [x] Verified live end to end: deleted a real disabled service account
      through the UI and watched it vanish from the table; confirmed via
      a direct API call that attempting to hard-delete a still-active one
      returns a real `409 CONFLICT`. Checked the browser console on a
      fresh tab — zero errors
- [x] Full backend test suite re-run clean (`go test ./... -race`)
- [x] Docs (`API.md`, `FRONTEND.md`, `README.md`) updated

**Phase 44** — this pass:
- [x] `tools.CancelApproval` — a real lifecycle gap found in the same
      domain audit as Phases 41-43: `ListApprovals`/`DecideApproval`
      existed, but a requester who realizes a `deploy_application`/
      `restart_container` call was a mistake had no way to withdraw it —
      only wait for an approver to reject it, or for it to expire
- [x] Self-service, the same "act on your own resource" pattern
      `RevokeSession`/`RevokeAPIToken`/`LeaveOrganization` already draw —
      needs no `approvals.decide` (that's only for deciding *someone
      else's* request), only that the caller is the original human
      requester (`requesting_user_id`); an agent- or service-account-
      originated request has no self-cancel path here
- [x] New migration `0016` adds a genuine terminal `cancelled` status to
      `approvals` (alongside `pending`/`approved`/`rejected`/`expired`) —
      distinct from `expired`/`rejected` so the trail records *why* a
      request never got decided, not just that it didn't
- [x] 2 new integration tests, passing under `-race` alongside every
      existing tools/approvals test: cancelling withdraws a pending
      request and the tool never runs (plus a decision or a second cancel
      on the now-cancelled approval both correctly refused), and a caller
      who isn't the original requester is forbidden — confirmed the
      approval stays genuinely untouched afterward
- [x] 1 new HTTP route (`POST /approvals/{id}/cancel`) and OpenAPI
      additions (including the `Approval` schema's `status` enum gaining
      `cancelled`), validated with `@redocly/cli lint`;
      `lib/api-types.generated.ts` regenerated
- [x] `web/app/(org)/tools/page.tsx`: a "Cancel" button next to
      Approve/Reject on every pending row (shown unconditionally, not
      pre-computed by requester — a real `403 FORBIDDEN` surfaces inline
      if the caller wasn't the requester, the same pattern the page's
      other forms already use), and a `cancelled` option added to the
      status filter
- [x] Verified live end to end: created a real pending approval through
      the UI, cancelled it, watched it disappear from the pending list
      and reappear under the new `cancelled` filter. Checked the browser
      console on a completely fresh tab — zero errors (a stale-HMR error
      surfaced on a reused dev-server tab after a `.next` cache clear;
      ruled out as a real bug by confirming `npm run build` was already
      clean and a genuinely fresh tab showed nothing)
- [x] Full backend test suite re-run clean (`go test ./... -race`),
      confirming migration `0016` applies cleanly against both the dev
      database and a fresh test database
- [x] Docs (`API.md`, `AGENTS.md`, `FRONTEND.md`, `README.md`) updated

**Phase 45** — this pass:
- [x] `identity.RevokeAllOtherSessions` — found in the same domain audit
      as Phases 41-44: `ChangePassword` revokes every other session as a
      side effect, and `RevokeSession` revokes one at a time, but there
      was no direct "log out all other devices" action a user could take
      without also rotating their password
- [x] Shares `ChangePassword`'s exact "resolve the current session,
      exclude it from the mass-revoke" logic — an invalid/expired/absent
      current token just means there's nothing to exclude, not an error,
      the same tradeoff `ChangePassword` already makes
- [x] 1 new integration test, passing under `-race` alongside every
      existing identity/session test: three sessions logged in, the
      calling one survives a bulk revoke while the other two are
      genuinely revoked (`ErrSessionInvalid`), and a call with no current
      token still succeeds cleanly
- [x] 1 new HTTP route (`POST /account/sessions/revoke-others`, in the
      same pre-organization `requireSession`-only group as
      `GET`/`DELETE /account/sessions`) and an OpenAPI addition,
      validated with `@redocly/cli lint`; `lib/api-types.generated.ts`
      regenerated
- [x] `web/app/(org)/account/page.tsx`: a "Log out all other devices (N)"
      button above the Active sessions table, only rendered when `N > 0`
- [x] Verified live end to end: created two extra sessions for the real
      test account via direct API calls, reloaded to see all three
      listed, clicked the bulk button through the UI, and watched both
      others disappear leaving only "this session" — then navigated to
      another page to confirm the browser's own session genuinely
      survived rather than just trusting the `204`. Checked the browser
      console on a fresh tab — zero errors
- [x] Full backend test suite re-run clean (`go test ./... -race`)
- [x] Docs (`API.md`, `FRONTEND.md`, `README.md`) updated

**Phase 46** — this pass:
- [x] Closed a concrete rate-limiting gap found in the same domain audit
      as Phases 41-45: `POST /account/password` re-verifies the caller's
      current password on every call — the same credential-verification
      shape `/auth/login` has — but had no throttle at all, unlike every
      other credential-verification surface (login, signup). A
      stolen/leaked session token let an attacker brute-force the
      account's real password (useful for credential reuse elsewhere)
      with no friction
- [x] New `changePasswordRate` limiter (`newRateLimiters` now returns 5
      limiters instead of 4; both the in-process and Redis-backed
      branches updated), keyed by user ID rather than IP — the caller is
      already authenticated by the time this endpoint is reachable, so an
      IP-keyed limit would just let an attacker spread attempts across
      many IPs against the same account; 5 attempts / 5 minutes, matching
      `/auth/login`'s own budget
- [x] No new domain logic, migration, or Go unit test — `cmd/server` has
      no test harness for router-level HTTP wiring (same situation Phase
      34's rate-limit fix hit), so this was verified live against the
      real running server, the same way Phase 34 and `/ai/chat`'s
      original rollout were
- [x] OpenAPI addition (documenting the existing `429`/`RATE_LIMITED`
      response on `POST /account/password`, mirroring how `/ai/chat` and
      `/agents/{id}/run` already document it), validated with
      `@redocly/cli lint`; `lib/api-types.generated.ts` regenerated. No
      frontend code change needed — the existing form already surfaces
      any `ApiError.message`, including a `429`
- [x] Verified live end to end: fired 7 real HTTP requests with a wrong
      current password at `POST /account/password` for a real account —
      the first 5 returned a real `401` (wrong password, correctly
      checked), the 6th and 7th returned a real `429`; confirmed a
      different account's own budget was unaffected (a fresh account
      hitting the same endpoint immediately afterward got a normal `401`,
      not `429`); then reproduced the exact same `429` through the real
      Account page form and watched "too many password change attempts,
      try again shortly" render inline. Checked the browser console on a
      fresh tab — zero errors
- [x] Full backend test suite re-run clean (`go test ./... -race`)
- [x] Docs (`API.md`, `SECURITY.md`, `README.md`) updated

**Phase 47** — this pass:
- [x] `identity.UpdateAPIToken` — a real gap found in the same domain
      audit as Phases 41-46: `CreateAPIToken` takes `name` only at mint
      time, and there was no way to fix a typo'd name or clarify a
      token's purpose later without revoking and reissuing — which loses
      the prefix/creation date and forces immediate re-authentication of
      whatever used the old token
- [x] Metadata-only, the same shape Phase 35's `secrets.UpdateDescription`
      already established — scopes are deliberately not editable here:
      they're fixed at mint time (`CreateAPIToken` already validated them
      as a subset of the creator's permissions *at that moment*),
      re-running that check against whatever the caller's permissions
      happen to be *now* would be a materially different operation than a
      name fix. Scoped to the caller's own tokens only, the same
      ownership-based scoping (no separate `rbac.Require` needed)
      `RevokeAPIToken` already uses
- [x] 2 new integration tests, passing under `-race` alongside every
      existing API-token test: rename changes the name without touching
      the prefix, scopes, or the token's own ability to authenticate (a
      real `AuthContextForAPIToken` call post-rename still works), plus
      an empty name is rejected; and a caller who doesn't own the token
      gets `NOT_FOUND`, not a silent no-op
- [x] 1 new HTTP route (`PUT /api-tokens/{id}`) and an OpenAPI addition,
      validated with `@redocly/cli lint`; `lib/api-types.generated.ts`
      regenerated
- [x] `web/app/(org)/access/page.tsx`: an inline "Rename" form per token
      row, next to "Revoke" — reloads both the caller's own token list
      and the org-wide admin listing below it, so the two sections never
      disagree about a token's current name (a real staleness bug caught
      live during this phase and fixed before commit, not left as a
      known issue)
- [x] Verified live end to end: renamed a real token through the UI and
      confirmed the new name appeared in both "My API tokens" and "All
      organization tokens" after a fresh reload, with the prefix and
      scopes untouched; confirmed via a direct API call that an empty
      name is rejected with a real `400 VALIDATION_ERROR`. Checked the
      browser console on a fresh tab — zero errors
- [x] Full backend test suite re-run clean (`go test ./... -race`)
- [x] Docs (`API.md`, `FRONTEND.md`, `README.md`) updated

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
