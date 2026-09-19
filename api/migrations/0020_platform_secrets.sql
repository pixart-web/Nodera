-- P1 hardening: platform secrets. Organization secrets (migration 0009)
-- already exist; platform-wide credentials (a future cloud AI provider
-- API key, an infrastructure provider credential) currently only come
-- from environment variables, which can't be managed, rotated, or
-- audited through the API the way an organization's own secrets can.
-- platform_secrets mirrors the org-scoped secrets table exactly, minus
-- organization_id — same AES-256-GCM-at-rest encryption
-- (internal/secrets.Service reuses its existing cipher, keyed by the
-- same NODERA_SECRETS_ENCRYPTION_KEY), same "metadata over HTTP, value
-- only in-process via Reveal" posture.
CREATE TABLE platform_secrets (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    key                TEXT NOT NULL UNIQUE,
    description        TEXT NOT NULL DEFAULT '',
    ciphertext         BYTEA NOT NULL,
    created_by_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO platform_permissions (key, description) VALUES
    ('platform.secrets.manage', 'Set, update, delete, and reveal platform-wide secrets'),
    ('platform.secrets.read',   'List platform-wide secret metadata (never values)');
