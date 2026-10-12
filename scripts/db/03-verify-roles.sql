-- Proves the role model. Run as nodera_app; every statement below that MUST
-- fail is wrapped so the script reports PASS/FAIL per check and exits non-zero
-- through ON_ERROR_STOP when a protection is missing.
--
--   PGPASSWORD=... psql -h localhost -U nodera_app -d nodera -v ON_ERROR_STOP=1 -f scripts/db/03-verify-roles.sql
DO $$
DECLARE
    must_fail TEXT[] := ARRAY[
        'UPDATE audit_log SET action = action',
        'DELETE FROM audit_log',
        'TRUNCATE audit_log',
        'DELETE FROM schema_migrations',
        'CREATE TABLE nodera_app_should_not_create (id int)',
        'DROP TABLE audit_log',
        'ALTER TABLE audit_log ADD COLUMN x int',
        'SELECT audit_log_apply_retention(NULL, now())',
        'SELECT audit_log_apply_retention(gen_random_uuid(), now())'  -- cutoff too recent
    ];
    stmt TEXT;
BEGIN
    IF to_regclass('public.audit_log') IS NULL THEN
        RAISE EXCEPTION 'FAIL: audit_log does not exist: run nodera-migrate first';
    END IF;
    FOREACH stmt IN ARRAY must_fail LOOP
        BEGIN
            EXECUTE stmt;
            RAISE EXCEPTION 'FAIL: nodera_app was allowed to run: %', stmt;
        EXCEPTION
            WHEN insufficient_privilege OR raise_exception OR feature_not_supported THEN
                IF SQLERRM LIKE 'FAIL:%' THEN RAISE; END IF;
                RAISE NOTICE 'PASS: refused: % (%)', stmt, SQLERRM;
        END;
    END LOOP;

    -- And the things the API legitimately needs still work.
    PERFORM count(*) FROM audit_log;
    PERFORM count(*) FROM users;
    PERFORM audit_log_apply_retention(gen_random_uuid(), now() - interval '120 days');
    RAISE NOTICE 'PASS: nodera_app can read audit_log and run retention for an old cutoff';
END $$;
