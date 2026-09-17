# Roadmap

Reflects the priority order in the product brief §37, adjusted for what's
now actually done. Not a committed schedule — a prioritized punch list.

## Done (phase 1, this pass)

- [x] Repository inspected (was empty) and architecture defined
- [x] Application skeleton (`api/cmd/server`, modular monolith)
- [x] Database (Postgres, embedded migrations, 7 migrations covering
      identity, rbac, audit, infrastructure, jobs, ai, agents schema)
- [x] Identity/authentication (signup, login, opaque sessions, logout)
- [x] Organizations/tenancy (create, list, membership, tenant isolation)
- [x] RBAC (permission catalog, seeded system roles, `rbac.Require` choke point)
- [x] Audit system (append-only, tenant-scoped query)
- [x] Infrastructure domain — node inventory only (register/list/get)
- [x] Tests: unit (identity password/validation, rbac) + integration against
      real Postgres (full signup→login→org→node→audit flow, cross-tenant
      isolation, RBAC-denied case)
- [x] Docs: ARCHITECTURE, DECISIONS (ADRs), SECURITY, DATABASE, API,
      AI_ARCHITECTURE, AGENTS, INFRASTRUCTURE, DEPLOYMENT, this file

## Next up

1. **Applications/services domain** — schema + register/list, mirroring the
   infrastructure domain's shape.
2. **API tokens** — issuance/validation endpoints for the already-seeded
   `api_tokens`/`service_accounts` schema, so non-human actors can call the
   API.
3. **Jobs worker** — an actual dispatcher consuming `jobs` rows (Postgres
   `FOR UPDATE SKIP LOCKED` is a reasonable first implementation before
   introducing a Redis-backed queue).
4. **AI Gateway HTTP surface** (`/api/v1/ai/*`) backed by the already-seeded
   `local-echo` provider, plus the deterministic router (profile → policy →
   model selection) — real logic, still no production provider yet.
5. **Frontend skeleton** (`web/`) — login, organization picker, node list,
   audit log view. Talks to real endpoints only; no fabricated dashboard
   data (rule 36).
6. **Secrets module** — reference-based secret storage abstraction, needed
   before any real AI provider credential or infrastructure credential can
   be stored.
7. **First real AI provider adapter** (likely Ollama, since it needs no
   cloud credential to develop against) behind the provider interface.
8. **Security hardening pass**: rate limiting on `/auth/login`, secure
   response headers, dependency scanning in CI once CI exists.
9. **Tool Gateway execution backend** for at least one `read`-risk tool
   (`get_server_metrics`) end to end, including the approval flow for a
   `privileged` one.
10. **CI pipeline**: `go build`, `go vet`, `gofmt -l`, `go test ./...`
    (with a Postgres service container) on every push.

## Explicitly not started (rule 38 — deferred by design)

Kubernetes support, multi-region orchestration, GPU auto-provisioning,
ML-based AI routing, model fine-tuning, unrestricted autonomous agents,
complete CyberAudit/Web Content Flow/Kiko functionality.
