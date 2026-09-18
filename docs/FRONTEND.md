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
      secrets/page.tsx          secret metadata list, set, delete — never values
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

## Why hand-written types instead of generated ones

There is no OpenAPI spec yet (`docs/API.md` "Not yet implemented" —
generation is planned). `lib/types.ts` is kept intentionally small and
mirrors only the fields the UI actually reads, not the full Go struct —
once OpenAPI generation exists, this file is the one to replace.

## What's deliberately not built yet

- Real-time updates (polling/websockets) — every page loads once and offers
  no live refresh beyond a manual reload after a mutating action
- AI profile / chat UI (the API supports it — `docs/AI_ARCHITECTURE.md` —
  but no page calls it yet)
- Approvals UI (no backend exists for it yet either — `docs/AGENTS.md`)
- Any settings/RBAC management UI (roles are seeded, not yet editable from
  the UI)
- Pagination (matches the API's current unpaginated list endpoints)

Tracked in `docs/ROADMAP.md`.
