package integration_test

import (
	"context"
	"testing"

	"github.com/nodera/nodera/internal/applications"
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

	list, err := appsSvc.List(ctx, ac)
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
