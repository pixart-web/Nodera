// Package identity implements Nodera's first-party authentication: users,
// password credentials, opaque server-side sessions, and scoped API tokens
// (ADR-005). It is the only module that resolves "who is calling" into an
// authctx.AuthContext; every other module receives that context already
// built.
package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/platform/authctx"
)

// PermissionResolver is the narrow interface identity needs from rbac to
// build an AuthContext. Depending on this interface (rather than importing
// *rbac.Service directly) keeps the dependency direction explicit and makes
// identity testable without a real rbac.Service. See ADR-002.
type PermissionResolver interface {
	ResolvePermissions(ctx context.Context, orgID, userID uuid.UUID) (map[string]struct{}, error)
}

type Service struct {
	pool       *pgxpool.Pool
	rbac       PermissionResolver
	sessionTTL time.Duration
}

func New(pool *pgxpool.Pool, rbac PermissionResolver, sessionTTL time.Duration) *Service {
	return &Service{pool: pool, rbac: rbac, sessionTTL: sessionTTL}
}

type User struct {
	ID          uuid.UUID `json:"id"`
	Email       string    `json:"email"`
	DisplayName string    `json:"display_name"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
}

var ErrEmailTaken = apierr.Conflict("an account with this email already exists")
var ErrInvalidCredentials = apierr.Unauthenticated("invalid email or password")
var ErrSessionInvalid = apierr.Unauthenticated("session is invalid or expired")

// SignUp creates a new user with a password credential. It does not create
// or join any organization — see internal/tenancy.CreateOrganization.
func (s *Service) SignUp(ctx context.Context, email, password, displayName string) (User, error) {
	if err := validateEmail(email); err != nil {
		return User{}, err
	}
	if err := validatePassword(password); err != nil {
		return User{}, err
	}
	if displayName == "" {
		return User{}, apierr.Validation("display name is required")
	}

	hash, err := hashPassword(password)
	if err != nil {
		return User{}, apierr.Wrap(apierr.CodeInternal, "failed to hash password", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return User{}, apierr.Wrap(apierr.CodeInternal, "failed to begin transaction", err)
	}
	defer tx.Rollback(ctx)

	var u User
	err = tx.QueryRow(ctx, `
		INSERT INTO users (email, display_name) VALUES (LOWER($1), $2)
		RETURNING id, email, display_name, status, created_at
	`, email, displayName).Scan(&u.ID, &u.Email, &u.DisplayName, &u.Status, &u.CreatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return User{}, ErrEmailTaken
		}
		return User{}, apierr.Wrap(apierr.CodeInternal, "failed to create user", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO user_password_credentials (user_id, password_hash) VALUES ($1, $2)
	`, u.ID, hash); err != nil {
		return User{}, apierr.Wrap(apierr.CodeInternal, "failed to store credential", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return User{}, apierr.Wrap(apierr.CodeInternal, "failed to commit transaction", err)
	}

	return u, nil
}

// Login verifies credentials and creates a new session. It returns the raw
// session token (given to the client once, never stored) and the user.
func (s *Service) Login(ctx context.Context, email, password, ipAddress, userAgent string) (token string, u User, err error) {
	var hash string
	err = s.pool.QueryRow(ctx, `
		SELECT u.id, u.email, u.display_name, u.status, u.created_at, c.password_hash
		FROM users u
		JOIN user_password_credentials c ON c.user_id = u.id
		WHERE u.email = LOWER($1)
	`, email).Scan(&u.ID, &u.Email, &u.DisplayName, &u.Status, &u.CreatedAt, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", User{}, ErrInvalidCredentials
	}
	if err != nil {
		return "", User{}, apierr.Wrap(apierr.CodeInternal, "failed to look up user", err)
	}

	if u.Status != "active" {
		return "", User{}, apierr.Forbidden("account is disabled")
	}

	ok, err := verifyPassword(password, hash)
	if err != nil {
		return "", User{}, apierr.Wrap(apierr.CodeInternal, "failed to verify password", err)
	}
	if !ok {
		return "", User{}, ErrInvalidCredentials
	}

	token, tokenHash, err := generateToken(32)
	if err != nil {
		return "", User{}, apierr.Wrap(apierr.CodeInternal, "failed to generate session token", err)
	}

	_, err = s.pool.Exec(ctx, `
		INSERT INTO sessions (user_id, token_hash, expires_at, ip_address, user_agent)
		VALUES ($1, $2, $3, NULLIF($4, '')::inet, $5)
	`, u.ID, tokenHash, time.Now().Add(s.sessionTTL), ipAddress, userAgent)
	if err != nil {
		return "", User{}, apierr.Wrap(apierr.CodeInternal, "failed to create session", err)
	}

	return token, u, nil
}

// Logout revokes a session by its raw token.
func (s *Service) Logout(ctx context.Context, token string) error {
	tokenHash := hashToken(token)
	_, err := s.pool.Exec(ctx, `
		UPDATE sessions SET revoked_at = now() WHERE token_hash = $1 AND revoked_at IS NULL
	`, tokenHash)
	if err != nil {
		return apierr.Wrap(apierr.CodeInternal, "failed to revoke session", err)
	}
	return nil
}

// UserIDForSession resolves a raw session token to its user ID, verifying
// the session is neither expired nor revoked, without requiring an
// organization. Used for routes a user can call before selecting one (e.g.
// listing their organizations).
func (s *Service) UserIDForSession(ctx context.Context, token string) (uuid.UUID, error) {
	userID, _, err := s.sessionUser(ctx, token)
	return userID, err
}

// sessionUser resolves a raw session token to its user, verifying it is
// neither expired nor revoked.
func (s *Service) sessionUser(ctx context.Context, token string) (uuid.UUID, uuid.UUID, error) {
	tokenHash := hashToken(token)
	var userID, sessionID uuid.UUID
	err := s.pool.QueryRow(ctx, `
		SELECT id, user_id FROM sessions
		WHERE token_hash = $1 AND revoked_at IS NULL AND expires_at > now()
	`, tokenHash).Scan(&sessionID, &userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, uuid.Nil, ErrSessionInvalid
	}
	if err != nil {
		return uuid.Nil, uuid.Nil, apierr.Wrap(apierr.CodeInternal, "failed to look up session", err)
	}
	return userID, sessionID, nil
}

// AuthContextForSession resolves a raw session token plus the organization
// the caller is acting within into a fully-populated AuthContext, including
// resolved permissions. The caller (httpserver middleware) is responsible
// for confirming the user is actually a member of orgID.
func (s *Service) AuthContextForSession(ctx context.Context, token string, orgID uuid.UUID, correlationID string) (authctx.AuthContext, error) {
	userID, sessionID, err := s.sessionUser(ctx, token)
	if err != nil {
		return authctx.AuthContext{}, err
	}

	isMember, err := s.isOrgMember(ctx, orgID, userID)
	if err != nil {
		return authctx.AuthContext{}, err
	}
	if !isMember {
		return authctx.AuthContext{}, apierr.Forbidden("user is not a member of this organization")
	}

	perms, err := s.rbac.ResolvePermissions(ctx, orgID, userID)
	if err != nil {
		return authctx.AuthContext{}, err
	}

	var email string
	if err := s.pool.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, userID).Scan(&email); err != nil {
		return authctx.AuthContext{}, apierr.Wrap(apierr.CodeInternal, "failed to load user for audit label", err)
	}

	return authctx.AuthContext{
		ActorType:      authctx.ActorUser,
		ActorID:        userID,
		ActorLabel:     email,
		OrganizationID: orgID,
		Permissions:    perms,
		SessionID:      sessionID,
		CorrelationID:  correlationID,
	}, nil
}

func (s *Service) isOrgMember(ctx context.Context, orgID, userID uuid.UUID) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM organization_members WHERE organization_id = $1 AND user_id = $2)
	`, orgID, userID).Scan(&exists)
	if err != nil {
		return false, apierr.Wrap(apierr.CodeInternal, "failed to check organization membership", err)
	}
	return exists, nil
}

// generateToken returns a random URL-safe token and its SHA-256 hash (the
// only thing persisted — see migration 0001's comment on the sessions table).
func generateToken(numBytes int) (token, hash string, err error) {
	raw := make([]byte, numBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("identity: failed to generate token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(raw)
	return token, hashToken(token), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func isUniqueViolation(err error) bool {
	var pgErr interface{ SQLState() string }
	if errors.As(err, &pgErr) {
		return pgErr.SQLState() == "23505"
	}
	return false
}
