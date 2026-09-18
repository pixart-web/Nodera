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

// APIToken is a scoped, revocable credential a user can use to call the API
// without a browser session (ADR-005). Phase 1 supports only user-owned
// tokens, not service-account-issued ones — service account token issuance
// is future work (docs/ROADMAP.md); the schema (api_tokens.service_account_id)
// already accommodates it.
type APIToken struct {
	ID          uuid.UUID  `json:"id"`
	Name        string     `json:"name"`
	TokenPrefix string     `json:"token_prefix"`
	Scopes      []string   `json:"scopes"`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   *time.Time `json:"expires_at"`
	LastUsedAt  *time.Time `json:"last_used_at"`
}

var ErrTokenInvalid = apierr.Unauthenticated("API token is invalid, expired, or revoked")

// creatingAPITokensRequires is deliberately the closest available
// permission — the catalog (migration 0002_rbac.sql) has no dedicated
// 'tokens.manage' key. Issuing a credential that can act on the
// organization's behalf is an organization-management-level action.
const creatingAPITokensRequires = "organization.manage"

// CreateAPIToken issues a new token, owned by the caller themselves,
// scoped to a subset of the caller's own permissions — a caller can never
// mint a token with more access than they themselves hold, even though
// they hold organization.manage (rule: no privilege escalation via token
// scoping).
func (s *Service) CreateAPIToken(ctx context.Context, ac authctx.AuthContext, name string, scopes []string, expiresAt *time.Time) (rawToken string, tok APIToken, err error) {
	if err := rbac.Require(ac, creatingAPITokensRequires); err != nil {
		return "", APIToken{}, err
	}
	if err := validateTokenScopes(ac, name, scopes); err != nil {
		return "", APIToken{}, err
	}
	userID := ac.ActorID
	return s.insertAPIToken(ctx, ac.OrganizationID, &userID, nil, name, scopes, expiresAt)
}

// CreateAPITokenForServiceAccount issues a token owned by a service
// account rather than the caller — the caller must still hold
// organization.manage and can still never grant a scope beyond their own
// permissions, same rule as CreateAPIToken. This is what makes a
// service-account-issued token possible at all: the schema
// (api_tokens.service_account_id) supported it from the start, but nothing
// could create one until this method existed.
func (s *Service) CreateAPITokenForServiceAccount(ctx context.Context, ac authctx.AuthContext, serviceAccountID uuid.UUID, name string, scopes []string, expiresAt *time.Time) (rawToken string, tok APIToken, err error) {
	if err := rbac.Require(ac, creatingAPITokensRequires); err != nil {
		return "", APIToken{}, err
	}
	if err := validateTokenScopes(ac, name, scopes); err != nil {
		return "", APIToken{}, err
	}
	active, err := s.serviceAccountExists(ctx, ac.OrganizationID, serviceAccountID)
	if err != nil {
		return "", APIToken{}, err
	}
	if !active {
		return "", APIToken{}, apierr.Validation("no active service account with that id in this organization")
	}
	return s.insertAPIToken(ctx, ac.OrganizationID, nil, &serviceAccountID, name, scopes, expiresAt)
}

func validateTokenScopes(ac authctx.AuthContext, name string, scopes []string) error {
	if name == "" {
		return apierr.Validation("token name is required")
	}
	if len(scopes) == 0 {
		return apierr.Validation("at least one scope is required")
	}
	for _, scope := range scopes {
		if !ac.HasPermission(scope) {
			return apierr.Forbidden("cannot grant scope you do not hold: " + scope)
		}
	}
	return nil
}

func (s *Service) insertAPIToken(ctx context.Context, orgID uuid.UUID, userID, serviceAccountID *uuid.UUID, name string, scopes []string, expiresAt *time.Time) (rawToken string, tok APIToken, err error) {
	raw, hash, err := generateToken(32)
	if err != nil {
		return "", APIToken{}, apierr.Wrap(apierr.CodeInternal, "failed to generate API token", err)
	}
	prefix := raw[:8]

	var t APIToken
	err = s.pool.QueryRow(ctx, `
		INSERT INTO api_tokens (organization_id, user_id, service_account_id, name, token_hash, token_prefix, scopes, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, name, token_prefix, scopes, created_at, expires_at, last_used_at
	`, orgID, userID, serviceAccountID, name, hash, prefix, scopes, expiresAt).Scan(
		&t.ID, &t.Name, &t.TokenPrefix, &t.Scopes, &t.CreatedAt, &t.ExpiresAt, &t.LastUsedAt,
	)
	if err != nil {
		return "", APIToken{}, apierr.Wrap(apierr.CodeInternal, "failed to store API token", err)
	}

	return raw, t, nil
}

func (s *Service) ListAPITokens(ctx context.Context, ac authctx.AuthContext) ([]APIToken, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, token_prefix, scopes, created_at, expires_at, last_used_at
		FROM api_tokens
		WHERE organization_id = $1 AND user_id = $2 AND revoked_at IS NULL
		ORDER BY created_at DESC
	`, ac.OrganizationID, ac.ActorID)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeInternal, "failed to list API tokens", err)
	}
	defer rows.Close()

	var out []APIToken
	for rows.Next() {
		var t APIToken
		if err := rows.Scan(&t.ID, &t.Name, &t.TokenPrefix, &t.Scopes, &t.CreatedAt, &t.ExpiresAt, &t.LastUsedAt); err != nil {
			return nil, apierr.Wrap(apierr.CodeInternal, "failed to scan API token", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// RevokeAPIToken revokes a token, scoped to the caller's own tokens — a
// user cannot revoke another user's token this way, even within the same
// organization. See AdminRevokeAPIToken for organization.manage-scoped
// revocation of any token in the org.
func (s *Service) RevokeAPIToken(ctx context.Context, ac authctx.AuthContext, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE api_tokens SET revoked_at = now()
		WHERE id = $1 AND organization_id = $2 AND user_id = $3 AND revoked_at IS NULL
	`, id, ac.OrganizationID, ac.ActorID)
	if err != nil {
		return apierr.Wrap(apierr.CodeInternal, "failed to revoke API token", err)
	}
	if tag.RowsAffected() == 0 {
		return apierr.NotFound("API token")
	}
	return nil
}

// AdminAPIToken is APIToken plus who actually owns it — only meaningful in
// the admin (org-wide) listing, where the caller isn't necessarily the owner.
type AdminAPIToken struct {
	APIToken
	OwnerType  string `json:"owner_type"` // "user" | "service_account"
	OwnerLabel string `json:"owner_label"`
}

// AdminListAPITokens lists every non-revoked token in the organization,
// regardless of owner — the organization.manage-scoped counterpart to
// ListAPITokens. This, plus AdminRevokeAPIToken, is what makes
// "org-admin management of other users' tokens" real rather than aspirational.
func (s *Service) AdminListAPITokens(ctx context.Context, ac authctx.AuthContext) ([]AdminAPIToken, error) {
	if err := rbac.Require(ac, creatingAPITokensRequires); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT t.id, t.name, t.token_prefix, t.scopes, t.created_at, t.expires_at, t.last_used_at,
		       CASE WHEN t.user_id IS NOT NULL THEN 'user' ELSE 'service_account' END,
		       COALESCE(u.email, sa.name, 'unknown')
		FROM api_tokens t
		LEFT JOIN users u ON u.id = t.user_id
		LEFT JOIN service_accounts sa ON sa.id = t.service_account_id
		WHERE t.organization_id = $1 AND t.revoked_at IS NULL
		ORDER BY t.created_at DESC
	`, ac.OrganizationID)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeInternal, "failed to list organization API tokens", err)
	}
	defer rows.Close()

	var out []AdminAPIToken
	for rows.Next() {
		var t AdminAPIToken
		if err := rows.Scan(&t.ID, &t.Name, &t.TokenPrefix, &t.Scopes, &t.CreatedAt, &t.ExpiresAt, &t.LastUsedAt,
			&t.OwnerType, &t.OwnerLabel); err != nil {
			return nil, apierr.Wrap(apierr.CodeInternal, "failed to scan API token", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// AdminRevokeAPIToken revokes any token in the organization, regardless of
// owner — requires organization.manage.
func (s *Service) AdminRevokeAPIToken(ctx context.Context, ac authctx.AuthContext, id uuid.UUID) error {
	if err := rbac.Require(ac, creatingAPITokensRequires); err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE api_tokens SET revoked_at = now()
		WHERE id = $1 AND organization_id = $2 AND revoked_at IS NULL
	`, id, ac.OrganizationID)
	if err != nil {
		return apierr.Wrap(apierr.CodeInternal, "failed to revoke API token", err)
	}
	if tag.RowsAffected() == 0 {
		return apierr.NotFound("API token")
	}
	return nil
}

// AuthContextForAPIToken resolves a raw API token to a fully-populated
// AuthContext. Unlike AuthContextForSession, the organization is embedded in
// the token itself (an API token is minted within one organization) and
// permissions come directly from the token's own scopes rather than a live
// role lookup — scopes were already validated to be a subset of the
// creator's permissions at CreateAPIToken time.
func (s *Service) AuthContextForAPIToken(ctx context.Context, token, correlationID string) (authctx.AuthContext, error) {
	tokenHash := hashToken(token)

	var (
		id            uuid.UUID
		orgID         uuid.UUID
		userID        *uuid.UUID
		serviceAcctID *uuid.UUID
		scopes        []string
		actorLabel    string
	)
	err := s.pool.QueryRow(ctx, `
		SELECT t.id, t.organization_id, t.user_id, t.service_account_id, t.scopes,
		       COALESCE(u.email, sa.name, 'unknown')
		FROM api_tokens t
		LEFT JOIN users u ON u.id = t.user_id
		LEFT JOIN service_accounts sa ON sa.id = t.service_account_id
		WHERE t.token_hash = $1 AND t.revoked_at IS NULL AND (t.expires_at IS NULL OR t.expires_at > now())
	`, tokenHash).Scan(&id, &orgID, &userID, &serviceAcctID, &scopes, &actorLabel)
	if errors.Is(err, pgx.ErrNoRows) {
		return authctx.AuthContext{}, ErrTokenInvalid
	}
	if err != nil {
		return authctx.AuthContext{}, apierr.Wrap(apierr.CodeInternal, "failed to look up API token", err)
	}

	perms := make(map[string]struct{}, len(scopes))
	for _, sc := range scopes {
		perms[sc] = struct{}{}
	}

	actorType := authctx.ActorUser
	actorID := uuid.Nil
	if userID != nil {
		actorID = *userID
	} else if serviceAcctID != nil {
		actorType = authctx.ActorServiceAccount
		actorID = *serviceAcctID
	}

	// Best-effort — a failure to update last_used_at must not fail
	// authentication itself.
	_, _ = s.pool.Exec(ctx, `UPDATE api_tokens SET last_used_at = now() WHERE id = $1`, id)

	return authctx.AuthContext{
		ActorType:      actorType,
		ActorID:        actorID,
		ActorLabel:     actorLabel,
		OrganizationID: orgID,
		Permissions:    perms,
		CorrelationID:  correlationID,
	}, nil
}
