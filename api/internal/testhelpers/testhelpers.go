// Package testhelpers provides integration-test scaffolding: a real
// Postgres connection pool, migrated fresh, and truncated between tests.
// Integration tests are skipped (not failed) when NODERA_TEST_DATABASE_URL
// is not set, so `go test ./...` still passes in an environment with no
// database available — see docs/DATABASE.md "Running tests".
package testhelpers

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nodera/nodera/internal/platform/db"
	"github.com/nodera/nodera/migrations"
)

// RequirePool returns a migrated Postgres pool for integration tests, or
// calls t.Skip if NODERA_TEST_DATABASE_URL is not configured.
func RequirePool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	url := os.Getenv("NODERA_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("NODERA_TEST_DATABASE_URL not set; skipping integration test")
	}

	ctx := context.Background()
	pool, err := db.Connect(ctx, url, 5)
	if err != nil {
		t.Fatalf("failed to connect to test database: %v", err)
	}
	t.Cleanup(pool.Close)

	migs, err := migrations.Load()
	if err != nil {
		t.Fatalf("failed to load migrations: %v", err)
	}
	if err := db.Migrate(ctx, pool, migs); err != nil {
		t.Fatalf("failed to apply migrations: %v", err)
	}

	truncateAll(ctx, t, pool)
	return pool
}

// truncateAll clears every domain table between tests. Truncating
// "organizations" with CASCADE also empties "roles" (it has a nullable FK to
// organizations for org-defined roles) even though the seeded system roles
// have a NULL organization_id — CASCADE truncates the whole dependent table,
// not just matching rows. reseedSystemRoles restores those rows afterward so
// every test starts from the same clean-but-seeded state migration 0002
// establishes.
func truncateAll(ctx context.Context, t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	tables := []string{
		"audit_log", "approvals", "agents",
		"ai_usage_records", "ai_profiles",
		"jobs",
		"applications", "nodes",
		"api_tokens", "service_accounts",
		"organization_member_roles", "organization_members",
		"sessions", "user_password_credentials", "users",
		"organizations", // CASCADE also empties roles/role_permissions — see reseedSystemRoles
	}
	for _, tbl := range tables {
		if _, err := pool.Exec(ctx, "TRUNCATE TABLE "+tbl+" CASCADE"); err != nil {
			t.Fatalf("failed to truncate %s: %v", tbl, err)
		}
	}
	reseedSystemRoles(ctx, t, pool)
	reseedLocalEchoModel(ctx, t, pool)
}

// reseedLocalEchoModel restores the 'echo-1' test model seeded by migration
// 0006_ai.sql. Truncating "nodes" with CASCADE also empties "ai_models" (its
// node_id column has a FK to nodes) even though the seeded echo-1 row has a
// NULL node_id — same CASCADE-truncates-the-whole-table behavior documented
// on reseedSystemRoles. The 'local-echo' provider row itself is untouched
// (nothing truncated references ai_providers), only its model.
func reseedLocalEchoModel(ctx context.Context, t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO ai_models (provider_id, model_identifier, display_name, capabilities, context_window, status, tags)
		SELECT id, 'echo-1', 'Echo 1 (deterministic test model)', ARRAY['chat'], 8192, 'available', ARRAY['test']
		FROM ai_providers WHERE key = 'local-echo'
		ON CONFLICT (provider_id, model_identifier) DO NOTHING
	`); err != nil {
		t.Fatalf("failed to reseed local-echo model: %v", err)
	}
}

// reseedSystemRoles restores the system roles and their permission grants
// seeded by migration 0002_rbac.sql. Keep this in sync with that file.
func reseedSystemRoles(ctx context.Context, t *testing.T, pool *pgxpool.Pool) {
	t.Helper()

	if _, err := pool.Exec(ctx, `
		INSERT INTO roles (id, organization_id, name, description, is_system) VALUES
			('00000000-0000-0000-0000-000000000001', NULL, 'owner',  'Full control of the organization', true),
			('00000000-0000-0000-0000-000000000002', NULL, 'admin',  'Manage infrastructure, AI, agents, and members', true),
			('00000000-0000-0000-0000-000000000003', NULL, 'member', 'Read access plus safe AI/tool usage', true)
		ON CONFLICT (id) DO NOTHING
	`); err != nil {
		t.Fatalf("failed to reseed system roles: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO role_permissions (role_id, permission_key)
		SELECT '00000000-0000-0000-0000-000000000001', key FROM permissions
		ON CONFLICT DO NOTHING
	`); err != nil {
		t.Fatalf("failed to reseed owner role_permissions: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO role_permissions (role_id, permission_key)
		SELECT '00000000-0000-0000-0000-000000000002', key FROM permissions
		WHERE key <> 'organization.manage'
		ON CONFLICT DO NOTHING
	`); err != nil {
		t.Fatalf("failed to reseed admin role_permissions: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO role_permissions (role_id, permission_key) VALUES
			('00000000-0000-0000-0000-000000000003', 'infrastructure.read'),
			('00000000-0000-0000-0000-000000000003', 'applications.read'),
			('00000000-0000-0000-0000-000000000003', 'ai.use'),
			('00000000-0000-0000-0000-000000000003', 'tools.read'),
			('00000000-0000-0000-0000-000000000003', 'tools.safe'),
			('00000000-0000-0000-0000-000000000003', 'jobs.read'),
			('00000000-0000-0000-0000-000000000003', 'secrets.read')
		ON CONFLICT DO NOTHING
	`); err != nil {
		t.Fatalf("failed to reseed member role_permissions: %v", err)
	}
}
