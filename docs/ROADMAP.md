# Roadmap

Reflects the priority order in the product brief §37, adjusted for what's
now actually done. Not a committed schedule — a prioritized punch list.

## Done

**Phase 1** — foundation: application skeleton, database, identity/auth,
tenancy, RBAC, audit, infrastructure (nodes) domain. See git history for
the full breakdown; the point-in-time detail isn't worth duplicating here.

**Phase 2**: applications domain, user-owned scope-limited API tokens, a
real Postgres-backed jobs worker, and the AI Gateway HTTP surface with a
deterministic, privacy-policy-enforcing router (backed by the `local-echo`
test provider).

**Phase 3** — this pass:
- [x] Secrets module — AES-256-GCM encryption at rest, masked metadata only
      over HTTP, `Reveal` callable only from in-process Go code, tenant
      isolation, optional at the config level (rule 36)
- [x] Login rate limiting — in-process, IP-keyed, 5/5min on `/auth/login`
- [x] Secure response headers (`nosniff`, `DENY`, `no-referrer`, `no-store`)
- [x] CI pipeline (`.github/workflows/ci.yml`): gofmt, vet, build,
      `test -race` against a real Postgres service container
- [x] Tests: secrets (set/list/delete/reveal, tenant isolation, wrong-key
      decryption failure), rate limiter (window behavior, per-key
      independence) — all passing with `-race`

## Next up

1. **Frontend skeleton** (`web/`) — login, organization picker, node/app
   list, job list, secrets metadata list, audit log view. Talks to real
   endpoints only; no fabricated dashboard data (rule 36).
2. **First real AI provider adapter** (likely Ollama, since it needs no
   cloud credential to develop against) behind the `providers.Provider`
   interface, resolving its credential through `internal/secrets` if it
   needs one.
3. **Tool Gateway execution backend** for at least one `read`-risk tool
   (`get_server_metrics`), including the approval flow for a `privileged`
   one — the `agents`/`tools`/`approvals` schema is ready, no Go domain
   package exists yet.
4. **API token target scoping**: today a user can only revoke their own
   tokens and only user-owned tokens exist — service-account-issued tokens
   and org-admin management of other users' tokens are deferred (see
   `internal/identity/apitoken.go`).
5. **Concrete job types**: the worker dispatcher is real but nothing
   enqueues a `backup.create` or `deploy.application` job yet — those
   arrive with the backups/deployment domains.
6. **Rate limiting beyond `/auth/login`**, and a Redis-backed limiter for
   multi-instance deployments (today's limiter is in-process only).
7. **Dependency vulnerability scanning** in CI (`govulncheck` or similar).
8. **OpenAPI/Swagger generation** and list-endpoint pagination.

## Explicitly not started (rule 38 — deferred by design)

Kubernetes support, multi-region orchestration, GPU auto-provisioning,
ML-based AI routing, model fine-tuning, unrestricted autonomous agents,
complete CyberAudit/Web Content Flow/Kiko functionality.
