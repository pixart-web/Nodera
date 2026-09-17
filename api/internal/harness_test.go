package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nodera/nodera/internal/audit"
	"github.com/nodera/nodera/internal/identity"
	"github.com/nodera/nodera/internal/platform/authctx"
	"github.com/nodera/nodera/internal/rbac"
	"github.com/nodera/nodera/internal/tenancy"
)

// testHarness wires the same services main.go wires, for integration tests
// that need more than one domain (e.g. applications needs an org + owner
// AuthContext already set up).
type testHarness struct {
	pool     *pgxpool.Pool
	identity *identity.Service
	tenancy  *tenancy.Service
	audit    *audit.Service
}

func newHarness(pool *pgxpool.Pool) *testHarness {
	rbacSvc := rbac.New(pool)
	auditSvc := audit.New(pool)
	return &testHarness{
		pool:     pool,
		identity: identity.New(pool, rbacSvc, 24*time.Hour),
		tenancy:  tenancy.New(pool),
		audit:    auditSvc,
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
