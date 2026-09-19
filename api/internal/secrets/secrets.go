// Package secrets implements Nodera's reference-based secret storage
// (section 21). A secret's plaintext value is never a plain database
// column — it is AES-256-GCM encrypted at rest, keyed by an application-
// level encryption key (NODERA_SECRETS_ENCRYPTION_KEY) that never itself
// touches the database. See docs/SECURITY.md.
//
// Reveal (the only way to get the plaintext back) is deliberately not
// reachable over HTTP — only Go code within the process (e.g. a future AI
// provider adapter resolving its credential) can call it. The HTTP surface
// only ever returns masked metadata.
package secrets

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
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
	"github.com/nodera/nodera/internal/rbac"
)

type AuditRecorder interface {
	Record(ctx context.Context, ac authctx.AuthContext, e audit.Entry) error
}

// PlatformAuthorizer checks platform-scoped permissions
// (internal/platformauth) — the Platform* methods below use this instead
// of internal/rbac, since a platform secret (a future cloud AI provider
// key, an infrastructure credential) is not owned by any one
// organization; gating it with an organization permission would let any
// organization admin manage platform-wide credentials, the same
// authorization mismatch internal/platformauth already closed for the AI
// provider/model registry.
type PlatformAuthorizer interface {
	Require(ctx context.Context, ac authctx.AuthContext, key string) error
}

type Service struct {
	pool     *pgxpool.Pool
	audit    AuditRecorder
	platform PlatformAuthorizer
	gcm      cipher.AEAD
}

// New constructs the secrets service. encryptionKeyBase64 must decode to
// exactly 32 bytes (AES-256). Callers (cmd/server) treat a missing or
// invalid key as "the secrets module is not configured" (rule 36: report
// unavailable, never fabricate) rather than crashing the whole process —
// see the wiring note in cmd/server/main.go. platform authorizes the
// Platform* methods (org-scoped Set/List/UpdateDescription/Delete/Reveal
// are unaffected and keep using internal/rbac); it may be nil if the
// caller never intends to use the Platform* methods (e.g. a test that
// only exercises org secrets), which then fail closed rather than
// panicking — see requirePlatformPermission.
func New(pool *pgxpool.Pool, audit AuditRecorder, platform PlatformAuthorizer, encryptionKeyBase64 string) (*Service, error) {
	key, err := base64.StdEncoding.DecodeString(encryptionKeyBase64)
	if err != nil {
		return nil, fmt.Errorf("secrets: NODERA_SECRETS_ENCRYPTION_KEY is not valid base64: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("secrets: NODERA_SECRETS_ENCRYPTION_KEY must decode to 32 bytes (AES-256), got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("secrets: failed to construct AES cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("secrets: failed to construct GCM mode: %w", err)
	}
	return &Service{pool: pool, audit: audit, platform: platform, gcm: gcm}, nil
}

const (
	permManage = "secrets.manage"
	permRead   = "secrets.read"

	permPlatformSecretsManage = "platform.secrets.manage"
	permPlatformSecretsRead   = "platform.secrets.read"
)

func (s *Service) requirePlatform(ctx context.Context, ac authctx.AuthContext, key string) error {
	if s.platform == nil {
		return apierr.New(apierr.CodeInternal, "platform secrets unavailable: no platform authorizer wired")
	}
	return s.platform.Require(ctx, ac, key)
}

// Meta is what the HTTP surface and List/Create ever return — the
// plaintext value is never included (rule: never expose full secret values
// through the frontend or logs).
type Meta struct {
	ID          uuid.UUID `json:"id"`
	Key         string    `json:"key"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (s *Service) encrypt(plaintext string) ([]byte, error) {
	nonce := make([]byte, s.gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("secrets: failed to generate nonce: %w", err)
	}
	return s.gcm.Seal(nonce, nonce, []byte(plaintext), nil), nil
}

func (s *Service) decrypt(ciphertext []byte) (string, error) {
	nonceSize := s.gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return "", errors.New("secrets: stored ciphertext is shorter than the nonce size")
	}
	nonce, sealed := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := s.gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		return "", fmt.Errorf("secrets: failed to decrypt (wrong key or corrupted data): %w", err)
	}
	return string(plaintext), nil
}

// Set creates or updates (upserts by organization+key) a secret's value.
// The plaintext value never appears in the returned Meta, logs, or the
// audit entry this writes.
func (s *Service) Set(ctx context.Context, ac authctx.AuthContext, key, value, description string) (Meta, error) {
	if err := rbac.Require(ac, permManage); err != nil {
		return Meta{}, err
	}
	if key == "" {
		return Meta{}, apierr.Validation("secret key is required")
	}
	if value == "" {
		return Meta{}, apierr.Validation("secret value is required")
	}

	ciphertext, err := s.encrypt(value)
	if err != nil {
		return Meta{}, apierr.Wrap(apierr.CodeInternal, "failed to encrypt secret", err)
	}

	var m Meta
	err = s.pool.QueryRow(ctx, `
		INSERT INTO secrets (organization_id, key, description, ciphertext, created_by_user_id)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (organization_id, key)
		DO UPDATE SET ciphertext = EXCLUDED.ciphertext, description = EXCLUDED.description, updated_at = now()
		RETURNING id, key, description, created_at, updated_at
	`, ac.OrganizationID, key, description, ciphertext, actorUserID(ac)).Scan(
		&m.ID, &m.Key, &m.Description, &m.CreatedAt, &m.UpdatedAt,
	)
	if err != nil {
		return Meta{}, apierr.Wrap(apierr.CodeInternal, "failed to store secret", err)
	}

	// Metadata only — the value itself is never written to the audit log.
	if err := s.audit.Record(ctx, ac, audit.Entry{
		Action: "secrets.secret.set", ResourceType: "secret", ResourceID: m.ID.String(),
		Success: true, ResultingState: m,
	}); err != nil {
		logger.FromContext(ctx).Error("failed to write audit entry", "error", err)
	}

	return m, nil
}

// UpdateDescription changes a secret's description metadata without
// touching its encrypted value — Set always requires resupplying the
// plaintext even to fix a typo in the description, which either forces a
// real rotation the caller didn't intend or means the description just
// never gets corrected. This never re-encrypts anything and never appears
// anywhere near the plaintext.
func (s *Service) UpdateDescription(ctx context.Context, ac authctx.AuthContext, key, description string) (Meta, error) {
	if err := rbac.Require(ac, permManage); err != nil {
		return Meta{}, err
	}
	if key == "" {
		return Meta{}, apierr.Validation("secret key is required")
	}

	var m Meta
	err := s.pool.QueryRow(ctx, `
		UPDATE secrets SET description = $1, updated_at = now()
		WHERE organization_id = $2 AND key = $3
		RETURNING id, key, description, created_at, updated_at
	`, description, ac.OrganizationID, key).Scan(&m.ID, &m.Key, &m.Description, &m.CreatedAt, &m.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Meta{}, apierr.NotFound("secret")
	}
	if err != nil {
		return Meta{}, apierr.Wrap(apierr.CodeInternal, "failed to update secret description", err)
	}

	if err := s.audit.Record(ctx, ac, audit.Entry{
		Action: "secrets.secret.description_updated", ResourceType: "secret", ResourceID: m.ID.String(),
		Success: true, ResultingState: m,
	}); err != nil {
		logger.FromContext(ctx).Error("failed to write audit entry", "error", err)
	}

	return m, nil
}

func (s *Service) List(ctx context.Context, ac authctx.AuthContext) ([]Meta, error) {
	if err := rbac.Require(ac, permRead); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, key, description, created_at, updated_at
		FROM secrets WHERE organization_id = $1 ORDER BY key ASC
	`, ac.OrganizationID)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeInternal, "failed to list secrets", err)
	}
	defer rows.Close()

	var out []Meta
	for rows.Next() {
		var m Meta
		if err := rows.Scan(&m.ID, &m.Key, &m.Description, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, apierr.Wrap(apierr.CodeInternal, "failed to scan secret", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Service) Delete(ctx context.Context, ac authctx.AuthContext, key string) error {
	if err := rbac.Require(ac, permManage); err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM secrets WHERE organization_id = $1 AND key = $2`, ac.OrganizationID, key)
	if err != nil {
		return apierr.Wrap(apierr.CodeInternal, "failed to delete secret", err)
	}
	if tag.RowsAffected() == 0 {
		return apierr.NotFound("secret")
	}
	if err := s.audit.Record(ctx, ac, audit.Entry{
		Action: "secrets.secret.deleted", ResourceType: "secret", ResourceID: key, Success: true,
	}); err != nil {
		logger.FromContext(ctx).Error("failed to write audit entry", "error", err)
	}
	return nil
}

// Reveal decrypts and returns a secret's plaintext value. It is
// intentionally not wired to any HTTP handler — only internal Go callers
// (e.g. an AI provider adapter resolving its own credential at call time)
// may use it. It still goes through the same permission check as Set,
// since read access to metadata (secrets.read) should not imply the
// ability to recover a plaintext credential.
func (s *Service) Reveal(ctx context.Context, ac authctx.AuthContext, key string) (string, error) {
	if err := rbac.Require(ac, permManage); err != nil {
		return "", err
	}
	var ciphertext []byte
	err := s.pool.QueryRow(ctx, `
		SELECT ciphertext FROM secrets WHERE organization_id = $1 AND key = $2
	`, ac.OrganizationID, key).Scan(&ciphertext)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", apierr.NotFound("secret")
	}
	if err != nil {
		return "", apierr.Wrap(apierr.CodeInternal, "failed to load secret", err)
	}
	value, err := s.decrypt(ciphertext)
	if err != nil {
		return "", apierr.Wrap(apierr.CodeInternal, "failed to decrypt secret", err)
	}
	return value, nil
}

// --- Platform-scoped secrets ---
//
// Same shape and encryption as the organization-scoped methods above —
// AES-256-GCM via the same cipher.AEAD, "metadata over HTTP, value only
// in-process via Reveal" — over platform_secrets (migration 0020)
// instead of secrets, with no organization_id to scope by. Gated by
// internal/platformauth (platform.secrets.manage/read), never
// internal/rbac: a platform secret is not owned by any one organization.
//
// Deliberate deferral (docs/SECURITY.md "Platform secrets"): this pass
// implements the platform-secret abstraction/schema/service and its full
// CRUD + Reveal, but does NOT yet migrate the AI provider adapters
// (internal/ai/providers/anthropic, openai) to resolve their API key
// from here — they still read NODERA_ANTHROPIC_API_KEY/
// NODERA_OPENAI_API_KEY at startup (cmd/server/main.go). Hot-swapping a
// running adapter's credential when a platform secret changes is a
// real architectural change (adapters are constructed once at process
// start) that risks destabilizing the AI Gateway if rushed; the
// documented, deliberate choice here is to ship the safe, tested,
// reference-based storage primitive now and wire adapters to it in a
// dedicated follow-up, rather than force that larger change into this
// pass.

// SetPlatform creates or updates (upserts by key) a platform secret's
// value. The plaintext never appears in the returned Meta, logs, or the
// audit entry this writes.
func (s *Service) SetPlatform(ctx context.Context, ac authctx.AuthContext, key, value, description string) (Meta, error) {
	if err := s.requirePlatform(ctx, ac, permPlatformSecretsManage); err != nil {
		return Meta{}, err
	}
	if key == "" {
		return Meta{}, apierr.Validation("secret key is required")
	}
	if value == "" {
		return Meta{}, apierr.Validation("secret value is required")
	}

	ciphertext, err := s.encrypt(value)
	if err != nil {
		return Meta{}, apierr.Wrap(apierr.CodeInternal, "failed to encrypt secret", err)
	}

	var m Meta
	err = s.pool.QueryRow(ctx, `
		INSERT INTO platform_secrets (key, description, ciphertext, created_by_user_id)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (key)
		DO UPDATE SET ciphertext = EXCLUDED.ciphertext, description = EXCLUDED.description, updated_at = now()
		RETURNING id, key, description, created_at, updated_at
	`, key, description, ciphertext, actorUserID(ac)).Scan(
		&m.ID, &m.Key, &m.Description, &m.CreatedAt, &m.UpdatedAt,
	)
	if err != nil {
		return Meta{}, apierr.Wrap(apierr.CodeInternal, "failed to store platform secret", err)
	}

	if err := s.audit.Record(ctx, ac, audit.Entry{
		Action: "platform.secret.set", ResourceType: "platform_secret", ResourceID: m.ID.String(),
		Success: true, ResultingState: m,
	}); err != nil {
		logger.FromContext(ctx).Error("failed to write audit entry", "error", err)
	}

	return m, nil
}

// UpdateDescriptionPlatform is UpdateDescription's platform-scope
// counterpart.
func (s *Service) UpdateDescriptionPlatform(ctx context.Context, ac authctx.AuthContext, key, description string) (Meta, error) {
	if err := s.requirePlatform(ctx, ac, permPlatformSecretsManage); err != nil {
		return Meta{}, err
	}
	if key == "" {
		return Meta{}, apierr.Validation("secret key is required")
	}

	var m Meta
	err := s.pool.QueryRow(ctx, `
		UPDATE platform_secrets SET description = $1, updated_at = now()
		WHERE key = $2
		RETURNING id, key, description, created_at, updated_at
	`, description, key).Scan(&m.ID, &m.Key, &m.Description, &m.CreatedAt, &m.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Meta{}, apierr.NotFound("platform secret")
	}
	if err != nil {
		return Meta{}, apierr.Wrap(apierr.CodeInternal, "failed to update platform secret description", err)
	}

	if err := s.audit.Record(ctx, ac, audit.Entry{
		Action: "platform.secret.description_updated", ResourceType: "platform_secret", ResourceID: m.ID.String(),
		Success: true, ResultingState: m,
	}); err != nil {
		logger.FromContext(ctx).Error("failed to write audit entry", "error", err)
	}

	return m, nil
}

// ListPlatform is List's platform-scope counterpart — gated by
// platform.secrets.read, distinct from platform.secrets.manage (the same
// read/manage split the organization-scoped secrets.read/secrets.manage
// permissions already draw).
func (s *Service) ListPlatform(ctx context.Context, ac authctx.AuthContext) ([]Meta, error) {
	if err := s.requirePlatform(ctx, ac, permPlatformSecretsRead); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, key, description, created_at, updated_at FROM platform_secrets ORDER BY key ASC
	`)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeInternal, "failed to list platform secrets", err)
	}
	defer rows.Close()

	var out []Meta
	for rows.Next() {
		var m Meta
		if err := rows.Scan(&m.ID, &m.Key, &m.Description, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, apierr.Wrap(apierr.CodeInternal, "failed to scan platform secret", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// DeletePlatform is Delete's platform-scope counterpart.
func (s *Service) DeletePlatform(ctx context.Context, ac authctx.AuthContext, key string) error {
	if err := s.requirePlatform(ctx, ac, permPlatformSecretsManage); err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM platform_secrets WHERE key = $1`, key)
	if err != nil {
		return apierr.Wrap(apierr.CodeInternal, "failed to delete platform secret", err)
	}
	if tag.RowsAffected() == 0 {
		return apierr.NotFound("platform secret")
	}
	if err := s.audit.Record(ctx, ac, audit.Entry{
		Action: "platform.secret.deleted", ResourceType: "platform_secret", ResourceID: key, Success: true,
	}); err != nil {
		logger.FromContext(ctx).Error("failed to write audit entry", "error", err)
	}
	return nil
}

// RevealPlatform decrypts and returns a platform secret's plaintext
// value — like Reveal, intentionally not wired to any HTTP handler; only
// internal Go callers (a future AI provider adapter resolving its own
// credential) may use it.
func (s *Service) RevealPlatform(ctx context.Context, ac authctx.AuthContext, key string) (string, error) {
	if err := s.requirePlatform(ctx, ac, permPlatformSecretsManage); err != nil {
		return "", err
	}
	var ciphertext []byte
	err := s.pool.QueryRow(ctx, `SELECT ciphertext FROM platform_secrets WHERE key = $1`, key).Scan(&ciphertext)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", apierr.NotFound("platform secret")
	}
	if err != nil {
		return "", apierr.Wrap(apierr.CodeInternal, "failed to load platform secret", err)
	}
	value, err := s.decrypt(ciphertext)
	if err != nil {
		return "", apierr.Wrap(apierr.CodeInternal, "failed to decrypt platform secret", err)
	}
	return value, nil
}

// actorUserID returns a pointer to the actor's ID when they're a human
// user, or nil (stored as SQL NULL) otherwise — created_by_user_id has no
// meaning for a system or service-account actor.
func actorUserID(ac authctx.AuthContext) *uuid.UUID {
	if ac.ActorType != authctx.ActorUser {
		return nil
	}
	id := ac.ActorID
	return &id
}
