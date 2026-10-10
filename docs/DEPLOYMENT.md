# Deployment

Status: **images, dev compose and a production compose template exist and build**; nothing has been
deployed to the Hetzner node yet, so everything marked *unverified* below is exactly that.

## Artifacts

| File | Purpose |
|---|---|
| `api/Dockerfile` (`--target server`, `--target agent`) | non-root Alpine images; server has a healthcheck on `/health`; the agent image is for each managed node |
| `web/Dockerfile` | Next standalone build; `NEXT_PUBLIC_NODERA_API_URL` is a **build arg** (it is inlined into the browser bundle) |
| `docker-compose.yml` | local development stack (postgres, redis, api, web, optional agent profile) |
| `docker-compose.prod.yml` | production template: required secrets (`${VAR:?}`), no published DB ports, read-only roots, `cap_drop: ALL`, Traefik labels, external `proxy-public` network, agent as a separate profile |
| `.github/workflows/ci.yml` | builds all three images and validates both compose files |

```bash
cp .env.example .env.production   # fill in real values; never commit
docker compose -f docker-compose.prod.yml --env-file .env.production up -d
docker compose -f docker-compose.prod.yml --profile agent up -d agent   # on each managed node
```

## First-deploy checklist for `nodera-prod-01` (Docker + Traefik, existing MariaDB, Nodera PostgreSQL, Redis)

1. Create the secrets: `openssl rand -base64 32` for `NODERA_SECRETS_ENCRYPTION_KEY` and `NODERA_AGENT_SIGNING_KEY`
   (**keep them: losing the first makes stored secrets unreadable; changing the second invalidates enrolled agents**), a strong `NODERA_POSTGRES_PASSWORD`.
2. Set `NODERA_API_HOST`, `NODERA_WEB_HOST`, `NODERA_CORS_ORIGINS` (= the web origin), `NODERA_PROXY_NETWORK` (default `proxy-public`), `NODERA_CERT_RESOLVER`.
3. Production DB role with `UPDATE`/`DELETE` revoked on `audit_log` (see `docs/SECURITY.md`).
4. Bootstrap the first platform admin once with `NODERA_PLATFORM_BOOTSTRAP_ADMIN_EMAIL`.
5. Enrol an agent for the node (`docs/NODE-AGENT.md`), then switch the API to `NODERA_PROVIDER_MODE=docker` **only on the node that owns the Docker socket**
   (or keep the API in `local` mode and let the agent drive Docker).
6. Existing MariaDB: the database provider interface is ready (`providers.DatabaseProvider`); a MariaDB adapter is **not written** (see audit) —
   until then WordPress provisioning needs `mock` or an adapter.
7. Point DNS at Traefik; Traefik's Let's Encrypt resolver issues the public certificates for the Nodera hosts.

## What is NOT verified until it runs on the real node

Traefik routing labels against your Traefik version, the Docker socket / `group_add` GID, the MariaDB adapter, Let's Encrypt/Cloudflare/Hetzner adapters,
real backups to off-box storage, and every `Hetzner` provider call. None of these are hardcoded; they are configuration + adapters still to be written.

## What's needed before a first production deploy

1. **A container image for the Core API.** *Done* (`api/Dockerfile`). Original note: `api/` compiles
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
4. **Redis** — optional but recommended for a multi-instance deployment.
   The jobs worker (`internal/jobs`) still polls Postgres directly
   (`FOR UPDATE SKIP LOCKED`) regardless. Rate limiting
   (`docs/SECURITY.md`), though, falls back to an in-process limiter when
   `NODERA_REDIS_URL` is unset — correct for one instance, but each
   instance then enforces its own separate budget rather than one shared
   across the deployment. Set `NODERA_REDIS_URL` in production to get a
   single shared limit across instances.
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
