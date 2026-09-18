package identity

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/platform/authctx"
	"github.com/nodera/nodera/internal/rbac"
)

// ServiceAccount represents a non-human actor (an agent, an integration, a
// consuming product in the Nodera ecosystem) that can hold its own scoped
// API tokens — see docs/API.md "Authenticating". Creating one requires
// organization.manage, same as creating a user-owned token, since it's
// equally an organization-management-level action.
type ServiceAccount struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
}

func (s *Service) CreateServiceAccount(ctx context.Context, ac authctx.AuthContext, name, description string) (ServiceAccount, error) {
	if err := rbac.Require(ac, creatingAPITokensRequires); err != nil {
		return ServiceAccount{}, err
	}
	if name == "" {
		return ServiceAccount{}, apierr.Validation("service account name is required")
	}

	var sa ServiceAccount
	err := s.pool.QueryRow(ctx, `
		INSERT INTO service_accounts (organization_id, name, description)
		VALUES ($1, $2, $3)
		RETURNING id, name, description, status, created_at
	`, ac.OrganizationID, name, description).Scan(&sa.ID, &sa.Name, &sa.Description, &sa.Status, &sa.CreatedAt)
	if err != nil {
		return ServiceAccount{}, apierr.Wrap(apierr.CodeInternal, "failed to create service account", err)
	}
	return sa, nil
}

func (s *Service) ListServiceAccounts(ctx context.Context, ac authctx.AuthContext) ([]ServiceAccount, error) {
	if err := rbac.Require(ac, creatingAPITokensRequires); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, description, status, created_at
		FROM service_accounts WHERE organization_id = $1 ORDER BY name ASC
	`, ac.OrganizationID)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeInternal, "failed to list service accounts", err)
	}
	defer rows.Close()

	var out []ServiceAccount
	for rows.Next() {
		var sa ServiceAccount
		if err := rows.Scan(&sa.ID, &sa.Name, &sa.Description, &sa.Status, &sa.CreatedAt); err != nil {
			return nil, apierr.Wrap(apierr.CodeInternal, "failed to scan service account", err)
		}
		out = append(out, sa)
	}
	return out, rows.Err()
}

// DisableServiceAccount does not delete the row (CASCADE would silently
// revoke its tokens without an audit-visible reason) — it flips status so
// AuthContextForAPIToken's own checks continue to work unchanged, and a
// disabled service account's history stays inspectable.
func (s *Service) DisableServiceAccount(ctx context.Context, ac authctx.AuthContext, id uuid.UUID) error {
	if err := rbac.Require(ac, creatingAPITokensRequires); err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE service_accounts SET status = 'disabled' WHERE id = $1 AND organization_id = $2
	`, id, ac.OrganizationID)
	if err != nil {
		return apierr.Wrap(apierr.CodeInternal, "failed to disable service account", err)
	}
	if tag.RowsAffected() == 0 {
		return apierr.NotFound("service account")
	}
	return nil
}

func (s *Service) serviceAccountExists(ctx context.Context, orgID, id uuid.UUID) (bool, error) {
	var status string
	err := s.pool.QueryRow(ctx, `
		SELECT status FROM service_accounts WHERE id = $1 AND organization_id = $2
	`, id, orgID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, apierr.Wrap(apierr.CodeInternal, "failed to look up service account", err)
	}
	return status == "active", nil
}
