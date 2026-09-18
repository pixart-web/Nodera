# Roadmap

Reflects the priority order in the product brief §37, adjusted for what's
now actually done. Not a committed schedule — a prioritized punch list.

## Done

**Phase 1** — foundation: application skeleton, database, identity/auth,
tenancy, RBAC, audit, infrastructure (nodes) domain.

**Phase 2**: applications domain, user-owned scope-limited API tokens, a
real Postgres-backed jobs worker, and the AI Gateway HTTP surface with a
deterministic, privacy-policy-enforcing router (backed by the `local-echo`
test provider).

**Phase 3**: secrets module (AES-256-GCM at rest), login rate limiting,
secure response headers, and a CI pipeline (gofmt/vet/build/`test -race`).

**Phase 4** — this pass:
- [x] Frontend skeleton (`web/`, Next.js + TypeScript + Tailwind): login/
      signup, organization picker + create, and an org-scoped shell with
      Dashboard, Infrastructure, Applications, Jobs, Secrets, and Audit
      pages — every page reads/writes the real API, no fabricated data
      (rule 36), verified live in a browser against a running API
- [x] CORS support on the API (`internal/platform/httpserver.CORS`,
      `NODERA_CORS_ORIGINS`) so the browser-based frontend can call it
- [x] `docs/FRONTEND.md`; `README.md`, `docs/API.md`, `docs/SECURITY.md`,
      `docs/DEPLOYMENT.md` updated to match

(See git history for the detailed breakdown of each phase; not duplicated
here so it can't drift out of sync.)

## Next up

1. **First real AI provider adapter** (likely Ollama, since it needs no
   cloud credential to develop against) behind the `providers.Provider`
   interface, resolving its credential through `internal/secrets` if it
   needs one.
2. **Tool Gateway execution backend** for at least one `read`-risk tool
   (`get_server_metrics`), including the approval flow for a `privileged`
   one — the `agents`/`tools`/`approvals` schema is ready, no Go domain
   package exists yet.
3. **API token target scoping**: today a user can only revoke their own
   tokens and only user-owned tokens exist — service-account-issued tokens
   and org-admin management of other users' tokens are deferred (see
   `internal/identity/apitoken.go`).
4. **Concrete job types**: the worker dispatcher is real but nothing
   enqueues a `backup.create` or `deploy.application` job yet — those
   arrive with the backups/deployment domains.
5. **Rate limiting beyond `/auth/login`**, and a Redis-backed limiter for
   multi-instance deployments (today's limiter is in-process only).
6. **Dependency vulnerability scanning** in CI (`govulncheck` for the API;
   an equivalent for `web/`, or the Next.js 16 upgrade that resolves the
   current PostCSS advisories — `docs/SECURITY.md`).
7. **OpenAPI/Swagger generation** and list-endpoint pagination — `web/`'s
   hand-written `lib/types.ts` is the thing to replace once this lands.
8. **Frontend follow-ups**: AI profile/chat UI, approvals UI (once its
   backend exists), RBAC/settings management UI, real-time updates
   (polling or websockets) instead of load-once pages.

## Explicitly not started (rule 38 — deferred by design)

Kubernetes support, multi-region orchestration, GPU auto-provisioning,
ML-based AI routing, model fine-tuning, unrestricted autonomous agents,
complete CyberAudit/Web Content Flow/Kiko functionality.
