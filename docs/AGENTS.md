# Agents, Tools, Approvals

Status: **Tool Gateway execution + approval workflow: IMPLEMENTED.**
**Agent execution loop: FOUNDATION ONLY** (schema only, no code reads
`system_instructions` and drives the AI gateway yet). See
`internal/tools/tools.go` and `internal/tools_integration_test.go`.

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

A pending approval also expires (`defaultApprovalTTL`, 24h, not yet
configurable). There is no background sweep — expiry is checked lazily at
the top of `ListApprovals` and `DecideApproval`, which is sufficient for a
human-facing queue and needs no new worker infrastructure; a scheduled
`jobs.Enqueue`-driven sweep is the natural upgrade if this ever needs to
run without anyone calling those methods. Verified by
`TestTools_ExpiredApprovalCannotBeDecided`.

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

## `agents` table

An agent definition ties together an AI profile, an allowed-tool list, and a
permission scope — but there is no agent execution loop yet (no code reads
`system_instructions` and calls the AI gateway in a loop, nor checks an
agent's `allowed_tool_keys` before letting it call `tools.Execute`). Kiko
and other concrete agents are explicitly out of scope for Nodera's core
(rule 17, rule 38) — Nodera provides the runtime substrate, not
agent-specific behavior.

## Not yet implemented

- Sandboxed tool execution of any kind — handlers today are trusted Go
  code composing existing domain services (ADR-002), not a sandbox around
  arbitrary/external commands; nothing shells out
- The policy engine beyond RBAC permission checks (a richer per-agent
  policy — usage limits, time windows, etc. — `internal/agents/policies`
  doesn't exist as a package yet)
- Agent execution loop / scheduling, and enforcing an agent's
  `allowed_tool_keys` (only human callers hit `tools.Execute` today, via
  the HTTP API, not an autonomous agent)
- A background approval-expiry sweep independent of `ListApprovals`/
  `DecideApproval` being called, and a per-tool/per-org-configurable TTL
- Handlers for tools besides `get_server_metrics` and `check_ssl`
