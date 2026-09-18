package main

import (
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nodera/nodera/internal/ai"
	"github.com/nodera/nodera/internal/ai/providers"
	"github.com/nodera/nodera/internal/applications"
	"github.com/nodera/nodera/internal/audit"
	"github.com/nodera/nodera/internal/identity"
	"github.com/nodera/nodera/internal/infrastructure"
	"github.com/nodera/nodera/internal/jobs"
	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/platform/httpserver"
	"github.com/nodera/nodera/internal/platform/ratelimit"
	"github.com/nodera/nodera/internal/secrets"
	"github.com/nodera/nodera/internal/tenancy"
)

type apiDeps struct {
	log         *slog.Logger
	identity    *identity.Service
	tenancy     *tenancy.Service
	audit       *audit.Service
	infra       *infrastructure.Service
	apps        *applications.Service
	jobs        *jobs.Service
	ai          *ai.Service
	secrets     *secrets.Service // nil if NODERA_SECRETS_ENCRYPTION_KEY is not configured — see main.go
	pool        *pgxpool.Pool
	loginRate   *ratelimit.Limiter
	corsOrigins []string
}

func newRouter(d apiDeps) http.Handler {
	r := chi.NewRouter()
	r.Use(httpserver.CORS(d.corsOrigins))
	r.Use(httpserver.WithRequestID(d.log))
	r.Use(httpserver.Recover)
	r.Use(httpserver.SecurityHeaders)
	r.Use(httpserver.Logging)

	// Unauthenticated platform endpoints (rule 27).
	r.Get("/health", d.handleHealth)
	r.Get("/ready", d.handleReady)

	r.Route("/api/v1", func(r chi.Router) {
		r.Post("/auth/signup", d.handleSignUp)
		r.Post("/auth/login", d.handleLogin)

		r.Group(func(r chi.Router) {
			r.Use(d.requireSession)

			r.Post("/auth/logout", d.handleLogout)
			r.Get("/organizations", d.handleListOrganizations)
			r.Post("/organizations", d.handleCreateOrganization)

			r.Group(func(r chi.Router) {
				r.Use(d.requireOrganization)

				r.Get("/organization", d.handleGetOrganization)

				r.Get("/infrastructure/nodes", d.handleListNodes)
				r.Post("/infrastructure/nodes", d.handleRegisterNode)
				r.Get("/infrastructure/nodes/{id}", d.handleGetNode)

				r.Get("/applications", d.handleListApplications)
				r.Post("/applications", d.handleRegisterApplication)
				r.Get("/applications/{id}", d.handleGetApplication)

				r.Get("/api-tokens", d.handleListAPITokens)
				r.Post("/api-tokens", d.handleCreateAPIToken)
				r.Delete("/api-tokens/{id}", d.handleRevokeAPIToken)

				r.Get("/jobs", d.handleListJobs)
				r.Post("/jobs", d.handleEnqueueJob)
				r.Get("/jobs/{id}", d.handleGetJob)
				r.Post("/jobs/{id}/cancel", d.handleCancelJob)

				r.Get("/ai/profiles", d.handleListAIProfiles)
				r.Post("/ai/profiles", d.handleCreateAIProfile)
				r.Post("/ai/chat", d.handleAIChat)
				r.Get("/ai/providers", d.handleListAIProviders)
				r.Post("/ai/providers", d.handleUpsertAIProvider)
				r.Get("/ai/models", d.handleListAIModels)
				r.Post("/ai/models", d.handleUpsertAIModel)

				r.Get("/secrets", d.handleListSecrets)
				r.Put("/secrets/{key}", d.handleSetSecret)
				r.Delete("/secrets/{key}", d.handleDeleteSecret)

				r.Get("/audit", d.handleListAudit)
			})
		})
	})

	return r
}

func (d apiDeps) handleHealth(w http.ResponseWriter, r *http.Request) {
	httpserver.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleReady checks the database is actually reachable, distinct from
// /health which only proves the process is alive (rule 27).
func (d apiDeps) handleReady(w http.ResponseWriter, r *http.Request) {
	if err := d.pool.Ping(r.Context()); err != nil {
		httpserver.WriteJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable", "reason": "database unreachable"})
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

// --- auth ---

func (d apiDeps) handleSignUp(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email       string `json:"email"`
		Password    string `json:"password"`
		DisplayName string `json:"display_name"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	u, err := d.identity.SignUp(r.Context(), body.Email, body.Password, body.DisplayName)
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusCreated, map[string]any{
		"id": u.ID, "email": u.Email, "display_name": u.DisplayName,
	})
}

func (d apiDeps) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}

	// Rate limit by client IP — bounds brute-force attempts against any
	// single account without needing to know the account up front (an
	// email-only key would let an attacker exhaust a victim's budget as a
	// denial-of-service; see docs/SECURITY.md).
	if d.loginRate != nil && !d.loginRate.Allow(clientIP(r)) {
		httpserver.WriteError(w, r, apierr.New(apierr.CodeRateLimited, "too many login attempts, try again shortly"))
		return
	}

	token, u, err := d.identity.Login(r.Context(), body.Email, body.Password, clientIP(r), r.UserAgent())
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	orgs, err := d.tenancy.ListForUser(r.Context(), u.ID)
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{
		"session_token": token,
		"user":          map[string]any{"id": u.ID, "email": u.Email, "display_name": u.DisplayName},
		"organizations": orgs,
	})
}

func (d apiDeps) handleLogout(w http.ResponseWriter, r *http.Request) {
	token := bearerToken(r)
	if err := d.identity.Logout(r.Context(), token); err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- organizations ---

func (d apiDeps) handleListOrganizations(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)
	orgs, err := d.tenancy.ListForUser(r.Context(), userID)
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, orgs)
}

func (d apiDeps) handleCreateOrganization(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
		Slug string `json:"slug"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	org, err := d.tenancy.CreateOrganization(r.Context(), userIDFromRequest(r), body.Name, body.Slug)
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusCreated, org)
}

func (d apiDeps) handleGetOrganization(w http.ResponseWriter, r *http.Request) {
	ac := mustAuthContext(r)
	org, err := d.tenancy.Get(r.Context(), ac)
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, org)
}

// --- infrastructure ---

func (d apiDeps) handleListNodes(w http.ResponseWriter, r *http.Request) {
	nodes, err := d.infra.List(r.Context(), mustAuthContext(r))
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, nodes)
}

func (d apiDeps) handleRegisterNode(w http.ResponseWriter, r *http.Request) {
	var body infrastructure.RegisterNodeInput
	if !decodeJSON(w, r, &body) {
		return
	}
	n, err := d.infra.RegisterNode(r.Context(), mustAuthContext(r), body)
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusCreated, n)
}

func (d apiDeps) handleGetNode(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpserver.WriteError(w, r, apierr.Validation("invalid node id"))
		return
	}
	n, err := d.infra.Get(r.Context(), mustAuthContext(r), id)
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, n)
}

// --- applications ---

func (d apiDeps) handleListApplications(w http.ResponseWriter, r *http.Request) {
	apps, err := d.apps.List(r.Context(), mustAuthContext(r))
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, apps)
}

func (d apiDeps) handleRegisterApplication(w http.ResponseWriter, r *http.Request) {
	var body applications.RegisterInput
	if !decodeJSON(w, r, &body) {
		return
	}
	a, err := d.apps.Register(r.Context(), mustAuthContext(r), body)
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusCreated, a)
}

func (d apiDeps) handleGetApplication(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpserver.WriteError(w, r, apierr.Validation("invalid application id"))
		return
	}
	a, err := d.apps.Get(r.Context(), mustAuthContext(r), id)
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, a)
}

// --- API tokens ---

func (d apiDeps) handleListAPITokens(w http.ResponseWriter, r *http.Request) {
	tokens, err := d.identity.ListAPITokens(r.Context(), mustAuthContext(r))
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, tokens)
}

func (d apiDeps) handleCreateAPIToken(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name      string     `json:"name"`
		Scopes    []string   `json:"scopes"`
		ExpiresAt *time.Time `json:"expires_at"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	raw, tok, err := d.identity.CreateAPIToken(r.Context(), mustAuthContext(r), body.Name, body.Scopes, body.ExpiresAt)
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusCreated, map[string]any{
		// The raw token is returned exactly once — it is never retrievable
		// again (only its hash is stored). See internal/identity/apitoken.go.
		"token": raw,
		"info":  tok,
	})
}

func (d apiDeps) handleRevokeAPIToken(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpserver.WriteError(w, r, apierr.Validation("invalid token id"))
		return
	}
	if err := d.identity.RevokeAPIToken(r.Context(), mustAuthContext(r), id); err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- jobs ---

func (d apiDeps) handleListJobs(w http.ResponseWriter, r *http.Request) {
	status := jobs.Status(r.URL.Query().Get("status"))
	list, err := d.jobs.List(r.Context(), mustAuthContext(r), status)
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, list)
}

func (d apiDeps) handleEnqueueJob(w http.ResponseWriter, r *http.Request) {
	var body jobs.EnqueueInput
	if !decodeJSON(w, r, &body) {
		return
	}
	j, err := d.jobs.Enqueue(r.Context(), mustAuthContext(r), body)
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusCreated, j)
}

func (d apiDeps) handleGetJob(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpserver.WriteError(w, r, apierr.Validation("invalid job id"))
		return
	}
	j, err := d.jobs.Get(r.Context(), mustAuthContext(r), id)
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, j)
}

func (d apiDeps) handleCancelJob(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpserver.WriteError(w, r, apierr.Validation("invalid job id"))
		return
	}
	if err := d.jobs.Cancel(r.Context(), mustAuthContext(r), id); err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- AI gateway ---

func (d apiDeps) handleListAIProfiles(w http.ResponseWriter, r *http.Request) {
	profiles, err := d.ai.ListProfiles(r.Context(), mustAuthContext(r))
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, profiles)
}

func (d apiDeps) handleCreateAIProfile(w http.ResponseWriter, r *http.Request) {
	var body ai.CreateProfileInput
	if !decodeJSON(w, r, &body) {
		return
	}
	p, err := d.ai.CreateProfile(r.Context(), mustAuthContext(r), body)
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusCreated, p)
}

func (d apiDeps) handleAIChat(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ProfileKey string              `json:"profile_key"`
		Messages   []providers.Message `json:"messages"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	result, err := d.ai.Chat(r.Context(), mustAuthContext(r), body.ProfileKey, body.Messages)
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, result)
}

func (d apiDeps) handleListAIProviders(w http.ResponseWriter, r *http.Request) {
	list, err := d.ai.ListProviders(r.Context(), mustAuthContext(r))
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, list)
}

func (d apiDeps) handleUpsertAIProvider(w http.ResponseWriter, r *http.Request) {
	var body ai.UpsertProviderInput
	if !decodeJSON(w, r, &body) {
		return
	}
	p, err := d.ai.UpsertProvider(r.Context(), mustAuthContext(r), body)
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, p)
}

func (d apiDeps) handleListAIModels(w http.ResponseWriter, r *http.Request) {
	list, err := d.ai.ListModels(r.Context(), mustAuthContext(r))
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, list)
}

func (d apiDeps) handleUpsertAIModel(w http.ResponseWriter, r *http.Request) {
	var body ai.UpsertModelInput
	if !decodeJSON(w, r, &body) {
		return
	}
	m, err := d.ai.UpsertModel(r.Context(), mustAuthContext(r), body)
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, m)
}

// --- secrets ---
//
// There is deliberately no GET endpoint that returns a secret's plaintext
// value (rule: never expose full secret values through the frontend) — see
// internal/secrets/secrets.go's Reveal, which only internal Go code can call.

func (d apiDeps) handleListSecrets(w http.ResponseWriter, r *http.Request) {
	if !d.secretsConfigured(w, r) {
		return
	}
	list, err := d.secrets.List(r.Context(), mustAuthContext(r))
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, list)
}

func (d apiDeps) handleSetSecret(w http.ResponseWriter, r *http.Request) {
	if !d.secretsConfigured(w, r) {
		return
	}
	var body struct {
		Value       string `json:"value"`
		Description string `json:"description"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	key := chi.URLParam(r, "key")
	m, err := d.secrets.Set(r.Context(), mustAuthContext(r), key, body.Value, body.Description)
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, m)
}

func (d apiDeps) handleDeleteSecret(w http.ResponseWriter, r *http.Request) {
	if !d.secretsConfigured(w, r) {
		return
	}
	key := chi.URLParam(r, "key")
	if err := d.secrets.Delete(r.Context(), mustAuthContext(r), key); err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// secretsConfigured writes a normalized UNAVAILABLE response and returns
// false when NODERA_SECRETS_ENCRYPTION_KEY was not set at startup — never
// panics, never silently no-ops (rule 36: report "not configured", don't
// fabricate success).
func (d apiDeps) secretsConfigured(w http.ResponseWriter, r *http.Request) bool {
	if d.secrets != nil {
		return true
	}
	httpserver.WriteError(w, r, apierr.New(apierr.CodeUnavailable, "the secrets module is not configured on this server (NODERA_SECRETS_ENCRYPTION_KEY unset)"))
	return false
}

// --- audit ---

func (d apiDeps) handleListAudit(w http.ResponseWriter, r *http.Request) {
	ac := mustAuthContext(r)
	records, err := d.audit.Query(r.Context(), ac, audit.QueryFilter{
		OrganizationID: ac.OrganizationID,
		ResourceType:   r.URL.Query().Get("resource_type"),
		Action:         r.URL.Query().Get("action"),
	})
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, records)
}

// --- helpers ---

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		httpserver.WriteError(w, r, apierr.Validation("invalid request body"))
		return false
	}
	return true
}

func clientIP(r *http.Request) string {
	// A dedicated proxy-aware resolution (trusted X-Forwarded-For hop count)
	// belongs to the eventual production reverse-proxy config; RemoteAddr is
	// correct for direct connections and local development.
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
