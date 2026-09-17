-- Secrets module (section 21): reference-based, encrypted-at-rest storage.
-- A secret's plaintext value is NEVER a plain column — ciphertext only,
-- AES-256-GCM (nonce || ciphertext), keyed by NODERA_SECRETS_ENCRYPTION_KEY
-- (an application-level env var, not stored in this database). A proper
-- external KMS/vault integration is future work — see docs/SECURITY.md.

CREATE TABLE secrets (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id    UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    key                TEXT NOT NULL, -- e.g. 'ai_provider.openai.api_key'
    description        TEXT NOT NULL DEFAULT '',
    ciphertext         BYTEA NOT NULL,
    created_by_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (organization_id, key)
);
CREATE INDEX idx_secrets_org ON secrets(organization_id);
