-- Core domain model: Organization -> Client -> Project -> (Application, Domain,
-- Database, Backup, Deployment, Migration, Monitor, ...). Every row carries
-- organization_id and every foreign key between tenant resources is checked
-- in the service layer as well (a project in org A can never reference a
-- client/node/domain of org B). Soft delete (deleted_at) is used where a
-- resource must stay referenceable from audit history.

CREATE TABLE clients (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    contact_email   TEXT NOT NULL DEFAULT '',
    notes           TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at      TIMESTAMPTZ
);
CREATE UNIQUE INDEX uq_clients_org_name ON clients(organization_id, lower(name)) WHERE deleted_at IS NULL;

CREATE TABLE projects (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    client_id       UUID REFERENCES clients(id) ON DELETE SET NULL,
    node_id         UUID REFERENCES nodes(id) ON DELETE SET NULL,
    name            TEXT NOT NULL,
    slug            TEXT NOT NULL,
    kind            TEXT NOT NULL CHECK (kind IN ('wordpress', 'application')),
    status          TEXT NOT NULL DEFAULT 'provisioning' CHECK (
        status IN ('provisioning', 'active', 'degraded', 'failed', 'maintenance', 'deleting', 'deleted')
    ),
    description     TEXT NOT NULL DEFAULT '',
    config          JSONB NOT NULL DEFAULT '{}', -- non-secret, kind-specific configuration
    created_by_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at      TIMESTAMPTZ
);
CREATE UNIQUE INDEX uq_projects_org_slug ON projects(organization_id, slug) WHERE deleted_at IS NULL;
CREATE INDEX idx_projects_org_status ON projects(organization_id, status) WHERE deleted_at IS NULL;
CREATE INDEX idx_projects_client ON projects(client_id);

ALTER TABLE applications ADD COLUMN project_id UUID REFERENCES projects(id) ON DELETE SET NULL;
CREATE INDEX idx_applications_project ON applications(project_id);

CREATE TABLE project_databases (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    project_id      UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    engine          TEXT NOT NULL CHECK (engine IN ('mariadb', 'postgres')),
    username        TEXT NOT NULL,
    -- Credentials live in internal/secrets (encrypted), never in this row.
    password_secret_key TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'provisioning' CHECK (status IN ('provisioning', 'ready', 'failed', 'deleted')),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (organization_id, name)
);
CREATE INDEX idx_project_databases_project ON project_databases(project_id);

-- Granular permissions (spec section 8). Existing keys are left untouched.
INSERT INTO permissions (key, description) VALUES
    ('clients.read',          'View clients'),
    ('clients.manage',        'Create, update, and delete clients'),
    ('projects.read',         'View projects'),
    ('projects.create',       'Create projects (provisioning)'),
    ('projects.update',       'Update projects'),
    ('projects.delete',       'Delete projects (requires approval by default)'),
    ('deployments.read',      'View deployments'),
    ('deployments.create',    'Start deployments'),
    ('deployments.cancel',    'Cancel running deployments'),
    ('deployments.rollback',  'Roll back deployments'),
    ('migrations.read',       'View site migrations'),
    ('migrations.create',     'Plan and run site migrations'),
    ('migrations.cutover',    'Perform a migration cutover (requires approval by default)'),
    ('backups.read',          'View backups, targets, and policies'),
    ('backups.delete',        'Delete backups'),
    ('backups.manage',        'Manage backup targets and policies'),
    ('domains.read',          'View domains and DNS records'),
    ('domains.manage',        'Add domains and change DNS records'),
    ('ssl.read',              'View certificates'),
    ('ssl.issue',             'Issue certificates'),
    ('ssl.renew',             'Renew certificates'),
    ('ssl.revoke',            'Revoke certificates'),
    ('wordpress.read',        'View WordPress sites'),
    ('wordpress.manage',      'Install, clone, update, and delete WordPress sites'),
    ('monitoring.read',       'View metrics, monitors, and health checks'),
    ('monitoring.manage',     'Manage monitors and alert rules'),
    ('incidents.read',        'View incidents'),
    ('incidents.manage',      'Acknowledge, investigate, and resolve incidents'),
    ('logs.read',             'View project and system logs'),
    ('operations.read',       'View operations and their steps/logs');

INSERT INTO role_permissions (role_id, permission_key)
SELECT '00000000-0000-0000-0000-000000000001', key FROM permissions
ON CONFLICT DO NOTHING;
INSERT INTO role_permissions (role_id, permission_key)
SELECT '00000000-0000-0000-0000-000000000002', key FROM permissions WHERE key <> 'organization.manage'
ON CONFLICT DO NOTHING;
INSERT INTO role_permissions (role_id, permission_key) VALUES
    ('00000000-0000-0000-0000-000000000003', 'clients.read'),
    ('00000000-0000-0000-0000-000000000003', 'projects.read'),
    ('00000000-0000-0000-0000-000000000003', 'deployments.read'),
    ('00000000-0000-0000-0000-000000000003', 'migrations.read'),
    ('00000000-0000-0000-0000-000000000003', 'backups.read'),
    ('00000000-0000-0000-0000-000000000003', 'domains.read'),
    ('00000000-0000-0000-0000-000000000003', 'ssl.read'),
    ('00000000-0000-0000-0000-000000000003', 'wordpress.read'),
    ('00000000-0000-0000-0000-000000000003', 'monitoring.read'),
    ('00000000-0000-0000-0000-000000000003', 'incidents.read'),
    ('00000000-0000-0000-0000-000000000003', 'logs.read'),
    ('00000000-0000-0000-0000-000000000003', 'operations.read')
ON CONFLICT DO NOTHING;
