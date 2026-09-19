-- Adds a terminal 'cancelled' status to approvals, for
-- internal/tools.CancelApproval. A requester who realizes a pending
-- tool call was a mistake can withdraw it themselves rather than waiting
-- for an approver to reject it or for it to expire — the same
-- "act on your own resource" pattern RevokeSession/RevokeAPIToken already
-- follow, applied to approvals for the first time.
ALTER TABLE approvals DROP CONSTRAINT approvals_status_check;
ALTER TABLE approvals ADD CONSTRAINT approvals_status_check
    CHECK (status IN ('pending', 'approved', 'rejected', 'expired', 'cancelled'));
