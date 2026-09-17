# Roadmap

Reflects the priority order in the product brief §37, adjusted for what's
now actually done. Not a committed schedule — a prioritized punch list.

## Done

**Phase 1** — foundation:
- [x] Repository inspected (was empty) and architecture defined
- [x] Application skeleton (`api/cmd/server`, modular monolith)
- [x] Database (Postgres, embedded migrations)
- [x] Identity/authentication (signup, login, opaque sessions, logout)
- [x] Organizations/tenancy (create, list, membership, tenant isolation)
- [x] RBAC (permission catalog, seeded system roles, `rbac.Require` choke point)
- [x] Audit system (append-only, tenant-scoped query)
- [x] Infrastructure domain — node inventory (register/list/get)
- [x] Docs: ARCHITECTURE, DECISIONS (ADRs), SECURITY, DATABASE, API,
      AI_ARCHITECTURE, AGENTS, INFRASTRUCTURE, DEPLOYMENT, this file

**Phase 2** — this pass:
- [x] Applications/services domain (register/list/get, mirrors infrastructure)
- [x] API tokens — user-owned, scope-limited (cannot exceed creator's own
      permissions), issuance/list/revoke endpoints, accepted as a bearer
      auth path alongside sessions
- [x] Jobs worker — real Postgres-backed dispatcher (`FOR UPDATE SKIP
      LOCKED`), handler registry, retry-up-to-max-attempts, visible failure
      for unregistered job types (never hangs, never fabricates completion)
- [x] AI Gateway HTTP surface (`/api/v1/ai/profiles`, `/api/v1/ai/chat`)
      backed by the deterministic router (profile → privacy-policy check →
      first available, policy-compliant model → provider adapter) and the
      `local-echo` test provider, with real usage tracking
- [x] Tests: integration coverage for every new domain (applications,
      API tokens including scope-escalation prevention, jobs including the
      unregistered-handler case, AI profile creation + chat round trip
      including the RESTRICTED-privacy-fails-closed case) against real
      Postgres

## Next up

1. **Frontend skeleton** (`web/`) — login, organization picker, node/app
   list, job list, audit log view. Talks to real endpoints only; no
   fabricated dashboard data (rule 36).
2. **Secrets module** — reference-based secret storage abstraction, needed
   before any real AI provider credential or infrastructure credential can
   be stored.
3. **First real AI provider adapter** (likely Ollama, since it needs no
   cloud credential to develop against) behind the `providers.Provider`
   interface already in place.
4. **Security hardening pass**: rate limiting on `/auth/login`, secure
   response headers, dependency scanning in CI once CI exists.
5. **Tool Gateway execution backend** for at least one `read`-risk tool
   (`get_server_metrics`), including the approval flow for a `privileged`
   one — the `agents`/`tools`/`approvals` schema is ready, no Go domain
   package exists yet.
6. **CI pipeline**: `go build`, `go vet`, `gofmt -l`, `go test ./...`
   (with a Postgres service container) on every push.
7. **API token target scoping**: today a user can only revoke their own
   tokens and only user-owned tokens exist — service-account-issued tokens
   and org-admin management of other users' tokens are deferred (see
   `internal/identity/apitoken.go`).
8. **Concrete job types**: the worker dispatcher is real but nothing
   enqueues a `backup.create` or `deploy.application` job yet — those
   arrive with the backups/deployment domains.

## Explicitly not started (rule 38 — deferred by design)

Kubernetes support, multi-region orchestration, GPU auto-provisioning,
ML-based AI routing, model fine-tuning, unrestricted autonomous agents,
complete CyberAudit/Web Content Flow/Kiko functionality.
