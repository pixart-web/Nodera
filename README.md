# Nodera

Nodera is the Infrastructure, AI, Agent, and Operations control plane for the
[ecosystem of products](docs/ARCHITECTURE.md) it will eventually serve
(CyberAudit, Web Content Flow, Kiko, SearchAnvil, and future SaaS products).
It is built as platform infrastructure, not a single-purpose app — see
`docs/ARCHITECTURE.md` and `docs/DECISIONS.md` for the reasoning.

This is early-stage, foundational work. See the status table below for
exactly what is real today.

## Stack

- **Core API, Node Agent, Workers, AI Gateway, Agent Runtime:** Go
- **Frontend:** TypeScript (Next.js)
- **AI/ML specialized services:** Python, only where a Go implementation
  isn't practical, behind the same provider abstraction
- **Database:** PostgreSQL (system of record) + Redis (queues/cache/locks)

See [ADR-001](docs/DECISIONS.md#adr-001-core-services-in-go-frontend-in-typescript-python-only-where-aiml-needs-it).

## Status

Legend: **IMPLEMENTED** (real, tested code) · **FOUNDATION ONLY** (real
schema/interfaces, no production backend yet) · **PLANNED** (not started).

| Module | Status | Notes |
|---|---|---|
| Identity (signup/login/sessions) | IMPLEMENTED | Argon2id passwords, opaque revocable sessions, self-service profile update + password change (revokes other sessions, not the current one), self-service session listing + individual revocation ("log out that device") or bulk revocation ("log out all other devices") |
| Tenancy (organizations, membership) | IMPLEMENTED | Rename/change slug, add/remove members (`organization.manage`), self-service leave for any member; refuses to remove or let leave an organization's last remaining `owner` |
| RBAC | IMPLEMENTED | Seeded owner/admin/member roles plus org-scoped custom roles, granular permission catalog; list/create/delete roles, list members, add an existing account as a member, and assign/revoke a member's role via API + UI (`organization.manage`, no privilege escalation) |
| Audit log | IMPLEMENTED | Append-only, tenant-scoped query, filterable by resource type/id, action, and time range (`?from=`/`?to=`, RFC3339) |
| Infrastructure (nodes) | IMPLEMENTED | Provider-agnostic node inventory (register/list/get/update), status reporting (what a future Node Agent heartbeat would call) + decommission (terminal, row kept for history) |
| Applications/services | IMPLEMENTED | Registration/inventory, editable fields, status reporting, deregistration (terminal, row kept for history) — no deployment execution yet |
| API tokens | IMPLEMENTED | User-owned or service-account-owned, scope-limited (cannot exceed creator's own permissions); self-owned rename (metadata only — scopes fixed at mint time); org-admin can list/revoke any token in the org; create/update/revoke write real audit entries (never the raw token value) |
| Service accounts | IMPLEMENTED | Create/list/update (name/description)/enable/disable/permanently delete, gated by `organization.manage`; hold their own scoped API tokens. Disable revokes every outstanding token immediately and permanently — re-enabling never restores them. Delete requires disabled first and cascades to every token the account ever held. Full lifecycle writes real audit entries |
| Jobs | IMPLEMENTED | Postgres-backed queue + `FOR UPDATE SKIP LOCKED` worker; cancel (queued) and retry (failed, resets for another full attempt cycle); enqueue/cancel/retry write real audit entries; no job types registered yet beyond what callers enqueue |
| AI provider/model registry | IMPLEMENTED | Platform-wide (not org-scoped by design), managed via API (`ai.manage`): upsert + permanent delete (a provider delete cascades to its models); seeded `local-echo` test provider + auto-registered `ollama` when configured |
| AI profiles/routing/usage | IMPLEMENTED | Create/list/update/delete org-owned profiles (`ai.manage`); deterministic router with enforced privacy-level policy, real usage tracking queryable via the API (`GET /ai/usage`, `ai.use`, filterable by `profile_key`/`provider_key`) now, not just written; a registry row with no registered Go adapter correctly fails closed rather than fabricating a response. A profile's `key` is immutable once created; Update/Delete only reach org-owned profiles, not system-defined (`organization_id IS NULL`) ones |
| AI provider adapters | IMPLEMENTED (Ollama + Anthropic + OpenAI) | `local-echo` (test) + real Ollama (`NODERA_OLLAMA_BASE_URL`), Anthropic (`NODERA_ANTHROPIC_API_KEY`), and OpenAI (`NODERA_OPENAI_API_KEY`) adapters |
| Secrets | IMPLEMENTED | AES-256-GCM encrypted at rest; values never exposed over HTTP, only `Reveal`-able in-process; set/list/delete plus update-description-without-resupplying-the-value; optional at config level |
| Tool Gateway + approvals | IMPLEMENTED | Permission → risk-tier → approval → execution → audit pipeline is real, with expiration (24h default, per-organization per-tool overrides via `tools.manage`, bounded 5m–30d); a pending approval can be self-cancelled by its own requester (no `approvals.decide` needed) as well as approved/rejected; `get_server_metrics` and `check_ssl` (a genuine live TLS check) have real handlers, every other seeded tool honestly reports `NOT_IMPLEMENTED` |
| Agent identity + scoped execution | IMPLEMENTED | Create/list/update/enable/disable/delete agent definitions (`agents.manage`); scoped chat (`Run`, drives `system_instructions` through the AI gateway under the agent's own `permission_scope`) and scoped tool execution (`ExecuteTool`, enforces `allowed_tool_keys` then reuses the real Tool Gateway pipeline) — `permission_scope` can never exceed the caller's own permissions, re-checked on every update, not just at creation. Delete is a hard delete and requires the agent to be disabled first. No autonomous/LLM-directed tool selection: a caller always names the tool (rule 38) |
| Rate limiting | IMPLEMENTED | `/auth/login` (5/5min/IP), `/auth/signup` (3/hour/IP), `/ai/chat` and `/agents/{id}/run` (60/min/org, sharing the same per-org budget — an agent's scoped chat drives the identical AI-gateway cost path, so it can't be used to bypass `/ai/chat`'s limit), `/organizations` create (10/hour/user), `/account/password` (5/5min/user — the only credential-verification endpoint that had no throttle); in-process by default, or Redis-backed and shared across instances when `NODERA_REDIS_URL` is set; other endpoints remain unlimited |
| Secure headers | IMPLEMENTED | `nosniff`, `DENY`, `no-referrer`, `no-store` on every response |
| CI | IMPLEMENTED | `gofmt`/`vet`/`build`/`test -race`/`govulncheck` (API) + `typecheck`/`build`/`npm audit` (web) on every push |
| OpenAPI spec + Swagger UI | IMPLEMENTED | `GET /openapi.json` (validated against the OpenAPI 3.0 schema in CI) + `GET /docs`; hand-maintained (not yet generated from Go code — see docs/ROADMAP.md Phase 50), but every response schema now declares `required`, and `web/`'s frontend types are generated straight from it (`lib/types.ts` is a thin alias layer over `lib/api-types.generated.ts`, not a hand-copied shape) |
| Pagination | IMPLEMENTED (4 endpoints) | `infrastructure/nodes`, `applications`, `jobs`, `audit` — `limit`/`offset` + a `has_more` envelope; other list endpoints stay unpaginated (small at phase-1 scale) |
| Dashboard / frontend | IMPLEMENTED | Next.js + TypeScript control plane UI (`web/`) — login, org picker, infrastructure, applications, jobs, tools/approvals, agents, AI Gateway (chat/profiles/providers/models), secrets, access (API tokens + service accounts), settings (roles + member role assignment), audit; every page reads/writes real API data, no fabricated placeholders. Jobs and Approvals refresh automatically via background polling (5s/7s) |
| Node Agent, AI Gateway, Agent Runtime as separate services | PLANNED | Currently packages inside the one Core API binary (ADR-002) |

## Repository layout

See `docs/ARCHITECTURE.md` §2.

## Local development

Requirements: Go 1.27+, Docker (for Postgres/Redis), Node.js 20+.

```bash
cp .env.example .env
docker compose up -d postgres redis   # or: docker-compose up -d postgres redis
```

Run the API (reads `NODERA_*` env vars — see `.env.example`):

```bash
cd api
export $(grep -v '^#' ../.env | xargs)   # or use direnv/your own loader
go run ./cmd/server
```

The server applies migrations automatically on startup. Check it's up:

```bash
curl localhost:8080/health
curl localhost:8080/ready
```

Browse the API at http://localhost:8080/docs (Swagger UI) or fetch the raw
spec at `/openapi.json`.

### Frontend (`web/`)

```bash
cd web
cp .env.local.example .env.local   # points at http://localhost:8080 by default
npm install
npm run dev
```

Open http://localhost:3000. The API must be running and must allow this
origin — the default `NODERA_CORS_ORIGINS=http://localhost:3000` already
does. `npm run build` produces a production build; `npm run typecheck`
runs `tsc --noEmit` on its own; `npm run gen:types` regenerates
`lib/api-types.generated.ts` from the API's OpenAPI spec.

### Running tests

```bash
cd api
go test ./...
```

Integration tests (identity/tenancy/rbac/audit/infrastructure against a real
Postgres) are skipped automatically unless `NODERA_TEST_DATABASE_URL` is set:

```bash
docker exec <postgres-container> psql -U nodera -d nodera -c "CREATE DATABASE nodera_test;"
export NODERA_TEST_DATABASE_URL=postgres://nodera:nodera_dev_password@localhost:5432/nodera_test?sslmode=disable
go test ./...
```

## Documentation

- [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) — system shape, module boundaries, request flow
- [docs/FRONTEND.md](docs/FRONTEND.md) — web/ structure, auth model
- [docs/DECISIONS.md](docs/DECISIONS.md) — ADRs
- [docs/SECURITY.md](docs/SECURITY.md) — auth, RBAC, tenant isolation, secrets handling
- [docs/DATABASE.md](docs/DATABASE.md) — schema overview, migration workflow
- [docs/API.md](docs/API.md) — API conventions and current endpoints
- [docs/AI_ARCHITECTURE.md](docs/AI_ARCHITECTURE.md) — AI gateway/provider/routing design
- [docs/AGENTS.md](docs/AGENTS.md) — agent/tool/approval design
- [docs/INFRASTRUCTURE.md](docs/INFRASTRUCTURE.md) — node/provider model
- [docs/DEPLOYMENT.md](docs/DEPLOYMENT.md) — deployment notes, required config, Hetzner placeholders
- [docs/ROADMAP.md](docs/ROADMAP.md) — prioritized next steps
