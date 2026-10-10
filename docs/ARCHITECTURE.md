# Nodera Architecture

Status legend used throughout this document and the rest of `/docs`:
**IMPLEMENTED** — working, tested code. **FOUNDATION ONLY** — real interfaces/
schema exist, no production backend behind them yet. **PLANNED** — not started.

## 1. System shape

Nodera's Core API is a **modular monolith**: one Go binary, strict internal
domain boundaries, PostgreSQL for durable state, Redis for queues/cache/locks.
See [ADR-002](DECISIONS.md#adr-002-modular-monolith-for-the-core-api-not-microservices).

```
                         ┌─────────────────────────┐
                         │        web (TS)         │  Next.js control-plane UI
                         └────────────┬─────────────┘
                                      │ HTTPS / JSON (/api/v1)
                         ┌────────────▼─────────────┐
                         │      api (Go binary)      │
                         │  cmd/server + internal/*  │
                         │                            │
                         │  identity · tenancy · rbac │
                         │  audit · infrastructure    │
                         │  applications · jobs        │
                         │  secrets · ai · agents      │
                         │  knowledge · notifications  │
                         └──────┬───────────┬─────────┘
                                │           │
                        ┌───────▼───┐   ┌───▼──────┐
                        │ PostgreSQL│   │  Redis    │
                        │ (system of│   │ (queues,  │
                        │  record)  │   │  cache)   │
                        └───────────┘   └───────────┘

  Future, same module boundaries, extractable without rewrite:
  ┌──────────────┐   ┌──────────────┐   ┌──────────────┐
  │  node-agent   │   │  ai-gateway   │   │ agent-runtime │
  │  (Go, runs on │   │  (Go service, │   │  (Go service, │
  │   managed     │   │   provider    │   │   tool exec,  │
  │   nodes)      │   │   adapters)   │   │   policies)   │
  └──────────────┘   └──────────────┘   └──────────────┘
```

## 2. Repository layout

```
Nodera/
  api/                    Go module: the Core API control plane
    cmd/server/           main() — wiring only, no business logic
    internal/
      platform/           cross-cutting: config, db, http, logger, events, authctx
      identity/           users, credentials, sessions, service accounts, API tokens
      tenancy/             organizations, projects, environments
      rbac/                roles, permissions, policy evaluation
      audit/               immutable-style audit log writer + query API
      infrastructure/     nodes, provider adapters, capabilities
      applications/       applications, services, deployments (foundation)
      jobs/                job/queue domain (Postgres-backed state + Redis delivery)
      secrets/             secret reference abstraction (never plaintext at rest)
      ai/
        gateway/           /api/v1/ai/* handlers
        providers/         provider adapter interface + local-echo test adapter
        models/             model registry
        profiles/           AI profile definitions + resolution
        routing/            deterministic router
        usage/              AI usage/metrics recording
      agents/
        runtime/            agent definition + execution envelope (foundation)
        tools/               tool registry + risk levels (foundation)
        policies/            policy engine interfaces (foundation)
        approvals/           approval request/decision model
      knowledge/            RAG ingestion/retrieval interfaces (foundation)
      notifications/        notification dispatch interface (foundation)
    migrations/            SQL migrations (source of truth for schema)
  web/                     Next.js + TypeScript control-plane UI
  docs/                    architecture, security, API, deployment, decisions...
  scripts/                 dev-environment helper scripts
  docker-compose.yml       local Postgres + Redis for development
```

## 3. Domain boundary rule

A domain package may depend on another domain **only** through the interface
that domain exports from its top-level package (its "service"), never through
another domain's repository or internal types. `platform/*` packages have no
knowledge of any domain and may be depended on by everyone. This is enforced
by code review today; an import-graph lint check is planned once the module
count justifies it.

## 4. Request flow

```
HTTP request
  → platform/httpserver middleware chain
      (request ID → structured logging → recover → auth → rate limit)
  → identity: resolve session/API token → AuthContext{UserID, OrgID, Roles}
  → rbac: PermissionCheck(AuthContext, permission, resource) before any
    domain service call that mutates or reads sensitive data
  → domain service (business logic, tenant-scoped by AuthContext.OrgID)
  → audit: sensitive operations write an audit record (actor, action,
    resource, before/after, correlation ID)
  → normalized JSON response (see docs/API.md)
```

## 5. Multi-tenancy & authorization

See [ADR-004](DECISIONS.md#adr-004-multi-tenancy-modeled-from-day-one-via-organization_id-on-tenant-scoped-tables)
and `docs/SECURITY.md`. Every tenant-scoped repository method takes an
`AuthContext` and filters by `organization_id` server-side; there is no
"trust the caller's filter" path.

## 6. AI subsystem shape (IMPLEMENTED, one real provider — Ollama; cloud adapters PLANNED)

```
caller (internal domain or external product via /api/v1/ai)
  → AI Profile (e.g. "infrastructure.analysis")
  → routing.Router: profile → policy (privacy level, capability) → eligible
    models from the model registry → deterministic selection
  → providers.Provider adapter (normalized request/response/error)
  → usage: record tokens, latency, cost, provider/model, success/failure
  → normalized response
```

Privacy classification (`PUBLIC`/`INTERNAL`/`CONFIDENTIAL`/`RESTRICTED`) is
enforced in `routing`, not left to the caller: a `RESTRICTED` profile can
never resolve to a cloud provider, regardless of what the caller requests.
See `docs/AI_ARCHITECTURE.md`.

## 7. Tools & approvals shape (IMPLEMENTED — one real handler; agent execution loop PLANNED)

```
Tool call (human caller via /api/v1/tools/{key}/execute, today — not yet an
autonomous agent; see docs/AGENTS.md)
  → tools.Registry: lookup tool + its risk level (READ/SAFE/PRIVILEGED/CRITICAL)
  → rbac.Require: tool's own required_permission, then the risk tier's
    permission (tools.safe/privileged/critical)
  → PRIVILEGED/CRITICAL → create an Approval record, return — never executes
    synchronously, regardless of whether a handler exists
  → READ/SAFE → run the registered handler now, or NOT_IMPLEMENTED if none
    is registered (rule 36: never a fabricated result)
  → audit
```

A human decision (`POST /api/v1/approvals/{id}/decide`) on a
PRIVILEGED/CRITICAL request then attempts execution and records the real
outcome — including an honest NOT_IMPLEMENTED if still no handler exists.
See `docs/AGENTS.md` for exactly which tool has a real handler today
(`get_server_metrics`, wrapping the infrastructure domain) versus which
don't yet.

## 8. What phase 1 actually implements

See the root `README.md` "Status" table for the authoritative implemented /
foundation / planned breakdown per module — kept there instead of duplicated
here so it can't drift.

## 9. Control plane (operations engine) — IMPLEMENTED

Nodera is an **infrastructure operating system**, not a dashboard: every important action is an *operation*.

```
HTTP request ─ authn/authz (RBAC, tenancy) ─▶ ops.Engine.Submit ─▶ jobs table (idempotency key, correlation id)
                                     │                                   │  FOR UPDATE SKIP LOCKED worker
                dangerous? ──▶ Tool Gateway ──▶ human approval ───────────┘
                                                                          ▼
                              Operation: Validate → Steps (persisted, each with idempotent Undo) → Result
                                                                          │ failure/cancel: reverse-order rollback
                                      providers.Set (interfaces) ◀────────┘        audit + notification + SSE stream
```

* **Engines depend only on provider interfaces** (`internal/providers`): Container, Filesystem, Database, DNS, Certificate, Backup, Monitoring, Node, Git.
  Implementations: `mock/` (in-memory, fault injection), `local/` (real FS, tar.gz backups, local X.509 CA, DNS zone store, SSRF-guarded monitoring),
  `docker/` (argv-only CLI adapter). A nil provider means "not configured": engines refuse to run instead of simulating.
  `/system/info` and `/ready` report each capability as `real | local | mock | not_configured`.
* **Engines:** `projects` + `provisioning` (clients, projects, `project.provision/delete`), `backups`, `network` (domains/DNS/SSL), `deployments`, `sitemig`
  (migrations), `wordpress` (clone/update/health), `monitoring` (monitors/rules/incidents), `notifications`, `logs` (+retention), `flags`, `dashboard` (+search),
  `aiplans`, `nodeagent`, `devseed`.
* **Traceability:** one correlation id links request → job → operation steps/logs → audit rows → notifications.
* **Approvals:** operations that declare a `ToolKey` can only be submitted through the Tool Gateway (`ops.Engine.Submit` refuses them). The gateway handler
  calls `SubmitTrusted` after a human decision, so the approval state machine's at-most-once execution applies to every dangerous operation:
  `project.delete`, `backup.restore`, `backup.delete`, `domain.remove`, `migration.cutover`, `deployment.production` (and `database.delete`, `credential.rotate` are registered tools without an operation yet).
* **Multi-tenancy:** every table carries `organization_id`; every query filters on it; project-scoped sub-resources return 404 (not an empty 200) for another tenant's project.
  Cross-tenant tests exist per engine plus a table-driven IDOR suite.
* **Realtime:** SSE (`/operations/{id}/stream`, `/events`) — fetch-based on the client because `EventSource` cannot send the `X-Nodera-Org` header.
* **Background loops** (one process, safe with several instances): job worker, node-agent sweeper, scheduled backups + retention + certificate renewal (period-keyed idempotency),
  monitor runner (`SKIP LOCKED`), alert evaluator, data-retention sweeper.
* **Node Agent:** pull model, signed both ways, allowlisted commands only — see `docs/NODE-AGENT.md`.
* **AI:** proposes plans only; a human approves and runs each step under their own permissions — see `docs/SECURITY.md` "AI operations layer".

Engine docs: `BACKUPS.md`, `DEPLOYMENTS.md`, `MIGRATION.md`, `MONITORING.md`, `NODE-AGENT.md`.
