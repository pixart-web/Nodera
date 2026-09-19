package integration_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nodera/nodera/internal/infrastructure"
	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/platform/authctx"
	"github.com/nodera/nodera/internal/testhelpers"
	"github.com/nodera/nodera/internal/tools"
)

func newInfraGetHandler(infraSvc *infrastructure.Service) tools.Handler {
	return func(ctx context.Context, ac authctx.AuthContext, resourceType, resourceID string, params map[string]any) (any, error) {
		nodeID, err := uuid.Parse(resourceID)
		if err != nil {
			return nil, apierr.Validation("resource_id must be a node UUID")
		}
		node, err := infraSvc.Get(ctx, ac, nodeID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"hostname": node.Hostname, "status": node.Status}, nil
	}
}

// A read-risk tool with a registered handler executes immediately (no
// approval), gated by its required_permission — the full happy path for
// the Tool Gateway (section 18).
func TestTools_ReadRiskToolExecutesImmediately(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "tools-owner@nodera.dev")

	infraSvc := infrastructure.New(pool, h.audit)
	node, err := infraSvc.RegisterNode(ctx, ac, infrastructure.RegisterNodeInput{Hostname: "test-node-01"})
	if err != nil {
		t.Fatalf("RegisterNode: %v", err)
	}

	toolsSvc := tools.New(pool, h.audit)
	toolsSvc.RegisterHandler("get_server_metrics", newInfraGetHandler(infraSvc))

	result, err := toolsSvc.Execute(ctx, ac, "get_server_metrics", tools.ExecuteInput{
		ResourceType: "node", ResourceID: node.ID.String(),
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != "executed" {
		t.Fatalf("expected status 'executed', got %q", result.Status)
	}
	if result.ApprovalID != nil {
		t.Fatal("a read-risk tool must never create an approval")
	}
	m, ok := result.Result.(map[string]any)
	if !ok || m["hostname"] != "test-node-01" {
		t.Fatalf("unexpected result: %+v", result.Result)
	}
}

// A caller without the tool's required_permission is forbidden before the
// handler ever runs.
func TestTools_ExecuteRequiresPermission(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ownerAC, _ := h.newOwnerContext(t, ctx, "tools-perm-owner@nodera.dev")

	// Add a plain 'member' (read-only) to the same org and act as them.
	memberEmail := "tools-member@nodera.dev"
	memberUser, err := h.identity.SignUp(ctx, memberEmail, "correct horse battery staple 9", "Member")
	if err != nil {
		t.Fatalf("SignUp: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO organization_members (organization_id, user_id) VALUES ($1, $2)`, ownerAC.OrganizationID, memberUser.ID); err != nil {
		t.Fatalf("add member: %v", err)
	}
	memberRoleID := uuid.MustParse("00000000-0000-0000-0000-000000000003")
	if _, err := pool.Exec(ctx, `INSERT INTO organization_member_roles (organization_id, user_id, role_id) VALUES ($1, $2, $3)`, ownerAC.OrganizationID, memberUser.ID, memberRoleID); err != nil {
		t.Fatalf("grant member role: %v", err)
	}
	memberToken, _, err := h.identity.Login(ctx, memberEmail, "correct horse battery staple 9", "127.0.0.1", "test-agent")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	memberAC, err := h.identity.AuthContextForSession(ctx, memberToken, ownerAC.OrganizationID, "test-correlation")
	if err != nil {
		t.Fatalf("AuthContextForSession: %v", err)
	}

	// 'member' holds infrastructure.read (per migration 0002_rbac.sql) so
	// get_server_metrics itself is allowed, but restart_container needs
	// infrastructure.manage, which 'member' does not hold.
	toolsSvc := tools.New(pool, h.audit)
	_, err = toolsSvc.Execute(ctx, memberAC, "restart_container", tools.ExecuteInput{ResourceType: "container", ResourceID: "abc"})
	if err == nil {
		t.Fatal("expected a 'member' to be forbidden from restart_container")
	}
	ae, ok := err.(*apierr.Error)
	if !ok || ae.Code != apierr.CodeForbidden {
		t.Fatalf("expected FORBIDDEN, got %v", err)
	}
}

// A privileged tool never executes synchronously — it creates a pending
// approval, regardless of whether a handler happens to be registered.
func TestTools_PrivilegedToolRequiresApproval(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "approval-owner@nodera.dev")

	toolsSvc := tools.New(pool, h.audit)

	result, err := toolsSvc.Execute(ctx, ac, "restart_container", tools.ExecuteInput{
		ResourceType: "container", ResourceID: "container-123", Parameters: map[string]any{"force": true},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != "approval_required" || result.ApprovalID == nil {
		t.Fatalf("expected an approval_required result with an approval id, got %+v", result)
	}

	approvals, err := toolsSvc.ListApprovals(ctx, ac, "pending")
	if err != nil {
		t.Fatalf("ListApprovals: %v", err)
	}
	if len(approvals) != 1 || approvals[0].ID != *result.ApprovalID {
		t.Fatalf("expected the pending approval to be listable, got %+v", approvals)
	}
	if approvals[0].RequestedAction != "restart_container" || approvals[0].RiskLevel != "privileged" {
		t.Fatalf("unexpected approval contents: %+v", approvals[0])
	}
}

// Approving a request for a tool with no execution backend yet is a
// legitimate outcome: the approval itself succeeds (a human authorized the
// action), but execution honestly reports NOT_IMPLEMENTED rather than
// fabricating success (rule 36).
func TestTools_ApprovingUnimplementedToolReportsNotImplemented(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "approve-unimpl-owner@nodera.dev")

	toolsSvc := tools.New(pool, h.audit)

	result, err := toolsSvc.Execute(ctx, ac, "restart_container", tools.ExecuteInput{
		ResourceType: "container", ResourceID: "container-456",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	decided, err := toolsSvc.DecideApproval(ctx, ac, *result.ApprovalID, true, "looks fine")
	if err != nil {
		t.Fatalf("DecideApproval: %v", err)
	}
	if decided.Status != "approved" {
		t.Fatalf("expected status 'approved', got %q", decided.Status)
	}
	if decided.DecisionReason != "looks fine" {
		t.Fatalf("unexpected decision reason: %q", decided.DecisionReason)
	}
	var execResult map[string]string
	if err := json.Unmarshal(decided.ExecutionResult, &execResult); err != nil {
		t.Fatalf("failed to unmarshal execution_result: %v", err)
	}
	if execResult["error"] != "NOT_IMPLEMENTED" {
		t.Fatalf("expected execution_result to report NOT_IMPLEMENTED, got %+v", execResult)
	}

	// A second decision on the same (now-decided) approval is rejected.
	if _, err := toolsSvc.DecideApproval(ctx, ac, *result.ApprovalID, true, "again"); err == nil {
		t.Fatal("expected deciding an already-decided approval to fail")
	}
}

// Approving a request for a tool that DOES have a handler actually runs it
// and records the real result on the approval row.
func TestTools_ApprovingImplementedToolExecutesIt(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "approve-impl-owner@nodera.dev")

	infraSvc := infrastructure.New(pool, h.audit)
	node, err := infraSvc.RegisterNode(ctx, ac, infrastructure.RegisterNodeInput{Hostname: "approval-node-01"})
	if err != nil {
		t.Fatalf("RegisterNode: %v", err)
	}

	// restart_container is 'privileged' risk in the seed data, so its
	// Execute call always creates an approval regardless of whether a
	// handler is registered — register one here to prove DecideApproval's
	// post-approval execution path actually runs it. The registry's
	// `implemented` flag is the source of truth for whether a tool can
	// actually run (runHandler checks it before consulting the handler
	// map) — wiring a Go handler without flipping it is exactly the
	// deployment-config bug that check guards against, so this test flips
	// it explicitly, mirroring what migration 0010 does for
	// get_server_metrics in the real registry.
	if _, err := pool.Exec(ctx, `UPDATE tools SET implemented = true WHERE key = 'restart_container'`); err != nil {
		t.Fatalf("failed to mark restart_container implemented: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `UPDATE tools SET implemented = false WHERE key = 'restart_container'`)
	})

	toolsSvc := tools.New(pool, h.audit)
	toolsSvc.RegisterHandler("restart_container", func(ctx context.Context, ac authctx.AuthContext, resourceType, resourceID string, params map[string]any) (any, error) {
		return map[string]any{"restarted": resourceID}, nil
	})

	result, err := toolsSvc.Execute(ctx, ac, "restart_container", tools.ExecuteInput{
		ResourceType: "container", ResourceID: node.ID.String(),
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	decided, err := toolsSvc.DecideApproval(ctx, ac, *result.ApprovalID, true, "ok")
	if err != nil {
		t.Fatalf("DecideApproval: %v", err)
	}
	var execResult map[string]any
	if err := json.Unmarshal(decided.ExecutionResult, &execResult); err != nil {
		t.Fatalf("failed to unmarshal execution_result: %v", err)
	}
	if execResult["restarted"] != node.ID.String() {
		t.Fatalf("expected the registered handler to have actually run, got %+v", execResult)
	}
}

// Rejecting an approval never executes anything.
func TestTools_RejectingApprovalNeverExecutes(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "reject-owner@nodera.dev")

	ran := false
	toolsSvc := tools.New(pool, h.audit)
	toolsSvc.RegisterHandler("restart_container", func(ctx context.Context, ac authctx.AuthContext, resourceType, resourceID string, params map[string]any) (any, error) {
		ran = true
		return nil, nil
	})

	result, err := toolsSvc.Execute(ctx, ac, "restart_container", tools.ExecuteInput{ResourceType: "container", ResourceID: "x"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	decided, err := toolsSvc.DecideApproval(ctx, ac, *result.ApprovalID, false, "not now")
	if err != nil {
		t.Fatalf("DecideApproval: %v", err)
	}
	if decided.Status != "rejected" {
		t.Fatalf("expected status 'rejected', got %q", decided.Status)
	}
	if ran {
		t.Fatal("a rejected approval must never execute the tool's handler")
	}
}

// CancelApproval lets the requester withdraw their own pending approval —
// the tool never runs, and the row lands in a real terminal 'cancelled'
// state, not silently deleted.
func TestTools_CancelApprovalWithdrawsOwnPendingRequest(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "cancel-owner@nodera.dev")

	ran := false
	toolsSvc := tools.New(pool, h.audit)
	toolsSvc.RegisterHandler("restart_container", func(ctx context.Context, ac authctx.AuthContext, resourceType, resourceID string, params map[string]any) (any, error) {
		ran = true
		return nil, nil
	})

	result, err := toolsSvc.Execute(ctx, ac, "restart_container", tools.ExecuteInput{ResourceType: "container", ResourceID: "x"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	cancelled, err := toolsSvc.CancelApproval(ctx, ac, *result.ApprovalID)
	if err != nil {
		t.Fatalf("CancelApproval: %v", err)
	}
	if cancelled.Status != "cancelled" {
		t.Fatalf("expected status 'cancelled', got %q", cancelled.Status)
	}
	if ran {
		t.Fatal("a cancelled approval must never execute the tool's handler")
	}

	// A decision on an already-cancelled approval is refused — cancelling
	// is itself a terminal outcome, not a no-op state.
	if _, err := toolsSvc.DecideApproval(ctx, ac, *result.ApprovalID, true, "too late"); err == nil {
		t.Fatal("expected deciding a cancelled approval to fail")
	}

	// A second cancel on the same approval is refused too.
	if _, err := toolsSvc.CancelApproval(ctx, ac, *result.ApprovalID); err == nil {
		t.Fatal("expected cancelling an already-cancelled approval to fail")
	} else if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeConflict {
		t.Fatalf("expected CONFLICT, got %v", err)
	}
}

// Only the original requester can cancel — an approver (or anyone else)
// must go through DecideApproval instead. Cancelling someone else's
// request would let a caller silently withdraw a request they don't own.
func TestTools_CancelApprovalRefusesNonRequester(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "cancel-other-owner@nodera.dev")
	otherAC := h.newMemberContext(t, ctx, ac.OrganizationID, "cancel-other-member@nodera.dev")

	toolsSvc := tools.New(pool, h.audit)
	result, err := toolsSvc.Execute(ctx, ac, "restart_container", tools.ExecuteInput{ResourceType: "container", ResourceID: "x"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if _, err := toolsSvc.CancelApproval(ctx, otherAC, *result.ApprovalID); err == nil {
		t.Fatal("expected a caller who didn't request the approval to be forbidden from cancelling it")
	} else if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeForbidden {
		t.Fatalf("expected FORBIDDEN, got %v", err)
	}

	// Confirm it's still genuinely pending, unaffected by the refused attempt.
	approvals, err := toolsSvc.ListApprovals(ctx, ac, "pending")
	if err != nil {
		t.Fatalf("ListApprovals: %v", err)
	}
	if len(approvals) != 1 || approvals[0].ID != *result.ApprovalID {
		t.Fatalf("expected the approval to remain pending and untouched, got %+v", approvals)
	}
}

// A pending approval past its expires_at is lazily marked 'expired' the
// next time it's touched (ListApprovals or DecideApproval), and can no
// longer be decided. See TestTools_RunExpirySweepExpiresAcrossOrganizations
// for the organization-independent background sweep.
func TestTools_ExpiredApprovalCannotBeDecided(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "expiry-owner@nodera.dev")

	toolsSvc := tools.New(pool, h.audit)
	result, err := toolsSvc.Execute(ctx, ac, "restart_container", tools.ExecuteInput{ResourceType: "container", ResourceID: "x"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// Force it into the past — simulating time passing without needing to
	// wait out the real 24h default TTL.
	if _, err := pool.Exec(ctx, `UPDATE approvals SET expires_at = now() - interval '1 minute' WHERE id = $1`, *result.ApprovalID); err != nil {
		t.Fatalf("failed to backdate approval expiry: %v", err)
	}

	approvals, err := toolsSvc.ListApprovals(ctx, ac, "expired")
	if err != nil {
		t.Fatalf("ListApprovals: %v", err)
	}
	if len(approvals) != 1 || approvals[0].ID != *result.ApprovalID {
		t.Fatalf("expected the backdated approval to show as expired, got %+v", approvals)
	}

	if _, err := toolsSvc.DecideApproval(ctx, ac, *result.ApprovalID, true, "too late"); err == nil {
		t.Fatal("expected deciding an expired approval to fail")
	}
}

// RunExpirySweep expires stale pending approvals across every
// organization in one call — proving an idle org's approvals don't need
// anyone to call ListApprovals/DecideApproval for that specific org to get
// flipped to 'expired' (what cmd/server/main.go's periodic goroutine
// relies on).
func TestTools_RunExpirySweepExpiresAcrossOrganizations(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	acA, _ := h.newOwnerContext(t, ctx, "sweep-org-a@nodera.dev")
	acB, _ := h.newOwnerContext(t, ctx, "sweep-org-b@nodera.dev")

	toolsSvc := tools.New(pool, h.audit)

	resultA, err := toolsSvc.Execute(ctx, acA, "restart_container", tools.ExecuteInput{ResourceType: "container", ResourceID: "a"})
	if err != nil {
		t.Fatalf("Execute (org A): %v", err)
	}
	resultB, err := toolsSvc.Execute(ctx, acB, "restart_container", tools.ExecuteInput{ResourceType: "container", ResourceID: "b"})
	if err != nil {
		t.Fatalf("Execute (org B): %v", err)
	}

	// Backdate org A's approval only — org B's should be untouched by the
	// sweep since it isn't actually expired.
	if _, err := pool.Exec(ctx, `UPDATE approvals SET expires_at = now() - interval '1 minute' WHERE id = $1`, *resultA.ApprovalID); err != nil {
		t.Fatalf("failed to backdate approval expiry: %v", err)
	}

	n, err := toolsSvc.RunExpirySweep(ctx)
	if err != nil {
		t.Fatalf("RunExpirySweep: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected the sweep to report exactly 1 expired approval, got %d", n)
	}

	approvalsA, err := toolsSvc.ListApprovals(ctx, acA, "expired")
	if err != nil {
		t.Fatalf("ListApprovals (org A): %v", err)
	}
	if len(approvalsA) != 1 || approvalsA[0].ID != *resultA.ApprovalID {
		t.Fatalf("expected org A's approval to be expired by the sweep, got %+v", approvalsA)
	}

	approvalsB, err := toolsSvc.ListApprovals(ctx, acB, "pending")
	if err != nil {
		t.Fatalf("ListApprovals (org B): %v", err)
	}
	if len(approvalsB) != 1 || approvalsB[0].ID != *resultB.ApprovalID {
		t.Fatalf("expected org B's approval to remain pending, got %+v", approvalsB)
	}
}

// approvalTTLTolerance accounts for the real (small) wall-clock gap between
// this test computing an expected expires_at and the server computing its
// own via time.Now() inside createApproval.
const approvalTTLTolerance = 5 * time.Second

// With no per-org override, a new approval's expiry is defaultApprovalTTL
// (24h) after creation.
func TestTools_ApprovalTTLDefaultsWhenNoOverride(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "ttl-default-owner@nodera.dev")

	toolsSvc := tools.New(pool, h.audit)
	if _, err := toolsSvc.Execute(ctx, ac, "restart_container", tools.ExecuteInput{ResourceType: "container", ResourceID: "x"}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	approvals, err := toolsSvc.ListApprovals(ctx, ac, "pending")
	if err != nil {
		t.Fatalf("ListApprovals: %v", err)
	}
	a := approvals[0]
	want := a.CreatedAt.Add(24 * time.Hour)
	if diff := a.ExpiresAt.Sub(want); diff < -approvalTTLTolerance || diff > approvalTTLTolerance {
		t.Fatalf("expected expires_at ~%v (24h default) after created_at, got %v (diff %v)", want, *a.ExpiresAt, diff)
	}
}

// SetApprovalTTL's override takes effect for approvals created after it,
// producing a materially different expires_at than the 24h default.
func TestTools_SetApprovalTTLOverrideAppliesToNewApprovals(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "ttl-override-owner@nodera.dev")

	toolsSvc := tools.New(pool, h.audit)

	setting, err := toolsSvc.SetApprovalTTL(ctx, ac, "restart_container", 10*time.Minute)
	if err != nil {
		t.Fatalf("SetApprovalTTL: %v", err)
	}
	if setting.ApprovalTTLSeconds != 600 {
		t.Fatalf("expected 600 seconds, got %d", setting.ApprovalTTLSeconds)
	}

	result, err := toolsSvc.Execute(ctx, ac, "restart_container", tools.ExecuteInput{ResourceType: "container", ResourceID: "y"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	approvals, err := toolsSvc.ListApprovals(ctx, ac, "pending")
	if err != nil {
		t.Fatalf("ListApprovals: %v", err)
	}
	var a tools.Approval
	for _, cand := range approvals {
		if cand.ID == *result.ApprovalID {
			a = cand
		}
	}
	want := a.CreatedAt.Add(10 * time.Minute)
	if diff := a.ExpiresAt.Sub(want); diff < -approvalTTLTolerance || diff > approvalTTLTolerance {
		t.Fatalf("expected expires_at ~%v (10m override) after created_at, got %v (diff %v)", want, *a.ExpiresAt, diff)
	}

	overrides, err := toolsSvc.ListApprovalTTLOverrides(ctx, ac)
	if err != nil {
		t.Fatalf("ListApprovalTTLOverrides: %v", err)
	}
	if len(overrides) != 1 || overrides[0].ToolKey != "restart_container" || overrides[0].ApprovalTTLSeconds != 600 {
		t.Fatalf("expected the override to be listed, got %+v", overrides)
	}

	// ClearApprovalTTL reverts subsequent approvals back to the 24h default.
	if err := toolsSvc.ClearApprovalTTL(ctx, ac, "restart_container"); err != nil {
		t.Fatalf("ClearApprovalTTL: %v", err)
	}
	overridesAfterClear, err := toolsSvc.ListApprovalTTLOverrides(ctx, ac)
	if err != nil {
		t.Fatalf("ListApprovalTTLOverrides after clear: %v", err)
	}
	if len(overridesAfterClear) != 0 {
		t.Fatalf("expected no overrides after clearing, got %+v", overridesAfterClear)
	}

	result2, err := toolsSvc.Execute(ctx, ac, "restart_container", tools.ExecuteInput{ResourceType: "container", ResourceID: "z"})
	if err != nil {
		t.Fatalf("Execute (after clear): %v", err)
	}
	approvals2, err := toolsSvc.ListApprovals(ctx, ac, "pending")
	if err != nil {
		t.Fatalf("ListApprovals (after clear): %v", err)
	}
	var a2 tools.Approval
	for _, cand := range approvals2 {
		if cand.ID == *result2.ApprovalID {
			a2 = cand
		}
	}
	want2 := a2.CreatedAt.Add(24 * time.Hour)
	if diff := a2.ExpiresAt.Sub(want2); diff < -approvalTTLTolerance || diff > approvalTTLTolerance {
		t.Fatalf("expected expires_at ~%v (back to 24h default) after created_at, got %v (diff %v)", want2, *a2.ExpiresAt, diff)
	}
}

// SetApprovalTTL rejects TTLs outside [minApprovalTTL, maxApprovalTTL] and
// unknown tool keys, before ever touching the database row.
func TestTools_SetApprovalTTLRejectsInvalidInput(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "ttl-invalid-owner@nodera.dev")

	toolsSvc := tools.New(pool, h.audit)

	if _, err := toolsSvc.SetApprovalTTL(ctx, ac, "restart_container", 1*time.Second); err == nil {
		t.Fatal("expected a TTL below the minimum to be rejected")
	}
	if _, err := toolsSvc.SetApprovalTTL(ctx, ac, "restart_container", 365*24*time.Hour); err == nil {
		t.Fatal("expected a TTL above the maximum to be rejected")
	}
	if _, err := toolsSvc.SetApprovalTTL(ctx, ac, "not_a_real_tool", 10*time.Minute); err == nil {
		t.Fatal("expected setting a TTL for an unknown tool to be rejected")
	}
}

// Configuring approval TTLs is gated by tools.manage, which a plain member
// doesn't hold — distinct from approvals.decide and tools.privileged.
func TestTools_SetApprovalTTLRequiresToolsManage(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "ttl-perm-owner@nodera.dev")
	memberAC := h.newMemberContext(t, ctx, ac.OrganizationID, "ttl-perm-member@nodera.dev")

	toolsSvc := tools.New(pool, h.audit)

	if _, err := toolsSvc.SetApprovalTTL(ctx, memberAC, "restart_container", 10*time.Minute); err == nil {
		t.Fatal("expected a member without tools.manage to be forbidden from setting an approval TTL")
	} else if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeForbidden {
		t.Fatalf("expected FORBIDDEN, got %v", err)
	}
}
