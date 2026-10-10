-- Migration lifecycle: a plan (discovery + preflight) is separate from the run,
-- and the run only ever touches a staging copy until an approved cutover.
ALTER TABLE site_migrations DROP CONSTRAINT IF EXISTS site_migrations_status_check;
ALTER TABLE site_migrations ADD CONSTRAINT site_migrations_status_check CHECK (status IN (
    'discovering', 'planned', 'preflight_failed', 'validating', 'transferring', 'importing',
    'ready_for_cutover', 'cutting_over', 'completed', 'failed', 'rolled_back'
));
ALTER TABLE site_migrations ADD COLUMN source_ref TEXT;                 -- where the uploaded archive lives (provider path)
ALTER TABLE site_migrations ADD COLUMN staging_db TEXT;                 -- staging database name
ALTER TABLE site_migrations ADD COLUMN safety_backup_id UUID REFERENCES backups(id) ON DELETE SET NULL;
ALTER TABLE site_migrations ADD COLUMN source_url TEXT;                 -- original site URL (for rewriting)
DROP INDEX IF EXISTS uq_site_migrations_active_target;
CREATE UNIQUE INDEX uq_site_migrations_active_target ON site_migrations(organization_id, lower(target_domain))
    WHERE status NOT IN ('completed', 'failed', 'rolled_back', 'preflight_failed');
