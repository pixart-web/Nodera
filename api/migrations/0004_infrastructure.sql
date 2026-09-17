-- Infrastructure domain (section 7-8): provider-agnostic node model. No
-- Hetzner-specific fields — provider-specific data lives in `provider_data`.

CREATE TABLE nodes (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id     UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    hostname            TEXT NOT NULL,
    provider            TEXT NOT NULL DEFAULT 'unknown', -- 'hetzner' | 'local' | 'aws' | ... | 'unknown'
    provider_resource_id TEXT,
    role                TEXT NOT NULL DEFAULT 'application', -- 'application' | 'database' | 'storage' | 'worker' | 'ai-inference' | 'monitoring'
    environment         TEXT NOT NULL DEFAULT 'production',
    status              TEXT NOT NULL DEFAULT 'unknown' CHECK (status IN ('unknown', 'online', 'offline', 'degraded')),
    operating_system    TEXT,
    cpu_cores           INTEGER,
    memory_mb           INTEGER,
    storage_gb          INTEGER,
    capabilities        TEXT[] NOT NULL DEFAULT '{}', -- e.g. 'gpu', 'docker'
    labels              JSONB NOT NULL DEFAULT '{}',
    provider_data       JSONB NOT NULL DEFAULT '{}', -- provider-specific metadata, opaque to core domain
    last_seen_at        TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (organization_id, hostname)
);
CREATE INDEX idx_nodes_org ON nodes(organization_id);
