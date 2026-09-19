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

	"github.com/nodera/nodera/internal/audit"
	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/platform/authctx"
	"github.com/nodera/nodera/internal/platform/logger"
)

// PermissionResolver is the narrow interface identity needs from rbac to
// build an AuthContext. Depending on this interface (rather than importing
// *rbac.Service directly) keeps the dependency direction explicit and makes
// identity testable without a real rbac.Service. See ADR-002.
type PermissionResolver interface {
	ResolvePermissions(ctx context.Context, orgID, userID uuid.UUID) (map[string]struct{}, error)
}

type AuditRecorder interface {
	Record(ctx context.Context, ac authctx.AuthContext, e audit.Entry) error
}

type Service struct {
	pool       *pgxpool.Pool
	rbac       PermissionResolver
	sessionTTL time.Duration
	audit      AuditRecorder
}

// New wires the identity service. SignUp, Login, Logout, ChangePassword,
// and session-revocation methods below now write real audit entries too
// (docs/SECURITY.md "Platform vs organization audit") — they run before
// an organization is ever selected (requireSession, not
// requireOrganization — cmd/server/router.go), so those entries carry no
// organization_id (audit.Record already supports this: a zero-value
// AuthContext.OrganizationID writes NULL) and are queryable only via
// audit.QueryPlatform (platform.audit.read), not the ordinary
// organization-scoped audit.Query. This was previously a documented gap
// (docs/ROADMAP.md Phase 41): writing such rows without a way to read
// them back would have been pointless; QueryPlatform closes that.
func New(pool *pgxpool.Pool, rbac PermissionResolver, sessionTTL time.Duration, auditRecorder AuditRecorder) *Service {
	return &Service{pool: pool, rbac: rbac, sessionTTL: sessionTTL, audit: auditRecorder}
}

// platformAuditContext builds the platform-scope AuthContext (no
// organization) identity's own audit.Record calls use — OrganizationID is
// deliberately left at its zero value (uuid.Nil), which audit.Record
// writes as a NULL organization_id.
func platformAuditContext(userID uuid.UUID) authctx.AuthContext {
	return authctx.AuthContext{ActorType: authctx.ActorUser, ActorID: userID}
}

// recordIdentityAudit is a small, deliberately-lenient wrapper around
// audit.Record for the identity methods below: a failure to write the
// audit entry must never fail (or roll back) the identity operation it
// describes — the same tradeoff audit.Service.Record's own doc comment
// establishes for every other domain, applied consistently here since
// these calls have no logger.FromContext-wired request context to log
// through the way HTTP-handler-adjacent code does.
func (s *Service) recordIdentityAudit(ctx context.Context, userID uuid.UUID, action string, success bool, metadata map[string]any) {
	if err := s.audit.Record(ctx, platformAuditContext(userID), audit.Entry{
		Action: action, ResourceType: "user", ResourceID: userID.String(),
		Success: success, Metadata: metadata,
	}); err != nil {
		logger.FromContext(ctx).Error("failed to write audit entry", "error", err, "action", action)
	}
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

// FindByEmail looks up an existing user by email. It never creates an
// account — that's SignUp's job — so it's safe to expose to other domains
// (internal/tenancy.AddMember uses it to resolve who's being added to an
// organization) without letting them provision users of their own.
func (s *Service) FindByEmail(ctx context.Context, email string) (User, error) {
	var u User
	err := s.pool.QueryRow(ctx, `
		SELECT id, email, display_name, status, created_at FROM users WHERE email = LOWER($1)
	`, email).Scan(&u.ID, &u.Email, &u.DisplayName, &u.Status, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, apierr.NotFound("user")
	}
	if err != nil {
		return User{}, apierr.Wrap(apierr.CodeInternal, "failed to look up user", err)
	}
	return u, nil
}

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

	s.recordIdentityAudit(ctx, u.ID, "identity.user.signed_up", true, map[string]any{"email": u.Email})

	return u, nil
}

// UpdateProfile changes a user's own display name. Takes a raw userID
// rather than an authctx.AuthContext — like CreateOrganization, this is a
// pre-organization identity operation (a session token alone resolves a
// user ID; the organization and permissions aren't known yet at this
// point in the request pipeline, see cmd/server/middleware.go
// requireSession vs. requireOrganization). Email is deliberately not
// editable here — changing it would need re-verification (no email
// delivery exists in this phase, docs/ROADMAP.md) and touches login
// identity, a bigger change than this method's scope.
func (s *Service) UpdateProfile(ctx context.Context, userID uuid.UUID, displayName string) (User, error) {
	if displayName == "" {
		return User{}, apierr.Validation("display name is required")
	}

	var u User
	err := s.pool.QueryRow(ctx, `
		UPDATE users SET display_name = $2, updated_at = now() WHERE id = $1
		RETURNING id, email, display_name, status, created_at
	`, userID, displayName).Scan(&u.ID, &u.Email, &u.DisplayName, &u.Status, &u.CreatedAt)
	if err != nil {
		return User{}, apierr.Wrap(apierr.CodeInternal, "failed to update profile", err)
	}
	return u, nil
}

// ChangePassword verifies the caller's current password, then rotates it
// and revokes every other active session for the account — the caller's
// own current session (identified by currentSessionToken, empty if the
// caller authenticated with an API token rather than a session) is
// deliberately left untouched, so changing your password doesn't also log
// you out. Any other session (on another device, or one an attacker
// established with a since-compromised password) is revoked immediately.
// Same pre-organization signature rationale as UpdateProfile.
func (s *Service) ChangePassword(ctx context.Context, userID uuid.UUID, currentSessionToken, currentPassword, newPassword string) error {
	if err := validatePassword(newPassword); err != nil {
		return err
	}

	var hash string
	err := s.pool.QueryRow(ctx, `
		SELECT password_hash FROM user_password_credentials WHERE user_id = $1
	`, userID).Scan(&hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return apierr.NotFound("password credential")
	}
	if err != nil {
		return apierr.Wrap(apierr.CodeInternal, "failed to load password credential", err)
	}

	ok, err := verifyPassword(currentPassword, hash)
	if err != nil {
		return apierr.Wrap(apierr.CodeInternal, "failed to verify current password", err)
	}
	if !ok {
		return apierr.Unauthenticated("current password is incorrect")
	}

	newHash, err := hashPassword(newPassword)
	if err != nil {
		return apierr.Wrap(apierr.CodeInternal, "failed to hash new password", err)
	}

	// Resolve the current session's ID (if any) so it can be excluded from
	// the mass-revoke below — an invalid/expired/absent token just means
	// there's nothing to exclude, not an error worth failing the whole
	// password change over.
	var currentSessionID uuid.UUID
	if currentSessionToken != "" {
		if _, sid, err := s.sessionUser(ctx, currentSessionToken); err == nil {
			currentSessionID = sid
		}
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return apierr.Wrap(apierr.CodeInternal, "failed to begin transaction", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		UPDATE user_password_credentials SET password_hash = $2, updated_at = now() WHERE user_id = $1
	`, userID, newHash); err != nil {
		return apierr.Wrap(apierr.CodeInternal, "failed to store new password", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE sessions SET revoked_at = now() WHERE user_id = $1 AND id != $2 AND revoked_at IS NULL
	`, userID, currentSessionID); err != nil {
		return apierr.Wrap(apierr.CodeInternal, "failed to revoke other sessions", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return apierr.Wrap(apierr.CodeInternal, "failed to commit transaction", err)
	}

	s.recordIdentityAudit(ctx, userID, "identity.user.password_changed", true, nil)

	return nil
}

// Session is one of a user's active (not revoked, not expired) sessions —
// what "log out other devices" shows and acts on. The raw token itself is
// never retrievable after Login (ADR-005: only its hash is stored).
type Session struct {
	ID        uuid.UUID `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
	IPAddress string    `json:"ip_address,omitempty"`
	UserAgent string    `json:"user_agent,omitempty"`
	IsCurrent bool      `json:"is_current"`
}

// ListSessions returns every active session for userID, most recent
// first, with IsCurrent marking whichever one currentSessionToken
// resolves to (empty, or a token that fails to resolve, marks none —
// an API-token caller has no session of its own to flag).
func (s *Service) ListSessions(ctx context.Context, userID uuid.UUID, currentSessionToken string) ([]Session, error) {
	var currentSessionID uuid.UUID
	if currentSessionToken != "" {
		if _, sid, err := s.sessionUser(ctx, currentSessionToken); err == nil {
			currentSessionID = sid
		}
	}

	rows, err := s.pool.Query(ctx, `
		SELECT id, created_at, expires_at, COALESCE(host(ip_address), ''), COALESCE(user_agent, '')
		FROM sessions
		WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > now()
		ORDER BY created_at DESC
	`, userID)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeInternal, "failed to list sessions", err)
	}
	defer rows.Close()

	var out []Session
	for rows.Next() {
		var sess Session
		if err := rows.Scan(&sess.ID, &sess.CreatedAt, &sess.ExpiresAt, &sess.IPAddress, &sess.UserAgent); err != nil {
			return nil, apierr.Wrap(apierr.CodeInternal, "failed to scan session", err)
		}
		sess.IsCurrent = sess.ID == currentSessionID
		out = append(out, sess)
	}
	return out, rows.Err()
}

// RevokeSession revokes one of userID's own sessions by ID — "log out
// [that] device." Scoped to userID so one user can never revoke another
// user's session by guessing/enumerating an ID.
func (s *Service) RevokeSession(ctx context.Context, userID, sessionID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE sessions SET revoked_at = now()
		WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL
	`, sessionID, userID)
	if err != nil {
		return apierr.Wrap(apierr.CodeInternal, "failed to revoke session", err)
	}
	if tag.RowsAffected() == 0 {
		return apierr.NotFound("session")
	}

	s.recordIdentityAudit(ctx, userID, "identity.session.revoked", true, map[string]any{"session_id": sessionID.String()})

	return nil
}

// RevokeAllOtherSessions revokes every one of userID's active sessions
// except the caller's own current one — "log out all other devices" as a
// direct action, rather than only as ChangePassword's side effect. Shares
// ChangePassword's exact "resolve the current session, exclude it from
// the mass-revoke" logic; an invalid/expired/absent currentSessionToken
// just means there's nothing to exclude, not an error worth failing the
// call over (same tradeoff ChangePassword makes).
func (s *Service) RevokeAllOtherSessions(ctx context.Context, userID uuid.UUID, currentSessionToken string) error {
	var currentSessionID uuid.UUID
	if currentSessionToken != "" {
		if _, sid, err := s.sessionUser(ctx, currentSessionToken); err == nil {
			currentSessionID = sid
		}
	}
	if _, err := s.pool.Exec(ctx, `
		UPDATE sessions SET revoked_at = now() WHERE user_id = $1 AND id != $2 AND revoked_at IS NULL
	`, userID, currentSessionID); err != nil {
		return apierr.Wrap(apierr.CodeInternal, "failed to revoke other sessions", err)
	}

	s.recordIdentityAudit(ctx, userID, "identity.session.revoked_all_others", true, nil)

	return nil
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
		s.recordIdentityAudit(ctx, u.ID, "identity.user.login_failed", false, map[string]any{"reason": "account_disabled", "ip_address": ipAddress})
		return "", User{}, apierr.Forbidden("account is disabled")
	}

	ok, err := verifyPassword(password, hash)
	if err != nil {
		return "", User{}, apierr.Wrap(apierr.CodeInternal, "failed to verify password", err)
	}
	if !ok {
		// Only recorded when the account itself is real (we already have
		// u.ID at this point) — a nonexistent email returns
		// ErrInvalidCredentials above without ever reaching here, so no
		// audit row is written for it. This isn't a coverage gap: there is
		// no real user to attribute a "failed login" to for an email that
		// was never signed up, and writing a row keyed by the attempted
		// email instead would turn the audit log into an account-
		// enumeration oracle for anyone who can read it.
		s.recordIdentityAudit(ctx, u.ID, "identity.user.login_failed", false, map[string]any{"reason": "invalid_password", "ip_address": ipAddress})
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

	s.recordIdentityAudit(ctx, u.ID, "identity.user.logged_in", true, map[string]any{"ip_address": ipAddress, "user_agent": userAgent})

	return token, u, nil
}

// Logout revokes a session by its raw token.
func (s *Service) Logout(ctx context.Context, token string) error {
	tokenHash := hashToken(token)
	userID, _, lookupErr := s.sessionUser(ctx, token)

	_, err := s.pool.Exec(ctx, `
		UPDATE sessions SET revoked_at = now() WHERE token_hash = $1 AND revoked_at IS NULL
	`, tokenHash)
	if err != nil {
		return apierr.Wrap(apierr.CodeInternal, "failed to revoke session", err)
	}

	// Only recorded when the token resolved to a real, still-valid
	// session — an already-invalid/expired token has no user to
	// attribute the entry to, and the UPDATE above already correctly
	// no-ops for it either way.
	if lookupErr == nil {
		s.recordIdentityAudit(ctx, userID, "identity.user.logged_out", true, nil)
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
