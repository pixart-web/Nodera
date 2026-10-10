# Development

## Run everything locally

```bash
git clone <repo> && cd Nodera
cp .env.example .env          # defaults work; nothing here is a real secret
docker compose up --build     # postgres, redis, api (:8080), web (:3000)
```

Open http://localhost:3000, create an account, create an organisation.

### Provider modes (`NODERA_PROVIDER_MODE`)

| Mode | What is real | Use it for |
|---|---|---|
| `local` (default) | filesystem, backups (tar.gz + SHA-256), certificates (real X.509 from a local CA), DNS zone store | everyday development; engines that need containers/databases/git **refuse to run** ("provider not configured") |
| `docker` | `local` + real containers through the Docker CLI (socket mount required) | trying provisioning against a real runtime |
| `mock` | nothing: every provider is in-memory | demos and UI work. Refused when `NODERA_ENV=production`. The UI shows a MODO DEMO banner and `/system/info` reports every capability as `mock` |

### Demo mode

```bash
NODERA_PROVIDER_MODE=mock NODERA_DEMO_SEED=true docker compose up --build
```

On every start the API rebuilds an organisation named **DEVELOPMENT (demo data)**
by driving the real services and operations (so history, steps and logs are
genuine) against the in-memory providers. Sign in as `demo@nodera.local`; set
`NODERA_SEED_PASSWORD` or read the generated password from the API log. Nothing
in demo mode touches real infrastructure.

### Keys in development

`NODERA_SECRETS_ENCRYPTION_KEY` and `NODERA_AGENT_SIGNING_KEY` are **required in
production** (startup fails without them). In development they are generated
once and persisted in `NODERA_DATA_DIR` (`dev-secrets.key`, `dev-agent-signing.key`, mode 0600)
so a restart does not make stored secrets undecryptable or invalidate enrolled
agents. Generate real ones with `openssl rand -base64 32`.

## Without Docker

```bash
docker compose up -d postgres redis
cd api && go run ./cmd/server          # reads .env values from the environment
cd web && npm ci && npm run dev
```

## Tests

```bash
export NODERA_TEST_DATABASE_URL=postgres://nodera:nodera_dev_password@localhost:5432/nodera_test?sslmode=disable
cd api && go vet ./... && go test ./... -race -p 1     # -p 1 is required: tests share one DB
cd web && npm run typecheck && npm test && npm run build
```

What the suites cover:

* `api/internal/*_integration_test.go` — every engine against real Postgres:
  operations (success/rollback/cancel/panic/idempotency), provisioning, backups
  (real tar.gz round trip, corruption, safety-snapshot rollback), network/SSL
  (real local CA), deployments, migrations (zip-slip, serialized URL rewrite,
  cutover rollback), WordPress, monitoring/incidents, AI plans, demo seed.
* `api/cmd/server/*_test.go` — the real HTTP router: core/infra/delivery/observe
  APIs, SSE, rate limits, the node-agent channel, the **security regression
  suite** (`security_test.go`) and an OpenAPI drift test that fails when a route
  is registered without being documented.
* `web/tests` — pure-function unit tests (formatting, status mapping, SSE parser).
  ESLint is not configured in this repository (`next lint` prompts for setup).

## Layout

```
api/cmd/server        HTTP API + worker + maintenance loops
api/cmd/agent         Node Agent (separate binary/image)
api/internal/ops      operation framework (steps, rollback, SSE source of truth)
api/internal/providers  interfaces + mock/ local/ docker/ implementations
api/internal/{projects,provisioning,backups,network,deployments,sitemig,wordpress,
              monitoring,notifications,logs,flags,dashboard,aiplans,nodeagent,devseed}
api/migrations        0001..0031 (embedded, applied at startup)
web/                  Next.js UI; services/index.ts is the only data layer
```

## Adding an operation

1. Implement `ops.Operation` (+ `Steps()` with idempotent `Undo` per step).
2. `engine.Register(ops.Definition{Name, Permission, ToolKey?, Flag?, Factory})`.
   Anything destructive or production-affecting must set `ToolKey` so it can only
   be requested through the approval gateway (`Submit` refuses it).
3. Add the route (+ OpenAPI entry — the drift test enforces it) and an
   integration test including the failure/rollback path and a cross-tenant check.
