package integration_test

import (
	"context"
	"encoding/json"
	"testing"

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

// A pending approval past its expires_at is lazily marked 'expired' the
// next time it's touched (ListApprovals or DecideApproval), and can no
// longer be decided (docs/AGENTS.md — no background sweep exists yet).
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
