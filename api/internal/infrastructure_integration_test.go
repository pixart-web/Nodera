package integration_test

import (
	"context"
	"testing"

	"github.com/nodera/nodera/internal/infrastructure"
	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/testhelpers"
)

func TestInfrastructure_UpdateNodeChangesOnlyProvidedFields(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "node-update-owner@nodera.dev")

	infraSvc := infrastructure.New(pool, h.audit)
	node, err := infraSvc.RegisterNode(ctx, ac, infrastructure.RegisterNodeInput{
		Hostname: "update-test-01", Role: "application", Environment: "staging",
	})
	if err != nil {
		t.Fatalf("RegisterNode: %v", err)
	}

	newRole := "database"
	updated, err := infraSvc.UpdateNode(ctx, ac, node.ID, infrastructure.UpdateNodeInput{Role: &newRole})
	if err != nil {
		t.Fatalf("UpdateNode: %v", err)
	}
	if updated.Role != "database" {
		t.Fatalf("expected role to be updated, got %q", updated.Role)
	}
	// Every other field is left exactly as it was — a nil pointer means
	// "don't touch it," not "reset to zero value."
	if updated.Hostname != "update-test-01" || updated.Environment != "staging" {
		t.Fatalf("expected untouched fields to remain unchanged, got %+v", updated)
	}
}

func TestInfrastructure_UpdateNodeRejectsEmptyHostname(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "node-update-empty-owner@nodera.dev")

	infraSvc := infrastructure.New(pool, h.audit)
	node, err := infraSvc.RegisterNode(ctx, ac, infrastructure.RegisterNodeInput{Hostname: "empty-hostname-test"})
	if err != nil {
		t.Fatalf("RegisterNode: %v", err)
	}

	empty := ""
	if _, err := infraSvc.UpdateNode(ctx, ac, node.ID, infrastructure.UpdateNodeInput{Hostname: &empty}); err == nil {
		t.Fatal("expected an empty hostname to be rejected")
	} else if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeValidation {
		t.Fatalf("expected VALIDATION_ERROR, got %v", err)
	}
}

// UpdateNodeStatus is what a future Node Agent heartbeat would call — it
// reports a node's current status and, since a status report is itself
// evidence the node was reachable, stamps last_seen_at too.
func TestInfrastructure_UpdateNodeStatusStampsLastSeenAt(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "node-status-owner@nodera.dev")

	infraSvc := infrastructure.New(pool, h.audit)
	node, err := infraSvc.RegisterNode(ctx, ac, infrastructure.RegisterNodeInput{Hostname: "status-test-01"})
	if err != nil {
		t.Fatalf("RegisterNode: %v", err)
	}
	if node.LastSeenAt != nil {
		t.Fatalf("expected a freshly registered node to have no last_seen_at, got %v", node.LastSeenAt)
	}

	updated, err := infraSvc.UpdateNodeStatus(ctx, ac, node.ID, "online")
	if err != nil {
		t.Fatalf("UpdateNodeStatus: %v", err)
	}
	if updated.Status != "online" {
		t.Fatalf("expected status 'online', got %q", updated.Status)
	}
	if updated.LastSeenAt == nil {
		t.Fatal("expected last_seen_at to be stamped by a status report")
	}
}

func TestInfrastructure_UpdateNodeStatusRejectsUnknownValue(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "node-status-invalid-owner@nodera.dev")

	infraSvc := infrastructure.New(pool, h.audit)
	node, err := infraSvc.RegisterNode(ctx, ac, infrastructure.RegisterNodeInput{Hostname: "status-invalid-test"})
	if err != nil {
		t.Fatalf("RegisterNode: %v", err)
	}

	if _, err := infraSvc.UpdateNodeStatus(ctx, ac, node.ID, "on-fire"); err == nil {
		t.Fatal("expected an unrecognized status value to be rejected")
	} else if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeValidation {
		t.Fatalf("expected VALIDATION_ERROR, got %v", err)
	}
}

// DecommissionNode is a terminal, one-way state: the row is kept (not
// deleted, so audit/application history referencing it stays meaningful),
// but no further status or field updates are accepted afterward.
func TestInfrastructure_DecommissionNodeIsTerminal(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "node-decommission-owner@nodera.dev")

	infraSvc := infrastructure.New(pool, h.audit)
	node, err := infraSvc.RegisterNode(ctx, ac, infrastructure.RegisterNodeInput{Hostname: "decommission-test-01"})
	if err != nil {
		t.Fatalf("RegisterNode: %v", err)
	}

	decommissioned, err := infraSvc.DecommissionNode(ctx, ac, node.ID)
	if err != nil {
		t.Fatalf("DecommissionNode: %v", err)
	}
	if decommissioned.Status != "decommissioned" {
		t.Fatalf("expected status 'decommissioned', got %q", decommissioned.Status)
	}

	// Decommissioning again is idempotent, not an error.
	if _, err := infraSvc.DecommissionNode(ctx, ac, node.ID); err != nil {
		t.Fatalf("expected re-decommissioning to be idempotent, got %v", err)
	}

	// But further status or field updates are refused.
	if _, err := infraSvc.UpdateNodeStatus(ctx, ac, node.ID, "online"); err == nil {
		t.Fatal("expected a status update on a decommissioned node to fail")
	} else if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeConflict {
		t.Fatalf("expected CONFLICT, got %v", err)
	}

	newRole := "worker"
	if _, err := infraSvc.UpdateNode(ctx, ac, node.ID, infrastructure.UpdateNodeInput{Role: &newRole}); err == nil {
		t.Fatal("expected a field update on a decommissioned node to fail")
	} else if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeConflict {
		t.Fatalf("expected CONFLICT, got %v", err)
	}

	// The node still shows up in listings — it's retired, not erased.
	nodes, err := infraSvc.List(ctx, ac, 50, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var found bool
	for _, n := range nodes {
		if n.ID == node.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("expected the decommissioned node to still appear in List")
	}
}
