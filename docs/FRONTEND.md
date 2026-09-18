# Frontend

Status: **IMPLEMENTED** as a skeleton — every page is real and reads/writes
the live API, but the page set matches only what the API itself implements
today (see `README.md`'s status table). No page is decorative or backed by
fabricated data (rule 26).

## Stack

Next.js (App Router) + TypeScript + Tailwind CSS, per
[ADR-001](DECISIONS.md#adr-001-core-services-in-go-frontend-in-typescript-python-only-where-aiml-needs-it).
No server-side rendering of authenticated data — every page is a client
component that calls the Core API directly from the browser. There is no
Next.js API route layer; `web/` is a pure client of `api/`.

## Structure

```
web/
  app/
    page.tsx              redirect router: → /login, /orgs, or /dashboard
    login/page.tsx         sign in / sign up
    orgs/page.tsx           organization picker + create
    (org)/layout.tsx        sidebar shell — guards session + org, then renders:
      dashboard/page.tsx     live counts + recent audit activity
      infrastructure/page.tsx  node list + register form
      applications/page.tsx    application list + register form
      jobs/page.tsx             job list (status filter), enqueue, cancel
      tools/page.tsx             tool registry + inline execute form + approvals queue
      agents/page.tsx             agent definitions: create/enable/disable, scoped
                                  chat (Run), scoped tool execution (ExecuteTool)
      ai/page.tsx                 AI Gateway: chat, profiles, providers, models
      secrets/page.tsx          secret metadata list, set, delete — never values
      access/page.tsx            own API tokens, service accounts + their tokens,
                                  org-wide token listing (organization.manage only)
      audit/page.tsx            full audit log
  lib/
    api.ts                 fetch wrapper: bearer token + X-Nodera-Org headers, normalized ApiError
    session.ts               localStorage-backed session/org/user state (client-only)
    useApi.ts                 shared data-fetching hook: explicit loading/error, never a guessed value
    types.ts                   hand-written TS types mirroring the Go API's JSON shapes
  components/               PageHeader, StatusBadge, ErrorBanner
```

## Auth model

The API is bearer-token based, not cookie-based (ADR-005), so there is no
CSRF surface here — the session token lives in `localStorage`
(`lib/session.ts`) and is attached as `Authorization: Bearer <token>` on
every request. The organization a user is currently acting within is also
client state (`X-Nodera-Org` header), matching the API's own design (one
session, many organizations — see `docs/API.md`).

## Hand-written types, with a generated alternative now available

An OpenAPI 3.0 spec exists (`api/openapi/openapi.json`, served at
`/openapi.json`) and `npm run gen:types` produces
`lib/api-types.generated.ts` from it. No page has been switched over to it
yet — `lib/types.ts` (kept intentionally small, mirroring only the fields
the UI actually reads, not the full Go struct) is still what every page
imports. `lib/types.ts` also defines `Page<T>`, the pagination envelope
(`infrastructure/nodes`, `applications`, `jobs`, `audit` — see
`docs/API.md` Pagination); the pages for those four hold their own
`visibleLimit` state and a "Load more" button that re-fetches with a
larger `?limit=`, rather than accumulating pages client-side.

## Tools & Access pages

`tools/page.tsx` lists the tool registry and lets a user execute one inline
(resource type/id + a JSON parameters textarea). A `read`/`safe` tool's
result renders directly; a `privileged`/`critical` tool instead shows its
new `approval_id` and points at the Approvals table below, which lists by
status and lets the user Approve/Reject a pending one with an optional
reason — exercising the real Tool Gateway pipeline end to end (verified
live: `check_ssl` against `github.com` returns genuine certificate data;
`deploy_application` correctly creates an approval instead of running).

`access/page.tsx` covers the caller's own API tokens (create/revoke),
service accounts (create/disable, and issuing a token owned by one instead
of the caller), and — only rendered if the `GET /organization/api-tokens`
call doesn't come back `FORBIDDEN` — an org-wide token listing with owner
attribution. A `FORBIDDEN` there is treated as "this section isn't
available to me," not an error to display, since a plain member lacking
`organization.manage` is an expected, not exceptional, case.

Both pages render lists via `.map()` returning more than one element per
item (a data row plus a conditional inline form row) — that needs
`<Fragment key={...}>`, not the `<>...</>` shorthand, which can't carry a
key. A first pass used the shorthand and shipped a real React "missing key"
warning, caught by checking the browser console during live verification
rather than trusting the build (`tsc`/`next build` don't catch this class
of bug) — fixed before commit.

## Agents page

`agents/page.tsx` lists agent definitions and lets a user create one (name,
AI profile key, system instructions, `allowed_tool_keys`, `permission_scope`
— all comma-separated inputs for the two list fields), enable/disable it,
and, once active, either Run it (a scoped chat message) or Execute a tool
through it (same resource type/id + JSON parameters shape as the Tools
page, restricted to a `<select>` of that agent's own `allowed_tool_keys`).
A new agent starts disabled, matching the API (`docs/AGENTS.md`); Run and
Execute are both disabled in the UI until enabled, rather than left to fail
server-side.

Verified live end to end: created an agent scoped to only `ai.use` +
`tools.read`, confirmed executing `check_ssl` through it correctly failed
with `FORBIDDEN` (`missing required permission: infrastructure.read` —
the agent's own scope, not the caller's, gates the call), then created a
second agent whose scope also included `infrastructure.read` and confirmed
`check_ssl` against `github.com` returned genuine certificate data through
it. Also verified `Run` against the `local-echo` provider returns real
`echo: <message>` content.

## AI Gateway page

`ai/page.tsx` covers the whole AI Gateway surface: a Chat panel (pick a
profile, send a message, see the real `ChatResult` including provider key,
model, and token counts), a Profiles section (list + create, with
`privacy_level` as a `<select>` and `preferred_model_ids`/
`fallback_model_ids`/`required_capabilities` as comma-separated inputs
matching the Tools/Agents pages' convention), and Providers/Models
sections (list + register, `ai.manage`-gated server-side — the page always
shows the forms and lets a `FORBIDDEN` response surface as an error banner
rather than trying to pre-compute the caller's permissions client-side,
same pattern as the Tools page's execute forms). The page explicitly notes
that a provider/model row is only *discoverable*, not necessarily
*callable* — that still depends on a matching Go adapter being registered
at server boot (`docs/AI_ARCHITECTURE.md`).

Caught a real bug during live verification (not a mock issue): the first
draft of the Profiles create form posted `preferred_model_refs`, but the
API's actual field is `preferred_model_ids` — found by testing the form
against the running API, not by assuming the guessed name was right, and
fixed in `lib/types.ts` and the form before commit. Verified live
end-to-end: created a profile, sent a chat message through `local-echo`
and got a real `echo: <message>` response with real token counts,
registered a new model (`ollama/llama3.1`) and saw it appear in the table
immediately.

## What's deliberately not built yet

- Real-time updates (polling/websockets) — every page loads once and offers
  no live refresh beyond a manual reload after a mutating action
- A tool-approval-TTL settings UI (`docs/AGENTS.md` — the API supports
  per-org per-tool overrides, but no page manages them yet)
- Any settings/RBAC management UI (roles are seeded, not yet editable from
  the UI)

Tracked in `docs/ROADMAP.md`.
