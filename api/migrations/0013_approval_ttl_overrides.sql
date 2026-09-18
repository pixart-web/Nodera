-- Per-organization, per-tool approval TTL overrides (docs/AGENTS.md,
-- docs/ROADMAP.md). Previously every pending approval used a single global
-- 24h constant (tools.defaultApprovalTTL) regardless of tool or tenant. An
-- organization can now shorten or lengthen how long a specific tool's
-- pending approvals stay actionable before expiring — e.g. a org that
-- wants same-day sign-off on 'restart_container' but is fine leaving
-- 'query_database' open for a week. No override row for a given
-- (organization, tool) pair means "use the 24h default", same behavior as
-- before this migration.
CREATE TABLE organization_tool_settings (
    organization_id      UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    tool_key              TEXT NOT NULL REFERENCES tools(key) ON DELETE CASCADE,
    approval_ttl_seconds  INTEGER NOT NULL,
    updated_by_user_id    UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, tool_key)
);

-- Configuring how long a tool's approvals stay open is an organization
-- admin decision, not something every member who can request/decide
-- approvals should be able to change — kept distinct from
-- approvals.decide and every tools.* execution permission.
INSERT INTO permissions (key, description) VALUES
    ('tools.manage', 'Configure per-tool approval TTL overrides for the organization');

INSERT INTO role_permissions (role_id, permission_key) VALUES
    ('00000000-0000-0000-0000-000000000001', 'tools.manage'), -- owner
    ('00000000-0000-0000-0000-000000000002', 'tools.manage'); -- admin
