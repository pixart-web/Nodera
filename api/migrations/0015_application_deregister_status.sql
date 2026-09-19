-- Adds a terminal 'deregistered' status to applications, mirroring
-- migration 0014's 'decommissioned' status for nodes — same rationale:
-- kept, not deleted, so audit history stays meaningful.
ALTER TABLE applications DROP CONSTRAINT applications_status_check;
ALTER TABLE applications ADD CONSTRAINT applications_status_check
    CHECK (status IN ('unknown', 'running', 'stopped', 'degraded', 'failed', 'deregistered'));
