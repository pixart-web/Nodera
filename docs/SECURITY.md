# Security

Status: the controls below marked IMPLEMENTED are real and covered by
`internal/integration_test.go` and `internal/rbac`, `internal/identity` unit
tests. Everything else is FOUNDATION ONLY or PLANNED — see `README.md`.

## Authentication — IMPLEMENTED

- Passwords hashed with **argon2id** (`internal/identity/password.go`),
  64 MB memory, self-describing encoded hash so parameters can change without
  invalidating existing hashes.
- Sessions are **opaque, server-side, instantly revocable** tokens
  (`sessions` table) — only the SHA-256 hash of the token is stored, never
  the token itself. See [ADR-005](DECISIONS.md#adr-005-sessionstokens-via-first-party-identity-module-no-external-idp-dependency-in-the-foundation)
  for why this was chosen over JWTs.
- Minimum password policy: 12+ characters, must mix letters with a digit or
  symbol (`internal/identity/validate.go`). No arbitrary composition rules.

## Authorization — IMPLEMENTED

- All permission checks go through `rbac.Require(ac, permission)`
  (`internal/rbac/rbac.go`) — there is no ad-hoc `if user.IsAdmin` anywhere
  in the domain layer.
- Permissions are resolved once per request from the actor's role grants
  within the organization they're acting in (`rbac.ResolvePermissions`),
  attached to the `authctx.AuthContext` passed explicitly into every domain
  service call.
- A `system` actor type (`authctx.System`) bypasses permission checks — it is
  only ever constructed internally (migrations, scheduled jobs), never
  derived from a client request, so this cannot be spoofed over HTTP.

## Tenant isolation — IMPLEMENTED

- Every tenant-scoped table has a non-null `organization_id`.
- Every tenant-scoped repository method takes `authctx.AuthContext` and
  filters by `ac.OrganizationID` server-side — there is no code path where a
  client-supplied organization filter widens a query. Verified by
  `internal/integration_test.go`'s cross-tenant isolation case (an owner in
  one org cannot see infrastructure registered under another org they also
  own).
- The HTTP layer requires an explicit `X-Nodera-Org` header and verifies
  session-holder membership in that org before any domain call runs
  (`cmd/server/middleware.go: requireOrganization`).

## Secrets — PLANNED

No production secret is ever stored in a normal database column. Today
nothing depends on production secrets yet (only local-dev Postgres/Redis
credentials, which live in `.env`, gitignored). The `secrets` module
(reference-based secret storage, masking, rotation) is not yet implemented —
see `README.md` status table. AI provider credentials will be referenced,
never stored inline in `ai_providers.config` (that column is documented as
non-secret config only — see migration `0006_ai.sql`).

## Input validation

- Request bodies are decoded into typed structs; domain services validate
  (email format, password strength, hostname presence, slug format, etc.)
  before touching the database — see e.g. `internal/identity/validate.go`,
  `internal/tenancy/tenancy.go`.
- SQL is exclusively parameterized via pgx (`$1`, `$2`, ...) — no string
  interpolation into queries anywhere in the codebase.

## Error handling — IMPLEMENTED

- `internal/platform/apierr` defines a normalized error type with a stable
  machine-readable `Code`. `internal/platform/httpserver.WriteError` never
  serializes a raw driver/vendor error or stack trace to the client — only
  `Code`, `Message`, and the request's correlation ID. Internal errors are
  logged server-side with full detail.

## Audit — IMPLEMENTED

- `internal/audit` is append-only by API design (no `Update`/`Delete`
  method exists). True DB-level immutability (revoking `UPDATE`/`DELETE`
  from the application's Postgres role) is deferred to `docs/DEPLOYMENT.md`
  since it depends on the still-unfinalized production role layout.
- Sensitive operations (e.g. node registration) call `audit.Record` with
  actor, action, resource, and resulting state.

## Not yet implemented / deliberately deferred

- CSRF protection (not yet relevant — no cookie-based auth flow exists; the
  session token is a bearer token, not a cookie, so CSRF is out of scope
  until a cookie-based web session flow is added)
- Rate limiting / brute-force protection on `/auth/login`
- Secure headers middleware (HSTS, CSP, etc.) for the eventual frontend
- API token issuance/validation (schema exists, no endpoints)
- Upload validation (no upload endpoints exist yet)
- Dependency vulnerability scanning in CI (no CI pipeline exists yet)

These are tracked in `docs/ROADMAP.md`, not silently skipped.
