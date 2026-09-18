# API

Base path: `/api/v1`. Unversioned `/health` and `/ready` exist outside it for
platform/orchestrator use (rule 27).

Cross-origin browser access (the `web/` frontend) is allowed only from
origins listed in `NODERA_CORS_ORIGINS` (default `http://localhost:3000`) —
see `internal/platform/httpserver.CORS`. Non-browser callers (curl, server-
to-server, the future Node Agent) are unaffected either way, since CORS is
a browser-enforced mechanism, not a server-side access control.

**Machine-readable spec**: `GET /openapi.json` serves a hand-maintained
OpenAPI 3.0 document (`api/openapi/openapi.json`, embedded in the binary —
`api/openapi/openapi.go`), validated against the OpenAPI schema in CI.
`GET /docs` serves a Swagger UI page against it. Both are unauthenticated,
like `/health`. `web/` can generate TypeScript types from it via
`npm run gen:types` (`web/lib/api-types.generated.ts`) — not yet swapped in
for the hand-written `web/lib/types.ts` (`docs/ROADMAP.md`).

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

## Pagination

Four list endpoints are paginated: `GET /infrastructure/nodes`,
`GET /applications`, `GET /jobs`, `GET /audit`. Each accepts `?limit=`
(default 50, capped at 200) and `?offset=`, and returns the standard
envelope (`internal/platform/httpserver.Page`) instead of a bare array:

```json
{ "items": [...], "limit": 50, "offset": 0, "has_more": true }
```

`has_more` is computed by fetching `limit+1` rows server-side and trimming
the extra one — no separate `COUNT` query, so it's cheap even on a large
table. Every other list endpoint (`ai/profiles`, `ai/providers`,
`ai/models`, `secrets`, `tools`, `approvals`, `api-tokens`,
`service-accounts`, `organizations`) still returns a bare array —
these are expected to stay small at phase-1 scale; paginating them is
tracked in `docs/ROADMAP.md` if that stops being true.

## Endpoints implemented today

| Method | Path | Auth | Description |
|---|---|---|---|
| GET | `/health` | none | Process liveness |
| GET | `/ready` | none | Liveness + database reachability |
| GET | `/openapi.json` | none | OpenAPI 3.0 spec |
| GET | `/docs` | none | Swagger UI |
| POST | `/api/v1/auth/signup` | none | Create a user (no org membership yet) |
| POST | `/api/v1/auth/login` | none | Returns a session token + the caller's organizations |
| POST | `/api/v1/auth/logout` | session | Revokes the current session |
| GET | `/api/v1/organizations` | session or token | List organizations the caller belongs to |
| POST | `/api/v1/organizations` | session or token | Create an organization; creator becomes `owner` |
| GET | `/api/v1/organization` | session or token + org | Get the current organization |
| GET | `/api/v1/infrastructure/nodes` | session or token + org | List nodes, paginated (`infrastructure.read`) |
| POST | `/api/v1/infrastructure/nodes` | session or token + org | Register a node (`infrastructure.manage`) |
| GET | `/api/v1/infrastructure/nodes/{id}` | session or token + org | Get a node |
| GET | `/api/v1/applications` | session or token + org | List applications, paginated (`applications.read`) |
| POST | `/api/v1/applications` | session or token + org | Register an application (`applications.deploy` — see docs/API.md note below) |
| GET | `/api/v1/applications/{id}` | session or token + org | Get an application |
| GET | `/api/v1/api-tokens` | session or token + org | List the caller's own API tokens |
| POST | `/api/v1/api-tokens` | session or token + org | Create an API token owned by the caller (`organization.manage`); returns the raw token once |
| DELETE | `/api/v1/api-tokens/{id}` | session or token + org | Revoke one of the caller's own tokens |
| GET | `/api/v1/organization/api-tokens` | session or token + org | List every non-revoked token in the org, any owner (`organization.manage`) |
| DELETE | `/api/v1/organization/api-tokens/{id}` | session or token + org | Revoke any token in the org, regardless of owner (`organization.manage`) |
| GET | `/api/v1/roles` | session or token + org | List every role available to the org — system roles plus any custom org roles (`organization.manage`) |
| GET | `/api/v1/organization/members` | session or token + org | List org members with their currently-assigned roles (`organization.manage`) |
| POST | `/api/v1/organization/members/{userID}/roles` | session or token + org | Grant a member a role (`organization.manage`); idempotent — already holding it is not an error |
| DELETE | `/api/v1/organization/members/{userID}/roles/{roleID}` | session or token + org | Revoke a role from a member (`organization.manage`) |
| GET | `/api/v1/service-accounts` | session or token + org | List service accounts (`organization.manage`) |
| POST | `/api/v1/service-accounts` | session or token + org | Create a service account (`organization.manage`) |
| DELETE | `/api/v1/service-accounts/{id}` | session or token + org | Disable a service account and immediately revoke all its outstanding tokens (`organization.manage`) — does not delete the account or its history |
| POST | `/api/v1/service-accounts/{id}/api-tokens` | session or token + org | Mint a token owned by the service account (`organization.manage`); returns the raw token once |
| GET | `/api/v1/jobs` | session or token + org | List jobs, paginated, optional `?status=` filter (`jobs.read`) |
| POST | `/api/v1/jobs` | session or token + org | Enqueue a job (`jobs.manage`) |
| GET | `/api/v1/jobs/{id}` | session or token + org | Get a job |
| POST | `/api/v1/jobs/{id}/cancel` | session or token + org | Cancel a queued job |
| GET | `/api/v1/ai/profiles` | session or token + org | List AI profiles (`ai.use`) |
| POST | `/api/v1/ai/profiles` | session or token + org | Create an AI profile (`ai.manage`) |
| POST | `/api/v1/ai/chat` | session or token + org | Call the AI gateway with a profile key + messages (`ai.use`) |
| GET | `/api/v1/ai/providers` | session or token + org | List the platform-wide provider registry (`ai.use`) |
| POST | `/api/v1/ai/providers` | session or token + org | Register/update a provider (`ai.manage`) — platform-wide, see note below |
| GET | `/api/v1/ai/models` | session or token + org | List the platform-wide model registry (`ai.use`) |
| POST | `/api/v1/ai/models` | session or token + org | Register/update a model under an existing provider (`ai.manage`) |
| GET | `/api/v1/secrets` | session or token + org | List secret metadata only — never values (`secrets.read`) |
| PUT | `/api/v1/secrets/{key}` | session or token + org | Create or rotate a secret (`secrets.manage`) — `503 UNAVAILABLE` if the server has no `NODERA_SECRETS_ENCRYPTION_KEY` configured |
| DELETE | `/api/v1/secrets/{key}` | session or token + org | Delete a secret (`secrets.manage`) |
| GET | `/api/v1/tools` | session or token + org | List the tool registry (`tools.read`) |
| POST | `/api/v1/tools/{key}/execute` | session or token + org | Execute a tool. `read`/`safe` run immediately (`200`); `privileged`/`critical` return `202` with an `approval_id` instead of running |
| GET | `/api/v1/tools/approval-ttl` | session or token + org | List the organization's per-tool approval TTL overrides (`tools.manage`) — a tool with no row uses the 24h default |
| PUT | `/api/v1/tools/{key}/approval-ttl` | session or token + org | Set/update the organization's approval TTL override for a tool (`tools.manage`); `approval_ttl_seconds` must be between 300 (5m) and 2592000 (30d) |
| DELETE | `/api/v1/tools/{key}/approval-ttl` | session or token + org | Clear the organization's override for a tool, reverting it to the 24h default (`tools.manage`) |
| GET | `/api/v1/approvals` | session or token + org | List approvals, optional `?status=` filter (`approvals.decide`) |
| POST | `/api/v1/approvals/{id}/decide` | session or token + org | Approve or reject a pending approval (`approvals.decide`) — approving attempts execution immediately |
| GET | `/api/v1/audit` | session or token + org | Query the audit log, paginated, optional `?resource_type=`/`?action=` filters (`audit.read`) |
| GET | `/api/v1/agents` | session or token + org | List agent definitions (`agents.execute`) |
| POST | `/api/v1/agents` | session or token + org | Create an agent definition (`agents.manage`) — `permission_scope` must be a subset of the caller's own permissions |
| GET | `/api/v1/agents/{id}` | session or token + org | Get an agent definition (`agents.execute`) |
| POST | `/api/v1/agents/{id}/enable` | session or token + org | Enable an agent (`agents.manage`) |
| POST | `/api/v1/agents/{id}/disable` | session or token + org | Disable an agent (`agents.manage`) |
| POST | `/api/v1/agents/{id}/run` | session or token + org | Send a message through the agent's scoped AI chat (`agents.execute`, and the agent's own `permission_scope` must include `ai.use`) |
| POST | `/api/v1/agents/{id}/tools/{key}/execute` | session or token + org | Execute a tool under the agent's scope (`agents.execute`); `key` must be in the agent's `allowed_tool_keys`, then follows the same `200`/`202` Tool Gateway semantics as `POST /api/v1/tools/{key}/execute` |

`applications.deploy` is used for registering an application record because
the current permission catalog has no separate `applications.manage` key —
see `internal/applications/applications.go`. Likewise API token creation
uses `organization.manage` rather than a dedicated `tokens.manage` key — see
`internal/identity/apitoken.go`.

There is deliberately no endpoint that returns a secret's plaintext value —
see `docs/SECURITY.md` Secrets.

`/api/v1/ai/providers` and `/api/v1/ai/models` manage a **platform-wide**
registry (`ai_providers`/`ai_models` have no `organization_id`) — any caller
with `ai.manage` in *any* organization can register a provider/model
affecting every organization on this deployment. This is a deliberate
phase-1 simplification for a single-administrating-org deployment, not an
oversight — see `internal/ai/registry.go`.

`POST /auth/login` (5 attempts / 5 minutes) and `POST /auth/signup`
(3 attempts / hour) are rate limited per client IP; `POST /api/v1/ai/chat`
is rate limited per organization (60 requests / minute). Exceeding any of
them returns `429` with code `RATE_LIMITED` — see `docs/SECURITY.md`.

Everything else described in `docs/ARCHITECTURE.md` (the agent execution
loop, cloud AI provider adapters) is schema/interfaces only — no HTTP
surface exists for them yet (PLANNED, tracked in `docs/ROADMAP.md`).

## Not yet implemented

- Generating the OpenAPI spec from code instead of hand-maintaining it
- Swapping `web/`'s hand-written types over to the generated ones
- Pagination on the remaining list endpoints, if they stop being small at
  phase-1 scale (see Pagination above)
- Rate limiting on endpoints other than `POST /auth/login`,
  `POST /auth/signup`, and `POST /api/v1/ai/chat`
