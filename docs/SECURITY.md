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

## Browser authentication — IMPLEMENTED

The opaque session token from ADR-005 is unchanged; what changed is how
the **human browser** carries it, versus how a machine/API client does.

**Human browser session: HttpOnly cookie.** `POST /auth/login` sets the
session token as an `HttpOnly`, `Path=/`, `SameSite=Lax` cookie
(`nodera_session` — `internal/platform/httpserver/cookies.go`), `Secure`
whenever `NODERA_ENV=production` (a plain-HTTP dev server cannot set a
browser-honored `Secure` cookie — a deliberate, documented dev-mode
exception, rule 23). `HttpOnly` means no JavaScript running on the page —
including an XSS payload — can ever read it via `document.cookie`; the
`web/` frontend (`lib/api.ts`) never reads or stores the raw token at all,
relying entirely on the browser attaching the cookie automatically.
`SameSite=Lax` (not `Strict`) still lets the cookie ride along on the
frontend's own top-level navigations and same-site `fetch`/XHR calls — the
API and the web app are meant to share a registrable domain in production
(e.g. `api.nodera.io` / `app.nodera.io`), which the `SameSite` spec treats
as "same-site" despite being different origins, and `localhost` at any
port is likewise treated as same-site by browsers in dev — while
withholding the cookie from genuinely cross-site requests.

**Machine/API client: `Authorization: Bearer`, unchanged.** `POST
/auth/login`'s JSON response still includes `session_token` in the body,
specifically for a non-browser caller (CLI, script) that needs the raw
value to send as `Authorization: Bearer <token>`. `cmd/server/middleware.go`'s
`requireSession` checks the `Authorization` header first and only falls
back to the cookie when no bearer header is present, so a machine client
is completely unaffected by any of this. An API token
(`POST /api-tokens`) was always Bearer-only and stays that way; it never
gets a cookie.

**CSRF protection: double-submit cookie, not just `SameSite`.** `SameSite`
alone is not treated as sufficient protection here — a subdomain
takeover, a misconfigured proxy, or a browser with a nonstandard
`SameSite` implementation would otherwise be a single point of failure.
Login also sets a second cookie, `nodera_csrf` (deliberately **not**
`HttpOnly` — the frontend must be able to read it), containing a fresh
random token. Every cookie-authenticated state-changing request
(`POST`/`PUT`/`PATCH`/`DELETE` — `GET`/`HEAD`/`OPTIONS` are exempt, since
those must have no side effects to begin with) must echo that value back
in an `X-CSRF-Token` header (`httpserver.VerifyCSRF`, a constant-time
comparison); a mismatch or missing header is a `403 FORBIDDEN`. This is
checked *only* for cookie-authenticated requests — a Bearer-authenticated
request is inherently immune to CSRF (nothing attaches an `Authorization`
header automatically the way a browser attaches cookies), so it never
needs one and is unaffected. The security property: an attacker's
cross-site page can trigger the browser to send a forged request with the
session/CSRF cookies attached automatically, but same-origin policy
prevents that page's JavaScript from ever reading the CSRF cookie's
*value* to put in the header — so it cannot produce a request that passes
the check.

**CORS.** `Access-Control-Allow-Credentials: true` is required for the
browser to send/accept the cookie cross-origin, which the Fetch spec
only permits paired with an exact reflected origin, never `"*"`
(`internal/platform/httpserver/httpserver.go`'s `CORS` middleware already
only ever reflects an allow-listed origin — see "CORS" below — so this
was a safe addition, not a new relaxation).

**Tested by** `cmd/server/cookie_csrf_test.go` end to end against the real
HTTP router (not just at the unit level): login sets both cookies; a
cookie-authenticated mutating request without `X-CSRF-Token` is
`403`-rejected; the same request with the correct header succeeds; a
wrong token is rejected; `GET` needs no CSRF header; a Bearer-token
request needs no CSRF header either and still works; logout clears both
cookies; CORS reflects the exact origin with credentials enabled. Also
live-verified through the real browser UI: `document.cookie` after login
shows only the CSRF cookie (the session cookie is genuinely invisible to
JS), `localStorage` holds only a non-sensitive "who's logged in" hint,
and a real mutating request (creating an organization) succeeds end to
end through the cookie + CSRF-header flow.

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

## Outbound network / SSRF policy — IMPLEMENTED

`check_ssl` (`internal/tools/handlers/checkssl.go`) is reachable by any
organization member holding `infrastructure.read` and takes an arbitrary
caller-supplied hostname — an SSRF primitive by construction unless the
target is validated: without a policy, a caller could point it at an
internal service, a cloud metadata endpoint, or the control plane's own
loopback interface and use the certificate-inspection response as an
oracle for what's reachable on the private network.

`internal/platform/netpolicy` is a reusable outbound-target validation
component, built once so future diagnostic/integration tools reuse it
instead of reimplementing ad hoc checks:

- Three graduated levels — `PublicOnly` (rejects loopback, unspecified,
  link-local, private RFC1918/RFC4193, and multicast addresses outright;
  what `check_ssl` always uses), `RegisteredResources` (additionally
  allows an address an `AllowedResource(ip) bool` callback recognizes as
  one of Nodera's own registered resources — not used by any tool yet,
  since the Node Agent it would serve doesn't exist, rule 27; exists so
  adding that tool later doesn't require redesigning this package), and
  `InternalAllowed` (permits everything — intended only for a future
  platform-admin-configured integration explicitly meant to reach
  internal infrastructure, never the default for anything an ordinary
  member can trigger). This is a policy abstraction, not a blanket
  "private IPs forbidden forever" hack — Nodera will legitimately need to
  reach registered infrastructure nodes eventually.
- The cloud metadata endpoint (`169.254.169.254`, used identically by
  AWS/GCP/Azure/DigitalOcean) needs no separate special case: it's
  link-local, so `PublicOnly` already refuses it.
- **DNS rebinding**: `Resolve` returns a `ResolvedTarget` bound to one
  specific, already-validated IP address; callers must dial that IP
  directly (`ResolvedTarget.DialAddr()`), never re-resolve the hostname
  at connection time. `checkssl.go` does exactly this — the TLS `dialer`
  connects to `target.DialAddr()` while `ServerName` (SNI) stays the
  original hostname, so there's no window between validation and
  connection where a second DNS lookup could hand back a different,
  unvalidated address.
- Tested by `internal/platform/netpolicy/netpolicy_test.go` (every
  blocked address class, IPv4 and IPv6, including IPv4-mapped IPv6
  addresses; that real public addresses — IPv4 and IPv6 — are correctly
  allowed; the graduated-policy paths; input validation) and
  `internal/tools/handlers/checkssl_test.go` (loopback, IPv6 loopback,
  the metadata address, and a private address are all refused with
  `FORBIDDEN`; a public IPv6 literal is *not* blocked — proving the
  policy doesn't over-block IPv6 wholesale; the genuine-TLS-handshake
  test runs against `InternalAllowed` explicitly, since `httptest`
  necessarily binds to loopback, without weakening what `CheckSSL` itself
  — always `PublicOnly` — actually ships).

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

A proper external KMS/vault integration remains future work; this is
deliberately a phase-1-appropriate, self-contained implementation.

## Key rotation (documented, not yet automated)

There is no automated key-rotation mechanism for
`NODERA_SECRETS_ENCRYPTION_KEY` in this phase — rotating it safely today
is a manual, deliberate operation:

1. Reveal every secret (org- and platform-scoped) under the *current*
   key via `Service.Reveal`/`RevealPlatform` (Go-only, in-process — see
   above).
2. Re-encrypt and re-`Set`/`SetPlatform` each one under the *new* key.
3. Only then rotate `NODERA_SECRETS_ENCRYPTION_KEY` and restart — every
   stored ciphertext must already be under the new key before the old
   one is discarded, since GCM authenticates against the exact key it
   was sealed with (`TestSecretsWrongKeyFailsToDecrypt`/
   `TestPlatformSecrets_WrongKeyFailsToDecrypt` prove a wrong key fails
   closed rather than returning garbled plaintext — the same property
   that makes an un-migrated rotation loud and obvious, not a silent
   data-loss bug).

A future automated rotation would most naturally be a versioned-key
scheme (store a key ID alongside each ciphertext, support decrypting
under any still-valid key while only ever encrypting under the current
one) rather than the single-key model above — not implemented here to
avoid expanding this pass's scope beyond what the secrets model
actually needed.

## Platform secrets — IMPLEMENTED

Organization secrets (above) are scoped to one organization
(`secrets.organization_id`). Platform-wide credentials — a future cloud
AI provider API key, an infrastructure provider credential — are not
owned by any one organization, so `internal/secrets.Service` gained a
parallel set of `Platform*` methods (`SetPlatform`/`ListPlatform`/
`UpdateDescriptionPlatform`/`DeletePlatform`/`RevealPlatform`) over a
separate `platform_secrets` table (migration `0020`, no
`organization_id` column at all) rather than trying to force a platform
secret into the org-scoped table with a null/fake organization.

- **Same encryption, same cipher instance**: `Platform*` methods reuse
  the exact same `cipher.AEAD` (`Service.gcm`) the org-scoped methods
  use — one `NODERA_SECRETS_ENCRYPTION_KEY`, one cipher, two tables.
- **Authorization**: gated by `internal/platformauth`
  (`platform.secrets.manage` for `Set`/`UpdateDescription`/`Delete`/
  `Reveal`, `platform.secrets.read` for `List` — the same manage/read
  split `secrets.manage`/`secrets.read` already draw for organization
  secrets), never `internal/rbac` — an organization admin holding
  `secrets.manage` in their own organization grants nothing here, the
  same authorization-mismatch fix `internal/platformauth` already
  applied to the AI provider/model registry.
- **No reveal-all endpoint, no plaintext over HTTP**: `RevealPlatform`,
  like `Reveal`, is intentionally never wired to any HTTP handler — only
  `List`/`Set`/`UpdateDescription`/`Delete` are (`GET`/`PUT`/`PATCH`/
  `DELETE /api/v1/platform/secrets...`), and none of them ever return a
  plaintext value.
- **Deliberate deferral**: this pass ships the platform-secret
  abstraction/schema/service and its full CRUD + `Reveal`, real and
  tested, but does **not** yet migrate the AI provider adapters
  (`internal/ai/providers/anthropic`, `openai`) to resolve their API key
  from a platform secret — they still read
  `NODERA_ANTHROPIC_API_KEY`/`NODERA_OPENAI_API_KEY` from the
  environment at startup (`cmd/server/main.go`), unchanged. Hot-swapping
  a running adapter's credential when a platform secret changes later is
  a real architectural change (adapters are constructed once at process
  start, not re-resolved per call) that risks destabilizing the AI
  Gateway if rushed into this same pass; shipping the safe, tested
  storage primitive now and wiring adapters to it as a dedicated
  follow-up is the documented, deliberate choice here (rule 36: this is
  a real, working primitive — not fabricated integration with the
  adapters it doesn't have yet).
- Tested by `internal/platform_secrets_test.go`: the full
  set/list/reveal/update-description/delete lifecycle; a caller without
  a `platform.secrets.*` grant (even a full organization owner) is
  forbidden from every method; a secret encrypted under one
  `Service` instance's key fails to decrypt under a different instance's
  key (GCM authenticated encryption, same property org secrets already
  have).

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
  method exists). DB-level immutability is provided by the production role
  model: the runtime role cannot `UPDATE`/`DELETE`/`TRUNCATE` `audit_log`
  (`scripts/db/02-post-migrate.sql`; see "Production database roles and audit
  immutability" below). Dev/test run as the owner role and are not protected.
- Sensitive operations (e.g. node registration) call `audit.Record` with
  actor, action, resource, and resulting state.
- `internal/identity`'s service-account and API-token lifecycle
  (create/disable/enable/update, create/revoke) is audited the same way —
  a raw API token value is never included in the recorded state, only its
  prefix.

## Platform vs organization audit — IMPLEMENTED

`SignUp`/`Login`/`Logout`/`ChangePassword`/session-revocation
(`internal/identity/identity.go`) run before an organization is ever
selected (`requireSession`, not `requireOrganization` —
`cmd/server/middleware.go`), so they were previously either not audited
at all, or would have written a `NULL`-`organization_id` row that
`audit.Query` (which always scopes by a specific `organization_id`) could
never read back — writing unverifiable, effectively invisible rows was
judged worse than not writing them (`docs/ROADMAP.md` Phase 41's original
reasoning). Rather than forcing these events into the organization-scoped
table with fake/null org semantics, or fabricating an organization to
attribute them to, `internal/audit` now supports a genuine **platform
scope**:

- `audit.Record` already wrote `organization_id = NULL` when
  `AuthContext.OrganizationID` is the zero value — that part didn't
  change. What's new is `audit.QueryPlatform`, which reads exactly the
  `organization_id IS NULL` slice, gated by the platform permission
  `platform.audit.read` (`internal/platformauth`) rather than any
  organization's own `audit.read` — seeing every user's authentication
  history across the whole system is a platform-admin capability, never
  something an organization's own audit permission implies. `GET
  /api/v1/platform/audit` exposes it, with the same `resource_type`/
  `action`/`from`/`to`/pagination filters `GET /api/v1/audit` has, minus
  `X-Nodera-Org` (there is no organization to scope by).
- `internal/identity/identity.go` now calls `audit.Record` (via its own
  `recordIdentityAudit` helper) for: `identity.user.signed_up`,
  `identity.user.logged_in`, `identity.user.login_failed` (only when the
  attempted email matches a real account — an attempt against a
  nonexistent email is never recorded, since there's no real user to
  attribute it to and recording one keyed by the attempted email would
  turn the audit log into an account-enumeration oracle for anyone who
  can read it), `identity.user.logged_out`,
  `identity.user.password_changed`, `identity.session.revoked`, and
  `identity.session.revoked_all_others`. Metadata carries IP
  address/user agent/failure reason where relevant — **never** a
  password, session token, or any other credential value.
- `audit.Record`'s returned `Record` type (`Query` and `QueryPlatform`
  both) now also exposes `actor_user_id`/`actor_service_account_id` —
  previously tracked in the schema and written, but never actually
  returned to any caller, human-readable `actor_label` was the only
  identity a query result carried.
- Wiring note: `audit.Service` and `platformauth.Service` depend on each
  other (`platformauth` writes its own grant/revoke audit entries;
  `audit.QueryPlatform` checks a platform permission before returning
  results), which Go's import rules don't allow as two constructor
  arguments pointing at each other. `audit.Service.SetPlatformAuthorizer`
  closes the cycle after both are constructed
  (`cmd/server/main.go`) — see that type's doc comment for the exact
  reasoning and wiring order.
- Tested by `internal/platform_audit_test.go` end to end against real
  Postgres: signup → login → a failed login (wrong password) → password
  change → login again → logout, all six actions land in
  `QueryPlatform`'s results with `organization_id` nil and
  `actor_user_id` correctly identifying the subject user; the same
  records never appear in an ordinary organization's `Query`; a caller
  without `platform.audit.read` (even a full organization owner) is
  forbidden from `QueryPlatform`.

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
- **`RedisLimiter`** — used when `NODERA_REDIS_URL` is set, verified reachable
  with a startup `PING` (a misconfigured URL fails startup rather than silently
  falling back). Counters are shared across API instances. **On a Redis error it
  degrades to a per-instance limiter with the same budget** (`WithFallback`), not to
  "allow everything": an outage weakens brute-force protection from deployment-wide to
  per-instance, it does not remove it. `/ready` reports `redis: down` / `status: degraded`.

**Which limiters are shared and which are not** (login, signup, AI, create-org and change-password
are Redis-backed; the API budget, dangerous-request budget and agent limits are in-process only) is
tabulated in `docs/DEPLOYMENT.md` "Rate limiters". **Before running more than one API instance, every
limit whose invariant must hold deployment-wide must move to shared state.** One instance is the supported topology.

**Client identity behind a reverse proxy.** IP-keyed limits (login, signup, agent enrolment) need the real
client address. `X-Forwarded-For` is honoured **only** when the TCP peer is inside `NODERA_TRUSTED_PROXIES`
(the dedicated `nodera-edge` network, where only Traefik can connect), and the **right-most untrusted** entry
is used, so a forged header cannot choose the identity being limited (`cmd/server/clientip.go`, tested). Production
refuses to start without `NODERA_TRUSTED_PROXIES`: without it every user behind the proxy would share one bucket
(one attacker could lock everyone out of login).

## Secure headers — IMPLEMENTED

`httpserver.SecurityHeaders` sets `X-Content-Type-Options: nosniff`,
`X-Frame-Options: DENY`, `Referrer-Policy: no-referrer`, and
`Cache-Control: no-store` on every response. No CSP is set — this server
returns only JSON, never HTML, so there's no inline-script surface for CSP
to restrict yet; one will be added if/when the API ever serves any HTML.

## CI — IMPLEMENTED

`.github/workflows/ci.yml` runs on every push/PR (read-only token, superseded runs cancelled):
- **API**: `gofmt -l` (must be empty), `go vet`, `go build`, `go test ./... -p 1` **and** `go test ./... -race -p 1`
  against real Postgres and Redis service containers (this includes the OpenAPI drift/coverage tests and the security suite),
  `govulncheck` **pinned** (fails on a vulnerability reachable from Nodera's code), and `@redocly/cli` lint of `api/openapi/openapi.json`.
- **Production database roles**: creates the roles, runs `nodera-migrate` as the owner, applies the audit protections and proves them as the runtime role; re-runs everything to prove idempotence.
- **Web**: `npm ci`, ESLint with zero warnings, `tsc --noEmit`, Vitest, `next build`, and the **dependency policy** below.
- **Images**: builds the API, Agent and Web images; checks they run as non-root; starts the Web image read-only with all capabilities dropped and checks it serves and contains no package manager and no baked-in localhost API URL;
  validates both compose files; runs `scripts/check-compose-prod.py` (no published ports, socket only on the agent, secrets as files, internal-only networks, pinned images, bounded logs, memory limits, migration gate, SSE flush, control-plane labels) and proves the production compose refuses to start without its mandatory values.

## Dependency policy (web)

CI runs `scripts/audit-policy.mjs`, which reads `npm audit --json` and **fails on any high or critical advisory** unless it is listed by exact GHSA id and package in
`web/audit-exceptions.json` with a reason, exposure, mitigation and an **expiry date**. Expired exceptions fail; exceptions that no longer match anything fail (so the list cannot rot);
new advisories fail. There is no blanket threshold exception.

State at the time of the staging-readiness pass:
- **Fixed:** `tinypool` critical (prototype pollution → RCE, via Vitest) — Vitest upgraded 3 → 5 (test-time only); PostCSS (XSS / path-traversal advisories, in Next's pipeline) — `overrides` forces `postcss ^8.5.30` (Next 15.5 builds and runs with it); `sharp`, `source-map-js` — patched by `npm audit fix`.
- **Accepted, expiring 2027-01-31:** `GHSA-vfj7-8cjw-p6xm` in `braces` — a stack-exhaustion DoS on deeply nested glob braces. **Every published version of `braces` is affected; no fix exists.** It is reachable only through
  Tailwind 3 (`micromatch`/`fast-glob`) and `eslint-config-next` at **build/lint time**, on glob patterns from this repository's own config, never from user input; the production image contains no `braces` (checked). Removing it means the Tailwind 4 migration, a styling change outside the scope of the deployment-readiness pass. Review at expiry or when Tailwind 4 is adopted.
- Below the gate (moderate): `postcss-selector-parser`, `postcss-nested`, same Tailwind 3 chain. Go: `govulncheck` reports 0 reachable; it notes the unmaintained `golang.org/x/crypto/openpgp` package in a required module — Nodera does not import it.

## CORS — IMPLEMENTED

`internal/platform/httpserver.CORS` reflects back only an allow-listed
origin (`NODERA_CORS_ORIGINS`, default `http://localhost:3000` for the
`web/` dev server) and sets `Access-Control-Allow-Credentials: true` —
never `*`, both because `Authorization` headers are in play and because
`Allow-Credentials: true` combined with a wildcard origin is invalid per
the Fetch spec (the browser would refuse it). Production deployments must
set this explicitly to their real frontend origin(s) (`docs/DEPLOYMENT.md`).

In production the API **refuses to start** unless every origin is an exact `https://` origin (no `*`, no path, no localhost) — `internal/platform/config` tests each rejection.
The supported topology is same-origin (UI and API on one host behind the proxy), where CORS never applies; the exact-origin allow-list is defence in depth.

## Production browser security posture — verified

Checked in production mode by `cmd/server/production_security_test.go` and, through Traefik with real TLS termination, by `scripts/staging-smoke.py security`:
- session cookie `nodera_session`: `HttpOnly`, `Secure`, `SameSite=Lax`, `Path=/`, host-only (no `Domain`); CSRF cookie `nodera_csrf`: `Secure`, `SameSite=Lax`, readable by the UI (double-submit)
- every cookie-authenticated mutating request needs `X-CSRF-Token` (wrong/missing → 403); bearer **API tokens** work without cookies/CSRF but only within their scopes
- no session token in `localStorage`/`sessionStorage` (the UI keeps only preferences); the login response body does not carry the session token
- CORS reflects only the configured exact origin with credentials; `null`, `*`, look-alike and localhost origins get no CORS headers
- JSON bodies are limited to 2 MiB (`413`/validation error beyond); the HTTP server has header and idle timeouts

## Production database roles and audit immutability

`scripts/db/01-roles.sql` splits `nodera_owner` (schema owner, used only by `nodera-migrate`) from `nodera_app` (the API: DML only, no DDL, no superuser).
`02-post-migrate.sql` revokes `UPDATE`, `DELETE`, `TRUNCATE` on `audit_log` and all writes to `schema_migrations` from the app role; audit retention deletes go through the
`SECURITY DEFINER` function `audit_log_apply_retention` (migration 0032) which refuses a cutoff younger than 90 days and only touches one organisation. `03-verify-roles.sql` proves
10 behaviours as the runtime role (CI and `scripts/post-migrate.sh`). **IMPLEMENTED + LOCALLY TESTED; real-database verification pending.** A superuser or the owner can still alter the table: this is
protection against application bugs and application-level compromise, not against a host administrator.

## Control-plane self-protection and trust model

The Node Agent holds the Docker socket and is therefore root-equivalent on the host; the API holds **no** socket (`NODERA_PROVIDER_MODE=agent`; the direct mode is refused in production without an explicit override).
The agent executes only a fixed allowlist (no shell exists), validated on both sides, and `internal/nodeagent/guard` refuses any managed operation that would stop/remove/replace `nodera-cp-*` containers, anything labelled
`nodera.role=control-plane`, or the configured extras (Traefik, MariaDB), and refuses workload mounts outside the data root (so the socket, `/etc`, the Postgres volume are unreachable), host networking and root users. Project slugs starting `cp-` are rejected.
Commands carry secrets only transiently: parameters of `docker.create` and `database.ensure` are blanked in `node_agent_commands` once finished; an executed command id is remembered so a replayed signed command is refused; every command has an execution time limit.
The break-glass path is SSH + Docker Compose on the host, independent of Nodera (`docs/DEPLOYMENT.md`). Customer containers sit on a different network from the control plane and cannot reach it.

## Control plane security (operations engine) — IMPLEMENTED

Regression coverage lives in `api/cmd/server/security_test.go` (runs the real router against Postgres).

| Threat | Control | Test |
|---|---|---|
| SQL injection | parameterised queries only; `LIKE` wildcards escaped in every search; no string-built SQL | hostile payloads across every list/search/filter parameter; tables intact, no cross-tenant rows |
| XSS | JSON-only API, `nosniff`, `Content-Type: application/json`; the UI renders text nodes, never `dangerouslySetInnerHTML` | `<script>` stored as data and returned inert |
| CSRF | cookie sessions + double-submit token; bearer tokens are not ambient | cookie request without the header → 403 on every new mutating route |
| SSRF | `internal/platform/netpolicy` for monitors, webhooks, `check_ssl`; pinned dial address, no redirects; bare IPv6 literals classified | loopback/metadata/private targets blocked by the real local provider; webhook targets rejected at creation |
| IDOR / cross-tenant | every query filters on `organization_id`; project sub-resources 404 for foreign projects; `X-Nodera-Org` must match membership | table-driven read + write attempts on every resource type with a second tenant |
| Privilege escalation | permission per operation, enforced in the service layer (not the router); dangerous operations unreachable without the gateway | member token against every mutating route → 403 |
| Secret leakage | passwords/keys only in `internal/secrets` (AES-GCM); redaction (`internal/platform/redact`) on log ingest, operation logs and container logs; credentials never in `source_config`, audit or errors; private keys never in a column | scans of API responses, audit and job logs for generated secrets and `PRIVATE KEY` |
| Path traversal | `SafeJoin`/rooted filesystem provider (symlink-safe), upload/zip path validators, agent path validators | upload `..`/absolute/backslash/empty segments, zip-slip archive, agent `filesystem.*` |
| Command injection | no shell anywhere: Docker adapter and agent use argv arrays; image/ref/name/tag/repository validators | hostile image tags, refs, repos, container names, agent params |
| Replay / forged agents | Ed25519 signed requests + nonce table + ±60 s window; signed, agent-bound, expiring commands; allowlist twice | replay, tamper, stale, unsigned, wrong agent, cross-agent result forgery, oversized body |
| Abuse | per-actor API budget (1200/min), dangerous-request budget (30/h), per-agent and enrolment limits, login/signup/AI limits | budget exhaustion returns 429 + `Retry-After` |
| Dangerous operations | approval gateway, atomic single execution, safety snapshots, automatic rollback | restore/cutover/delete/production-deploy all require a human decision |

### AI operations layer

The model is untrusted. It receives only the project's name/type/status and the list of operations the caller can already run
(never secrets or config). Its answer is parsed as data (≤ 32 KiB, ≤ 8 steps), unknown operation names are dropped, and each step is vetted
against the real registry (`Engine.Check`: permission, flag, payload, preconditions). A human approves the plan (`approvals.decide`) and runs steps
in order under **their own** permissions; dangerous steps still need gateway approval. A row lock prevents double-running a step. There is no path from model text to a shell or an arbitrary provider call.

### Known limits (honest list)

* The Node Agent, Docker adapter and MariaDB adapter have run against a real Docker daemon and MariaDB container **on a local Linux VM only**; none has run on the Hetzner host (LOCALLY TESTED, not STAGING VALIDATED).
* Email delivery is not implemented (reported as such). Password-reset flow does not exist (no mail transport), so there is nothing to rate limit yet.
* Dev keys persisted in `NODERA_DATA_DIR` are a convenience; production requires explicit keys and refuses to start without them.
* The API/dangerous/agent rate limiters are in-process (per instance); login/signup/AI/create-org/change-password use Redis with a per-instance fallback. Shared state for the rest is required before scaling out (`docs/DEPLOYMENT.md`).
* TLS verification is disabled in exactly two places — the certificate *inspection* probes (`check_ssl` tool and the SSL monitor) — which dial, read the peer certificate, validate it explicitly, report, and send no application data. Nothing else (SFTP uses a pinned host key; the agent talks https with verification) skips verification.
* Local DNS is in-memory and the local certificate provider is a development CA; both are reported as `local_dev` and are not production DNS/TLS.
* An operator with root on the host (or the owner database role) can do anything; the guard and the database protections defend against application faults, not against host administrators.

## Not yet implemented / deliberately deferred

- Email delivery, password recovery, invitations (no mail transport) — NOT IMPLEMENTED
- External KMS/vault integration and automated rotation of `NODERA_SECRETS_ENCRYPTION_KEY` (current scheme is self-contained AES-256-GCM; see Key rotation and `docs/KEY-CUSTODY.md`) — NOT IMPLEMENTED
- Shared-state rate limiting for the in-process limiters (needed only before running several API instances)
- Content-Security-Policy for the web UI (the API returns only JSON)

These are tracked in `docs/ROADMAP.md`, not silently skipped.
