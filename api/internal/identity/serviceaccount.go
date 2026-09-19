package identity

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/nodera/nodera/internal/audit"
	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/platform/authctx"
	"github.com/nodera/nodera/internal/platform/logger"
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
	if err := s.audit.Record(ctx, ac, audit.Entry{
		Action: "identity.service_account.created", ResourceType: "service_account", ResourceID: sa.ID.String(),
		Success: true, ResultingState: sa,
	}); err != nil {
		logger.FromContext(ctx).Error("failed to write audit entry", "error", err)
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
// revoke its tokens without an audit-visible reason) — it flips status,
// and, in the same transaction, revokes every one of its outstanding
// tokens. Disabling is meant to actually cut off access immediately, not
// merely block minting new tokens while old ones keep working — so both
// happen atomically: a caller never observes a disabled service account
// whose tokens still authenticate.
func (s *Service) DisableServiceAccount(ctx context.Context, ac authctx.AuthContext, id uuid.UUID) error {
	if err := rbac.Require(ac, creatingAPITokensRequires); err != nil {
		return err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return apierr.Wrap(apierr.CodeInternal, "failed to begin transaction", err)
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx, `
		UPDATE service_accounts SET status = 'disabled' WHERE id = $1 AND organization_id = $2
	`, id, ac.OrganizationID)
	if err != nil {
		return apierr.Wrap(apierr.CodeInternal, "failed to disable service account", err)
	}
	if tag.RowsAffected() == 0 {
		return apierr.NotFound("service account")
	}

	if _, err := tx.Exec(ctx, `
		UPDATE api_tokens SET revoked_at = now()
		WHERE service_account_id = $1 AND organization_id = $2 AND revoked_at IS NULL
	`, id, ac.OrganizationID); err != nil {
		return apierr.Wrap(apierr.CodeInternal, "failed to revoke service account's tokens", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return apierr.Wrap(apierr.CodeInternal, "failed to commit transaction", err)
	}
	if err := s.audit.Record(ctx, ac, audit.Entry{
		Action: "identity.service_account.disabled", ResourceType: "service_account", ResourceID: id.String(), Success: true,
	}); err != nil {
		logger.FromContext(ctx).Error("failed to write audit entry", "error", err)
	}
	return nil
}

// EnableServiceAccount reverses Disable's status flip. It does not restore
// any token Disable revoked — those stay revoked permanently (Disable's own
// doc comment explains why: a caller must never observe a disabled service
// account whose old tokens still authenticate, and that guarantee would be
// worthless if Enable quietly undid it). A caller mints fresh tokens for a
// re-enabled service account the same way as for a newly created one.
func (s *Service) EnableServiceAccount(ctx context.Context, ac authctx.AuthContext, id uuid.UUID) error {
	if err := rbac.Require(ac, creatingAPITokensRequires); err != nil {
		return err
	}

	tag, err := s.pool.Exec(ctx, `
		UPDATE service_accounts SET status = 'active' WHERE id = $1 AND organization_id = $2
	`, id, ac.OrganizationID)
	if err != nil {
		return apierr.Wrap(apierr.CodeInternal, "failed to enable service account", err)
	}
	if tag.RowsAffected() == 0 {
		return apierr.NotFound("service account")
	}
	if err := s.audit.Record(ctx, ac, audit.Entry{
		Action: "identity.service_account.enabled", ResourceType: "service_account", ResourceID: id.String(), Success: true,
	}); err != nil {
		logger.FromContext(ctx).Error("failed to write audit entry", "error", err)
	}
	return nil
}

// UpdateServiceAccountInput follows the pointer-based partial-update
// convention used across the codebase: a nil field leaves the existing
// value untouched. There is no way to change status here — that stays
// Enable/Disable's job, since disabling also has the token-revocation side
// effect a plain field update must not trigger.
type UpdateServiceAccountInput struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
}

func (s *Service) UpdateServiceAccount(ctx context.Context, ac authctx.AuthContext, id uuid.UUID, in UpdateServiceAccountInput) (ServiceAccount, error) {
	if err := rbac.Require(ac, creatingAPITokensRequires); err != nil {
		return ServiceAccount{}, err
	}
	if in.Name != nil && *in.Name == "" {
		return ServiceAccount{}, apierr.Validation("service account name cannot be empty")
	}

	var existing ServiceAccount
	if err := s.pool.QueryRow(ctx, `
		SELECT id, name, description, status, created_at FROM service_accounts WHERE id = $1 AND organization_id = $2
	`, id, ac.OrganizationID).Scan(&existing.ID, &existing.Name, &existing.Description, &existing.Status, &existing.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ServiceAccount{}, apierr.NotFound("service account")
		}
		return ServiceAccount{}, apierr.Wrap(apierr.CodeInternal, "failed to load service account", err)
	}

	name := existing.Name
	if in.Name != nil {
		name = *in.Name
	}
	description := existing.Description
	if in.Description != nil {
		description = *in.Description
	}

	var sa ServiceAccount
	err := s.pool.QueryRow(ctx, `
		UPDATE service_accounts SET name = $1, description = $2 WHERE id = $3 AND organization_id = $4
		RETURNING id, name, description, status, created_at
	`, name, description, id, ac.OrganizationID).Scan(&sa.ID, &sa.Name, &sa.Description, &sa.Status, &sa.CreatedAt)
	if err != nil {
		return ServiceAccount{}, apierr.Wrap(apierr.CodeInternal, "failed to update service account", err)
	}
	if err := s.audit.Record(ctx, ac, audit.Entry{
		Action: "identity.service_account.updated", ResourceType: "service_account", ResourceID: sa.ID.String(),
		Success: true, PreviousState: existing, ResultingState: sa,
	}); err != nil {
		logger.FromContext(ctx).Error("failed to write audit entry", "error", err)
	}
	return sa, nil
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
