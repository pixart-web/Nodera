-- P1 hardening: platform/identity audit. Identity events (signup, login,
-- logout, password change, session revocation) happen before an
-- organization is ever selected and so are written to audit_log with
-- organization_id NULL (internal/audit.Record already supported this —
-- see its nullable organization_id handling). Reading that slice back
-- (internal/audit.QueryPlatform) is a platform-admin capability, gated by
-- this new permission rather than any organization's own audit.read.
INSERT INTO platform_permissions (key, description) VALUES
    ('platform.audit.read', 'Read the platform-scope audit log (identity/auth events not tied to any organization)');
