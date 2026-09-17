# Infrastructure

Status: **IMPLEMENTED** for the node inventory model (register/list/get).
Everything else in this document (containers, networks, storage, domains,
backups, the Node Agent) is **PLANNED**.

## Node model

`internal/infrastructure` (`nodes` table, migration `0004_infrastructure.sql`)
is deliberately provider-agnostic — no `hetzner_*` field exists anywhere in
the core domain (rule 7, rule 30). Provider-specific data has a home
(`provider`, `provider_resource_id`, `provider_data JSONB`) without leaking
into the shape every other provider has to conform to.

```go
type Node struct {
    ID, OrganizationID   uuid.UUID
    Hostname             string
    Provider             string // "hetzner" | "local" | "aws" | ... | "unknown"
    ProviderResourceID   string
    Role                 string // "application" | "database" | "storage" | "worker" | "ai-inference" | "monitoring"
    Environment          string
    Status               string // "unknown" | "online" | "offline" | "degraded"
    CPUCores, MemoryMB, StorageGB int
    Capabilities         []string // e.g. "docker", "gpu"
    ...
}
```

`RegisterNode` is pure inventory state — it does not contact the node or
verify it exists. That's the future Node Agent's job: a small Go binary
(same module, `ADR-002` extractable) that runs *on* a managed node, reports
health/metrics, and executes the subset of Tool Gateway calls scoped to
infrastructure operations. It does not exist yet.

## Multi-provider by design

`provider` is a free-text field validated at the application layer, not a DB
enum — adding "digitalocean" or "ovh" tomorrow needs no migration. See
`docs/DECISIONS.md` ADR-001 for why Go was chosen partly for this (single
static binary, easy to ship the Node Agent to arbitrary hosts regardless of
provider).

## Not yet implemented

- Node Agent (the actual on-node process)
- Containers, networks, storage, domains, DNS, TLS models
- Backups/restores
- Health check ingestion (`last_seen_at` exists on the schema; nothing
  updates it yet)
- Deployment/rollback tracking
