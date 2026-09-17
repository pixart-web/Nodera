# Architecture Decision Records

Decisions are numbered sequentially and are not renumbered when superseded — a
superseded ADR stays in place with a note pointing to the one that replaces it.

---

## ADR-001: Core services in Go, frontend in TypeScript, Python only where AI/ML needs it

**Status:** Accepted (2026-09-17)

**Context:** Nodera is a long-lived infrastructure/AI/agent control plane, not a
short-lived app. It needs to run reliably as a set of long-running services
(API, node agents, job workers, AI gateway, agent runtime), be easy to deploy
as static binaries onto arbitrary nodes, and have first-class concurrency for
job/worker and node-agent workloads.

**Decision:**
- **Core API / Control Plane:** Go
- **Node Agent** (runs on managed infrastructure nodes): Go
- **Workers / Jobs:** Go
- **AI Gateway:** Go
- **Agent Runtime:** Go
- **AI / ML specialized services** (e.g. embedding pipelines, model-specific
  tooling that only has a mature Python SDK): Python, only when a Go
  implementation is not practical. These are isolated services behind the AI
  provider abstraction — never a dependency of core control-plane logic.
- **Frontend:** TypeScript (Next.js/React)

**Consequences:**
- A single Go module set can be compiled to static binaries and shipped to any
  node without a language runtime dependency — good fit for the Node Agent.
- Strong concurrency primitives (goroutines/channels) fit job workers and the
  agent/tool execution model well.
- Go's weaker ML ecosystem means AI/ML-heavy work (embeddings, local model
  serving glue, etc.) may be delegated to small Python sidecar services
  reached only through the AI provider adapter interface — the core domain
  never imports Python-specific concepts.
- Team needs Go proficiency across almost the whole backend surface.

**Alternatives considered:** Node.js/TypeScript across the board (rejected:
weaker fit for node-agent static binaries and CPU-bound job workers); Python
FastAPI across the board (rejected: weaker long-term reliability/concurrency
story for a control plane meant to run for years).

---

## ADR-002: Modular monolith for the Core API, not microservices

**Status:** Accepted (2026-09-17)

**Context:** Section 3 of the product brief requires strict domain boundaries
without prematurely fragmenting into microservices.

**Decision:** The Core API ships as a single Go binary (`api/cmd/server`)
composed of internal packages per domain (`internal/identity`,
`internal/infrastructure`, `internal/ai`, `internal/agents`, ...). Each domain
package exposes a Go interface (its "service" contract) and depends on other
domains only through their exported interfaces, never their internal types or
repositories. This keeps extraction into a separate service (later, if ever
justified) a matter of moving a package and wiring a network transport, not a
rewrite.

**Consequences:** Faster initial development, one deployable artifact, one
transaction/database initially. Domain leakage is a discipline problem, not a
process-boundary one — enforced via code review and (later) import-boundary
lint rules.

---

## ADR-003: PostgreSQL as system of record, Redis for ephemeral coordination only

**Status:** Accepted (2026-09-17)

**Decision:** PostgreSQL is the only source of truth for durable business
state (users, orgs, infrastructure inventory, jobs, audit log, AI usage,
agents, approvals, etc.). Redis is used only for queues, caching, locks, and
rate limiting; nothing that must survive a Redis flush lives only in Redis.
Job records are persisted in Postgres first; Redis (via a queue library) is
the delivery mechanism.

---

## ADR-004: Multi-tenancy modeled from day one via `organization_id` on tenant-scoped tables

**Status:** Accepted (2026-09-17)

**Decision:** Every tenant-scoped table carries a non-null `organization_id`
foreign key. All repository queries are required to filter by organization
through a request-scoped `AuthContext`, never by trusting a client-supplied
filter. RBAC checks and tenant-scoping checks both happen server-side in the
service layer, never only in the frontend or only via SQL row-level security
(RLS may be added later as defense-in-depth, not as the only control).

---

## ADR-005: Sessions/tokens via first-party identity module; no external IdP dependency in the foundation

**Status:** Accepted (2026-09-17)

**Decision:** Phase 1 implements first-party email/password authentication
with argon2id password hashing, server-side sessions (opaque tokens in
Postgres, not JWTs, so they can be revoked instantly) and a scoped API-token
mechanism for service accounts. OAuth/SSO providers are a future addition
behind the same `identity` module interface, not a phase-1 requirement.

**Why opaque sessions over JWT:** immediate revocation matters more than
avoiding a DB lookup per request at this stage, and audit/session-management
requirements (section 6, 41) need a queryable session record.

---

## ADR-006: AI Gateway, Agent Runtime, and Tool Gateway ship as interfaces + deterministic stubs in phase 1

**Status:** Accepted (2026-09-17)

**Decision:** Phase 1 implements the AI provider abstraction, model registry,
AI profile, and deterministic router *as real, working code*, but with zero
production AI providers wired in yet beyond a `local-echo` test provider used
for integration tests. Real provider adapters (OpenAI, Anthropic, Ollama,
vLLM, etc.) are added in a later phase behind the same interface. This
follows rule 36 (no fake functionality): unconfigured providers report
`unavailable`, never fabricated responses.
