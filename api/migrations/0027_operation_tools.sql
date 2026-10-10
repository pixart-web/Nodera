-- Dangerous operations are exposed through the Tool Gateway so they inherit
-- the existing permission -> risk-tier -> approval -> execution -> audit
-- pipeline (and its atomic single-execution guarantee). Their handlers are
-- registered by internal/ops at startup; implemented=true means "has a real
-- execution path", not "touches production" — providers decide that.
INSERT INTO tools (key, description, risk_level, required_permission, implemented) VALUES
    ('project.delete',      'Delete a project and its resources',                'critical',   'projects.delete',     true),
    ('database.delete',     'Delete a project database',                          'critical',   'projects.delete',     true),
    ('backup.restore',      'Restore a backup over a live project',               'critical',   'backups.restore',     true),
    ('backup.delete',       'Delete a backup',                                    'privileged', 'backups.delete',      true),
    ('migration.cutover',   'Cut a migrated site over to the new target',         'critical',   'migrations.cutover',  true),
    ('credential.rotate',   'Rotate a stored credential',                         'privileged', 'secrets.manage',      true),
    ('deployment.production','Deploy to the production environment',              'privileged', 'deployments.create',  true),
    ('domain.remove',       'Remove a domain and its DNS records',                'privileged', 'domains.manage',      true);
