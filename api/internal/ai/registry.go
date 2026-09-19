package ai

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

// ProviderInfo/ModelInfo and the Upsert*/List* methods below manage the
// platform-wide (not organization-scoped — see migration 0006_ai.sql)
// provider/model registry. They exist so an operator can register a real
// provider like Ollama through the API instead of only via a migration
// seed (which is how the local-echo test provider is registered).
//
// Because ai_providers/ai_models carry no organization_id, the mutating
// methods (UpsertProvider/DeleteProvider/UpsertModel/DeleteModel) are
// gated by internal/platformauth's platform.ai.providers.manage /
// platform.ai.models.manage — not by ai.manage, which is organization-
// scoped and would otherwise let any organization admin mutate global
// state merely by administering their own organization. List* stays
// gated by the ordinary organization permission ai.use: reading the
// registry to configure an org's own AI profile is not a platform-admin
// operation.
type ProviderInfo struct {
	ID          uuid.UUID `json:"id"`
	Key         string    `json:"key"`
	Kind        string    `json:"kind"` // "cloud" | "local"
	DisplayName string    `json:"display_name"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type UpsertProviderInput struct {
	Key         string `json:"key"`
	Kind        string `json:"kind"`
	DisplayName string `json:"display_name"`
	Status      string `json:"status"`
}

var validProviderKinds = map[string]bool{"cloud": true, "local": true}
var validProviderStatuses = map[string]bool{"unconfigured": true, "active": true, "disabled": true, "unavailable": true}

// UpsertProvider creates or updates a provider registry row (upsert by
// key). It does not register a Go adapter for it — that only happens at
// process startup (cmd/server/main.go passes registered adapters to
// ai.New) — so a provider can exist in the registry with no adapter
// wired, in which case the router correctly treats it as unavailable
// rather than fabricating a call (rule 36).
func (s *Service) UpsertProvider(ctx context.Context, ac authctx.AuthContext, in UpsertProviderInput) (ProviderInfo, error) {
	if err := s.platform.Require(ctx, ac, permPlatformAIProvidersManage); err != nil {
		return ProviderInfo{}, err
	}
	if in.Key == "" {
		return ProviderInfo{}, apierr.Validation("provider key is required")
	}
	if !validProviderKinds[in.Kind] {
		return ProviderInfo{}, apierr.Validation("kind must be 'cloud' or 'local'")
	}
	if in.Status == "" {
		in.Status = "unconfigured"
	}
	if !validProviderStatuses[in.Status] {
		return ProviderInfo{}, apierr.Validation("status must be one of unconfigured, active, disabled, unavailable")
	}
	if in.DisplayName == "" {
		in.DisplayName = in.Key
	}

	var p ProviderInfo
	err := s.pool.QueryRow(ctx, `
		INSERT INTO ai_providers (key, kind, display_name, status)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (key) DO UPDATE SET
			kind = EXCLUDED.kind, display_name = EXCLUDED.display_name,
			status = EXCLUDED.status, updated_at = now()
		RETURNING id, key, kind, display_name, status, created_at, updated_at
	`, in.Key, in.Kind, in.DisplayName, in.Status).Scan(
		&p.ID, &p.Key, &p.Kind, &p.DisplayName, &p.Status, &p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		return ProviderInfo{}, apierr.Wrap(apierr.CodeInternal, "failed to upsert AI provider", err)
	}

	if err := s.audit.Record(ctx, ac, audit.Entry{
		Action: "ai.provider.upserted", ResourceType: "ai_provider", ResourceID: p.ID.String(),
		Success: true, ResultingState: p,
	}); err != nil {
		logger.FromContext(ctx).Error("failed to write audit entry", "error", err)
	}

	return p, nil
}

// DeleteProvider permanently removes a provider registry row — for
// cleaning up a mis-registered key (Upsert can correct every other field,
// but not the key itself) or one that will never be used. ai_models.
// provider_id is ON DELETE CASCADE (migration 0006), so this also removes
// every model registered under it; ai_profiles.preferred_model_ids/
// fallback_model_ids are free-form "provider_key/model_identifier" text
// with no FK, so a profile referencing a deleted provider fails closed at
// resolve time (the router already handles an unresolvable reference this
// way) rather than via a cascading delete or a dangling FK.
func (s *Service) DeleteProvider(ctx context.Context, ac authctx.AuthContext, key string) error {
	if err := s.platform.Require(ctx, ac, permPlatformAIProvidersManage); err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM ai_providers WHERE key = $1`, key)
	if err != nil {
		return apierr.Wrap(apierr.CodeInternal, "failed to delete AI provider", err)
	}
	if tag.RowsAffected() == 0 {
		return apierr.NotFound("AI provider")
	}
	if err := s.audit.Record(ctx, ac, audit.Entry{
		Action: "ai.provider.deleted", ResourceType: "ai_provider", ResourceID: key, Success: true,
	}); err != nil {
		logger.FromContext(ctx).Error("failed to write audit entry", "error", err)
	}
	return nil
}

func (s *Service) ListProviders(ctx context.Context, ac authctx.AuthContext) ([]ProviderInfo, error) {
	if err := rbac.Require(ac, permUse); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, key, kind, display_name, status, created_at, updated_at
		FROM ai_providers ORDER BY key ASC
	`)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeInternal, "failed to list AI providers", err)
	}
	defer rows.Close()

	var out []ProviderInfo
	for rows.Next() {
		var p ProviderInfo
		if err := rows.Scan(&p.ID, &p.Key, &p.Kind, &p.DisplayName, &p.Status, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, apierr.Wrap(apierr.CodeInternal, "failed to scan AI provider", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

type ModelInfo struct {
	ID              uuid.UUID `json:"id"`
	ProviderKey     string    `json:"provider_key"`
	ModelIdentifier string    `json:"model_identifier"`
	DisplayName     string    `json:"display_name"`
	Capabilities    []string  `json:"capabilities"`
	ContextWindow   int       `json:"context_window"`
	Status          string    `json:"status"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type UpsertModelInput struct {
	ProviderKey     string   `json:"provider_key"`
	ModelIdentifier string   `json:"model_identifier"`
	DisplayName     string   `json:"display_name"`
	Capabilities    []string `json:"capabilities"`
	ContextWindow   int      `json:"context_window"`
	Status          string   `json:"status"`
}

var validModelStatuses = map[string]bool{"available": true, "unavailable": true, "deprecated": true}

func (s *Service) UpsertModel(ctx context.Context, ac authctx.AuthContext, in UpsertModelInput) (ModelInfo, error) {
	if err := s.platform.Require(ctx, ac, permPlatformAIModelsManage); err != nil {
		return ModelInfo{}, err
	}
	if in.ProviderKey == "" || in.ModelIdentifier == "" {
		return ModelInfo{}, apierr.Validation("provider_key and model_identifier are required")
	}
	if in.Status == "" {
		in.Status = "available"
	}
	if !validModelStatuses[in.Status] {
		return ModelInfo{}, apierr.Validation("status must be one of available, unavailable, deprecated")
	}
	if in.DisplayName == "" {
		in.DisplayName = in.ModelIdentifier
	}
	if in.Capabilities == nil {
		in.Capabilities = []string{}
	}

	var providerID uuid.UUID
	err := s.pool.QueryRow(ctx, `SELECT id FROM ai_providers WHERE key = $1`, in.ProviderKey).Scan(&providerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ModelInfo{}, apierr.Validation("no AI provider registered with key '" + in.ProviderKey + "' — register it first via POST /api/v1/ai/providers")
	}
	if err != nil {
		return ModelInfo{}, apierr.Wrap(apierr.CodeInternal, "failed to look up provider", err)
	}

	var m ModelInfo
	err = s.pool.QueryRow(ctx, `
		INSERT INTO ai_models (provider_id, model_identifier, display_name, capabilities, context_window, status)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (provider_id, model_identifier) DO UPDATE SET
			display_name = EXCLUDED.display_name, capabilities = EXCLUDED.capabilities,
			context_window = EXCLUDED.context_window, status = EXCLUDED.status, updated_at = now()
		RETURNING id, model_identifier, display_name, capabilities, COALESCE(context_window, 0), status, created_at, updated_at
	`, providerID, in.ModelIdentifier, in.DisplayName, in.Capabilities, nullableInt(in.ContextWindow), in.Status).Scan(
		&m.ID, &m.ModelIdentifier, &m.DisplayName, &m.Capabilities, &m.ContextWindow, &m.Status, &m.CreatedAt, &m.UpdatedAt,
	)
	if err != nil {
		return ModelInfo{}, apierr.Wrap(apierr.CodeInternal, "failed to upsert AI model", err)
	}
	m.ProviderKey = in.ProviderKey

	if err := s.audit.Record(ctx, ac, audit.Entry{
		Action: "ai.model.upserted", ResourceType: "ai_model", ResourceID: m.ID.String(),
		Success: true, ResultingState: m,
	}); err != nil {
		logger.FromContext(ctx).Error("failed to write audit entry", "error", err)
	}

	return m, nil
}

// DeleteModel permanently removes a single model registry row — the same
// cleanup path DeleteProvider offers for a provider, scoped to one model
// rather than everything under a provider.
func (s *Service) DeleteModel(ctx context.Context, ac authctx.AuthContext, providerKey, modelIdentifier string) error {
	if err := s.platform.Require(ctx, ac, permPlatformAIModelsManage); err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM ai_models
		WHERE model_identifier = $2
		  AND provider_id = (SELECT id FROM ai_providers WHERE key = $1)
	`, providerKey, modelIdentifier)
	if err != nil {
		return apierr.Wrap(apierr.CodeInternal, "failed to delete AI model", err)
	}
	if tag.RowsAffected() == 0 {
		return apierr.NotFound("AI model")
	}
	if err := s.audit.Record(ctx, ac, audit.Entry{
		Action: "ai.model.deleted", ResourceType: "ai_model", ResourceID: providerKey + "/" + modelIdentifier, Success: true,
	}); err != nil {
		logger.FromContext(ctx).Error("failed to write audit entry", "error", err)
	}
	return nil
}

func (s *Service) ListModels(ctx context.Context, ac authctx.AuthContext) ([]ModelInfo, error) {
	if err := rbac.Require(ac, permUse); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT m.id, p.key, m.model_identifier, m.display_name, m.capabilities,
		       COALESCE(m.context_window, 0), m.status, m.created_at, m.updated_at
		FROM ai_models m JOIN ai_providers p ON p.id = m.provider_id
		ORDER BY p.key ASC, m.model_identifier ASC
	`)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeInternal, "failed to list AI models", err)
	}
	defer rows.Close()

	var out []ModelInfo
	for rows.Next() {
		var m ModelInfo
		if err := rows.Scan(&m.ID, &m.ProviderKey, &m.ModelIdentifier, &m.DisplayName, &m.Capabilities,
			&m.ContextWindow, &m.Status, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, apierr.Wrap(apierr.CodeInternal, "failed to scan AI model", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func nullableInt(n int) *int {
	if n <= 0 {
		return nil
	}
	return &n
}
