# API

Base path: `/api/v1`. Unversioned `/health` and `/ready` exist outside it for
platform/orchestrator use (rule 27).

Cross-origin browser access (the `web/` frontend) is allowed only from
origins listed in `NODERA_CORS_ORIGINS` (default `http://localhost:3000`) —
see `internal/platform/httpserver.CORS`. Non-browser callers (curl, server-
to-server, the future Node Agent) are unaffected either way, since CORS is
a browser-enforced mechanism, not a server-side access control.

## Conventions

- JSON in, JSON out. Response bodies use `snake_case` field names.
- Auth: `Authorization: Bearer <session_token>` (from `POST /auth/login`).
- Organization scoping: routes that operate within an organization require
  `X-Nodera-Org: <organization_id>`; the server verifies the session holder
  is actually a member before resolving permissions (rule: never trust a
  client-supplied tenant filter — see `docs/SECURITY.md`).
- Every request gets an `X-Request-ID` (echoed from the client if supplied,
  otherwise generated) returned on the response and included in every log
  line and error body for correlation.
- Errors are normalized:
  ```json
  { "error": { "code": "FORBIDDEN", "message": "...", "request_id": "..." } }
  ```
  See `internal/platform/apierr` for the full code list
  (`VALIDATION_ERROR`, `UNAUTHENTICATED`, `FORBIDDEN`, `NOT_FOUND`,
  `CONFLICT`, `RATE_LIMITED`, `NOT_IMPLEMENTED`, `UNAVAILABLE`,
  `INTERNAL_ERROR`). Internal errors never leak a raw driver/vendor message.

## Authenticating

Two bearer token types are accepted on `Authorization: Bearer <token>`, and
`requireSession` (the auth middleware) tries them in order:

1. **Session token** (from `POST /auth/login`). Requires `X-Nodera-Org` on
   any org-scoped route — the organization isn't known until that header is
   read, since one session can act on behalf of any organization the user
   belongs to.
2. **API token** (from `POST /api-tokens`). Already bound to one
   organization at creation time, so `X-Nodera-Org` is optional; if
   present, it must match the token's own organization or the request is
   rejected. Permissions come directly from the token's granted scopes, not
   a live role lookup.

## Endpoints implemented today

| Method | Path | Auth | Description |
|---|---|---|---|
| GET | `/health` | none | Process liveness |
| GET | `/ready` | none | Liveness + database reachability |
| POST | `/api/v1/auth/signup` | none | Create a user (no org membership yet) |
| POST | `/api/v1/auth/login` | none | Returns a session token + the caller's organizations |
| POST | `/api/v1/auth/logout` | session | Revokes the current session |
| GET | `/api/v1/organizations` | session or token | List organizations the caller belongs to |
| POST | `/api/v1/organizations` | session or token | Create an organization; creator becomes `owner` |
| GET | `/api/v1/organization` | session or token + org | Get the current organization |
| GET | `/api/v1/infrastructure/nodes` | session or token + org | List nodes (`infrastructure.read`) |
| POST | `/api/v1/infrastructure/nodes` | session or token + org | Register a node (`infrastructure.manage`) |
| GET | `/api/v1/infrastructure/nodes/{id}` | session or token + org | Get a node |
| GET | `/api/v1/applications` | session or token + org | List applications (`applications.read`) |
| POST | `/api/v1/applications` | session or token + org | Register an application (`applications.deploy` — see docs/API.md note below) |
| GET | `/api/v1/applications/{id}` | session or token + org | Get an application |
| GET | `/api/v1/api-tokens` | session or token + org | List the caller's own API tokens |
| POST | `/api/v1/api-tokens` | session or token + org | Create an API token (`organization.manage`); returns the raw token once |
| DELETE | `/api/v1/api-tokens/{id}` | session or token + org | Revoke one of the caller's own tokens |
| GET | `/api/v1/jobs` | session or token + org | List jobs, optional `?status=` filter (`jobs.read`) |
| POST | `/api/v1/jobs` | session or token + org | Enqueue a job (`jobs.manage`) |
| GET | `/api/v1/jobs/{id}` | session or token + org | Get a job |
| POST | `/api/v1/jobs/{id}/cancel` | session or token + org | Cancel a queued job |
| GET | `/api/v1/ai/profiles` | session or token + org | List AI profiles (`ai.use`) |
| POST | `/api/v1/ai/profiles` | session or token + org | Create an AI profile (`ai.manage`) |
| POST | `/api/v1/ai/chat` | session or token + org | Call the AI gateway with a profile key + messages (`ai.use`) |
| GET | `/api/v1/secrets` | session or token + org | List secret metadata only — never values (`secrets.read`) |
| PUT | `/api/v1/secrets/{key}` | session or token + org | Create or rotate a secret (`secrets.manage`) — `503 UNAVAILABLE` if the server has no `NODERA_SECRETS_ENCRYPTION_KEY` configured |
| DELETE | `/api/v1/secrets/{key}` | session or token + org | Delete a secret (`secrets.manage`) |
| GET | `/api/v1/audit` | session or token + org | Query the audit log (`audit.read`) |

`applications.deploy` is used for registering an application record because
the current permission catalog has no separate `applications.manage` key —
see `internal/applications/applications.go`. Likewise API token creation
uses `organization.manage` rather than a dedicated `tokens.manage` key — see
`internal/identity/apitoken.go`.

There is deliberately no endpoint that returns a secret's plaintext value —
see `docs/SECURITY.md` Secrets.

`POST /auth/login` is rate limited (5 attempts / 5 minutes per client IP);
exceeding it returns `429` with code `RATE_LIMITED`.

Everything else described in `docs/ARCHITECTURE.md` (agents, approvals,
real AI provider adapters) is schema/interfaces only — no HTTP surface
exists for them yet (PLANNED, tracked in `docs/ROADMAP.md`).

## Not yet implemented

- OpenAPI/Swagger generation
- Pagination on list endpoints (today `GET /infrastructure/nodes` and
  `GET /audit` return unpaginated/simple-limit results — fine at current
  scale, will need `limit`/`cursor` params before this matters in production)
- Rate limiting on endpoints other than `POST /auth/login`
- Service-account-issued API tokens (user-owned tokens work today; see `docs/API.md` Authenticating)
