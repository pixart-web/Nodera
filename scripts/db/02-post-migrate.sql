-- Run as nodera_owner after EVERY nodera-migrate (idempotent). Applies the
-- database-level protections that cannot be expressed as default privileges.
--
--   docker exec -i nodera-cp-postgres psql -U nodera_owner -d nodera -v ON_ERROR_STOP=1 < scripts/db/02-post-migrate.sql

-- Audit log is append-only for the runtime role: it can INSERT and SELECT, never
-- UPDATE, DELETE or TRUNCATE. Retention is the one legitimate deletion, and goes
-- through a SECURITY DEFINER function that refuses to purge anything younger
-- than 90 days (migration 0032).
REVOKE UPDATE, DELETE, TRUNCATE ON audit_log FROM nodera_app;
GRANT EXECUTE ON FUNCTION audit_log_apply_retention(UUID, TIMESTAMPTZ) TO nodera_app;

-- The runtime role reads the migration ledger but cannot rewrite it.
REVOKE INSERT, UPDATE, DELETE, TRUNCATE ON schema_migrations FROM nodera_app;

-- Platform audit permission grants / secrets are managed through the API, so the
-- app role keeps DML there; the protections above are the immutable parts.
