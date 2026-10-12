-- Audit retention through a SECURITY DEFINER function so the runtime role does
-- not need blanket DELETE on audit_log. In production the application role has
-- INSERT/SELECT only on audit_log (scripts/db/production-roles.sql) and may call
-- this function, which refuses to purge anything younger than 90 days and only
-- ever deletes rows of the single organisation named by the caller.
CREATE OR REPLACE FUNCTION audit_log_apply_retention(p_org UUID, p_cutoff TIMESTAMPTZ)
RETURNS BIGINT
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE
    n BIGINT;
BEGIN
    IF p_org IS NULL THEN
        RAISE EXCEPTION 'organization is required';
    END IF;
    IF p_cutoff > now() - interval '90 days' THEN
        RAISE EXCEPTION 'audit retention cutoff must be at least 90 days in the past';
    END IF;
    DELETE FROM audit_log WHERE organization_id = p_org AND created_at < p_cutoff;
    GET DIAGNOSTICS n = ROW_COUNT;
    RETURN n;
END
$$;

REVOKE ALL ON FUNCTION audit_log_apply_retention(UUID, TIMESTAMPTZ) FROM PUBLIC;
