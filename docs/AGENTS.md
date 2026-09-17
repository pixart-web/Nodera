# Agents, Tools, Approvals

Status: **FOUNDATION ONLY**. Schema (`0007_agents.sql`) is real; there is no
execution backend. This is deliberate — rule 18 and 36 both prohibit shipping
unrestricted or fabricated tool execution, so the registry and approval
model exist before any tool can actually run.

## Shape

```
Agent definition (ai_profile, allowed_tool_keys, permission_scope)
  → tools registry lookup (risk_level: read | safe | privileged | critical)
  → policy check: is this tool call allowed for this agent right now?
  → privileged/critical → create an `approvals` row, block until a human
    decision is recorded (approve/reject), only then proceed
  → execution — NOT IMPLEMENTED in phase 1; every seeded tool has
    `implemented = false`, so calling one today returns NOT_IMPLEMENTED,
    never a fabricated result
  → audit record either way
```

## Why the tool catalog is seeded but marked `implemented = false`

Section 18 of the product brief names concrete example tools
(`get_server_metrics`, `restart_container`, `deploy_application`, ...).
Registering them now — with their risk level and required permission — means
the permission mapping and approval-routing decisions (which tools need
human sign-off) are made deliberately and reviewably, in a migration, rather
than being invented ad hoc when an execution backend is eventually written.
Nothing calls these tools yet.

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
`system_instructions` and calls the AI gateway in a loop). Kiko and other
concrete agents are explicitly out of scope for Nodera's core (rule 17,
rule 38) — Nodera provides the runtime substrate, not agent-specific
behavior.

## Not yet implemented

- Sandboxed tool execution of any kind
- The policy engine (`internal/agents/policies` package doesn't exist yet —
  only the schema/architecture doc reference it)
- Approval decision endpoints
- Agent execution loop / scheduling
