-- RBAC foundation: roles, granular permissions, role<->permission mapping,
-- and per-organization role assignment. See section 5 of the product brief
-- for the permission naming convention ("infrastructure.read", etc).

CREATE TABLE permissions (
    key         TEXT PRIMARY KEY, -- e.g. 'infrastructure.manage'
    description TEXT NOT NULL
);

CREATE TABLE roles (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    -- organization_id NULL => system-defined role available to every org
    -- (e.g. 'owner', 'admin', 'member'); non-null => a custom org-defined role.
    organization_id UUID REFERENCES organizations(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    is_system       BOOLEAN NOT NULL DEFAULT false,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (organization_id, name)
);

CREATE TABLE role_permissions (
    role_id         UUID NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    permission_key  TEXT NOT NULL REFERENCES permissions(key) ON DELETE CASCADE,
    PRIMARY KEY (role_id, permission_key)
);

-- A member may hold more than one role within the same organization.
CREATE TABLE organization_member_roles (
    organization_id UUID NOT NULL,
    user_id         UUID NOT NULL,
    role_id         UUID NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    granted_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, user_id, role_id),
    FOREIGN KEY (organization_id, user_id) REFERENCES organization_members(organization_id, user_id) ON DELETE CASCADE
);

CREATE INDEX idx_org_member_roles_user ON organization_member_roles(organization_id, user_id);

-- Seed the initial permission catalog (section 5). New permissions are added
-- via migration, never invented ad hoc in application code.
INSERT INTO permissions (key, description) VALUES
    ('infrastructure.read',    'View infrastructure inventory (nodes, containers, networks, storage)'),
    ('infrastructure.manage',  'Create, update, and delete infrastructure resources'),
    ('applications.read',      'View applications and services'),
    ('applications.deploy',    'Deploy or roll back applications'),
    ('backups.create',         'Trigger a backup'),
    ('backups.restore',        'Restore from a backup'),
    ('ai.use',                 'Invoke the AI gateway'),
    ('ai.manage',              'Manage AI providers, models, and profiles'),
    ('agents.execute',         'Trigger agent execution'),
    ('tools.read',              'View the tool registry'),
    ('tools.safe',              'Invoke SAFE-risk tools'),
    ('tools.privileged',        'Invoke PRIVILEGED-risk tools (requires approval workflow)'),
    ('tools.critical',          'Invoke CRITICAL-risk tools (requires approval workflow)'),
    ('secrets.read',            'View secret references and metadata (never secret values)'),
    ('secrets.manage',          'Create, rotate, and delete secrets'),
    ('audit.read',              'Query the audit log'),
    ('organization.manage',     'Manage organization settings, members, and roles'),
    ('jobs.read',                'View job status and history'),
    ('jobs.manage',              'Cancel or retry jobs'),
    ('approvals.decide',         'Approve or reject pending approval requests');

-- System roles: 'owner' (every permission), 'admin' (everything except
-- organization.manage's most destructive aspects — kept equal to owner for
-- phase 1, refined later), 'member' (read-only + safe tool use + ai.use).
INSERT INTO roles (id, organization_id, name, description, is_system) VALUES
    ('00000000-0000-0000-0000-000000000001', NULL, 'owner',  'Full control of the organization', true),
    ('00000000-0000-0000-0000-000000000002', NULL, 'admin',  'Manage infrastructure, AI, agents, and members', true),
    ('00000000-0000-0000-0000-000000000003', NULL, 'member', 'Read access plus safe AI/tool usage', true);

INSERT INTO role_permissions (role_id, permission_key)
SELECT '00000000-0000-0000-0000-000000000001', key FROM permissions;

INSERT INTO role_permissions (role_id, permission_key)
SELECT '00000000-0000-0000-0000-000000000002', key FROM permissions
WHERE key <> 'organization.manage';

INSERT INTO role_permissions (role_id, permission_key) VALUES
    ('00000000-0000-0000-0000-000000000003', 'infrastructure.read'),
    ('00000000-0000-0000-0000-000000000003', 'applications.read'),
    ('00000000-0000-0000-0000-000000000003', 'ai.use'),
    ('00000000-0000-0000-0000-000000000003', 'tools.read'),
    ('00000000-0000-0000-0000-000000000003', 'tools.safe'),
    ('00000000-0000-0000-0000-000000000003', 'jobs.read'),
    ('00000000-0000-0000-0000-000000000003', 'secrets.read');
