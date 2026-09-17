# API

Base path: `/api/v1`. Unversioned `/health` and `/ready` exist outside it for
platform/orchestrator use (rule 27).

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

## Endpoints implemented today

| Method | Path | Auth | Description |
|---|---|---|---|
| GET | `/health` | none | Process liveness |
| GET | `/ready` | none | Liveness + database reachability |
| POST | `/api/v1/auth/signup` | none | Create a user (no org membership yet) |
| POST | `/api/v1/auth/login` | none | Returns a session token + the caller's organizations |
| POST | `/api/v1/auth/logout` | session | Revokes the current session |
| GET | `/api/v1/organizations` | session | List organizations the caller belongs to |
| POST | `/api/v1/organizations` | session | Create an organization; creator becomes `owner` |
| GET | `/api/v1/organization` | session + org | Get the current organization |
| GET | `/api/v1/infrastructure/nodes` | session + org | List nodes (requires `infrastructure.read`) |
| POST | `/api/v1/infrastructure/nodes` | session + org | Register a node (requires `infrastructure.manage`) |
| GET | `/api/v1/infrastructure/nodes/{id}` | session + org | Get a node |
| GET | `/api/v1/audit` | session + org | Query the audit log (requires `audit.read`) |

Everything else described in `docs/ARCHITECTURE.md` (AI gateway, agents,
jobs, approvals, applications, secrets) is schema/interfaces only — no HTTP
surface exists for them yet (PLANNED, tracked in `docs/ROADMAP.md`).

## Not yet implemented

- OpenAPI/Swagger generation
- Pagination on list endpoints (today `GET /infrastructure/nodes` and
  `GET /audit` return unpaginated/simple-limit results — fine at current
  scale, will need `limit`/`cursor` params before this matters in production)
- Rate limiting
- API token (service-account) authentication — only user sessions work today
