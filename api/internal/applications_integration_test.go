package integration_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/nodera/nodera/internal/applications"
	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/testhelpers"
)

func TestApplicationsRegisterAndList(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "apps-owner@nodera.dev")

	appsSvc := applications.New(pool, h.audit)

	a, err := appsSvc.Register(ctx, ac, applications.RegisterInput{
		Name: "web-frontend",
		Kind: "web",
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if a.OrganizationID != ac.OrganizationID {
		t.Fatalf("application registered under wrong organization")
	}
	if a.Status != "unknown" {
		t.Fatalf("expected default status 'unknown', got %q", a.Status)
	}

	list, err := appsSvc.List(ctx, ac, 100, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].ID != a.ID {
		t.Fatalf("expected exactly the registered application in the list, got %+v", list)
	}

	// Duplicate name in the same org is rejected.
	if _, err := appsSvc.Register(ctx, ac, applications.RegisterInput{Name: "web-frontend"}); err == nil {
		t.Fatal("expected a conflict registering a duplicate application name")
	}
}

func TestApplications_UpdateChangesOnlyProvidedFields(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "app-update-owner@nodera.dev")

	appsSvc := applications.New(pool, h.audit)
	app, err := appsSvc.Register(ctx, ac, applications.RegisterInput{
		Name: "update-test-app", Kind: "service", Environment: "staging",
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	newEnv := "production"
	updated, err := appsSvc.Update(ctx, ac, app.ID, applications.UpdateInput{Environment: &newEnv})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Environment != "production" {
		t.Fatalf("expected environment to be updated, got %q", updated.Environment)
	}
	if updated.Name != "update-test-app" || updated.Kind != "service" {
		t.Fatalf("expected untouched fields to remain unchanged, got %+v", updated)
	}
}

func TestApplications_UpdateRejectsEmptyName(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "app-update-empty-owner@nodera.dev")

	appsSvc := applications.New(pool, h.audit)
	app, err := appsSvc.Register(ctx, ac, applications.RegisterInput{Name: "empty-name-test"})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	empty := ""
	if _, err := appsSvc.Update(ctx, ac, app.ID, applications.UpdateInput{Name: &empty}); err == nil {
		t.Fatal("expected an empty name to be rejected")
	} else if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeValidation {
		t.Fatalf("expected VALIDATION_ERROR, got %v", err)
	}
}

func TestApplications_UpdateCanClearNodeID(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "app-clear-node-owner@nodera.dev")

	appsSvc := applications.New(pool, h.audit)
	app, err := appsSvc.Register(ctx, ac, applications.RegisterInput{Name: "clear-node-test"})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if app.NodeID != nil {
		t.Fatalf("expected a freshly registered app to have no node_id, got %v", app.NodeID)
	}

	// Passing an explicit uuid.Nil pointer clears node_id (distinct from a
	// nil *uuid.UUID, which means "don't touch this field").
	nilID := uuid.Nil
	updated, err := appsSvc.Update(ctx, ac, app.ID, applications.UpdateInput{NodeID: &nilID})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.NodeID != nil {
		t.Fatalf("expected node_id to remain nil, got %v", updated.NodeID)
	}
}

func TestApplications_UpdateStatusRejectsUnknownValue(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "app-status-invalid-owner@nodera.dev")

	appsSvc := applications.New(pool, h.audit)
	app, err := appsSvc.Register(ctx, ac, applications.RegisterInput{Name: "status-invalid-test"})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	if _, err := appsSvc.UpdateStatus(ctx, ac, app.ID, "exploding"); err == nil {
		t.Fatal("expected an unrecognized status value to be rejected")
	} else if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeValidation {
		t.Fatalf("expected VALIDATION_ERROR, got %v", err)
	}
}

// Deregister is a terminal, one-way state: the row is kept (so audit
// history referencing it stays meaningful), but no further status or
// field updates are accepted afterward.
func TestApplications_DeregisterIsTerminal(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "app-deregister-owner@nodera.dev")

	appsSvc := applications.New(pool, h.audit)
	app, err := appsSvc.Register(ctx, ac, applications.RegisterInput{Name: "deregister-test-01"})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	deregistered, err := appsSvc.Deregister(ctx, ac, app.ID)
	if err != nil {
		t.Fatalf("Deregister: %v", err)
	}
	if deregistered.Status != "deregistered" {
		t.Fatalf("expected status 'deregistered', got %q", deregistered.Status)
	}

	// Deregistering again is idempotent, not an error.
	if _, err := appsSvc.Deregister(ctx, ac, app.ID); err != nil {
		t.Fatalf("expected re-deregistering to be idempotent, got %v", err)
	}

	// But further status or field updates are refused.
	if _, err := appsSvc.UpdateStatus(ctx, ac, app.ID, "running"); err == nil {
		t.Fatal("expected a status update on a deregistered application to fail")
	} else if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeConflict {
		t.Fatalf("expected CONFLICT, got %v", err)
	}

	newKind := "worker"
	if _, err := appsSvc.Update(ctx, ac, app.ID, applications.UpdateInput{Kind: &newKind}); err == nil {
		t.Fatal("expected a field update on a deregistered application to fail")
	} else if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeConflict {
		t.Fatalf("expected CONFLICT, got %v", err)
	}

	// The application still shows up in listings — it's retired, not
	// erased.
	apps, err := appsSvc.List(ctx, ac, 50, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var found bool
	for _, a := range apps {
		if a.ID == app.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("expected the deregistered application to still appear in List")
	}
}
