-- Audit log (section 6). Append-only by convention: application code only
-- ever INSERTs here; no UPDATE/DELETE path is exposed by the audit module.
-- True immutability (revoke UPDATE/DELETE at the DB role level) is a
-- deployment-time hardening step documented in docs/DEPLOYMENT.md, since it
-- depends on the production DB role layout which is not finalized yet.

CREATE TABLE audit_log (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id     UUID REFERENCES organizations(id) ON DELETE SET NULL,
    actor_user_id       UUID REFERENCES users(id) ON DELETE SET NULL,
    actor_service_account_id UUID REFERENCES service_accounts(id) ON DELETE SET NULL,
    actor_label         TEXT NOT NULL, -- human-readable actor snapshot, survives actor deletion
    action              TEXT NOT NULL, -- e.g. 'infrastructure.node.created'
    resource_type       TEXT NOT NULL, -- e.g. 'node'
    resource_id         TEXT,
    source              TEXT NOT NULL DEFAULT 'api', -- 'api' | 'agent' | 'system'
    correlation_id      TEXT,
    success             BOOLEAN NOT NULL,
    previous_state      JSONB,
    resulting_state     JSONB,
    metadata            JSONB NOT NULL DEFAULT '{}',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT audit_log_single_actor CHECK (
        actor_user_id IS NULL OR actor_service_account_id IS NULL
    )
);

CREATE INDEX idx_audit_log_org_created ON audit_log(organization_id, created_at DESC);
CREATE INDEX idx_audit_log_resource ON audit_log(resource_type, resource_id);
CREATE INDEX idx_audit_log_correlation ON audit_log(correlation_id);
