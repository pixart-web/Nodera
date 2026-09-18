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
| Identity (signup/login/sessions) | IMPLEMENTED | Argon2id passwords, opaque revocable sessions |
| Tenancy (organizations, membership) | IMPLEMENTED | |
| RBAC | IMPLEMENTED | Seeded owner/admin/member roles, granular permission catalog |
| Audit log | IMPLEMENTED | Append-only, tenant-scoped query |
| Infrastructure (nodes) | IMPLEMENTED | Provider-agnostic node inventory (register/list/get) |
| Applications/services | IMPLEMENTED | Registration/inventory only — no deployment execution yet |
| API tokens | IMPLEMENTED | User-owned, scope-limited (cannot exceed creator's own permissions); service-account-issued tokens are PLANNED |
| Jobs | IMPLEMENTED | Postgres-backed queue + `FOR UPDATE SKIP LOCKED` worker; no job types registered yet beyond what callers enqueue |
| AI provider/model registry | FOUNDATION ONLY | Real schema + seeded `local-echo` test provider; no production provider adapter |
| AI profiles/routing/usage | IMPLEMENTED | Deterministic router with enforced privacy-level policy, real usage tracking — backed only by the `local-echo` test provider so far |
| Secrets | IMPLEMENTED | AES-256-GCM encrypted at rest; values never exposed over HTTP, only `Reveal`-able in-process; optional at config level |
| Agents/Tools/Approvals | FOUNDATION ONLY | Schema + tool catalog registered as `implemented=false`; no execution backend |
| Rate limiting | IMPLEMENTED | In-process, `/auth/login` only (5/5min per IP); other endpoints and a multi-instance-safe (Redis) limiter are PLANNED |
| Secure headers | IMPLEMENTED | `nosniff`, `DENY`, `no-referrer`, `no-store` on every response |
| CI | IMPLEMENTED | `gofmt`/`vet`/`build`/`test -race` on every push, via GitHub Actions |
| Dashboard / frontend | IMPLEMENTED | Next.js + TypeScript control plane UI (`web/`) — login, org picker, infrastructure, applications, jobs, secrets, audit; every page reads/writes real API data, no fabricated placeholders |
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
runs `tsc --noEmit` on its own.

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
