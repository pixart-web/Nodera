-- DNS + SSL engines. Private keys are never stored here: key_secret_key is a
-- reference into internal/secrets (AES-256-GCM at rest).

CREATE TABLE domains (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    project_id      UUID REFERENCES projects(id) ON DELETE SET NULL,
    name            TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'active', 'misconfigured', 'removed')),
    dns_status      TEXT NOT NULL DEFAULT 'pending' CHECK (dns_status IN ('pending', 'ok', 'misconfigured')),
    ssl_status      TEXT NOT NULL DEFAULT 'none' CHECK (ssl_status IN ('none', 'pending', 'valid', 'expiring', 'expired', 'error')),
    dns_provider    TEXT NOT NULL DEFAULT 'mock',
    verified_at     TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at      TIMESTAMPTZ
);
CREATE UNIQUE INDEX uq_domains_org_name ON domains(organization_id, lower(name)) WHERE deleted_at IS NULL;
CREATE INDEX idx_domains_project ON domains(project_id);

CREATE TABLE dns_records (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    domain_id       UUID NOT NULL REFERENCES domains(id) ON DELETE CASCADE,
    type            TEXT NOT NULL CHECK (type IN ('A', 'AAAA', 'CNAME', 'MX', 'TXT', 'CAA')),
    name            TEXT NOT NULL,
    value           TEXT NOT NULL,
    ttl             INTEGER NOT NULL DEFAULT 300 CHECK (ttl >= 30 AND ttl <= 86400),
    priority        INTEGER,
    provider_record_id TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (domain_id, type, name, value)
);

CREATE TABLE certificates (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    domain_id       UUID NOT NULL REFERENCES domains(id) ON DELETE CASCADE,
    status          TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'valid', 'expiring', 'expired', 'error', 'revoked')),
    provider        TEXT NOT NULL DEFAULT 'mock',
    issuer          TEXT NOT NULL DEFAULT '',
    serial          TEXT NOT NULL DEFAULT '',
    not_before      TIMESTAMPTZ,
    not_after       TIMESTAMPTZ,
    auto_renew      BOOLEAN NOT NULL DEFAULT true,
    key_secret_key  TEXT,                       -- reference to the encrypted private key
    last_error      TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- At most one live certificate per domain: this is the SSL idempotency guard.
CREATE UNIQUE INDEX uq_certificates_live_domain ON certificates(domain_id) WHERE status NOT IN ('revoked', 'error', 'expired');

CREATE TABLE certificate_orders (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    certificate_id  UUID NOT NULL REFERENCES certificates(id) ON DELETE CASCADE,
    kind            TEXT NOT NULL CHECK (kind IN ('issue', 'renew', 'revoke')),
    status          TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'running', 'completed', 'failed')),
    job_id          UUID REFERENCES jobs(id) ON DELETE SET NULL,
    error           TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at     TIMESTAMPTZ
);
