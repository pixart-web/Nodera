// Package rbac centralizes authorization decisions. No domain package should
// implement its own ad-hoc "is admin" boolean check (rule 5) — everything
// goes through Check or authctx.AuthContext.HasPermission, which Check uses
// to resolve the permission set in the first place.
package rbac

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/platform/authctx"
)

type Service struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

// ResolvePermissions computes the full set of permission keys a user holds
// within an organization, by unioning the permissions of every role granted
// to them there. This is called once per request (by the identity module,
// when it builds the AuthContext) rather than on every permission check.
func (s *Service) ResolvePermissions(ctx context.Context, orgID, userID uuid.UUID) (map[string]struct{}, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT rp.permission_key
		FROM organization_member_roles omr
		JOIN role_permissions rp ON rp.role_id = omr.role_id
		WHERE omr.organization_id = $1 AND omr.user_id = $2
	`, orgID, userID)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeInternal, "failed to resolve permissions", err)
	}
	defer rows.Close()

	perms := make(map[string]struct{})
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, apierr.Wrap(apierr.CodeInternal, "failed to scan permission", err)
		}
		perms[key] = struct{}{}
	}
	return perms, rows.Err()
}

// Require returns a *apierr.Error (CodeForbidden) unless ac holds permission,
// or ac is a system actor (authctx.ActorSystem), which bypasses checks
// entirely because it is never derived from an untrusted client request.
//
// Every domain service method that performs a sensitive read or any mutation
// must call Require before touching data (docs/SECURITY.md).
func Require(ac authctx.AuthContext, permission string) error {
	if ac.ActorType == authctx.ActorSystem {
		return nil
	}
	if !ac.HasPermission(permission) {
		return apierr.Forbidden(fmt.Sprintf("missing required permission: %s", permission))
	}
	return nil
}
