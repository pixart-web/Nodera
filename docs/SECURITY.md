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
- Self-service password change (`POST /api/v1/account/password`,
  `internal/identity.ChangePassword`) requires the current password and,
  on success, revokes every other active session for the account — the
  session that made the request is deliberately left alone, so the caller
  isn't logged out by their own request, but every other session (another
  device, or one an attacker holds with a since-compromised password) is
  cut off immediately.
- Self-service session management (`GET /api/v1/account/sessions`,
  `DELETE /api/v1/account/sessions/{id}`, `internal/identity.ListSessions`/
  `RevokeSession`) — a user can see every active session on their account
  (device/IP, created/expires) and revoke any one individually ("log out
  that device"), scoped so one user can never revoke another user's
  session even by guessing/enumerating a session ID.

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

## Platform vs organization authorization — IMPLEMENTED

Some resources are platform-wide, not owned by any single organization —
today, the AI provider/model registry (`internal/ai/registry.go`;
`ai_providers`/`ai_models` carry no `organization_id`). Gating a mutation
to platform-wide state with an *organization* permission (e.g. `ai.manage`)
would let any organization admin mutate global state merely by
administering their own organization — a real authorization mismatch for
a control plane. `internal/platformauth` closes it with a second,
independent authorization axis:

- `platform_permissions` is an explicit catalog table (currently
  `platform.ai.providers.manage`, `platform.ai.models.manage`,
  `platform.admins.manage`), and `platform_user_permissions` is a
  per-user, per-permission grant table (migration `0018`). There is no
  single "is platform admin" boolean — a user holds whatever subset of
  platform permissions they were explicitly granted, extensible to future
  platform-scoped resources (nodes, global config) without collapsing
  into god-mode.
- `platformauth.Service.Require(ctx, ac, key)` is platform authorization's
  single choke point, mirroring `rbac.Require`. It never consults
  `AuthContext.Permissions` (organization-scoped) or infers platform
  authority from organization ownership/role — it always queries the
  explicit grant table for the caller's own user id. Only `ActorUser`
  identities can hold platform permissions in this phase (agents/service
  accounts act within an organization's scope only); a `system` actor
  bypasses it, same exception as `rbac.Require`, which is what lets a
  configured provider auto-register at startup.
- `UpsertProvider`/`DeleteProvider`/`UpsertModel`/`DeleteModel` require
  the platform permission; `ListProviders`/`ListModels` stay gated by the
  ordinary organization permission `ai.use` — reading the registry to
  configure an org's own AI profile is not a platform-admin operation.
- Granting/revoking platform permissions (`POST`/`DELETE
  /api/v1/platform/admins/{userID}/permissions/{key}`) itself requires
  `platform.admins.manage` — not subject to the no-escalation check
  organization role assignment uses, because holding
  `platform.admins.manage` already implies full platform-admin trust (the
  same posture an organization `owner` holding every organization
  permission takes). Revoking the last remaining
  `platform.admins.manage` grant is refused, mirroring tenancy's
  "cannot remove the last owner" guard, to avoid permanently locking out
  platform administration with no recovery path short of a manual SQL fix.
- **Bootstrapping the first platform administrator**: set
  `NODERA_PLATFORM_BOOTSTRAP_ADMIN_EMAIL` to a real user's email.
  `platformauth.Service.BootstrapAdmin` runs once at every startup
  (`cmd/server/main.go`) and idempotently grants that user every catalog
  permission if they exist; a nonexistent email logs a warning rather
  than failing startup. This is deliberately not a standing authorization
  rule — nothing at request time compares an email to this value (no
  `if email == admin@...` anywhere in the request path), so removing the
  env var after first boot doesn't revoke anything, and leaving it set
  permanently just re-runs the same idempotent grant on every restart.
- Tested by `internal/platform_authorization_test.go`: an organization
  owner (holding `ai.manage`) is forbidden from mutating the platform
  registry without an explicit grant; a granted user can; a platform
  grant never widens organization membership/permissions in another
  organization (tenant isolation is unaffected); grant/revoke require
  `platform.admins.manage`; the last-admin guard; `BootstrapAdmin`'s
  idempotency.

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
- `internal/identity`'s service-account and API-token lifecycle
  (create/disable/enable/update, create/revoke) is audited the same way —
  a raw API token value is never included in the recorded state, only its
  prefix. `SignUp`/`Login`/`Logout`/`ChangePassword`/session management
  are deliberately NOT audited: those run before an organization is
  selected (`requireSession`, not `requireOrganization`), and
  `audit.Query` always scopes by `organization_id`, so a NULL-org entry
  would be written but could never be read back through any existing API
  surface — writing unverifiable, effectively invisible rows was judged
  worse than not writing them (`docs/ROADMAP.md` Phase 41).

## Rate limiting — IMPLEMENTED (login, signup, AI chat, org creation, password change)

`internal/platform/ratelimit` applies fixed-window limits
(`cmd/server/router.go`) to:
- `POST /auth/login` — 5 attempts / 5 minutes per client IP
- `POST /auth/signup` — 3 attempts / hour per client IP
- `POST /api/v1/ai/chat` and `POST /api/v1/agents/{id}/run` — 60 requests
  / minute **per organization, sharing one budget between the two**
- `POST /api/v1/organizations` — 10 organizations / hour **per user**
- `POST /api/v1/account/password` — 5 attempts / 5 minutes **per user**

Login/signup are keyed by IP rather than the submitted email, so an
attacker can't use either endpoint to lock out a victim account/address by
exhausting *their* budget (a form of denial-of-service the naive per-email
design would enable). AI chat is keyed by organization instead — the real
risk there isn't login lockout, it's one tenant (a careless or compromised
integration) running up real provider cost or crowding out other tenants
on a shared local model; an IP-keyed limiter wouldn't even bound that
(many legitimate calls can share an IP behind NAT). `agents/{id}/run`
draws from that identical limiter (same `Allower`, same organization key)
rather than a limiter of its own, since it drives the exact same
`ai.Service.Chat` cost path — a separate budget there would have let
`agents.execute` bypass `ai.chat`'s cost control entirely. Verified live:
60 rapid calls to `agents/{id}/run` succeed and the 61st returns a real
`429`, and a subsequent call to `ai/chat` for the same organization is
also `429`, proving the two endpoints share state rather than each
tolerating 60 of their own. Organization creation
is keyed by the calling user's ID rather than IP — unlike login/signup,
it's only reachable once authenticated, so the actor is already known and
stable; bounds spam-organization creation by any single account.
`account/password` is likewise keyed by user ID: it re-verifies the
caller's current password on every call, so without a throttle a
stolen/leaked session token would let an attacker brute-force the
account's real password (useful for credential reuse elsewhere) with no
friction — the only credential-verification endpoint that had no rate
limit until this pass. Verified live: 5 rapid attempts with a wrong
current password each return a real `401`, the 6th returns a real `429`,
and a different account's own budget is unaffected (confirmed with a
fresh account hitting the same endpoint immediately afterward and getting
a normal `401`, not `429`).

Two interchangeable implementations exist behind the same `Allower`
interface, chosen at startup (`cmd/server/main.go: newRateLimiters`):
- **`Limiter`** (in-process, no dependencies) — the default when
  `NODERA_REDIS_URL` is unset. Correct for a single API instance; counters
  don't survive a restart and aren't shared with any other instance.
- **`RedisLimiter`** — used automatically when `NODERA_REDIS_URL` is set,
  verified reachable with a startup `PING` (a misconfigured URL fails
  startup rather than silently falling back to the in-process limiter).
  Counters are shared across every API process instance pointed at the
  same Redis, so the limit is enforced deployment-wide, not per-process.
  On a Redis error mid-request it **fails open** (allows the call, logs
  the error) rather than turning a Redis blip into a full login/signup/AI
  outage for every legitimate user — see `internal/platform/ratelimit/redis.go`.

## Secure headers — IMPLEMENTED

`httpserver.SecurityHeaders` sets `X-Content-Type-Options: nosniff`,
`X-Frame-Options: DENY`, `Referrer-Policy: no-referrer`, and
`Cache-Control: no-store` on every response. No CSP is set — this server
returns only JSON, never HTML, so there's no inline-script surface for CSP
to restrict yet; one will be added if/when the API ever serves any HTML.

## CI — IMPLEMENTED

`.github/workflows/ci.yml` runs on every push/PR:
- **API**: `gofmt -l` (must be empty), `go vet`, `go build`,
  `go test ./... -race` against a real Postgres service container,
  `govulncheck` (fails the build on a known-exploitable vulnerability
  reachable from Nodera's own code — not merely present in a dependency
  tree, which is a much noisier signal), and `@redocly/cli lint` against
  `api/openapi/openapi.json` (fails only on structural OpenAPI errors, not
  style warnings).
- **Web**: `tsc --noEmit`, `next build`, and `npm audit --audit-level=critical`
  (fails only on `critical` — the known high-severity PostCSS finding below
  stays visible in the log without blocking every PR on an issue that
  needs a Next.js major upgrade to fully resolve).

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
- Rate limiting on endpoints beyond `/auth/login`, `/auth/signup`,
  `/ai/chat` (shared with `/agents/{id}/run`), `/organizations`, and
  `/account/password` — every other endpoint remains unlimited (most
  mutations beyond these are already `organization.manage`-gated,
  which meaningfully narrows who can even attempt abuse)
- Upload validation (no upload endpoints exist yet)
- External KMS/vault integration for secrets (current implementation is a
  self-contained AES-256-GCM scheme — see Secrets above)

These are tracked in `docs/ROADMAP.md`, not silently skipped.
