-- Applications/services domain (foundation): mirrors the infrastructure
-- node model's shape. Deployment history is deliberately not modeled yet —
-- it belongs to the jobs system (a deployment is a job) once a real deploy
-- backend exists; see docs/ROADMAP.md.

CREATE TABLE applications (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    kind            TEXT NOT NULL DEFAULT 'service', -- 'service' | 'web' | 'worker' | 'job' | 'database'
    node_id         UUID REFERENCES nodes(id) ON DELETE SET NULL,
    environment     TEXT NOT NULL DEFAULT 'production',
    status          TEXT NOT NULL DEFAULT 'unknown' CHECK (
        status IN ('unknown', 'running', 'stopped', 'degraded', 'failed')
    ),
    repository_url  TEXT,
    labels          JSONB NOT NULL DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (organization_id, name)
);
CREATE INDEX idx_applications_org ON applications(organization_id);
CREATE INDEX idx_applications_node ON applications(node_id);
