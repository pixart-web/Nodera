# Agents, Tools, Approvals

Status: **Tool Gateway execution + approval workflow: IMPLEMENTED.**
**Agent identity + scoped execution: IMPLEMENTED** — an agent has its own
`permission_scope` and `allowed_tool_keys`, can run a scoped AI chat, and
can execute a specific tool under its own scope. **Autonomous tool
selection: NOT IMPLEMENTED and not planned as unrestricted autonomy**
(rule 38) — a human or service caller always decides which tool an agent
invokes; the agent never picks its own tool. See `internal/tools/tools.go`,
`internal/agents/agents.go`, `internal/tools_integration_test.go`, and
`internal/agents_integration_test.go`.

## Shape

```
Tool call request (tool key, resource_type/id, parameters)
  → tools registry lookup (risk_level: read | safe | privileged | critical)
  → permission check: tool.required_permission, then the risk tier's own
    permission (tools.safe / tools.privileged / tools.critical)
  → privileged/critical → create a pending `approvals` row, return
    approval_id — never executes synchronously, regardless of whether a
    handler exists for the tool
  → read/safe → run the registered handler now, or NOT_IMPLEMENTED if none
    is registered (`implemented=false` in the registry)
  → audit record either way
```

A human later calls `POST /api/v1/approvals/{id}/decide`
(`approvals.decide`). Approving re-checks the tool and, if a handler is
registered, actually runs it with the originally-captured parameters,
recording the real result on the approval row — including an honest
`NOT_IMPLEMENTED` outcome if no handler exists (the approval itself still
succeeded; a human did authorize the action, execution just isn't built
yet — those are different facts, both recorded truthfully, rule 36).
Rejecting never executes anything, proven by
`TestTools_RejectingApprovalNeverExecutes`.

### Approval state machine and execution concurrency

```
pending → rejected            (DecideApproval, approve=false)
pending → cancelled           (CancelApproval, requester only)
pending → expired             (expirePending / RunExpirySweep)
pending → executing → executed          (DecideApproval, approve=true, handler succeeded)
pending → executing → execution_failed  (DecideApproval, approve=true, handler errored —
                                          including a legitimate NOT_IMPLEMENTED outcome)
```

`approved` remains a legal value in the `approvals_status_check` constraint
(migration `0017`) for rows written before this state machine existed, but
no code path produces it anymore — approving now moves straight through
`executing` to a terminal execution status in the same call.

**The double-execution fix.** Every state transition above is a single
conditional SQL statement — `UPDATE approvals SET status = <new> WHERE id =
$1 AND status = 'pending' RETURNING ...` (or `WHERE status = 'executing'`
for the final `executing → executed/execution_failed` step, which only the
caller that already owns the row can ever reach). PostgreSQL serializes
concurrent `UPDATE`s against the same row, so at most one concurrent
caller's `WHERE status = 'pending'` can match; everyone else gets zero rows
back and a deterministic `CONFLICT` — before either of them has called the
tool handler. `Registry.DecideApproval`'s handler invocation happens only
*after* this atomic claim succeeds, which is what actually makes "one
approval → at most one execution" true, not just the presence of an
`executing` state. See `internal/tools/tools.go` (`DecideApproval`'s doc
comment) and `internal/approvals_concurrency_test.go`:

- `TestApprovals_ConcurrentApprovalsExecuteExactlyOnce` — 20 concurrent
  `DecideApproval(approve=true)` calls on one pending approval; an atomic
  counter inside the handler proves it ran exactly once, and exactly 19
  callers get `CONFLICT`.
- `TestApprovals_ApproveVsRejectRaceIsDeterministic` /
  `TestApprovals_ApproveVsCancelRaceIsDeterministic` — approve racing
  reject/cancel, 15 trials each; exactly one side wins, and the handler
  runs iff approve won.
- `TestApprovals_ExpiredApprovalNeverExecutes` — a backdated `expires_at`
  is flipped to `expired` before the decide call's atomic claim ever sees
  `pending`.
- `TestApprovals_SequentialRepeatedDecisionExecutesOnce` — the same
  invariant holds for two *sequential* (non-racing) decide calls, proving
  the fix isn't merely papering over concurrency.

Run with `go test ./... -race` (all pass clean under the race detector).

**Crash semantics.** If the process crashes after the `pending → executing`
transition but before the final `executing → executed/execution_failed`
transition, the row is left in `executing` permanently — there is no
automatic reclaim or retry. This is intentional: resuming an
unknown-outcome privileged/critical operation automatically risks a second,
uncoordinated execution of something that might restart a service, modify
DNS, or touch a database, which is strictly worse than requiring an
operator to inspect and manually resolve a stuck `executing` row. Safety
over availability for this one state.

### Requester / approver / execution identity

An `Approval` never collapses "who asked" and "who authorized" into one
actor. The row (and the JSON the API returns) carries them separately:

- `requested_by_user_id` / `requested_by_agent_id` — exactly one is set
  (or neither, for a service-account-originated call), identifying who
  actually called `Execute` and triggered the approval requirement. An
  agent's own scoped identity (`ActorAgent`, its own `ActorID` — see
  `agents.agentAuthContext`) is preserved here even though execution later
  runs under the approving human's `AuthContext`, not the agent's.
- `decided_by_user_id` — the human who called `DecideApproval`, distinct
  from the requester whenever the two differ (e.g. an agent requests,
  a human approves).
- `created_at` / `decided_at` / `decision_reason` / `execution_result` —
  the rest of the immutable trail: when requested, when decided, why, and
  what executing it actually produced.

`TestApprovals_PreservesRequesterAndApproverIdentitySeparately` exercises
exactly the scenario this guards against: an agent (`ActorAgent`) requests
`restart_container`, a human (`ActorUser`, holding `approvals.decide`)
approves it, and the test asserts `requested_by_agent_id` still identifies
the agent and `decided_by_user_id` identifies the human — post-execution,
not just at request time. No secret values are ever stored in `parameters`
or `execution_result` beyond what the tool call itself was given; the
approval record is not a place to route credentials through.

The requester themselves can also withdraw their own pending approval —
`POST /api/v1/approvals/{id}/cancel` (`Registry.CancelApproval`) —
without holding `approvals.decide`; that permission is only needed to
decide *someone else's* request, the same "act on your own resource"
distinction `RevokeSession`/`RevokeAPIToken`/`LeaveOrganization` already
draw. Only reaches approvals a human user requested
(`requesting_user_id`); an agent- or service-account-originated request
has no self-cancel path. Lands in a real terminal `cancelled` status
(migration `0016`), distinct from `expired`/`rejected` — the trail
records *why* it never got decided. Proven by
`TestTools_CancelApprovalWithdrawsOwnPendingRequest` and
`TestTools_CancelApprovalRefusesNonRequester`.

A pending approval also expires — `defaultApprovalTTL` (24h) unless the
organization has configured its own TTL for that specific tool via
`organization_tool_settings` (migration `0013`; `tools.manage` permission,
distinct from `approvals.decide` and every `tools.*` execution
permission). `Registry.SetApprovalTTL`/`ClearApprovalTTL`/
`ListApprovalTTLOverrides` manage those overrides
(`PUT`/`DELETE`/`GET /api/v1/tools/{key}/approval-ttl` and
`GET /api/v1/tools/approval-ttl`), bounded to
`[minApprovalTTL, maxApprovalTTL]` = `[5m, 30d]`. `createApproval` resolves
the effective TTL per call (`resolveApprovalTTL`) — changing an
organization's override only affects approvals created afterward, never
retroactively. Verified by `TestTools_ApprovalTTLDefaultsWhenNoOverride`
and `TestTools_SetApprovalTTLOverrideAppliesToNewApprovals`, and live
against the real HTTP API (setting a 10-minute override, confirming the
next approval's `expires_at` was exactly 10 minutes after `created_at`,
clearing it, and confirming the next one reverted to 24h).

Expiry itself is checked two ways: lazily, at the top of
`ListApprovals` and `DecideApproval` (scoped to the calling org — instant,
no waiting on the background sweep), and by `Registry.RunExpirySweep`, run
every 5 minutes across every organization by a goroutine started in
`cmd/server/main.go` (`runApprovalExpirySweep`) — so an idle organization's
stale approvals still flip to `expired` on schedule even if nobody calls
`ListApprovals`/`DecideApproval` for that org. Verified by
`TestTools_ExpiredApprovalCannotBeDecided` and
`TestTools_RunExpirySweepExpiresAcrossOrganizations`.

## What's actually implemented vs. not, per tool

Two tools have registered handlers and `implemented=true`:

- **`get_server_metrics`** (`cmd/server/main.go: newGetServerMetricsHandler`,
  wrapping `infrastructure.Service.Get`, migration `0010`). Returns the
  node's last-known **inventory** record (cpu_cores, memory_mb, storage_gb,
  status, last_seen_at) — explicitly labeled `"source": "inventory"`, not
  live-sampled telemetry, since no Node Agent exists yet to report that
  (`docs/INFRASTRUCTURE.md`).
- **`check_ssl`** (`internal/tools/handlers/checkssl.go`, migration `0011`).
  Performs a genuine TLS handshake against the given host and reports the
  real leaf certificate's validity window, days remaining, and whether it
  verifies against the system trust store — verified live against
  `github.com` and by unit tests against a real local TLS listener
  (`httptest.NewTLSServer`), not a fabricated response.

Every other seeded tool (`get_container_logs`, `restart_container`,
`create_backup`, `verify_backup`, `deploy_application`,
`rollback_application`, `scan_wordpress`, `query_database`) remains
`implemented=false` — calling one returns `NOT_IMPLEMENTED`, and approving
a privileged/critical one records that same honest outcome (verified live
and by `TestTools_ApprovingUnimplementedToolReportsNotImplemented`). This
is a deliberately minimal proof that the whole pipeline (permission → risk
tier → approval → execution → audit) works end to end for more than one
tool shape (immediate-read and immediate-external-check) — not a claim
that the rest of the catalog is built.

## Why the tool catalog is seeded ahead of most handlers

Section 18 of the product brief names concrete example tools. Registering
them — with their risk level and required permission — means the
permission mapping and approval-routing decisions (which tools need human
sign-off) are made deliberately and reviewably, in a migration, rather than
invented ad hoc as each execution backend is eventually written.

## Risk levels

| Level | Meaning | Approval required? |
|---|---|---|
| `read` | Read-only, non-sensitive | No |
| `safe` | Mutating but low-blast-radius (e.g. triggering a backup) | No |
| `privileged` | Meaningful blast radius (e.g. restarting a container, deploying) | Yes |
| `critical` | High blast radius or hard to reverse (e.g. querying a production database) | Yes |

## `agents` table and `internal/agents`

An agent definition (`internal/agents/agents.go`) ties together an AI
profile, an allowed-tool list, and a permission scope. `agents.manage`
(create/enable/disable) is a distinct, more sensitive permission than
`agents.execute` (run/use an already-defined agent) — reusing the same
permission for both would let anyone who can run an agent also redefine
what it's allowed to do, which is exactly the privilege-escalation shape
the rest of the codebase avoids (migration `0012`).

Two capabilities exist today, both requiring the caller to hold
`agents.execute` and the agent to be `active`:

- **`Run(ctx, ac, id, userMessage)`** — builds the agent's own scoped
  `AuthContext` (`agentAuthContext`: `ActorType: agent`, permissions built
  from the agent's `permission_scope`, not the caller's), prepends
  `system_instructions` as a system message if set, and calls
  `ai.Service.Chat` under that scoped context. The agent's own scope — not
  the caller's `agents.execute` — must include `ai.use`, or the call is
  refused (`TestAgents_RunRequiresAIUseInAgentScope`). The HTTP handler
  (`POST /agents/{id}/run`) draws from the exact same per-organization
  rate-limit budget as `POST /ai/chat` (`docs/SECURITY.md`) — `Run` drives
  the identical `ai.Service.Chat` cost path, so it can't be used as a way
  around that limiter.
- **`ExecuteTool(ctx, ac, id, toolKey, input)`** — checks `toolKey` is in
  the agent's `allowed_tool_keys` (refused before it ever reaches the Tool
  Gateway if not — `TestAgents_ExecuteToolRespectsAllowlistAndScope`), then
  calls the existing `tools.Registry.Execute` under the agent's scoped
  `AuthContext`, so permission/risk-tier/approval logic all apply exactly
  as they would for a human caller, just evaluated against the agent's own
  `permission_scope`.

Both are audited under the **calling** `AuthContext` (so the audit trail
shows which human/service caller directed the agent), while the underlying
tool-execution or AI-usage audit entries the called service writes are
attributed to the **agent's own** actor label — verified live by checking
`GET /api/v1/audit` after a real `ExecuteTool` call.

`CreateAgent` enforces no-privilege-escalation: `PermissionScope` must be a
subset of the creating caller's own held permissions
(`TestAgents_CannotExceedCreatorPermissions`), and every `AllowedToolKeys`
entry must reference a real row in `tools` (`TestAgents_RejectsUnknownToolKey`).
A freshly created agent starts `disabled`; `SetStatus` is the only way to
`active`/`disabled` it, and both `Run` and `ExecuteTool` refuse a disabled
agent (`TestAgents_CreateEnableRun`, `TestAgents_DisabledAgentCannotExecuteTool`).

`Update` edits an agent's configuration after creation — name, description,
system instructions, AI profile key, allowed tool keys, permission scope,
timeout — via the same pointer-based partial-update pattern used elsewhere
(nil scalar = don't touch; `AllowedToolKeys`/`PermissionScope` nil = don't
touch, `[]` = clear). `PermissionScope` is re-validated against the
*caller's own currently-held* permissions on every update, not grandfathered
from the agent's existing scope (`TestAgents_UpdateRejectsPermissionEscalation`)
— the same rule `CreateAgent` enforces, closed for the update path too.

`Delete` permanently removes the row — a hard delete, unlike nodes'/
applications' terminal-status-with-row-kept pattern, because an agent is
closer to an access-scoped config object (an API token or role) than
physical/operational infrastructure, and its only FK reference
(`approvals.requesting_agent_id`) uses `ON DELETE SET NULL`. It requires
the agent to already be `disabled` — a real precondition check, returning
`CONFLICT` otherwise (`TestAgents_DeleteRequiresDisabledFirst`).

**What this is not:** the agent never decides on its own which tool to
call, or calls `Run` and `ExecuteTool` in a loop by itself. A caller (human
via the HTTP API, or another service) directs each `ExecuteTool` call
individually. There is no scheduler and no LLM-driven tool-selection loop
— that would be exactly the "unrestricted autonomous infrastructure
agent" rule 38 prohibits. Kiko and other concrete agents remain explicitly
out of scope for Nodera's core (rule 17) — Nodera provides this bounded
identity + execution substrate, not agent-specific behavior.

## Not yet implemented

- Sandboxed tool execution of any kind — handlers today are trusted Go
  code composing existing domain services (ADR-002), not a sandbox around
  arbitrary/external commands; nothing shells out
- The policy engine beyond RBAC permission checks (a richer per-agent
  policy — usage limits, time windows, etc. — `internal/agents/policies`
  doesn't exist as a package yet)
- Any autonomous/LLM-directed tool selection or scheduling loop (by design,
  see above — rule 38)
- Handlers for tools besides `get_server_metrics` and `check_ssl`
