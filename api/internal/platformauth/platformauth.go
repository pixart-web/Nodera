// Package platformauth implements Nodera's platform-scoped authorization:
// permissions that apply across every organization, entirely separate
// from internal/rbac's organization-scoped permissions. An organization
// owner holding, say, ai.manage within their own organization must never
// be able to mutate platform-wide state (the AI provider/model registry
// today, more platform-wide config later) purely by virtue of owning an
// organization — that requires an explicit platform permission grant,
// checked here. See docs/SECURITY.md "Platform vs organization
// authorization".
package platformauth

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nodera/nodera/internal/audit"
	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/platform/authctx"
	"github.com/nodera/nodera/internal/platform/logger"
)

type AuditRecorder interface {
	Record(ctx context.Context, ac authctx.AuthContext, e audit.Entry) error
}

type Service struct {
	pool  *pgxpool.Pool
	audit AuditRecorder
}

func New(pool *pgxpool.Pool, audit AuditRecorder) *Service {
	return &Service{pool: pool, audit: audit}
}

// PermManagePlatformAdmins gates Grant/Revoke/ListGrants — holding it lets
// a user grant any platform permission (including this one) to anyone,
// so it is deliberately never auto-granted; see BootstrapAdmin.
const PermManagePlatformAdmins = "platform.admins.manage"

type Permission struct {
	Key         string `json:"key"`
	Description string `json:"description"`
}

// ListCatalog returns every platform permission that exists to grant —
// open to any authenticated user (it reveals capability names, not who
// holds them), the same "read the shape of the system" posture RBAC's own
// permission catalog listing takes.
func (s *Service) ListCatalog(ctx context.Context) ([]Permission, error) {
	rows, err := s.pool.Query(ctx, `SELECT key, description FROM platform_permissions ORDER BY key ASC`)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeInternal, "failed to list platform permission catalog", err)
	}
	defer rows.Close()
	var out []Permission
	for rows.Next() {
		var p Permission
		if err := rows.Scan(&p.Key, &p.Description); err != nil {
			return nil, apierr.Wrap(apierr.CodeInternal, "failed to scan platform permission", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Has reports whether the given user directly holds a platform permission.
// Only human users can hold platform permissions in this phase — agents
// and service accounts always act within an organization's own scope, so
// Require below fails closed for any other actor type before this is even
// called for them.
func (s *Service) Has(ctx context.Context, userID uuid.UUID, key string) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM platform_user_permissions WHERE user_id = $1 AND permission_key = $2)
	`, userID, key).Scan(&exists)
	if err != nil {
		return false, apierr.Wrap(apierr.CodeInternal, "failed to check platform permission", err)
	}
	return exists, nil
}

// Require is platform authorization's single choke point — every
// platform-scoped mutation must call it, mirroring how rbac.Require is
// organization RBAC's single choke point. It never consults
// AuthContext.Permissions (organization-scoped) or infers platform
// authority from organization ownership/role; it always checks the
// explicit grant table for the caller's own user id.
//
// A system actor (authctx.ActorSystem — internal jobs, startup seeding,
// never a client request; see authctx.System) bypasses this check, the
// same exception rbac.Require makes. This is what lets cmd/server/main.go
// auto-register a configured provider's registry row at startup without
// requiring a human to already hold platform.ai.providers.manage before
// the process can even boot.
func (s *Service) Require(ctx context.Context, ac authctx.AuthContext, key string) error {
	if ac.ActorType == authctx.ActorSystem {
		return nil
	}
	if ac.ActorType != authctx.ActorUser {
		return apierr.Forbidden("platform permission '" + key + "' requires a human user identity")
	}
	ok, err := s.Has(ctx, ac.ActorID, key)
	if err != nil {
		return err
	}
	if !ok {
		return apierr.Forbidden("missing platform permission: " + key)
	}
	return nil
}

// ListMine returns the calling user's own platform permissions — no
// platform.admins.manage required, since seeing your own grants isn't a
// privileged operation (the identity self-service pattern used throughout,
// e.g. RevokeSession/RevokeAPIToken).
func (s *Service) ListMine(ctx context.Context, ac authctx.AuthContext) ([]string, error) {
	if ac.ActorType != authctx.ActorUser {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT permission_key FROM platform_user_permissions WHERE user_id = $1 ORDER BY permission_key ASC
	`, ac.ActorID)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeInternal, "failed to list your platform permissions", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, apierr.Wrap(apierr.CodeInternal, "failed to scan platform permission", err)
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

type Grant struct {
	UserID          uuid.UUID  `json:"user_id"`
	PermissionKey   string     `json:"permission_key"`
	GrantedByUserID *uuid.UUID `json:"granted_by_user_id,omitempty"`
	GrantedAt       time.Time  `json:"granted_at"`
}

// ListGrants returns every user/permission grant in the system —
// platform.admins.manage-gated, since it reveals who holds platform
// authority.
func (s *Service) ListGrants(ctx context.Context, ac authctx.AuthContext) ([]Grant, error) {
	if err := s.Require(ctx, ac, PermManagePlatformAdmins); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT user_id, permission_key, granted_by_user_id, granted_at
		FROM platform_user_permissions ORDER BY granted_at ASC
	`)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeInternal, "failed to list platform grants", err)
	}
	defer rows.Close()
	var out []Grant
	for rows.Next() {
		var g Grant
		if err := rows.Scan(&g.UserID, &g.PermissionKey, &g.GrantedByUserID, &g.GrantedAt); err != nil {
			return nil, apierr.Wrap(apierr.CodeInternal, "failed to scan platform grant", err)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// Grant gives targetUserID a platform permission. Requires
// platform.admins.manage — deliberately not subject to any
// no-privilege-escalation check against the granter's own permissions the
// way organization role assignment is, because platform.admins.manage
// itself already implies full platform-admin trust (holding it means you
// are trusted to grant any platform permission, by design, same as an
// organization 'owner' holding every organization permission).
func (s *Service) Grant(ctx context.Context, ac authctx.AuthContext, targetUserID uuid.UUID, key string) error {
	if err := s.Require(ctx, ac, PermManagePlatformAdmins); err != nil {
		return err
	}
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM platform_permissions WHERE key = $1)`, key).Scan(&exists); err != nil {
		return apierr.Wrap(apierr.CodeInternal, "failed to validate platform permission key", err)
	}
	if !exists {
		return apierr.Validation("unknown platform permission: " + key)
	}
	var exists2 bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id = $1)`, targetUserID).Scan(&exists2); err != nil {
		return apierr.Wrap(apierr.CodeInternal, "failed to validate target user", err)
	}
	if !exists2 {
		return apierr.NotFound("user")
	}

	granterID := ac.ActorID
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO platform_user_permissions (user_id, permission_key, granted_by_user_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (user_id, permission_key) DO NOTHING
	`, targetUserID, key, granterID); err != nil {
		return apierr.Wrap(apierr.CodeInternal, "failed to grant platform permission", err)
	}

	if err := s.audit.Record(ctx, ac, audit.Entry{
		Action: "platform.permission.granted", ResourceType: "platform_user_permission", ResourceID: targetUserID.String(),
		Success: true, ResultingState: map[string]string{"user_id": targetUserID.String(), "permission_key": key},
	}); err != nil {
		logger.FromContext(ctx).Error("failed to write audit entry", "error", err)
	}
	return nil
}

// Revoke removes a platform permission grant. Not an error if the user
// never held it — same idempotent-removal posture as
// tenancy.RemoveMember/identity.RevokeAPIToken.
func (s *Service) Revoke(ctx context.Context, ac authctx.AuthContext, targetUserID uuid.UUID, key string) error {
	if err := s.Require(ctx, ac, PermManagePlatformAdmins); err != nil {
		return err
	}
	if targetUserID == ac.ActorID && key == PermManagePlatformAdmins {
		// A platform admin revoking their own platform.admins.manage is
		// allowed to fail loudly rather than silently lock every admin
		// out of the system with no recovery path short of a manual SQL
		// fix — mirrors tenancy's "can't remove the last owner" guard.
		var remaining int
		if err := s.pool.QueryRow(ctx, `
			SELECT count(*) FROM platform_user_permissions WHERE permission_key = $1 AND user_id != $2
		`, PermManagePlatformAdmins, ac.ActorID).Scan(&remaining); err != nil {
			return apierr.Wrap(apierr.CodeInternal, "failed to check remaining platform admins", err)
		}
		if remaining == 0 {
			return apierr.Conflict("cannot revoke the last platform.admins.manage grant")
		}
	}

	if _, err := s.pool.Exec(ctx, `
		DELETE FROM platform_user_permissions WHERE user_id = $1 AND permission_key = $2
	`, targetUserID, key); err != nil {
		return apierr.Wrap(apierr.CodeInternal, "failed to revoke platform permission", err)
	}

	if err := s.audit.Record(ctx, ac, audit.Entry{
		Action: "platform.permission.revoked", ResourceType: "platform_user_permission", ResourceID: targetUserID.String(),
		Success: true, ResultingState: map[string]string{"user_id": targetUserID.String(), "permission_key": key},
	}); err != nil {
		logger.FromContext(ctx).Error("failed to write audit entry", "error", err)
	}
	return nil
}

// BootstrapAdmin grants every catalog permission to the user with the
// given email, if that user exists and doesn't already hold them. Called
// once at process startup (cmd/server/main.go) when
// NODERA_PLATFORM_BOOTSTRAP_ADMIN_EMAIL is set — the explicit,
// operator-controlled mechanism for establishing the first platform
// administrator (see docs/SECURITY.md). It is not a standing
// authorization rule: nothing at request time compares an email to this
// value, so removing the env var after first boot doesn't revoke anything
// and setting it again doesn't silently grant it a second way — it just
// re-runs the same idempotent grant. A missing user is logged, not fatal:
// misconfiguring this must never crash startup.
func (s *Service) BootstrapAdmin(ctx context.Context, email string) error {
	var userID uuid.UUID
	err := s.pool.QueryRow(ctx, `SELECT id FROM users WHERE email = $1`, email).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		logger.FromContext(ctx).Warn("platform bootstrap admin email does not match any existing user; no grant made", "email", email)
		return nil
	}
	if err != nil {
		return apierr.Wrap(apierr.CodeInternal, "failed to resolve platform bootstrap admin", err)
	}

	rows, err := s.pool.Query(ctx, `SELECT key FROM platform_permissions`)
	if err != nil {
		return apierr.Wrap(apierr.CodeInternal, "failed to load platform permission catalog", err)
	}
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			rows.Close()
			return apierr.Wrap(apierr.CodeInternal, "failed to scan platform permission", err)
		}
		keys = append(keys, k)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return apierr.Wrap(apierr.CodeInternal, "failed to load platform permission catalog", err)
	}

	for _, key := range keys {
		if _, err := s.pool.Exec(ctx, `
			INSERT INTO platform_user_permissions (user_id, permission_key, granted_by_user_id)
			VALUES ($1, $2, NULL)
			ON CONFLICT (user_id, permission_key) DO NOTHING
		`, userID, key); err != nil {
			return apierr.Wrap(apierr.CodeInternal, "failed to bootstrap platform admin grant", err)
		}
	}
	logger.FromContext(ctx).Info("bootstrapped platform administrator", "email", email, "user_id", userID.String())
	return nil
}
