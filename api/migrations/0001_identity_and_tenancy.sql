-- Identity & tenancy foundation: organizations, users, credentials, sessions,
-- service accounts, API tokens. See docs/DATABASE.md.

CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE organizations (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL,
    slug        TEXT NOT NULL UNIQUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE users (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email          TEXT NOT NULL UNIQUE,
    display_name   TEXT NOT NULL,
    status         TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Password credentials are separated from the user record so future
-- credential types (WebAuthn, SSO-linked) can be added without touching the
-- users table, and so a password hash is never accidentally selected by a
-- generic "get user" query.
CREATE TABLE user_password_credentials (
    user_id         UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    password_hash   TEXT NOT NULL, -- argon2id encoded hash, never plaintext
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Opaque, server-side, instantly revocable sessions (ADR-005). The token
-- itself is never stored — only its SHA-256 hash — so a DB read of this
-- table cannot be used to impersonate a session.
CREATE TABLE sessions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash      TEXT NOT NULL UNIQUE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at      TIMESTAMPTZ NOT NULL,
    revoked_at      TIMESTAMPTZ,
    ip_address      INET,
    user_agent      TEXT
);
CREATE INDEX idx_sessions_user_id ON sessions(user_id);
CREATE INDEX idx_sessions_expires_at ON sessions(expires_at);

CREATE TABLE organization_members (
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, user_id)
);

-- Service accounts represent non-human actors (agents, integrations,
-- other Nodera-ecosystem products) that act within an organization.
CREATE TABLE service_accounts (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_service_accounts_org ON service_accounts(organization_id);

-- Scoped API tokens for users or service accounts. Like sessions, only the
-- token hash is persisted. `scopes` holds permission strings (e.g.
-- "infrastructure.read") checked by the rbac module.
CREATE TABLE api_tokens (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id     UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id             UUID REFERENCES users(id) ON DELETE CASCADE,
    service_account_id  UUID REFERENCES service_accounts(id) ON DELETE CASCADE,
    name                TEXT NOT NULL,
    token_hash          TEXT NOT NULL UNIQUE,
    token_prefix        TEXT NOT NULL, -- short, non-secret prefix shown in the UI for identification
    scopes              TEXT[] NOT NULL DEFAULT '{}',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at          TIMESTAMPTZ,
    revoked_at          TIMESTAMPTZ,
    last_used_at        TIMESTAMPTZ,
    CONSTRAINT api_tokens_actor_check CHECK (
        (user_id IS NOT NULL AND service_account_id IS NULL) OR
        (user_id IS NULL AND service_account_id IS NOT NULL)
    )
);
CREATE INDEX idx_api_tokens_org ON api_tokens(organization_id);
