CREATE TABLE monitors (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    project_id      UUID REFERENCES projects(id) ON DELETE CASCADE,
    kind            TEXT NOT NULL CHECK (kind IN ('http', 'tcp', 'dns', 'ssl', 'container', 'database')),
    name            TEXT NOT NULL,
    target          TEXT NOT NULL,
    interval_seconds INTEGER NOT NULL DEFAULT 60 CHECK (interval_seconds >= 10),
    enabled         BOOLEAN NOT NULL DEFAULT true,
    last_status     TEXT NOT NULL DEFAULT 'unknown' CHECK (last_status IN ('unknown', 'ok', 'warning', 'failing')),
    last_checked_at TIMESTAMPTZ,
    last_latency_ms INTEGER,
    last_error      TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (organization_id, name)
);

CREATE TABLE metric_samples (
    id              BIGSERIAL PRIMARY KEY,
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    node_id         UUID REFERENCES nodes(id) ON DELETE CASCADE,
    project_id      UUID REFERENCES projects(id) ON DELETE CASCADE,
    metric          TEXT NOT NULL CHECK (metric IN ('cpu', 'ram', 'disk', 'network_up', 'network_down', 'container_unhealthy', 'http_latency', 'ssl_days_remaining')),
    value           DOUBLE PRECISION NOT NULL,
    sampled_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_metric_samples_lookup ON metric_samples(organization_id, metric, sampled_at DESC);

CREATE TABLE alert_rules (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    condition       TEXT NOT NULL CHECK (condition IN (
        'cpu_above', 'ram_above', 'disk_above', 'http_failure', 'ssl_expiry_days', 'container_unhealthy', 'database_unavailable'
    )),
    threshold       DOUBLE PRECISION NOT NULL DEFAULT 0,
    severity        TEXT NOT NULL DEFAULT 'warning' CHECK (severity IN ('info', 'warning', 'critical')),
    channels        TEXT[] NOT NULL DEFAULT '{in_app}',
    enabled         BOOLEAN NOT NULL DEFAULT true,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (organization_id, name)
);

CREATE TABLE incidents (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    project_id      UUID REFERENCES projects(id) ON DELETE SET NULL,
    rule_id         UUID REFERENCES alert_rules(id) ON DELETE SET NULL,
    title           TEXT NOT NULL,
    severity        TEXT NOT NULL CHECK (severity IN ('info', 'warning', 'critical')),
    status          TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'acknowledged', 'investigating', 'resolved', 'closed')),
    resource_type   TEXT NOT NULL DEFAULT '',
    resource_id     TEXT NOT NULL DEFAULT '',
    dedupe_key      TEXT,
    detected_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    acknowledged_at TIMESTAMPTZ,
    resolved_at     TIMESTAMPTZ,
    closed_at       TIMESTAMPTZ
);
-- One live incident per (rule, resource): alert evaluation is idempotent.
CREATE UNIQUE INDEX uq_incidents_live_dedupe ON incidents(organization_id, dedupe_key) WHERE dedupe_key IS NOT NULL AND status NOT IN ('resolved', 'closed');
CREATE INDEX idx_incidents_org_status ON incidents(organization_id, status, detected_at DESC);

CREATE TABLE incident_events (
    id              BIGSERIAL PRIMARY KEY,
    incident_id     UUID NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    kind            TEXT NOT NULL,
    message         TEXT NOT NULL,
    actor_label     TEXT NOT NULL DEFAULT 'system',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE alerts (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    rule_id         UUID NOT NULL REFERENCES alert_rules(id) ON DELETE CASCADE,
    incident_id     UUID REFERENCES incidents(id) ON DELETE SET NULL,
    status          TEXT NOT NULL DEFAULT 'firing' CHECK (status IN ('firing', 'resolved')),
    value           DOUBLE PRECISION,
    fired_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at     TIMESTAMPTZ
);

CREATE TABLE notification_channels (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    kind            TEXT NOT NULL CHECK (kind IN ('email', 'webhook', 'in_app')),
    name            TEXT NOT NULL,
    target          TEXT NOT NULL DEFAULT '',     -- webhook URL (SSRF-validated) or email
    enabled         BOOLEAN NOT NULL DEFAULT true,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (organization_id, name)
);

CREATE TABLE notifications (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id         UUID REFERENCES users(id) ON DELETE CASCADE,  -- NULL = visible to the whole organization
    kind            TEXT NOT NULL,
    title           TEXT NOT NULL,
    body            TEXT NOT NULL DEFAULT '',
    resource_type   TEXT NOT NULL DEFAULT '',
    resource_id     TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_notifications_org ON notifications(organization_id, created_at DESC);
CREATE TABLE notification_reads (
    notification_id UUID NOT NULL REFERENCES notifications(id) ON DELETE CASCADE,
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    read_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (notification_id, user_id)
);

CREATE TABLE log_entries (
    id              BIGSERIAL PRIMARY KEY,
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    project_id      UUID REFERENCES projects(id) ON DELETE CASCADE,
    source          TEXT NOT NULL CHECK (source IN ('application', 'container', 'deployment', 'migration', 'system', 'audit')),
    service         TEXT NOT NULL DEFAULT '',
    level           TEXT NOT NULL DEFAULT 'info' CHECK (level IN ('debug', 'info', 'success', 'warning', 'error')),
    message         TEXT NOT NULL,
    metadata        JSONB NOT NULL DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_log_entries_lookup ON log_entries(organization_id, project_id, created_at DESC);

-- Data retention policy (spec section 51), enforced by the retention sweeper.
CREATE TABLE retention_policies (
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    resource        TEXT NOT NULL CHECK (resource IN ('audit_log', 'job_logs', 'metric_samples', 'incidents', 'log_entries')),
    retention_days  INTEGER NOT NULL CHECK (retention_days >= 1),
    PRIMARY KEY (organization_id, resource)
);
