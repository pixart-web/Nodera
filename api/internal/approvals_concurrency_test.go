package integration_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/platform/authctx"
	"github.com/nodera/nodera/internal/testhelpers"
	"github.com/nodera/nodera/internal/tools"
)

// countingHandler wires an atomic counter into a tool handler so a test can
// prove exactly how many times it actually ran, independent of how many
// callers attempted to trigger it.
func countingHandler(counter *int64) tools.Handler {
	return func(ctx context.Context, ac authctx.AuthContext, resourceType, resourceID string, params map[string]any) (any, error) {
		atomic.AddInt64(counter, 1)
		return map[string]any{"ok": true}, nil
	}
}

// Test A: simultaneous approvals of the same pending request. Exactly one
// concurrent DecideApproval call may acquire execution ownership and run
// the handler; every other concurrent call must receive a deterministic
// conflict and never touch the handler.
func TestApprovals_ConcurrentApprovalsExecuteExactlyOnce(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "race-approve-owner@nodera.dev")

	if _, err := pool.Exec(ctx, `UPDATE tools SET implemented = true WHERE key = 'restart_container'`); err != nil {
		t.Fatalf("failed to mark restart_container implemented: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `UPDATE tools SET implemented = false WHERE key = 'restart_container'`)
	})

	toolsSvc := tools.New(pool, h.audit)
	var counter int64
	toolsSvc.RegisterHandler("restart_container", countingHandler(&counter))

	result, err := toolsSvc.Execute(ctx, ac, "restart_container", tools.ExecuteInput{
		ResourceType: "container", ResourceID: "race-a",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	approvalID := *result.ApprovalID

	const n = 20
	var wg sync.WaitGroup
	var successes int64
	var conflicts int64
	var otherErrors int64
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := toolsSvc.DecideApproval(ctx, ac, approvalID, true, "concurrent")
			if err == nil {
				atomic.AddInt64(&successes, 1)
				return
			}
			if ae, ok := err.(*apierr.Error); ok && ae.Code == apierr.CodeConflict {
				atomic.AddInt64(&conflicts, 1)
				return
			}
			atomic.AddInt64(&otherErrors, 1)
			t.Logf("unexpected decide error: %v", err)
		}()
	}
	close(start)
	wg.Wait()

	if atomic.LoadInt64(&counter) != 1 {
		t.Fatalf("expected the handler to run exactly once, ran %d times", counter)
	}
	if successes != 1 {
		t.Fatalf("expected exactly 1 successful decision, got %d", successes)
	}
	if conflicts != n-1 {
		t.Fatalf("expected %d conflicts, got %d (otherErrors=%d)", n-1, conflicts, otherErrors)
	}
	if otherErrors != 0 {
		t.Fatalf("expected zero non-conflict errors, got %d", otherErrors)
	}

	approvals, err := toolsSvc.ListApprovals(ctx, ac, "")
	if err != nil {
		t.Fatalf("ListApprovals: %v", err)
	}
	if len(approvals) != 1 || approvals[0].Status != "executed" {
		t.Fatalf("expected exactly one approval in final status 'executed', got %+v", approvals)
	}
}

// Test B: approve and reject racing the same pending approval. Exactly one
// of the two competing transitions may win; the loser must get a
// deterministic conflict, never a corrupted/contradictory state. If
// approve wins, the handler runs exactly once; if reject wins, it never
// runs.
func TestApprovals_ApproveVsRejectRaceIsDeterministic(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)

	const trials = 15
	var approveWins, rejectWins int
	for i := 0; i < trials; i++ {
		ac, _ := h.newOwnerContext(t, ctx, uuid.NewString()+"-race-ar@nodera.dev")
		if _, err := pool.Exec(ctx, `UPDATE tools SET implemented = true WHERE key = 'restart_container'`); err != nil {
			t.Fatalf("failed to mark restart_container implemented: %v", err)
		}

		toolsSvc := tools.New(pool, h.audit)
		var counter int64
		toolsSvc.RegisterHandler("restart_container", countingHandler(&counter))

		result, err := toolsSvc.Execute(ctx, ac, "restart_container", tools.ExecuteInput{
			ResourceType: "container", ResourceID: "race-ar",
		})
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		approvalID := *result.ApprovalID

		var wg sync.WaitGroup
		start := make(chan struct{})
		var approveErr, rejectErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, approveErr = toolsSvc.DecideApproval(ctx, ac, approvalID, true, "approve")
		}()
		go func() {
			defer wg.Done()
			<-start
			_, rejectErr = toolsSvc.DecideApproval(ctx, ac, approvalID, false, "reject")
		}()
		close(start)
		wg.Wait()

		approveOK := approveErr == nil
		rejectOK := rejectErr == nil
		if approveOK == rejectOK {
			t.Fatalf("trial %d: expected exactly one of approve/reject to win, approveErr=%v rejectErr=%v", i, approveErr, rejectErr)
		}

		approvals, err := toolsSvc.ListApprovals(ctx, ac, "")
		if err != nil {
			t.Fatalf("ListApprovals: %v", err)
		}
		if len(approvals) != 1 {
			t.Fatalf("trial %d: expected exactly one approval row, got %d", i, len(approvals))
		}
		final := approvals[0].Status

		if approveOK {
			approveWins++
			if final != "executed" {
				t.Fatalf("trial %d: approve won but final status is %q, not 'executed'", i, final)
			}
			if atomic.LoadInt64(&counter) != 1 {
				t.Fatalf("trial %d: approve won but handler ran %d times", i, counter)
			}
		} else {
			rejectWins++
			if final != "rejected" {
				t.Fatalf("trial %d: reject won but final status is %q, not 'rejected'", i, final)
			}
			if atomic.LoadInt64(&counter) != 0 {
				t.Fatalf("trial %d: reject won but the handler still ran", i)
			}
		}

		pool.Exec(context.Background(), `UPDATE tools SET implemented = false WHERE key = 'restart_container'`)
	}
	t.Logf("approve won %d/%d trials, reject won %d/%d", approveWins, trials, rejectWins, trials)
}

// Test C: approve racing the requester's own cancellation. Same invariant
// as Test B — exactly one legal terminal state, execution happens at most
// once, and only if approve wins the race.
func TestApprovals_ApproveVsCancelRaceIsDeterministic(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)

	const trials = 15
	var approveWins, cancelWins int
	for i := 0; i < trials; i++ {
		ac, _ := h.newOwnerContext(t, ctx, uuid.NewString()+"-race-ac@nodera.dev")
		if _, err := pool.Exec(ctx, `UPDATE tools SET implemented = true WHERE key = 'restart_container'`); err != nil {
			t.Fatalf("failed to mark restart_container implemented: %v", err)
		}

		toolsSvc := tools.New(pool, h.audit)
		var counter int64
		toolsSvc.RegisterHandler("restart_container", countingHandler(&counter))

		result, err := toolsSvc.Execute(ctx, ac, "restart_container", tools.ExecuteInput{
			ResourceType: "container", ResourceID: "race-ac",
		})
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		approvalID := *result.ApprovalID

		var wg sync.WaitGroup
		start := make(chan struct{})
		var approveErr, cancelErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, approveErr = toolsSvc.DecideApproval(ctx, ac, approvalID, true, "approve")
		}()
		go func() {
			defer wg.Done()
			<-start
			_, cancelErr = toolsSvc.CancelApproval(ctx, ac, approvalID)
		}()
		close(start)
		wg.Wait()

		approveOK := approveErr == nil
		cancelOK := cancelErr == nil
		if approveOK == cancelOK {
			t.Fatalf("trial %d: expected exactly one of approve/cancel to win, approveErr=%v cancelErr=%v", i, approveErr, cancelErr)
		}

		approvals, err := toolsSvc.ListApprovals(ctx, ac, "")
		if err != nil {
			t.Fatalf("ListApprovals: %v", err)
		}
		if len(approvals) != 1 {
			t.Fatalf("trial %d: expected exactly one approval row, got %d", i, len(approvals))
		}
		final := approvals[0].Status

		if approveOK {
			approveWins++
			if final != "executed" || atomic.LoadInt64(&counter) != 1 {
				t.Fatalf("trial %d: approve won but final=%q counter=%d", i, final, counter)
			}
		} else {
			cancelWins++
			if final != "cancelled" || atomic.LoadInt64(&counter) != 0 {
				t.Fatalf("trial %d: cancel won but final=%q counter=%d", i, final, counter)
			}
		}

		pool.Exec(context.Background(), `UPDATE tools SET implemented = false WHERE key = 'restart_container'`)
	}
	t.Logf("approve won %d/%d trials, cancel won %d/%d", approveWins, trials, cancelWins, trials)
}

// Test D: an approval past its expiry must never execute, even if a
// decision call races in right after expiry — expirePending runs first and
// flips it to 'expired' before the conditional UPDATE ever gets a chance
// to match 'pending'.
func TestApprovals_ExpiredApprovalNeverExecutes(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "expired-owner@nodera.dev")

	if _, err := pool.Exec(ctx, `UPDATE tools SET implemented = true WHERE key = 'restart_container'`); err != nil {
		t.Fatalf("failed to mark restart_container implemented: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `UPDATE tools SET implemented = false WHERE key = 'restart_container'`)
	})

	toolsSvc := tools.New(pool, h.audit)
	var counter int64
	toolsSvc.RegisterHandler("restart_container", countingHandler(&counter))

	result, err := toolsSvc.Execute(ctx, ac, "restart_container", tools.ExecuteInput{
		ResourceType: "container", ResourceID: "expired-1",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	approvalID := *result.ApprovalID

	if _, err := pool.Exec(ctx, `UPDATE approvals SET expires_at = now() - interval '1 hour' WHERE id = $1`, approvalID); err != nil {
		t.Fatalf("failed to backdate expiry: %v", err)
	}

	if _, err := toolsSvc.DecideApproval(ctx, ac, approvalID, true, "too late"); err == nil {
		t.Fatal("expected deciding an expired approval to fail")
	} else if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeConflict {
		t.Fatalf("expected a CONFLICT error, got %v", err)
	}

	if atomic.LoadInt64(&counter) != 0 {
		t.Fatal("an expired approval must never execute the handler")
	}

	approvals, err := toolsSvc.ListApprovals(ctx, ac, "")
	if err != nil {
		t.Fatalf("ListApprovals: %v", err)
	}
	if len(approvals) != 1 || approvals[0].Status != "expired" {
		t.Fatalf("expected final status 'expired', got %+v", approvals)
	}
}

// Test E: calling DecideApproval twice in sequence (not concurrently) must
// execute the tool exactly once — the second call sees a non-'pending'
// status and is refused, proving the fix isn't merely masking the race
// under concurrency but is correct for the ordinary repeated-request case
// too.
func TestApprovals_SequentialRepeatedDecisionExecutesOnce(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "repeat-owner@nodera.dev")

	if _, err := pool.Exec(ctx, `UPDATE tools SET implemented = true WHERE key = 'restart_container'`); err != nil {
		t.Fatalf("failed to mark restart_container implemented: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `UPDATE tools SET implemented = false WHERE key = 'restart_container'`)
	})

	toolsSvc := tools.New(pool, h.audit)
	var counter int64
	toolsSvc.RegisterHandler("restart_container", countingHandler(&counter))

	result, err := toolsSvc.Execute(ctx, ac, "restart_container", tools.ExecuteInput{
		ResourceType: "container", ResourceID: "repeat-1",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	approvalID := *result.ApprovalID

	first, err := toolsSvc.DecideApproval(ctx, ac, approvalID, true, "first")
	if err != nil {
		t.Fatalf("first DecideApproval: %v", err)
	}
	if first.Status != "executed" {
		t.Fatalf("expected first decision to result in 'executed', got %q", first.Status)
	}

	if _, err := toolsSvc.DecideApproval(ctx, ac, approvalID, true, "second"); err == nil {
		t.Fatal("expected the second decision on an already-decided approval to fail")
	} else if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeConflict {
		t.Fatalf("expected a CONFLICT error on the repeat, got %v", err)
	}

	if atomic.LoadInt64(&counter) != 1 {
		t.Fatalf("expected the handler to have run exactly once across both calls, ran %d times", counter)
	}
}

// Requester/approver identity: an agent-originated request, human-approved,
// must preserve both identities distinctly on the approval record — never
// collapse into looking like the approving human originated the request.
func TestApprovals_PreservesRequesterAndApproverIdentitySeparately(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ownerAC, _ := h.newOwnerContext(t, ctx, "identity-owner@nodera.dev")

	if _, err := pool.Exec(ctx, `UPDATE tools SET implemented = true WHERE key = 'restart_container'`); err != nil {
		t.Fatalf("failed to mark restart_container implemented: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `UPDATE tools SET implemented = false WHERE key = 'restart_container'`)
	})

	toolsSvc := tools.New(pool, h.audit)
	var counter int64
	toolsSvc.RegisterHandler("restart_container", countingHandler(&counter))

	// Simulate an agent (e.g. "Kiko") requesting the tool under its own
	// scoped identity, mirroring agents.Service.ExecuteTool's use of
	// agentAuthContext — a distinct ActorType/ActorID from any human user,
	// but the same organization and enough of the agent's permission_scope
	// to pass the gate. requesting_agent_id has a real FK to agents(id),
	// so a real (minimal) row is required, not just a fresh uuid.
	var agentID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO agents (organization_id, name, ai_profile_key, permission_scope)
		VALUES ($1, 'kiko-agent', 'test-profile', ARRAY['infrastructure.manage', 'tools.privileged'])
		RETURNING id
	`, ownerAC.OrganizationID).Scan(&agentID); err != nil {
		t.Fatalf("failed to insert test agent: %v", err)
	}
	agentAC := authctx.AuthContext{
		ActorType:      authctx.ActorAgent,
		ActorID:        agentID,
		ActorLabel:     "kiko-agent",
		OrganizationID: ownerAC.OrganizationID,
		Permissions:    map[string]struct{}{"infrastructure.manage": {}, "tools.privileged": {}},
	}

	result, err := toolsSvc.Execute(ctx, agentAC, "restart_container", tools.ExecuteInput{
		ResourceType: "container", ResourceID: "identity-1",
	})
	if err != nil {
		t.Fatalf("Execute (as agent): %v", err)
	}
	approvalID := *result.ApprovalID

	pending, err := toolsSvc.ListApprovals(ctx, ownerAC, "pending")
	if err != nil {
		t.Fatalf("ListApprovals: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending approval, got %d", len(pending))
	}
	if pending[0].RequestedByAgentID == nil || *pending[0].RequestedByAgentID != agentID {
		t.Fatalf("expected requested_by_agent_id to be the agent's id, got %+v", pending[0].RequestedByAgentID)
	}
	if pending[0].RequestedByUserID != nil {
		t.Fatalf("expected requested_by_user_id to be nil for an agent-originated request, got %v", pending[0].RequestedByUserID)
	}

	// A human administrator (ownerAC) approves it.
	decided, err := toolsSvc.DecideApproval(ctx, ownerAC, approvalID, true, "authorized for kiko")
	if err != nil {
		t.Fatalf("DecideApproval (as human): %v", err)
	}
	if decided.Status != "executed" || atomic.LoadInt64(&counter) != 1 {
		t.Fatalf("expected the human's approval to execute the agent's request exactly once, status=%q counter=%d", decided.Status, counter)
	}

	// The requester identity must survive execution unchanged, and the
	// approver must be recorded distinctly — never merged into one actor.
	if decided.RequestedByAgentID == nil || *decided.RequestedByAgentID != agentID {
		t.Fatalf("expected the original agent requester to still be recorded post-execution, got %+v", decided.RequestedByAgentID)
	}
	if decided.RequestedByUserID != nil {
		t.Fatal("execution must not fabricate a human requester for an agent-originated approval")
	}
	if decided.DecidedByUserID == nil || *decided.DecidedByUserID != ownerAC.ActorID {
		t.Fatalf("expected decided_by_user_id to identify the approving human, got %+v", decided.DecidedByUserID)
	}
}
