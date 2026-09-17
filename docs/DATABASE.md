# Database

PostgreSQL is Nodera's only system of record ([ADR-003](DECISIONS.md#adr-003-postgresql-as-system-of-record-redis-for-ephemeral-coordination-only)).
Schema is defined entirely by the migrations in `api/migrations/*.sql`,
embedded into the compiled binary (`api/migrations/migrations.go`) and
applied automatically, in order, on every process start
(`internal/platform/db.Migrate`) — there is no manual schema step.

## Migration workflow

1. Add a new file `api/migrations/NNNN_description.sql`, one greater than
   the current highest version.
2. Write forward-only SQL. Nodera does not ship down-migrations — see the
   comment in `internal/platform/db/db.go` on why (production safety; a
   broken migration is fixed forward, not rolled back automatically).
3. Restart the server (or run the test suite, which migrates the test DB) —
   `schema_migrations` tracks what's applied and `Migrate` is idempotent.

## Schema overview (by migration)

| File | Tables |
|---|---|
| `0001_identity_and_tenancy.sql` | `organizations`, `users`, `user_password_credentials`, `sessions`, `organization_members`, `service_accounts`, `api_tokens` |
| `0002_rbac.sql` | `permissions`, `roles`, `role_permissions`, `organization_member_roles` (+ seeded permission catalog and owner/admin/member system roles) |
| `0003_audit.sql` | `audit_log` |
| `0004_infrastructure.sql` | `nodes` |
| `0005_jobs.sql` | `jobs` |
| `0006_ai.sql` | `ai_providers`, `ai_models`, `ai_profiles`, `ai_usage_records` (+ seeded `local-echo` test provider/model) |
| `0007_agents.sql` | `agents`, `tools`, `approvals` (+ seeded tool catalog, all `implemented=false`) |
| `0008_applications.sql` | `applications` |
| `0009_secrets.sql` | `secrets` |

## Conventions

- Primary keys are `UUID DEFAULT gen_random_uuid()` (via `pgcrypto`).
- Every tenant-scoped table has `organization_id UUID NOT NULL REFERENCES organizations(id)`.
- Timestamps are `TIMESTAMPTZ`, defaulting to `now()`.
- Enumerated string columns use `CHECK` constraints rather than Postgres
  `ENUM` types, so adding a new status value is a simple migration rather
  than an `ALTER TYPE`.
- Secrets are never columns in these tables — see `docs/SECURITY.md`.

## Tests against a real database

`internal/testhelpers.RequirePool(t)` connects to `NODERA_TEST_DATABASE_URL`,
runs every migration, truncates all domain tables, and re-seeds two things
`TRUNCATE ... CASCADE` collaterally wipes even though they're seed data, not
per-test data: the system roles/permissions (truncating `organizations`
cascades into `roles`) and the `echo-1` test AI model (truncating `nodes`
cascades into `ai_models`, since `ai_models.node_id` references it) — see
the comments in `internal/testhelpers/testhelpers.go` for why. If the env
var isn't set, integration tests skip rather than fail.
