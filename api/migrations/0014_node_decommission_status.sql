-- Adds a terminal 'decommissioned' status to nodes, for
-- internal/infrastructure.DecommissionNode. A decommissioned node is kept
-- (not deleted) so audit history and any historical application/model
-- reference to it stays meaningful — deleting the row outright would only
-- null those references (both existing FKs use ON DELETE SET NULL),
-- silently discarding "this application used to run on that node"
-- information a control plane should keep, not lose.
ALTER TABLE nodes DROP CONSTRAINT nodes_status_check;
ALTER TABLE nodes ADD CONSTRAINT nodes_status_check
    CHECK (status IN ('unknown', 'online', 'offline', 'degraded', 'decommissioned'));
