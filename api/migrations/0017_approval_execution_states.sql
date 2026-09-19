-- P0 hardening: the approval decision flow previously moved a 'pending'
-- approval straight to 'approved' and executed the tool as part of the
-- same request, with the UPDATE unconditioned on status. Two concurrent
-- decisions could both observe 'pending' and both execute. The fix
-- (internal/tools/tools.go DecideApproval) requires an explicit
-- 'executing' state that a single atomic `UPDATE ... WHERE status =
-- 'pending'` transitions into, so only one caller can ever acquire the
-- right to run the handler, plus explicit terminal 'executed' /
-- 'execution_failed' states so a failed execution is never hidden inside
-- a generic 'approved' status. 'approved' stays a legal value for
-- historical rows written before this migration; new decisions never
-- produce it.
ALTER TABLE approvals DROP CONSTRAINT approvals_status_check;
ALTER TABLE approvals ADD CONSTRAINT approvals_status_check
    CHECK (status IN ('pending', 'approved', 'executing', 'executed', 'execution_failed', 'rejected', 'expired', 'cancelled'));
