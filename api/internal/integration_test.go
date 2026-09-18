// Package integration_test exercises identity, tenancy, rbac, audit, and
// infrastructure together against a real Postgres database — the seams
// unit tests with mocks would hide (tenant scoping, permission resolution
// via actual role grants, unique constraints). See internal/testhelpers.
package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nodera/nodera/internal/audit"
	"github.com/nodera/nodera/internal/identity"
	"github.com/nodera/nodera/internal/infrastructure"
	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/rbac"
	"github.com/nodera/nodera/internal/tenancy"
	"github.com/nodera/nodera/internal/testhelpers"
)

func TestSignupLoginOrgAndNodeFlow(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()

	auditSvc := audit.New(pool)
	rbacSvc := rbac.New(pool, auditSvc)
	identitySvc := identity.New(pool, rbacSvc, 24*time.Hour)
	tenancySvc := tenancy.New(pool, identitySvc, auditSvc)
	infraSvc := infrastructure.New(pool, auditSvc)

	// Sign up creates a user with no organizations yet.
	u, err := identitySvc.SignUp(ctx, "owner@nodera.dev", "correct horse battery staple 9", "Owner")
	if err != nil {
		t.Fatalf("SignUp: %v", err)
	}

	token, loggedIn, err := identitySvc.Login(ctx, "owner@nodera.dev", "correct horse battery staple 9", "127.0.0.1", "test-agent")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if loggedIn.ID != u.ID {
		t.Fatalf("logged in user ID mismatch: got %s want %s", loggedIn.ID, u.ID)
	}

	// Creating an organization grants the creator the system 'owner' role.
	org, err := tenancySvc.CreateOrganization(ctx, u.ID, "Acme", "acme")
	if err != nil {
		t.Fatalf("CreateOrganization: %v", err)
	}

	ac, err := identitySvc.AuthContextForSession(ctx, token, org.ID, "test-correlation")
	if err != nil {
		t.Fatalf("AuthContextForSession: %v", err)
	}
	if !ac.HasPermission("infrastructure.manage") {
		t.Fatal("expected organization owner to hold infrastructure.manage")
	}

	// The owner can register a node.
	node, err := infraSvc.RegisterNode(ctx, ac, infrastructure.RegisterNodeInput{
		Hostname: "nodera-prod-01",
		Provider: "hetzner",
	})
	if err != nil {
		t.Fatalf("RegisterNode: %v", err)
	}
	if node.OrganizationID != org.ID {
		t.Fatalf("node registered under wrong organization: got %s want %s", node.OrganizationID, org.ID)
	}

	// Registering it wrote an audit record the owner can read.
	records, err := auditSvc.Query(ctx, ac, audit.QueryFilter{OrganizationID: org.ID})
	if err != nil {
		t.Fatalf("audit Query: %v", err)
	}
	if len(records) != 1 || records[0].Action != "infrastructure.node.registered" {
		t.Fatalf("expected exactly one node-registered audit record, got %+v", records)
	}

	// A second organization cannot see the first organization's node
	// (ADR-004 tenant isolation) even for the same infrastructure.read call.
	otherOrg, err := tenancySvc.CreateOrganization(ctx, u.ID, "Other Co", "other-co")
	if err != nil {
		t.Fatalf("CreateOrganization (other): %v", err)
	}
	otherAC, err := identitySvc.AuthContextForSession(ctx, token, otherOrg.ID, "test-correlation-2")
	if err != nil {
		t.Fatalf("AuthContextForSession (other): %v", err)
	}
	nodesInOtherOrg, err := infraSvc.List(ctx, otherAC, 100, 0)
	if err != nil {
		t.Fatalf("List (other org): %v", err)
	}
	if len(nodesInOtherOrg) != 0 {
		t.Fatalf("expected zero nodes visible from a different organization, got %d", len(nodesInOtherOrg))
	}

	// A user who is a 'member' (not owner/admin) cannot register nodes.
	memberUser, err := identitySvc.SignUp(ctx, "member@nodera.dev", "correct horse battery staple 9", "Member")
	if err != nil {
		t.Fatalf("SignUp (member): %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO organization_members (organization_id, user_id) VALUES ($1, $2)`, org.ID, memberUser.ID); err != nil {
		t.Fatalf("failed to add member: %v", err)
	}
	memberRoleID := uuid.MustParse("00000000-0000-0000-0000-000000000003")
	if _, err := pool.Exec(ctx, `INSERT INTO organization_member_roles (organization_id, user_id, role_id) VALUES ($1, $2, $3)`, org.ID, memberUser.ID, memberRoleID); err != nil {
		t.Fatalf("failed to grant member role: %v", err)
	}

	memberToken, _, err := identitySvc.Login(ctx, "member@nodera.dev", "correct horse battery staple 9", "127.0.0.1", "test-agent")
	if err != nil {
		t.Fatalf("Login (member): %v", err)
	}
	memberAC, err := identitySvc.AuthContextForSession(ctx, memberToken, org.ID, "test-correlation-3")
	if err != nil {
		t.Fatalf("AuthContextForSession (member): %v", err)
	}

	_, err = infraSvc.RegisterNode(ctx, memberAC, infrastructure.RegisterNodeInput{Hostname: "should-be-forbidden"})
	if err == nil {
		t.Fatal("expected a 'member' role to be forbidden from registering a node")
	}
	var ae *apierr.Error
	if !isForbidden(err, &ae) {
		t.Fatalf("expected apierr.CodeForbidden, got %v", err)
	}
}

func isForbidden(err error, target **apierr.Error) bool {
	ae, ok := err.(*apierr.Error)
	if !ok {
		return false
	}
	*target = ae
	return ae.Code == apierr.CodeForbidden
}
