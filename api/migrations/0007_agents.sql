-- Agent/tool/approval foundation (sections 17-19). No execution backend
-- ships in phase 1 — this is schema + domain interfaces so the approval and
-- audit trail exist before any tool is ever actually invoked.

CREATE TABLE agents (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id     UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name                TEXT NOT NULL,
    description         TEXT NOT NULL DEFAULT '',
    system_instructions TEXT NOT NULL DEFAULT '',
    ai_profile_key      TEXT NOT NULL,
    allowed_tool_keys   TEXT[] NOT NULL DEFAULT '{}',
    permission_scope    TEXT[] NOT NULL DEFAULT '{}', -- permission keys the agent may act with
    status              TEXT NOT NULL DEFAULT 'disabled' CHECK (status IN ('active', 'disabled')),
    timeout_seconds     INTEGER NOT NULL DEFAULT 120,
    usage_limits        JSONB NOT NULL DEFAULT '{}',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (organization_id, name)
);

CREATE TABLE tools (
    key             TEXT PRIMARY KEY, -- e.g. 'get_server_metrics'
    description     TEXT NOT NULL,
    risk_level      TEXT NOT NULL CHECK (risk_level IN ('read', 'safe', 'privileged', 'critical')),
    required_permission TEXT NOT NULL REFERENCES permissions(key),
    -- implemented=false means the tool is registered (documented, permission-mapped)
    -- but has no execution backend yet; calling it returns NOT_IMPLEMENTED,
    -- never a fabricated result (rule 36).
    implemented     BOOLEAN NOT NULL DEFAULT false,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE approvals (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id     UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    requested_action     TEXT NOT NULL, -- tool key or action identifier
    requesting_agent_id  UUID REFERENCES agents(id) ON DELETE SET NULL,
    requesting_user_id   UUID REFERENCES users(id) ON DELETE SET NULL,
    risk_level           TEXT NOT NULL CHECK (risk_level IN ('privileged', 'critical')),
    resource_type        TEXT,
    resource_id          TEXT,
    parameters            JSONB NOT NULL DEFAULT '{}',
    reason                TEXT NOT NULL DEFAULT '',
    status                TEXT NOT NULL DEFAULT 'pending' CHECK (
        status IN ('pending', 'approved', 'rejected', 'expired')
    ),
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at            TIMESTAMPTZ,
    decided_by_user_id    UUID REFERENCES users(id) ON DELETE SET NULL,
    decided_at            TIMESTAMPTZ,
    decision_reason       TEXT,
    execution_result       JSONB
);
CREATE INDEX idx_approvals_org_status ON approvals(organization_id, status);

-- Register the example tools named in section 18 as documented-but-not-yet-
-- implemented, so the registry, risk levels, and permission mapping are real
-- from day one even before an execution backend exists.
INSERT INTO tools (key, description, risk_level, required_permission, implemented) VALUES
    ('get_server_metrics',   'Read CPU/memory/disk metrics for a node',        'read',       'infrastructure.read', false),
    ('get_container_logs',   'Read recent logs for a container',                'read',       'infrastructure.read', false),
    ('restart_container',    'Restart a container',                             'privileged', 'infrastructure.manage', false),
    ('create_backup',        'Trigger a backup job',                            'safe',       'backups.create', false),
    ('verify_backup',        'Verify a backup is restorable',                   'safe',       'backups.create', false),
    ('deploy_application',   'Deploy a new application version',                'privileged', 'applications.deploy', false),
    ('rollback_application', 'Roll back an application to a previous version',  'privileged', 'applications.deploy', false),
    ('check_ssl',            'Check a domain''s TLS certificate status',        'read',       'infrastructure.read', false),
    ('scan_wordpress',       'Run a WordPress security scan',                   'safe',       'infrastructure.read', false),
    ('query_database',       'Run a read query against a managed database',     'critical',   'infrastructure.manage', false);
