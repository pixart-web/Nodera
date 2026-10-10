-- Operation framework: jobs gain steps, logs, project/creator linkage and the
-- richer lifecycle (paused / waiting / rolling_back). 'succeeded' remains the
-- persisted spelling of SUCCESS so existing data and the UI keep working.

ALTER TABLE jobs DROP CONSTRAINT jobs_status_check;
ALTER TABLE jobs ADD CONSTRAINT jobs_status_check CHECK (
    status IN ('queued', 'running', 'paused', 'waiting', 'succeeded', 'failed', 'cancelled', 'retrying', 'rolling_back')
);
ALTER TABLE jobs ADD COLUMN project_id UUID REFERENCES projects(id) ON DELETE SET NULL;
ALTER TABLE jobs ADD COLUMN created_by_user_id UUID REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE jobs ADD COLUMN operation TEXT;           -- operation name, e.g. 'project.create'
ALTER TABLE jobs ADD COLUMN cancel_requested BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE jobs ADD COLUMN rolled_back BOOLEAN NOT NULL DEFAULT false;
CREATE INDEX idx_jobs_project ON jobs(project_id);

CREATE TABLE job_steps (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    job_id      UUID NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    seq         INTEGER NOT NULL,
    name        TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'pending' CHECK (
        status IN ('pending', 'running', 'succeeded', 'failed', 'skipped', 'rolled_back', 'rollback_failed')
    ),
    progress    NUMERIC(5,2) NOT NULL DEFAULT 0,
    error       TEXT,
    started_at  TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    UNIQUE (job_id, seq)
);

CREATE TABLE job_logs (
    id          BIGSERIAL PRIMARY KEY,
    job_id      UUID NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    step_seq    INTEGER,
    ts          TIMESTAMPTZ NOT NULL DEFAULT now(),
    level       TEXT NOT NULL DEFAULT 'info' CHECK (level IN ('debug', 'info', 'success', 'warning', 'error')),
    message     TEXT NOT NULL,
    metadata    JSONB NOT NULL DEFAULT '{}'
);
CREATE INDEX idx_job_logs_job ON job_logs(job_id, id);

-- Node agents (separate from AI "agents"): identity, enrolment, credentials,
-- heartbeats and the signed command queue. See docs/NODE-AGENT.md.
CREATE TABLE node_agent_registrations (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    node_id         UUID NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    token_hash      TEXT NOT NULL UNIQUE,        -- SHA-256 of the one-time enrolment token
    expires_at      TIMESTAMPTZ NOT NULL,
    used_at         TIMESTAMPTZ,
    created_by_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE node_agents (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    node_id         UUID NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    status          TEXT NOT NULL DEFAULT 'unknown' CHECK (status IN ('online', 'offline', 'degraded', 'unknown', 'revoked')),
    version         TEXT NOT NULL DEFAULT '',
    capabilities    TEXT[] NOT NULL DEFAULT '{}',
    public_key      BYTEA NOT NULL,              -- Ed25519 public key; the private key never leaves the node
    last_seen_at    TIMESTAMPTZ,
    last_heartbeat  JSONB NOT NULL DEFAULT '{}',
    registered_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at      TIMESTAMPTZ,
    UNIQUE (node_id)
);

CREATE TABLE node_agent_nonces (
    agent_id    UUID NOT NULL REFERENCES node_agents(id) ON DELETE CASCADE,
    nonce       TEXT NOT NULL,
    seen_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (agent_id, nonce)               -- replay protection
);
CREATE INDEX idx_node_agent_nonces_seen ON node_agent_nonces(seen_at);

CREATE TABLE node_agent_commands (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    agent_id        UUID NOT NULL REFERENCES node_agents(id) ON DELETE CASCADE,
    request_id      TEXT NOT NULL,
    job_id          UUID REFERENCES jobs(id) ON DELETE SET NULL,
    op              TEXT NOT NULL,               -- allowlisted operation, e.g. 'docker.start'
    params          JSONB NOT NULL DEFAULT '{}',
    status          TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'delivered', 'succeeded', 'failed', 'expired')),
    result          JSONB,
    error           TEXT,
    signature       BYTEA NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at      TIMESTAMPTZ NOT NULL,
    delivered_at    TIMESTAMPTZ,
    completed_at    TIMESTAMPTZ
);
CREATE INDEX idx_node_agent_commands_pending ON node_agent_commands(agent_id, status);

-- Feature flags (spec section 53): per-organization override over a global default.
CREATE TABLE feature_flags (
    key         TEXT PRIMARY KEY,
    description TEXT NOT NULL,
    default_enabled BOOLEAN NOT NULL DEFAULT false
);
CREATE TABLE organization_feature_flags (
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    flag_key        TEXT NOT NULL REFERENCES feature_flags(key) ON DELETE CASCADE,
    enabled         BOOLEAN NOT NULL,
    PRIMARY KEY (organization_id, flag_key)
);
INSERT INTO feature_flags (key, description, default_enabled) VALUES
    ('operation_engine',  'Operation framework and job steps', true),
    ('migration_engine',  'WordPress/site migration engine', true),
    ('deployment_engine', 'Deployment engine', true),
    ('node_agent',        'Node agent enrolment and command channel', true),
    ('ai_assistant',      'AI planning assistant (plan -> approval -> job)', true),
    ('client_portal',     'Client portal (foundation only)', false);

INSERT INTO platform_permissions (key, description) VALUES
    ('platform.flags.manage', 'Change global feature flag defaults');
