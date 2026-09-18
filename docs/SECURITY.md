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

**Deliberate exception:** the AI provider/model registry
(`ai_providers`/`ai_models`, managed via `internal/ai/registry.go` and
`/api/v1/ai/providers`, `/api/v1/ai/models`) is platform-wide by design —
these tables carry no `organization_id` (migration `0006_ai.sql`), matching
the product brief's model of a centralized, shared registry underneath
per-org `ai_profiles` policy. Any caller with `ai.manage` in *any*
organization can affect this shared registry. This is a documented
phase-1 simplification, not an oversight — see `docs/API.md`.

## Secrets — IMPLEMENTED

`internal/secrets` stores values AES-256-GCM encrypted at rest
(`secrets.ciphertext`), keyed by `NODERA_SECRETS_ENCRYPTION_KEY` (a
32-byte, base64-encoded key that itself never touches the database — an
env var only). The module is optional at the config level: if the key is
unset, its endpoints return `UNAVAILABLE` rather than the server failing to
start or fabricating success (rule 36).

- **Masking**: `List`/`Set` only ever return `Meta` (key, description,
  timestamps) — the plaintext value is never serialized in an HTTP
  response, and there is no HTTP endpoint that returns it at all.
- **Reveal**: the only way to recover plaintext is `Service.Reveal`, callable
  only from Go code in-process (e.g. a future AI provider adapter resolving
  its own credential) — never wired to any HTTP handler.
- **Tenant isolation**: every method takes `authctx.AuthContext` and scopes
  by `ac.OrganizationID`, same as every other domain (ADR-004). Verified by
  `internal/secrets_integration_test.go`'s cross-tenant case.
- **Authenticated encryption**: GCM detects tampering or a wrong key rather
  than returning garbled plaintext — verified by
  `TestSecretsWrongKeyFailsToDecrypt`.

AI provider credentials, when a real provider adapter is added, will be
resolved through this module by reference — never stored inline in
`ai_providers.config` (that column is documented as non-secret config only —
see migration `0006_ai.sql`). A proper external KMS/vault integration
remains future work; this is deliberately a phase-1-appropriate,
self-contained implementation.

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

## Rate limiting — IMPLEMENTED (login only)

`internal/platform/ratelimit` is a simple in-process fixed-window limiter
(5 attempts per 5 minutes per client IP), applied to `POST /auth/login`
(`cmd/server/router.go`). It is deliberately in-process, not Redis-backed —
adequate for a single API instance; a multi-instance deployment will need a
shared limiter (tracked in `docs/ROADMAP.md`). Keyed by IP rather than the
submitted email, so an attacker can't use the endpoint to lock out a victim
account by exhausting *their* budget (a form of denial-of-service the naive
per-email design would enable).

## Secure headers — IMPLEMENTED

`httpserver.SecurityHeaders` sets `X-Content-Type-Options: nosniff`,
`X-Frame-Options: DENY`, `Referrer-Policy: no-referrer`, and
`Cache-Control: no-store` on every response. No CSP is set — this server
returns only JSON, never HTML, so there's no inline-script surface for CSP
to restrict yet; one will be added if/when the API ever serves any HTML.

## CI — IMPLEMENTED

`.github/workflows/ci.yml` runs on every push/PR: `gofmt -l` (must be
empty), `go vet`, `go build`, and `go test ./... -race` against a real
Postgres service container. Dependency vulnerability scanning
(`govulncheck` or similar) is not yet wired in — tracked in
`docs/ROADMAP.md`.

## CORS — IMPLEMENTED

`internal/platform/httpserver.CORS` reflects back only an allow-listed
origin (`NODERA_CORS_ORIGINS`, default `http://localhost:3000` for the
`web/` dev server) — never `*`, since `Authorization` headers are in play.
Production deployments must set this explicitly to their real frontend
origin(s) (`docs/DEPLOYMENT.md`).

## Known dependency finding: `web/` transitive PostCSS advisories

`npm audit` flags PostCSS (bundled inside Next.js's own build pipeline,
not a direct dependency) for XSS/path-traversal issues in its CSS
stringifier and sourcemap loader. These apply to processing *untrusted*
CSS at build time — `web/`'s build only ever processes its own
repository's CSS, so the practical exposure here is effectively nil. The
fix requires a Next.js 16 major upgrade (breaking change), deliberately
not taken during this foundation-building pass; tracked in
`docs/ROADMAP.md`.

## Not yet implemented / deliberately deferred

- CSRF protection (not yet relevant — no cookie-based auth flow exists; the
  session token is a bearer token, not a cookie, so CSRF is out of scope
  until a cookie-based web session flow is added)
- Rate limiting on endpoints other than `/auth/login` (e.g. `/auth/signup`)
- Upload validation (no upload endpoints exist yet)
- Dependency vulnerability scanning in CI
- External KMS/vault integration for secrets (current implementation is a
  self-contained AES-256-GCM scheme — see Secrets above)

These are tracked in `docs/ROADMAP.md`, not silently skipped.
