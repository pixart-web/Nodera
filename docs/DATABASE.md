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
| `0010_tools_get_server_metrics.sql` | data-only: flips `tools.implemented` to `true` for `get_server_metrics` now that it has a real handler |
| `0011_tools_check_ssl.sql` | data-only: same, for `check_ssl` |
| `0012`–`0020` | agents.manage permission, approval TTL overrides, node decommission and application deregister statuses, approval cancelled/executing states, platform authorization / audit permission / platform secrets |
| `0021_core_domain.sql` | `clients`, `projects` (soft delete, unique slug per org), `project_databases` (password only as a secret *reference*), `applications.project_id`, 29 granular permissions granted to owner/admin (member gets the `*.read` set) |
| `0022_operations_engine.sql` | `jobs` extended (operation, project, paused/waiting/rolling_back, cancel flag), `job_steps`, `job_logs`, `node_agent_registrations`, `node_agents`, `node_agent_nonces` (replay table), `node_agent_commands`, `feature_flags`, `organization_feature_flags` |
| `0023_network_ssl.sql` | `domains`, `dns_records`, `certificates` (one live certificate per domain), `certificate_orders` |
| `0024_backups.sql` | `backup_targets`, `backups`, `backup_policies`, `restores` |
| `0025_deployments_migrations.sql` | `deployments`, `deployment_artifacts`, `site_migrations` |
| `0026_monitoring_incidents.sql` | `monitors`, `metric_samples`, `alert_rules`, `incidents` (live-dedupe unique index), `incident_events`, `alerts`, `notification_channels`, `notifications`, `notification_reads`, `log_entries`, `retention_policies` |
| `0027_operation_tools.sql` | data-only: the eight dangerous operations registered as gateway tools (`implemented = true`) |
| `0028_certificate_material.sql` | `certificates.cert_pem`, `subject_names` (the private key is **never** a column: it lives encrypted in `secrets`) |
| `0029_deployment_constraints.sql` | one active deployment per project/environment; deployment stage/file count; one active migration per target domain |
| `0030_migration_states.sql` | `planned`/`preflight_failed` states, staging db, safety backup, source URL |
| `0031_ai_plans.sql` | `ai_plans` (AI proposes, humans approve and run) |

## Conventions

- Primary keys are `UUID DEFAULT gen_random_uuid()` (via `pgcrypto`).
- Every tenant-scoped table has `organization_id UUID NOT NULL REFERENCES organizations(id)`.
- Timestamps are `TIMESTAMPTZ`, defaulting to `now()`.
- Enumerated string columns use `CHECK` constraints rather than Postgres
  `ENUM` types, so adding a new status value is a simple migration rather
  than an `ALTER TYPE`.
- Soft delete (`deleted_at`) on clients/projects/domains; partial unique indexes make names reusable after deletion.
- Integrity rules live in the database, not only in code: one live certificate per domain, one live incident per dedupe key,
  one active deployment per project/environment, one active migration per target domain, nonce primary key for agent replay protection.
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
