CREATE TABLE backup_targets (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    kind            TEXT NOT NULL CHECK (kind IN ('local', 's3', 'sftp')),
    config          JSONB NOT NULL DEFAULT '{}',  -- non-secret settings only (bucket, path)
    credential_secret_key TEXT,                   -- reference into internal/secrets
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (organization_id, name)
);

CREATE TABLE backups (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    project_id      UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    target_id       UUID REFERENCES backup_targets(id) ON DELETE SET NULL,
    policy_id       UUID,
    type            TEXT NOT NULL CHECK (type IN ('database', 'files', 'media', 'full', 'configuration')),
    status          TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'running', 'completed', 'failed', 'verifying', 'corrupt', 'deleted')),
    size_bytes      BIGINT NOT NULL DEFAULT 0,
    checksum_sha256 TEXT,
    storage_ref     TEXT,
    retention_until TIMESTAMPTZ,
    verified_at     TIMESTAMPTZ,
    job_id          UUID REFERENCES jobs(id) ON DELETE SET NULL,
    created_by_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    error           TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at     TIMESTAMPTZ
);
CREATE INDEX idx_backups_org_project ON backups(organization_id, project_id, created_at DESC);

CREATE TABLE backup_policies (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    project_id      UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    target_id       UUID REFERENCES backup_targets(id) ON DELETE SET NULL,
    schedule        TEXT NOT NULL CHECK (schedule IN ('daily', 'weekly', 'monthly')),
    type            TEXT NOT NULL CHECK (type IN ('database', 'files', 'media', 'full', 'configuration')),
    retention_days  INTEGER NOT NULL DEFAULT 30 CHECK (retention_days BETWEEN 1 AND 3650),
    enabled         BOOLEAN NOT NULL DEFAULT true,
    last_run_at     TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (project_id, schedule, type)
);
ALTER TABLE backups ADD CONSTRAINT backups_policy_fk FOREIGN KEY (policy_id) REFERENCES backup_policies(id) ON DELETE SET NULL;

CREATE TABLE restores (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    project_id      UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    backup_id       UUID NOT NULL REFERENCES backups(id) ON DELETE CASCADE,
    safety_backup_id UUID REFERENCES backups(id) ON DELETE SET NULL,
    status          TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'running', 'completed', 'failed', 'rolled_back')),
    job_id          UUID REFERENCES jobs(id) ON DELETE SET NULL,
    created_by_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    error           TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at     TIMESTAMPTZ
);
