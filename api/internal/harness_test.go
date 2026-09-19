package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nodera/nodera/internal/audit"
	"github.com/nodera/nodera/internal/identity"
	"github.com/nodera/nodera/internal/platform/authctx"
	"github.com/nodera/nodera/internal/platformauth"
	"github.com/nodera/nodera/internal/rbac"
	"github.com/nodera/nodera/internal/tenancy"
)

// systemMemberRoleID is the seeded 'member' system role from migration
// 0002_rbac.sql (read access plus safe AI/tool usage — see that file for
// its exact permission grants).
var systemMemberRoleID = uuid.MustParse("00000000-0000-0000-0000-000000000003")

// testHarness wires the same services main.go wires, for integration tests
// that need more than one domain (e.g. applications needs an org + owner
// AuthContext already set up).
type testHarness struct {
	pool     *pgxpool.Pool
	identity *identity.Service
	tenancy  *tenancy.Service
	audit    *audit.Service
	rbac     *rbac.Service
	platform *platformauth.Service
}

func newHarness(pool *pgxpool.Pool) *testHarness {
	auditSvc := audit.New(pool)
	rbacSvc := rbac.New(pool, auditSvc)
	platformSvc := platformauth.New(pool, auditSvc)
	auditSvc.SetPlatformAuthorizer(platformSvc)
	identitySvc := identity.New(pool, rbacSvc, 24*time.Hour, auditSvc)
	return &testHarness{
		pool:     pool,
		identity: identitySvc,
		tenancy:  tenancy.New(pool, identitySvc, auditSvc),
		audit:    auditSvc,
		rbac:     rbacSvc,
		platform: platformSvc,
	}
}

// newOwnerContext signs up a fresh user, creates an organization for them
// (granting the system 'owner' role), and returns a ready-to-use
// AuthContext plus the raw session token backing it.
func (h *testHarness) newOwnerContext(t *testing.T, ctx context.Context, email string) (authctx.AuthContext, string) {
	t.Helper()

	u, err := h.identity.SignUp(ctx, email, "correct horse battery staple 9", "Test Owner")
	if err != nil {
		t.Fatalf("SignUp: %v", err)
	}
	token, _, err := h.identity.Login(ctx, email, "correct horse battery staple 9", "127.0.0.1", "test-agent")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	org, err := h.tenancy.CreateOrganization(ctx, u.ID, "Test Org "+email, slugify(email))
	if err != nil {
		t.Fatalf("CreateOrganization: %v", err)
	}
	ac, err := h.identity.AuthContextForSession(ctx, token, org.ID, "test-correlation")
	if err != nil {
		t.Fatalf("AuthContextForSession: %v", err)
	}
	return ac, token
}

// newMemberContext signs up a fresh user, adds them to orgID as a plain
// 'member' (read access plus safe AI/tool usage — not owner/admin), and
// returns a ready-to-use AuthContext for them. Useful for tests that need
// to prove a permission-denial path, which an org owner (who holds every
// permission) can't exercise.
func (h *testHarness) newMemberContext(t *testing.T, ctx context.Context, orgID uuid.UUID, email string) authctx.AuthContext {
	t.Helper()

	u, err := h.identity.SignUp(ctx, email, "correct horse battery staple 9", "Test Member")
	if err != nil {
		t.Fatalf("SignUp: %v", err)
	}
	if _, err := h.pool.Exec(ctx, `INSERT INTO organization_members (organization_id, user_id) VALUES ($1, $2)`, orgID, u.ID); err != nil {
		t.Fatalf("add member: %v", err)
	}
	if _, err := h.pool.Exec(ctx, `INSERT INTO organization_member_roles (organization_id, user_id, role_id) VALUES ($1, $2, $3)`, orgID, u.ID, systemMemberRoleID); err != nil {
		t.Fatalf("grant member role: %v", err)
	}
	token, _, err := h.identity.Login(ctx, email, "correct horse battery staple 9", "127.0.0.1", "test-agent")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	ac, err := h.identity.AuthContextForSession(ctx, token, orgID, "test-correlation")
	if err != nil {
		t.Fatalf("AuthContextForSession: %v", err)
	}
	return ac
}

// grantPlatformPermission directly inserts a platform_user_permissions row
// for test setup, bypassing platformauth.Service.Grant's own
// platform.admins.manage requirement (which would be circular for
// bootstrapping a test's first grant) — the same "insert the fixture
// directly" pattern newMemberContext uses for organization_member_roles.
func (h *testHarness) grantPlatformPermission(t *testing.T, ctx context.Context, userID uuid.UUID, key string) {
	t.Helper()
	if _, err := h.pool.Exec(ctx, `
		INSERT INTO platform_user_permissions (user_id, permission_key) VALUES ($1, $2)
		ON CONFLICT (user_id, permission_key) DO NOTHING
	`, userID, key); err != nil {
		t.Fatalf("grantPlatformPermission(%s): %v", key, err)
	}
}

func slugify(email string) string {
	out := make([]byte, 0, len(email))
	for _, r := range email {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			out = append(out, byte(r))
		case r >= 'A' && r <= 'Z':
			out = append(out, byte(r+32))
		default:
			if len(out) > 0 && out[len(out)-1] != '-' {
				out = append(out, '-')
			}
		}
	}
	for len(out) > 0 && out[len(out)-1] == '-' {
		out = out[:len(out)-1]
	}
	return string(out)
}
