# Deployment

Status: **PLANNED**. Nodera runs locally today (see `README.md`); no
production deployment exists. This document records what production
deployment will need, and explicitly the Hetzner values that **cannot** be
filled in yet (rule 30).

## What's needed before a first production deploy

1. **A container image for the Core API.** Not built yet — `api/` compiles
   to a single static-ish Go binary (`cmd/server`), so a minimal
   `FROM gcr.io/distroless/static` (or `scratch` + CA certs) multi-stage
   Dockerfile is the natural shape once this is prioritized. Dev's
   `docker-compose.yml` intentionally only runs Postgres/Redis, not the API
   itself — rule 29 (dev compose ≠ production compose).
2. **A production Postgres instance** with connection details injected via
   `NODERA_DATABASE_URL` — never hardcoded.
3. **A production DB role with `UPDATE`/`DELETE` revoked on `audit_log`**
   (see `docs/SECURITY.md`) — depends on how the production role/schema
   layout is finalized.
4. **Redis** — still optional. The jobs worker (`internal/jobs`) polls
   Postgres directly (`FOR UPDATE SKIP LOCKED`); Redis becomes relevant only
   if/when a shared, multi-instance-safe queue or rate limiter is added
   (today's rate limiter is in-process only — `docs/SECURITY.md`).
5. **TLS termination** — not decided (reverse proxy vs. Go's own TLS); no
   assumption made yet.
6. **`NODERA_SECRETS_ENCRYPTION_KEY`** — a real, securely-generated
   (`openssl rand -base64 32`) production key, delivered via whatever
   secrets-injection mechanism the deployment platform provides (not this
   repo's `.env`). The application-level encryption itself is implemented
   (`docs/SECURITY.md` Secrets); only the production key-delivery mechanism
   is undecided.
7. **A build and hosting target for `web/`** (`npm run build` output —
   static export or a Node server via `next start`; not decided yet) and
   its `NEXT_PUBLIC_NODERA_API_URL` pointed at the production API origin,
   which must also appear in the API's `NODERA_CORS_ORIGINS`.

## Required environment variables

See `.env.example` at the repo root — it is the authoritative list.
`NODERA_DATABASE_URL` and (implicitly) a reachable Postgres are the only
hard requirements to boot the server; everything else has a safe default.

## Hetzner integration — exact values needed once finalized

Per rule 30, none of the following are assumed anywhere in the codebase.
When the Hetzner environment is ready, these are the concrete values this
project will need:

- Production `NODERA_DATABASE_URL` (host, port, database name, credentials)
- Production Redis URL, if/when the job system needs it deployed
- Final domain name(s) for the Core API and the frontend, for CORS/TLS config
- Node hostnames/IPs for each Hetzner server that should be registered via
  `POST /api/v1/infrastructure/nodes` (or an eventual auto-discovery flow)
  with `provider: "hetzner"` and its real `provider_resource_id`
- Firewall rules permitting the Core API to reach Postgres/Redis and
  (eventually) each Node Agent
- Backup destination/credentials (S3-compatible bucket, Hetzner Storage Box,
  or otherwise) once the backups domain is implemented

## Health checks for orchestration

`GET /health` (process liveness) and `GET /ready` (liveness + DB
reachability) already exist and are safe to wire into any orchestrator
(Docker healthcheck, Kubernetes probes, a Hetzner load balancer) today.
