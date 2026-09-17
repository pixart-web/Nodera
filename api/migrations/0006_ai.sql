-- AI subsystem foundation (sections 9-15): provider registry, model
-- registry, AI profiles, and usage tracking. Operational metadata only —
-- prompt/response content is never persisted here by default (section 15).

CREATE TABLE ai_providers (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    key             TEXT NOT NULL UNIQUE, -- e.g. 'openai', 'ollama-local', 'local-echo'
    kind            TEXT NOT NULL CHECK (kind IN ('cloud', 'local')),
    display_name    TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'unconfigured' CHECK (
        status IN ('unconfigured', 'active', 'disabled', 'unavailable')
    ),
    config          JSONB NOT NULL DEFAULT '{}', -- non-secret config (base URL, region...); credentials referenced via secrets module, never stored here
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE ai_models (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    provider_id         UUID NOT NULL REFERENCES ai_providers(id) ON DELETE CASCADE,
    model_identifier    TEXT NOT NULL, -- provider's own model ID/name
    display_name        TEXT NOT NULL,
    capabilities        TEXT[] NOT NULL DEFAULT '{}', -- 'chat' | 'reasoning' | 'coding' | 'vision' | 'embeddings' | 'reranking' | 'structured_output' | 'agent_execution'
    context_window      INTEGER,
    status              TEXT NOT NULL DEFAULT 'unavailable' CHECK (
        status IN ('available', 'unavailable', 'deprecated')
    ),
    node_id             UUID REFERENCES nodes(id) ON DELETE SET NULL, -- set for locally-hosted models
    quantization        TEXT,
    cost_input_per_1k_usd   NUMERIC(12,6),
    cost_output_per_1k_usd  NUMERIC(12,6),
    tags                TEXT[] NOT NULL DEFAULT '{}',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (provider_id, model_identifier)
);

CREATE TABLE ai_profiles (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id     UUID REFERENCES organizations(id) ON DELETE CASCADE, -- NULL => system-defined profile
    key                 TEXT NOT NULL, -- e.g. 'infrastructure.analysis'
    description         TEXT NOT NULL DEFAULT '',
    required_capabilities TEXT[] NOT NULL DEFAULT '{}',
    privacy_level       TEXT NOT NULL DEFAULT 'internal' CHECK (
        privacy_level IN ('public', 'internal', 'confidential', 'restricted')
    ),
    preferred_model_ids TEXT[] NOT NULL DEFAULT '{}', -- ordered preference, resolved by the router
    fallback_model_ids  TEXT[] NOT NULL DEFAULT '{}',
    temperature         NUMERIC(3,2) NOT NULL DEFAULT 0.2,
    max_tokens          INTEGER NOT NULL DEFAULT 2048,
    timeout_seconds     INTEGER NOT NULL DEFAULT 60,
    retry_policy        JSONB NOT NULL DEFAULT '{"max_attempts": 2}',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (organization_id, key)
);

-- Operational metrics only (section 15) — no prompt/response content.
CREATE TABLE ai_usage_records (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    profile_key     TEXT NOT NULL,
    provider_key    TEXT NOT NULL,
    model_identifier TEXT NOT NULL,
    classification  TEXT NOT NULL CHECK (classification IN ('local', 'cloud')),
    input_tokens    INTEGER NOT NULL DEFAULT 0,
    output_tokens   INTEGER NOT NULL DEFAULT 0,
    total_tokens    INTEGER NOT NULL DEFAULT 0,
    latency_ms      INTEGER,
    status          TEXT NOT NULL CHECK (status IN ('success', 'error', 'timeout')),
    retries         INTEGER NOT NULL DEFAULT 0,
    fallback_used   BOOLEAN NOT NULL DEFAULT false,
    estimated_cost_usd NUMERIC(12,6),
    correlation_id  TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_ai_usage_org_created ON ai_usage_records(organization_id, created_at DESC);

-- Seed the local-echo test provider (ADR-006) so integration tests and the
-- deterministic router have something real, non-fake to exercise. It never
-- claims to be a production model.
INSERT INTO ai_providers (key, kind, display_name, status, config) VALUES
    ('local-echo', 'local', 'Local Echo (test provider)', 'active', '{}');

INSERT INTO ai_models (provider_id, model_identifier, display_name, capabilities, context_window, status, tags)
SELECT id, 'echo-1', 'Echo 1 (deterministic test model)', ARRAY['chat'], 8192, 'available', ARRAY['test']
FROM ai_providers WHERE key = 'local-echo';
