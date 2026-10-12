-- Nodera production database roles. Run ONCE as the PostgreSQL superuser, on an
-- empty `nodera` database, BEFORE the first nodera-migrate run:
--
--   docker exec -i nodera-cp-postgres psql -U postgres -v ON_ERROR_STOP=1 \
--     -v owner_pw="$(cat /etc/nodera/secrets/db-owner-password)" \
--     -v app_pw="$(cat /etc/nodera/secrets/db-app-password)" < scripts/db/01-roles.sql
--
-- Roles:
--   nodera_owner  owns the schema; used ONLY by nodera-migrate (DDL). Never by the API.
--   nodera_app    the API's runtime role: DML on application tables, nothing else.
--   (backups)     pg_dump runs as nodera_owner from the host (read access to everything);
--                 see scripts/backup-nodera.sh. No separate backup role is needed at this scale.
--
-- The API never needs CREATE/ALTER/DROP, superuser, or ownership of anything.

SELECT 'CREATE ROLE nodera_owner LOGIN PASSWORD ' || quote_literal(:'owner_pw')
WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'nodera_owner') \gexec
SELECT 'CREATE ROLE nodera_app LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE PASSWORD ' || quote_literal(:'app_pw')
WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'nodera_app') \gexec

-- Idempotent password rotation.
SELECT 'ALTER ROLE nodera_owner PASSWORD ' || quote_literal(:'owner_pw') \gexec
SELECT 'ALTER ROLE nodera_app PASSWORD ' || quote_literal(:'app_pw') \gexec

ALTER DATABASE nodera OWNER TO nodera_owner;
REVOKE ALL ON DATABASE nodera FROM PUBLIC;
GRANT CONNECT ON DATABASE nodera TO nodera_owner, nodera_app;

\connect nodera
ALTER SCHEMA public OWNER TO nodera_owner;
REVOKE ALL ON SCHEMA public FROM PUBLIC;
GRANT USAGE ON SCHEMA public TO nodera_app;

-- Anything the owner creates later (every migration) is usable by the app role
-- without a per-release grant step.
ALTER DEFAULT PRIVILEGES FOR ROLE nodera_owner IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO nodera_app;
ALTER DEFAULT PRIVILEGES FOR ROLE nodera_owner IN SCHEMA public GRANT USAGE, SELECT ON SEQUENCES TO nodera_app;
