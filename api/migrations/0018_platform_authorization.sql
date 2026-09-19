-- P1 hardening: platform-scoped authorization, distinct from
-- internal/rbac's organization-scoped permissions. Some resources (the AI
-- provider/model registry today) are platform-wide, not owned by any one
-- organization — gating them with an org permission like ai.manage lets
-- any organization admin mutate global state, which is an authorization
-- mismatch for a control plane. Platform permissions are granted
-- explicitly per user and never inferred from organization ownership or
-- role (no "if email == admin@..." shortcuts, no implicit god-mode).
CREATE TABLE platform_permissions (
    key         TEXT PRIMARY KEY,
    description TEXT NOT NULL
);

INSERT INTO platform_permissions (key, description) VALUES
    ('platform.ai.providers.manage', 'Manage the platform-wide AI provider registry (register/update/delete providers)'),
    ('platform.ai.models.manage',    'Manage the platform-wide AI model registry (register/update/delete models)'),
    ('platform.admins.manage',       'Grant or revoke other users'' platform permissions');

-- Deliberately per-permission, not a single "is_platform_admin" boolean —
-- extensible to future platform-scoped resources (nodes, global
-- infrastructure settings) without collapsing everything into one
-- god-mode flag. A user can hold any subset of platform_permissions.
CREATE TABLE platform_user_permissions (
    user_id            UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    permission_key     TEXT NOT NULL REFERENCES platform_permissions(key) ON DELETE CASCADE,
    granted_by_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    granted_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, permission_key)
);
