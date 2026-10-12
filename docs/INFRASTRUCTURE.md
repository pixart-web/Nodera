# Infrastructure

Status: **IMPLEMENTED** for the node inventory model (register/list/get/
update/status-report/decommission). Everything else in this document
(containers, networks, storage, domains, backups, the Node Agent) is
**PLANNED**.

## Node model

`internal/infrastructure` (`nodes` table, migration `0004_infrastructure.sql`,
extended by `0014_node_decommission_status.sql`) is deliberately
provider-agnostic — no `hetzner_*` field exists anywhere in the core
domain (rule 7, rule 30). Provider-specific data has a home (`provider`,
`provider_resource_id`, `provider_data JSONB`) without leaking into the
shape every other provider has to conform to.

```go
type Node struct {
    ID, OrganizationID   uuid.UUID
    Hostname             string
    Provider             string // "hetzner" | "local" | "aws" | ... | "unknown"
    ProviderResourceID   string
    Role                 string // "application" | "database" | "storage" | "worker" | "ai-inference" | "monitoring"
    Environment          string
    Status               string // "unknown" | "online" | "offline" | "degraded" | "decommissioned"
    CPUCores, MemoryMB, StorageGB int
    Capabilities         []string // e.g. "docker", "gpu"
    ...
}
```

`RegisterNode` is pure inventory state — it does not contact the node or
verify it exists. That's the future Node Agent's job: a small Go binary
(same module, `ADR-002` extractable) that runs *on* a managed node, reports
health/metrics, and executes the subset of Tool Gateway calls scoped to
infrastructure operations. It does not exist yet — but the API surface it
would call to report in already does: `UpdateNodeStatus`
(`POST /api/v1/infrastructure/nodes/{id}/status`) sets `status` and stamps
`last_seen_at` in one call, exactly what a heartbeat needs. Until a real
Node Agent exists, this is only reachable via a direct API call, not an
automatic process — no code calls it on a schedule.

`UpdateNode` (`PUT /api/v1/infrastructure/nodes/{id}`) edits the rest of a
node's editable inventory (hostname, role, environment, operating_system,
cpu/memory/storage, capabilities) — everything except identity fields set
once at registration (`provider`, `provider_resource_id`) and `status`,
which is reported separately.

`DecommissionNode` (`POST /api/v1/infrastructure/nodes/{id}/decommission`)
sets `status = 'decommissioned'`, a terminal, one-way state: further
`UpdateNode`/`UpdateNodeStatus` calls on that node are refused
(`CONFLICT`). The row itself is kept, not deleted — both existing foreign
keys (`applications.node_id`, `ai_models.node_id`) use `ON DELETE SET
NULL`, so a hard delete would only null those references and silently
discard "this application used to run on that node" history a control
plane should keep.

## Multi-provider by design

`provider` is a free-text field validated at the application layer, not a DB
enum — adding "digitalocean" or "ovh" tomorrow needs no migration. See
`docs/DECISIONS.md` ADR-001 for why Go was chosen partly for this (single
static binary, easy to ship the Node Agent to arbitrary hosts regardless of
provider).

## What exists beyond the node inventory

The Node Agent (`docs/NODE-AGENT.md`) reports heartbeats that drive node status, and executes signed container/database commands; backups (`docs/BACKUPS.md`), domains/DNS/SSL, deployments and
migrations are separate engines (`docs/ARCHITECTURE.md` §9). The node inventory itself is still provider-agnostic data.

## Not yet implemented

- A Hetzner Cloud provider for machine-level actions (reboot, rescale, snapshot) — `NodeProvider` is mock-only; NOT IMPLEMENTED
- Multi-node orchestration: `NODERA_LOCAL_AGENT_ID` names a single local agent; routing operations to a specific node's agent is future work
- Container networks/volumes as managed objects (Nodera creates containers on the configured workload network only)
