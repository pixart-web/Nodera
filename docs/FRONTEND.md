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
      settings/page.tsx           roles catalog + member role assignment
                                  (organization.manage only)
      audit/page.tsx            full audit log
  lib/
    api.ts                 fetch wrapper: bearer token + X-Nodera-Org headers, normalized ApiError
    session.ts               localStorage-backed session/org/user state (client-only)
    useApi.ts                 shared data-fetching hook: explicit loading/error, never a guessed
                                  value; optional `{ pollMs }` for background auto-refresh
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

The table also has an "Approval TTL" column (`ApprovalTTLCell`), populated
only for `privileged`/`critical` tools (a `read`/`safe` tool never has an
approval, so it always shows `—`): the effective TTL (an org override, or
`24h (default)`), with an inline "Edit" control that sets or clears the
org's override for that tool (`tools.manage`-gated server-side; a
`FORBIDDEN` surfaces inline rather than being pre-checked client-side).
Verified live: set `restart_container` to a 15-minute override, executed
it, and confirmed the resulting approval's real `expires_at` was exactly
15 minutes after `created_at` (not the 24h default); cleared the override
and confirmed the column reverted to `1d (default)`.

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

## Settings page

`settings/page.tsx` covers RBAC: a Roles table (name, description, and
permission count/list from `GET /api/v1/roles`) and a Members table
(`GET /api/v1/organization/members`) showing each member's currently
assigned roles as removable badges, with an "Assign role" control that
only offers roles the member doesn't already hold. Both sections are
`organization.manage`-gated server-side; a plain member sees the same page
render an `ErrorBanner` with the real `FORBIDDEN` message for each section
rather than a blank or crashed page — verified live by logging in as an
actual member-role user and confirming the graceful degradation, then as
an owner and confirming role assign/revoke actually changes what's stored
(assigned `admin` to a member alongside their existing `owner` role, then
revoked it, watching the badges update each time).

This required new backend surface that didn't exist before this pass:
`internal/rbac.ListRoles`/`ListMembers`/`AssignRole`/`RevokeRole`, all
gated by `organization.manage`.

The Roles table also has a "Create role" form (name, description,
comma-separated permissions — same convention as the Agents/AI Gateway
pages' comma-separated inputs) and, per custom (non-system) role, a
"Delete" button; system roles show `system` instead, never a delete
control. Creating a role enforces the same no-privilege-escalation rule
as API token scopes and agent `permission_scope`: the permissions can
never exceed the caller's own. Deleting a role that's still assigned to a
member is refused server-side (`CONFLICT`) rather than silently changing
what that member can do — verified live: created a custom `auditor` role,
assigned it to a member alongside their existing `member` role, attempted
delete and saw the real `CONFLICT` ("role is still assigned to one or
more members; revoke it from them first") render inline, revoked it from
the member, and confirmed delete then succeeded and the role disappeared
from the table.

The Members section also has an "Add member" form (email in, `POST
/api/v1/organization/members`) — added in a later pass once the gap it
fills (there was previously no path from "user has an account" to "user
is a member of this org" at all) surfaced from actually using the page.
It adds an *existing* Nodera account (`internal/identity.FindByEmail`) to
the org with the `member` role; it does not create an account or send an
invite email, and the form's own copy says so. Verified live: added a
real second account through the form and watched it appear with the
`member` role; resubmitting the same email surfaced the real `CONFLICT`
("user is already a member of this organization"); an email with no
account surfaced the real `NOT_FOUND` ("user not found") — both as
`ErrorBanner`s inside the form, not silent failures.

The Roles table also has an "Edit" control per custom role, alongside
"Delete" — an inline form (pre-filled with the role's current name and
description) that calls `PUT /api/v1/roles/{id}`, leaving the permission
set untouched (that's still `PUT /roles/{id}/permissions`, a separate
call). System roles show `system` instead of either control, same as
before. Verified live: renamed a custom role and changed its description
through the form, confirmed the table updated and the permission set
(`audit.read`) was unaffected by a details-only edit.

## Real-time updates (polling)

`lib/useApi.ts` takes an optional third argument, `{ pollMs }`: when set,
it re-fetches in the background on that interval, updating `data` on
success and — deliberately — doing nothing on a failed poll tick rather
than surfacing an error or clearing already-displayed data. A transient
network hiccup should degrade to briefly-stale data, not a flashing
loading spinner or a blanked page; `reload()` (still called by every
mutating action, e.g. after enqueuing a job or deciding an approval)
keeps its original behavior of showing the loading state and surfacing
real errors — only the background poll tick is silent.

Applied to the two pages whose data changes independently of anything the
viewer does on that page: `jobs/page.tsx` (5s — job status changes as the
worker processes it) and the Approvals section of `tools/page.tsx` (7s —
a pending approval can be created by another user, expire, or be decided
by someone else entirely). Each page says so in its own copy ("Refreshes
automatically every Ns") rather than silently refreshing with no
indication anything is happening.

Verified live: enqueued a job via a direct API call (not through the
page) and watched it appear with a real `failed` status once the worker
picked it up, with no reload or navigation; separately, triggered a
`restart_container` approval via a direct API call and watched it appear
in the pending list, again with no reload. Checked the browser console
on a fresh tab afterward — zero errors.

## What's deliberately not built yet

- Real-time updates via websockets/SSE (polling now covers the two
  pages where it matters most; every other page still loads once)
- Editing a system role's fixed permission set at all (only custom roles
  can be renamed or have their permissions replaced)

Tracked in `docs/ROADMAP.md`.
