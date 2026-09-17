package rbac_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/nodera/nodera/internal/platform/authctx"
	"github.com/nodera/nodera/internal/rbac"
)

func TestRequire_MissingPermissionIsForbidden(t *testing.T) {
	ac := authctx.AuthContext{
		ActorType:   authctx.ActorUser,
		ActorID:     uuid.New(),
		Permissions: map[string]struct{}{"infrastructure.read": {}},
	}

	if err := rbac.Require(ac, "infrastructure.manage"); err == nil {
		t.Fatal("expected an error when the actor lacks the permission")
	}
}

func TestRequire_HeldPermissionSucceeds(t *testing.T) {
	ac := authctx.AuthContext{
		ActorType:   authctx.ActorUser,
		ActorID:     uuid.New(),
		Permissions: map[string]struct{}{"infrastructure.manage": {}},
	}

	if err := rbac.Require(ac, "infrastructure.manage"); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

// System actors (internal jobs, migrations) bypass permission checks
// entirely because they are never derived from an untrusted client request
// (see authctx.System and docs/SECURITY.md).
func TestRequire_SystemActorBypassesChecks(t *testing.T) {
	ac := authctx.System(uuid.New())

	if err := rbac.Require(ac, "secrets.manage"); err != nil {
		t.Fatalf("expected system actor to bypass permission checks, got %v", err)
	}
}

func TestRequire_EmptyPermissionSetIsForbidden(t *testing.T) {
	ac := authctx.AuthContext{ActorType: authctx.ActorUser, ActorID: uuid.New()}

	if err := rbac.Require(ac, "audit.read"); err == nil {
		t.Fatal("expected an error for an actor with no permissions at all")
	}
}
