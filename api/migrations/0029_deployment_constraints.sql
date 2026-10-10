-- At most one active deployment per project/environment: concurrent deploys
-- to the same target would race on the live directory.
CREATE UNIQUE INDEX uq_deployments_active ON deployments(project_id, environment) WHERE status IN ('queued', 'running');

-- Deployment pipeline stage, surfaced in the UI.
ALTER TABLE deployments ADD COLUMN stage TEXT NOT NULL DEFAULT 'queued';
ALTER TABLE deployments ADD COLUMN file_count INTEGER NOT NULL DEFAULT 0;

-- Same guard for migrations: one non-terminal migration per target domain.
CREATE UNIQUE INDEX uq_site_migrations_active_target ON site_migrations(organization_id, lower(target_domain))
    WHERE status NOT IN ('completed', 'failed', 'rolled_back');
