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

## 6. AI subsystem shape (FOUNDATION ONLY beyond the local-echo test provider)

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

## 7. Agents & tools shape (FOUNDATION ONLY — no execution backend yet)

```
Agent definition (profile, allowed tools, permission scope)
  → tools.Registry: lookup tool + its risk level (READ/SAFE/PRIVILEGED/CRITICAL)
  → policies.Engine: is this tool call allowed for this agent/actor right now?
  → approvals: PRIVILEGED/CRITICAL calls create an Approval record and block
    until a human decision is recorded
  → execution (not implemented in phase 1 — no sandboxed executor exists yet;
    calling an unimplemented tool returns AGENT_TOOL_NOT_IMPLEMENTED, never a
    fabricated result)
  → audit
```

## 8. What phase 1 actually implements

See the root `README.md` "Status" table for the authoritative implemented /
foundation / planned breakdown per module — kept there instead of duplicated
here so it can't drift.
