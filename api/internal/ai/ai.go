// Package ai implements the AI Gateway foundation (sections 9-15): AI
// profiles, a deterministic router, and usage tracking, composed on top of
// the providers.Provider interface. See docs/AI_ARCHITECTURE.md.
//
// Model references in AI profiles use the human-readable form
// "<provider_key>/<model_identifier>" (e.g. "local-echo/echo-1") rather than
// a model UUID, so a profile can be authored without first looking one up.
package ai

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nodera/nodera/internal/ai/providers"
	"github.com/nodera/nodera/internal/audit"
	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/platform/authctx"
	"github.com/nodera/nodera/internal/platform/logger"
	"github.com/nodera/nodera/internal/rbac"
)

type AuditRecorder interface {
	Record(ctx context.Context, ac authctx.AuthContext, e audit.Entry) error
}

// Service composes the AI profile registry, the deterministic router, and
// usage tracking. Provider adapters are registered at process wiring time
// (cmd/server/main.go) — the Service never imports a vendor SDK directly.
type Service struct {
	pool      *pgxpool.Pool
	audit     AuditRecorder
	providers map[string]providers.Provider // keyed by ai_providers.key
}

func New(pool *pgxpool.Pool, audit AuditRecorder, registeredProviders ...providers.Provider) *Service {
	m := make(map[string]providers.Provider, len(registeredProviders))
	for _, p := range registeredProviders {
		m[p.Key()] = p
	}
	return &Service{pool: pool, audit: audit, providers: m}
}

const (
	permManage = "ai.manage"
	permUse    = "ai.use"
)

// --- Profiles ---

type Profile struct {
	ID                   uuid.UUID `json:"id"`
	Key                  string    `json:"key"`
	Description          string    `json:"description"`
	RequiredCapabilities []string  `json:"required_capabilities"`
	PrivacyLevel         string    `json:"privacy_level"`
	PreferredModelRefs   []string  `json:"preferred_model_ids"`
	FallbackModelRefs    []string  `json:"fallback_model_ids"`
	Temperature          float64   `json:"temperature"`
	MaxTokens            int       `json:"max_tokens"`
	TimeoutSeconds       int       `json:"timeout_seconds"`
	CreatedAt            time.Time `json:"created_at"`
}

type CreateProfileInput struct {
	Key                  string   `json:"key"`
	Description          string   `json:"description"`
	RequiredCapabilities []string `json:"required_capabilities"`
	PrivacyLevel         string   `json:"privacy_level"`
	PreferredModelRefs   []string `json:"preferred_model_ids"`
	FallbackModelRefs    []string `json:"fallback_model_ids"`
	Temperature          float64  `json:"temperature"`
	MaxTokens            int      `json:"max_tokens"`
	TimeoutSeconds       int      `json:"timeout_seconds"`
}

var validPrivacyLevels = map[string]bool{
	"public": true, "internal": true, "confidential": true, "restricted": true,
}

func (s *Service) CreateProfile(ctx context.Context, ac authctx.AuthContext, in CreateProfileInput) (Profile, error) {
	if err := rbac.Require(ac, permManage); err != nil {
		return Profile{}, err
	}
	if in.Key == "" {
		return Profile{}, apierr.Validation("profile key is required")
	}
	if in.PrivacyLevel == "" {
		in.PrivacyLevel = "internal"
	}
	if !validPrivacyLevels[in.PrivacyLevel] {
		return Profile{}, apierr.Validation("privacy_level must be one of public, internal, confidential, restricted")
	}
	if len(in.PreferredModelRefs) == 0 {
		return Profile{}, apierr.Validation("at least one preferred model reference is required, formatted 'provider_key/model_identifier'")
	}
	for _, ref := range append(append([]string{}, in.PreferredModelRefs...), in.FallbackModelRefs...) {
		if !strings.Contains(ref, "/") {
			return Profile{}, apierr.Validation("model reference '" + ref + "' must be formatted 'provider_key/model_identifier'")
		}
	}
	if in.Temperature < 0 || in.Temperature > 2 {
		return Profile{}, apierr.Validation("temperature must be between 0 and 2")
	}
	if in.MaxTokens <= 0 {
		in.MaxTokens = 2048
	}
	if in.TimeoutSeconds <= 0 {
		in.TimeoutSeconds = 60
	}
	if in.RequiredCapabilities == nil {
		in.RequiredCapabilities = []string{}
	}
	if in.FallbackModelRefs == nil {
		in.FallbackModelRefs = []string{}
	}

	var p Profile
	err := s.pool.QueryRow(ctx, `
		INSERT INTO ai_profiles (organization_id, key, description, required_capabilities, privacy_level,
		                          preferred_model_ids, fallback_model_ids, temperature, max_tokens, timeout_seconds)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING id, key, description, required_capabilities, privacy_level,
		          preferred_model_ids, fallback_model_ids, temperature, max_tokens, timeout_seconds, created_at
	`, ac.OrganizationID, in.Key, in.Description, in.RequiredCapabilities, in.PrivacyLevel,
		in.PreferredModelRefs, in.FallbackModelRefs, in.Temperature, in.MaxTokens, in.TimeoutSeconds).Scan(
		&p.ID, &p.Key, &p.Description, &p.RequiredCapabilities, &p.PrivacyLevel,
		&p.PreferredModelRefs, &p.FallbackModelRefs, &p.Temperature, &p.MaxTokens, &p.TimeoutSeconds, &p.CreatedAt,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return Profile{}, apierr.Conflict("an AI profile with this key already exists in this organization")
		}
		return Profile{}, apierr.Wrap(apierr.CodeInternal, "failed to create AI profile", err)
	}

	if err := s.audit.Record(ctx, ac, audit.Entry{
		Action: "ai.profile.created", ResourceType: "ai_profile", ResourceID: p.ID.String(),
		Success: true, ResultingState: p,
	}); err != nil {
		logger.FromContext(ctx).Error("failed to write audit entry", "error", err)
	}

	return p, nil
}

func (s *Service) ListProfiles(ctx context.Context, ac authctx.AuthContext) ([]Profile, error) {
	if err := rbac.Require(ac, permUse); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, key, description, required_capabilities, privacy_level,
		       preferred_model_ids, fallback_model_ids, temperature, max_tokens, timeout_seconds, created_at
		FROM ai_profiles
		WHERE organization_id = $1 OR organization_id IS NULL
		ORDER BY key ASC
	`, ac.OrganizationID)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeInternal, "failed to list AI profiles", err)
	}
	defer rows.Close()

	var out []Profile
	for rows.Next() {
		var p Profile
		if err := rows.Scan(&p.ID, &p.Key, &p.Description, &p.RequiredCapabilities, &p.PrivacyLevel,
			&p.PreferredModelRefs, &p.FallbackModelRefs, &p.Temperature, &p.MaxTokens, &p.TimeoutSeconds, &p.CreatedAt); err != nil {
			return nil, apierr.Wrap(apierr.CodeInternal, "failed to scan AI profile", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// UpdateProfileInput follows the pointer-based partial-update convention
// used across the codebase: a nil scalar field leaves the existing value
// untouched, a non-nil one replaces it. The two model-reference slices are
// plain []string — nil leaves them untouched, an explicit empty slice
// clears them. Key is intentionally not editable: it's the stable handle
// agents and callers reference a profile by, and renaming it out from
// under existing references would silently break them (there's no FK to
// catch it, matching agents.ai_profile_key's own free-form design).
type UpdateProfileInput struct {
	Description          *string  `json:"description"`
	RequiredCapabilities []string `json:"required_capabilities"`
	PrivacyLevel         *string  `json:"privacy_level"`
	PreferredModelRefs   []string `json:"preferred_model_ids"`
	FallbackModelRefs    []string `json:"fallback_model_ids"`
	Temperature          *float64 `json:"temperature"`
	MaxTokens            *int     `json:"max_tokens"`
	TimeoutSeconds       *int     `json:"timeout_seconds"`
}

func (s *Service) UpdateProfile(ctx context.Context, ac authctx.AuthContext, id uuid.UUID, in UpdateProfileInput) (Profile, error) {
	if err := rbac.Require(ac, permManage); err != nil {
		return Profile{}, err
	}
	existing, err := s.getProfileByID(ctx, ac.OrganizationID, id)
	if err != nil {
		return Profile{}, err
	}

	description := existing.Description
	if in.Description != nil {
		description = *in.Description
	}
	requiredCapabilities := existing.RequiredCapabilities
	if in.RequiredCapabilities != nil {
		requiredCapabilities = in.RequiredCapabilities
	}
	privacyLevel := existing.PrivacyLevel
	if in.PrivacyLevel != nil {
		if !validPrivacyLevels[*in.PrivacyLevel] {
			return Profile{}, apierr.Validation("privacy_level must be one of public, internal, confidential, restricted")
		}
		privacyLevel = *in.PrivacyLevel
	}
	preferredModelRefs := existing.PreferredModelRefs
	if in.PreferredModelRefs != nil {
		if len(in.PreferredModelRefs) == 0 {
			return Profile{}, apierr.Validation("at least one preferred model reference is required, formatted 'provider_key/model_identifier'")
		}
		preferredModelRefs = in.PreferredModelRefs
	}
	fallbackModelRefs := existing.FallbackModelRefs
	if in.FallbackModelRefs != nil {
		fallbackModelRefs = in.FallbackModelRefs
	}
	for _, ref := range append(append([]string{}, preferredModelRefs...), fallbackModelRefs...) {
		if !strings.Contains(ref, "/") {
			return Profile{}, apierr.Validation("model reference '" + ref + "' must be formatted 'provider_key/model_identifier'")
		}
	}
	temperature := existing.Temperature
	if in.Temperature != nil {
		if *in.Temperature < 0 || *in.Temperature > 2 {
			return Profile{}, apierr.Validation("temperature must be between 0 and 2")
		}
		temperature = *in.Temperature
	}
	maxTokens := existing.MaxTokens
	if in.MaxTokens != nil {
		if *in.MaxTokens <= 0 {
			return Profile{}, apierr.Validation("max_tokens must be positive")
		}
		maxTokens = *in.MaxTokens
	}
	timeoutSeconds := existing.TimeoutSeconds
	if in.TimeoutSeconds != nil {
		if *in.TimeoutSeconds <= 0 {
			return Profile{}, apierr.Validation("timeout_seconds must be positive")
		}
		timeoutSeconds = *in.TimeoutSeconds
	}

	var p Profile
	err = s.pool.QueryRow(ctx, `
		UPDATE ai_profiles
		SET description = $1, required_capabilities = $2, privacy_level = $3,
		    preferred_model_ids = $4, fallback_model_ids = $5, temperature = $6,
		    max_tokens = $7, timeout_seconds = $8, updated_at = now()
		WHERE id = $9 AND organization_id = $10
		RETURNING id, key, description, required_capabilities, privacy_level,
		          preferred_model_ids, fallback_model_ids, temperature, max_tokens, timeout_seconds, created_at
	`, description, requiredCapabilities, privacyLevel, preferredModelRefs, fallbackModelRefs,
		temperature, maxTokens, timeoutSeconds, id, ac.OrganizationID).Scan(
		&p.ID, &p.Key, &p.Description, &p.RequiredCapabilities, &p.PrivacyLevel,
		&p.PreferredModelRefs, &p.FallbackModelRefs, &p.Temperature, &p.MaxTokens, &p.TimeoutSeconds, &p.CreatedAt,
	)
	if err != nil {
		return Profile{}, apierr.Wrap(apierr.CodeInternal, "failed to update AI profile", err)
	}

	if err := s.audit.Record(ctx, ac, audit.Entry{
		Action: "ai.profile.updated", ResourceType: "ai_profile", ResourceID: p.ID.String(),
		Success: true, PreviousState: existing, ResultingState: p,
	}); err != nil {
		logger.FromContext(ctx).Error("failed to write audit entry", "error", err)
	}

	return p, nil
}

// DeleteProfile permanently removes an org-owned AI profile. There is no FK
// from agents.ai_profile_key or ai_usage_records.profile_key back to this
// row (both are free-form text, resolved by key at call time, matching the
// router's existing fail-closed behavior for an unresolvable reference) —
// deleting a profile still in use surfaces as a clear NotFound the next
// time something tries to resolve it, rather than a cascading delete or an
// orphaned FK.
func (s *Service) DeleteProfile(ctx context.Context, ac authctx.AuthContext, id uuid.UUID) error {
	if err := rbac.Require(ac, permManage); err != nil {
		return err
	}
	existing, err := s.getProfileByID(ctx, ac.OrganizationID, id)
	if err != nil {
		return err
	}

	tag, err := s.pool.Exec(ctx, `DELETE FROM ai_profiles WHERE id = $1 AND organization_id = $2`, id, ac.OrganizationID)
	if err != nil {
		return apierr.Wrap(apierr.CodeInternal, "failed to delete AI profile", err)
	}
	if tag.RowsAffected() == 0 {
		return apierr.NotFound("AI profile")
	}

	if err := s.audit.Record(ctx, ac, audit.Entry{
		Action: "ai.profile.deleted", ResourceType: "ai_profile", ResourceID: id.String(),
		Success: true, PreviousState: existing,
	}); err != nil {
		logger.FromContext(ctx).Error("failed to write audit entry", "error", err)
	}

	return nil
}

// getProfileByID loads an org-owned profile for Update/Delete. Unlike
// getProfileByKey (used by the router, which also considers system-defined
// organization_id IS NULL profiles), this only matches rows the calling
// org actually owns — a system-defined profile can't be edited or removed
// through an org-scoped ai.manage call.
func (s *Service) getProfileByID(ctx context.Context, orgID, id uuid.UUID) (Profile, error) {
	var p Profile
	err := s.pool.QueryRow(ctx, `
		SELECT id, key, description, required_capabilities, privacy_level,
		       preferred_model_ids, fallback_model_ids, temperature, max_tokens, timeout_seconds, created_at
		FROM ai_profiles
		WHERE id = $1 AND organization_id = $2
	`, id, orgID).Scan(&p.ID, &p.Key, &p.Description, &p.RequiredCapabilities, &p.PrivacyLevel,
		&p.PreferredModelRefs, &p.FallbackModelRefs, &p.Temperature, &p.MaxTokens, &p.TimeoutSeconds, &p.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Profile{}, apierr.NotFound("AI profile")
	}
	if err != nil {
		return Profile{}, apierr.Wrap(apierr.CodeInternal, "failed to load AI profile", err)
	}
	return p, nil
}

func (s *Service) getProfileByKey(ctx context.Context, orgID uuid.UUID, key string) (Profile, error) {
	var p Profile
	err := s.pool.QueryRow(ctx, `
		SELECT id, key, description, required_capabilities, privacy_level,
		       preferred_model_ids, fallback_model_ids, temperature, max_tokens, timeout_seconds, created_at
		FROM ai_profiles
		WHERE key = $1 AND (organization_id = $2 OR organization_id IS NULL)
		ORDER BY organization_id NULLS LAST
		LIMIT 1
	`, key, orgID).Scan(&p.ID, &p.Key, &p.Description, &p.RequiredCapabilities, &p.PrivacyLevel,
		&p.PreferredModelRefs, &p.FallbackModelRefs, &p.Temperature, &p.MaxTokens, &p.TimeoutSeconds, &p.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Profile{}, apierr.NotFound("AI profile")
	}
	if err != nil {
		return Profile{}, apierr.Wrap(apierr.CodeInternal, "failed to load AI profile", err)
	}
	return p, nil
}

// --- resolved model (router's output) ---

type resolvedModel struct {
	providerKey     string
	providerKind    string // "local" | "cloud"
	modelIdentifier string
}

// resolve implements Nodera's deterministic AI router (section 13): profile
// → privacy policy → first available model from preferred, then fallback,
// references. It is deterministic on purpose (section 13 explicitly asks
// for a deterministic router before any quality/cost/latency-based one).
func (s *Service) resolve(ctx context.Context, profile Profile) (resolvedModel, error) {
	for _, ref := range append(append([]string{}, profile.PreferredModelRefs...), profile.FallbackModelRefs...) {
		providerKey, modelIdentifier, ok := strings.Cut(ref, "/")
		if !ok {
			continue
		}

		var providerKind, providerStatus, modelStatus string
		err := s.pool.QueryRow(ctx, `
			SELECT p.kind, p.status, m.status
			FROM ai_models m
			JOIN ai_providers p ON p.id = m.provider_id
			WHERE p.key = $1 AND m.model_identifier = $2
		`, providerKey, modelIdentifier).Scan(&providerKind, &providerStatus, &modelStatus)
		if errors.Is(err, pgx.ErrNoRows) {
			continue // referenced model doesn't exist (yet) — try the next ref, never fabricate
		}
		if err != nil {
			return resolvedModel{}, apierr.Wrap(apierr.CodeInternal, "failed to resolve AI model reference", err)
		}

		if providerStatus != "active" || modelStatus != "available" {
			continue
		}
		// Privacy enforcement (section 14): RESTRICTED can never resolve to
		// a cloud provider, regardless of what the caller or profile asks
		// for elsewhere. This is enforced here, not left to the caller.
		if profile.PrivacyLevel == "restricted" && providerKind == "cloud" {
			continue
		}
		// The provider must actually have a registered Go adapter — a row
		// existing in ai_providers does not mean it's callable (rule 36).
		if _, ok := s.providers[providerKey]; !ok {
			continue
		}

		return resolvedModel{providerKey: providerKey, providerKind: providerKind, modelIdentifier: modelIdentifier}, nil
	}

	return resolvedModel{}, apierr.New(apierr.CodeUnavailable, "no available, policy-compliant model found for this AI profile")
}

// --- Chat ---

type ChatResult struct {
	Content      string `json:"content"`
	ProfileKey   string `json:"profile_key"`
	ProviderKey  string `json:"provider_key"`
	Model        string `json:"model"`
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
}

// Chat is the AI Gateway's single entry point (section 9): profile in,
// normalized response out. It never lets a caller pick a provider/model
// directly — that would bypass the privacy policy (section 14).
func (s *Service) Chat(ctx context.Context, ac authctx.AuthContext, profileKey string, messages []providers.Message) (ChatResult, error) {
	if err := rbac.Require(ac, permUse); err != nil {
		return ChatResult{}, err
	}
	if len(messages) == 0 {
		return ChatResult{}, apierr.Validation("at least one message is required")
	}

	profile, err := s.getProfileByKey(ctx, ac.OrganizationID, profileKey)
	if err != nil {
		return ChatResult{}, err
	}

	resolved, err := s.resolve(ctx, profile)
	if err != nil {
		s.recordUsage(ctx, ac, profile.Key, "", "", "error", 0, 0, 0, false)
		return ChatResult{}, err
	}

	provider := s.providers[resolved.providerKey]

	start := time.Now()
	resp, chatErr := provider.Chat(ctx, providers.ChatRequest{
		Model:       resolved.modelIdentifier,
		Messages:    messages,
		Temperature: profile.Temperature,
		MaxTokens:   profile.MaxTokens,
	})
	latencyMs := int(time.Since(start).Milliseconds())

	status := "success"
	if chatErr != nil {
		status = "error"
	}
	s.recordUsage(ctx, ac, profile.Key, resolved.providerKey, resolved.modelIdentifier, status,
		resp.InputTokens, resp.OutputTokens, latencyMs, resolved.providerKind == "local")

	if chatErr != nil {
		return ChatResult{}, apierr.Wrap(apierr.CodeUnavailable, "AI provider request failed", chatErr)
	}

	return ChatResult{
		Content:      resp.Content,
		ProfileKey:   profile.Key,
		ProviderKey:  resolved.providerKey,
		Model:        resolved.modelIdentifier,
		InputTokens:  resp.InputTokens,
		OutputTokens: resp.OutputTokens,
	}, nil
}

// recordUsage never fails the caller's request — a usage-tracking write
// failure is a monitoring problem, not a reason to fail an otherwise
// successful AI call (same tradeoff as audit.Service.Record).
func (s *Service) recordUsage(ctx context.Context, ac authctx.AuthContext, profileKey, providerKey, model, status string, inputTokens, outputTokens, latencyMs int, isLocal bool) {
	classification := "cloud"
	if isLocal {
		classification = "local"
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO ai_usage_records (organization_id, profile_key, provider_key, model_identifier,
		                               classification, input_tokens, output_tokens, total_tokens,
		                               latency_ms, status, correlation_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NULLIF($11, ''))
	`, ac.OrganizationID, profileKey, providerKey, model, classification,
		inputTokens, outputTokens, inputTokens+outputTokens, latencyMs, status, ac.CorrelationID)
	if err != nil {
		logger.FromContext(ctx).Error("failed to record AI usage", "error", err)
	}
}

// UsageRecord is one row from ai_usage_records — the operational metrics
// every Chat call already writes via recordUsage, but that (until now) had
// no way to ever be read back through the API. No prompt/response content
// is ever included (section 15) — only counts, timing, and outcome.
type UsageRecord struct {
	ID              uuid.UUID `json:"id"`
	ProfileKey      string    `json:"profile_key"`
	ProviderKey     string    `json:"provider_key"`
	ModelIdentifier string    `json:"model_identifier"`
	Classification  string    `json:"classification"` // "local" | "cloud"
	InputTokens     int       `json:"input_tokens"`
	OutputTokens    int       `json:"output_tokens"`
	TotalTokens     int       `json:"total_tokens"`
	LatencyMs       *int      `json:"latency_ms"`
	Status          string    `json:"status"` // "success" | "error" | "timeout"
	CorrelationID   string    `json:"correlation_id"`
	CreatedAt       time.Time `json:"created_at"`
}

// ListUsage returns the calling organization's own AI usage history, most
// recent first. Tenant-scoped like every other list method (ADR-004) —
// unlike the provider/model registry above, ai_usage_records does carry
// organization_id, so this needs no platform-wide-vs-tenant-scoped caveat.
// UsageFilter narrows a ListUsage call. ProfileKey/ProviderKey are
// optional, exact-match — an org running several AI profiles/providers
// (see docs/AI_ARCHITECTURE.md) couldn't otherwise isolate one profile's
// or provider's usage/cost without paging through every other one's rows
// first.
type UsageFilter struct {
	ProfileKey  string
	ProviderKey string
	Limit       int
	Offset      int
}

func (s *Service) ListUsage(ctx context.Context, ac authctx.AuthContext, f UsageFilter) ([]UsageRecord, error) {
	if err := rbac.Require(ac, permUse); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, profile_key, provider_key, model_identifier, classification,
		       input_tokens, output_tokens, total_tokens, latency_ms, status,
		       COALESCE(correlation_id, ''), created_at
		FROM ai_usage_records
		WHERE organization_id = $1
		  AND ($4 = '' OR profile_key = $4)
		  AND ($5 = '' OR provider_key = $5)
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`, ac.OrganizationID, f.Limit, f.Offset, f.ProfileKey, f.ProviderKey)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeInternal, "failed to list AI usage records", err)
	}
	defer rows.Close()

	var out []UsageRecord
	for rows.Next() {
		var u UsageRecord
		if err := rows.Scan(&u.ID, &u.ProfileKey, &u.ProviderKey, &u.ModelIdentifier, &u.Classification,
			&u.InputTokens, &u.OutputTokens, &u.TotalTokens, &u.LatencyMs, &u.Status,
			&u.CorrelationID, &u.CreatedAt); err != nil {
			return nil, apierr.Wrap(apierr.CodeInternal, "failed to scan AI usage record", err)
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func isUniqueViolation(err error) bool {
	var pgErr interface{ SQLState() string }
	if errors.As(err, &pgErr) {
		return pgErr.SQLState() == "23505"
	}
	return false
}
