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

type Service struct {
	pool  *pgxpool.Pool
	audit AuditRecorder
	gcm   cipher.AEAD
}

// New constructs the secrets service. encryptionKeyBase64 must decode to
// exactly 32 bytes (AES-256). Callers (cmd/server) treat a missing or
// invalid key as "the secrets module is not configured" (rule 36: report
// unavailable, never fabricate) rather than crashing the whole process —
// see the wiring note in cmd/server/main.go.
func New(pool *pgxpool.Pool, audit AuditRecorder, encryptionKeyBase64 string) (*Service, error) {
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
	return &Service{pool: pool, audit: audit, gcm: gcm}, nil
}

const (
	permManage = "secrets.manage"
	permRead   = "secrets.read"
)

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
