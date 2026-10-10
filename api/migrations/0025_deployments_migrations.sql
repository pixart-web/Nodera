CREATE TABLE deployments (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    project_id      UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    application_id  UUID REFERENCES applications(id) ON DELETE SET NULL,
    source          TEXT NOT NULL CHECK (source IN ('github', 'git', 'upload')),
    repository      TEXT,
    ref             TEXT NOT NULL DEFAULT 'main',
    commit_sha      TEXT,
    environment     TEXT NOT NULL DEFAULT 'production',
    status          TEXT NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'rolled_back', 'cancelled')),
    previous_deployment_id UUID REFERENCES deployments(id) ON DELETE SET NULL,
    job_id          UUID REFERENCES jobs(id) ON DELETE SET NULL,
    created_by_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    error           TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at     TIMESTAMPTZ
);
CREATE INDEX idx_deployments_project ON deployments(organization_id, project_id, created_at DESC);

CREATE TABLE deployment_artifacts (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    deployment_id   UUID NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    kind            TEXT NOT NULL CHECK (kind IN ('source', 'build', 'image', 'bundle')),
    name            TEXT NOT NULL,
    checksum_sha256 TEXT,
    size_bytes      BIGINT NOT NULL DEFAULT 0,
    storage_ref     TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE site_migrations (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    project_id      UUID REFERENCES projects(id) ON DELETE SET NULL,
    source_kind     TEXT NOT NULL CHECK (source_kind IN ('cpanel', 'ftp', 'sftp', 'ssh', 'updraftplus', 'zip', 'wordpress', 'manual')),
    source_config   JSONB NOT NULL DEFAULT '{}',   -- non-secret descriptors; credentials via secret reference
    credential_secret_key TEXT,
    target_domain   TEXT NOT NULL,
    target_node_id  UUID REFERENCES nodes(id) ON DELETE SET NULL,
    status          TEXT NOT NULL DEFAULT 'discovering' CHECK (status IN (
        'discovering', 'validating', 'transferring', 'importing', 'ready_for_cutover', 'cutting_over', 'completed', 'failed', 'rolled_back'
    )),
    phase           TEXT NOT NULL DEFAULT 'discovery' CHECK (phase IN (
        'discovery', 'preflight', 'transfer', 'database', 'media', 'plugins', 'theme', 'users', 'url_rewrite', 'validation', 'cutover', 'complete'
    )),
    preflight_report JSONB,
    validation_report JSONB,
    health_score    INTEGER CHECK (health_score BETWEEN 0 AND 100),
    job_id          UUID REFERENCES jobs(id) ON DELETE SET NULL,
    created_by_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    error           TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at     TIMESTAMPTZ
);
CREATE INDEX idx_site_migrations_org ON site_migrations(organization_id, created_at DESC);
