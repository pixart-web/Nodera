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

	"github.com/nodera/nodera/internal/agents"
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
	"github.com/nodera/nodera/internal/rbac"
	"github.com/nodera/nodera/internal/secrets"
	"github.com/nodera/nodera/internal/tenancy"
	"github.com/nodera/nodera/internal/tools"
)

type apiDeps struct {
	log                *slog.Logger
	identity           *identity.Service
	tenancy            *tenancy.Service
	audit              *audit.Service
	infra              *infrastructure.Service
	apps               *applications.Service
	jobs               *jobs.Service
	ai                 *ai.Service
	secrets            *secrets.Service // nil if NODERA_SECRETS_ENCRYPTION_KEY is not configured — see main.go
	tools              *tools.Registry
	agents             *agents.Service
	rbac               *rbac.Service
	pool               *pgxpool.Pool
	loginRate          ratelimit.Allower
	signupRate         ratelimit.Allower
	aiChatRate         ratelimit.Allower
	createOrgRate      ratelimit.Allower
	changePasswordRate ratelimit.Allower
	corsOrigins        []string
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
	r.Get("/openapi.json", d.handleOpenAPISpec)
	r.Get("/docs", d.handleAPIDocs)

	r.Route("/api/v1", func(r chi.Router) {
		r.Post("/auth/signup", d.handleSignUp)
		r.Post("/auth/login", d.handleLogin)

		r.Group(func(r chi.Router) {
			r.Use(d.requireSession)

			r.Post("/auth/logout", d.handleLogout)
			r.Get("/organizations", d.handleListOrganizations)
			r.Post("/organizations", d.handleCreateOrganization)

			r.Put("/account/profile", d.handleUpdateProfile)
			r.Post("/account/password", d.handleChangePassword)
			r.Get("/account/sessions", d.handleListSessions)
			r.Delete("/account/sessions/{id}", d.handleRevokeSession)
			r.Post("/account/sessions/revoke-others", d.handleRevokeAllOtherSessions)

			r.Group(func(r chi.Router) {
				r.Use(d.requireOrganization)

				r.Get("/organization", d.handleGetOrganization)
				r.Put("/organization", d.handleUpdateOrganization)
				r.Post("/organization/leave", d.handleLeaveOrganization)

				r.Get("/infrastructure/nodes", d.handleListNodes)
				r.Post("/infrastructure/nodes", d.handleRegisterNode)
				r.Get("/infrastructure/nodes/{id}", d.handleGetNode)
				r.Put("/infrastructure/nodes/{id}", d.handleUpdateNode)
				r.Post("/infrastructure/nodes/{id}/status", d.handleUpdateNodeStatus)
				r.Post("/infrastructure/nodes/{id}/decommission", d.handleDecommissionNode)

				r.Get("/applications", d.handleListApplications)
				r.Post("/applications", d.handleRegisterApplication)
				r.Get("/applications/{id}", d.handleGetApplication)
				r.Put("/applications/{id}", d.handleUpdateApplication)
				r.Post("/applications/{id}/status", d.handleUpdateApplicationStatus)
				r.Post("/applications/{id}/deregister", d.handleDeregisterApplication)

				r.Get("/api-tokens", d.handleListAPITokens)
				r.Post("/api-tokens", d.handleCreateAPIToken)
				r.Put("/api-tokens/{id}", d.handleUpdateAPIToken)
				r.Delete("/api-tokens/{id}", d.handleRevokeAPIToken)
				r.Get("/organization/api-tokens", d.handleAdminListAPITokens)
				r.Delete("/organization/api-tokens/{id}", d.handleAdminRevokeAPIToken)

				r.Get("/roles", d.handleListRoles)
				r.Post("/roles", d.handleCreateRole)
				r.Put("/roles/{id}", d.handleUpdateRoleDetails)
				r.Put("/roles/{id}/permissions", d.handleUpdateRolePermissions)
				r.Delete("/roles/{id}", d.handleDeleteRole)
				r.Get("/organization/members", d.handleListMembers)
				r.Post("/organization/members", d.handleAddMember)
				r.Delete("/organization/members/{userID}", d.handleRemoveMember)
				r.Post("/organization/members/{userID}/roles", d.handleAssignRole)
				r.Delete("/organization/members/{userID}/roles/{roleID}", d.handleRevokeRole)

				r.Get("/service-accounts", d.handleListServiceAccounts)
				r.Post("/service-accounts", d.handleCreateServiceAccount)
				r.Put("/service-accounts/{id}", d.handleUpdateServiceAccount)
				r.Delete("/service-accounts/{id}", d.handleDisableServiceAccount)
				r.Post("/service-accounts/{id}/enable", d.handleEnableServiceAccount)
				r.Delete("/service-accounts/{id}/permanent", d.handleDeleteServiceAccount)
				r.Post("/service-accounts/{id}/api-tokens", d.handleCreateServiceAccountAPIToken)

				r.Get("/jobs", d.handleListJobs)
				r.Post("/jobs", d.handleEnqueueJob)
				r.Get("/jobs/{id}", d.handleGetJob)
				r.Post("/jobs/{id}/cancel", d.handleCancelJob)
				r.Post("/jobs/{id}/retry", d.handleRetryJob)

				r.Get("/ai/usage", d.handleListAIUsage)
				r.Get("/ai/profiles", d.handleListAIProfiles)
				r.Post("/ai/profiles", d.handleCreateAIProfile)
				r.Put("/ai/profiles/{id}", d.handleUpdateAIProfile)
				r.Delete("/ai/profiles/{id}", d.handleDeleteAIProfile)
				r.Post("/ai/chat", d.handleAIChat)
				r.Get("/ai/providers", d.handleListAIProviders)
				r.Post("/ai/providers", d.handleUpsertAIProvider)
				r.Delete("/ai/providers/{key}", d.handleDeleteAIProvider)
				r.Get("/ai/models", d.handleListAIModels)
				r.Post("/ai/models", d.handleUpsertAIModel)
				r.Delete("/ai/providers/{providerKey}/models/{modelIdentifier}", d.handleDeleteAIModel)

				r.Get("/secrets", d.handleListSecrets)
				r.Put("/secrets/{key}", d.handleSetSecret)
				r.Patch("/secrets/{key}/description", d.handleUpdateSecretDescription)
				r.Delete("/secrets/{key}", d.handleDeleteSecret)

				r.Get("/tools", d.handleListTools)
				r.Post("/tools/{key}/execute", d.handleExecuteTool)
				r.Get("/tools/approval-ttl", d.handleListApprovalTTLOverrides)
				r.Put("/tools/{key}/approval-ttl", d.handleSetApprovalTTL)
				r.Delete("/tools/{key}/approval-ttl", d.handleClearApprovalTTL)
				r.Get("/approvals", d.handleListApprovals)
				r.Post("/approvals/{id}/decide", d.handleDecideApproval)
				r.Post("/approvals/{id}/cancel", d.handleCancelApproval)

				r.Get("/agents", d.handleListAgents)
				r.Post("/agents", d.handleCreateAgent)
				r.Get("/agents/{id}", d.handleGetAgent)
				r.Put("/agents/{id}", d.handleUpdateAgent)
				r.Delete("/agents/{id}", d.handleDeleteAgent)
				r.Post("/agents/{id}/enable", d.handleEnableAgent)
				r.Post("/agents/{id}/disable", d.handleDisableAgent)
				r.Post("/agents/{id}/run", d.handleRunAgent)
				r.Post("/agents/{id}/tools/{key}/execute", d.handleAgentExecuteTool)

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
	// Rate limit by client IP — bounds automated account-creation abuse
	// (spam accounts, credential-stuffing setup). See handleLogin for why
	// this is IP-keyed rather than email-keyed.
	if d.signupRate != nil && !d.signupRate.Allow(clientIP(r)) {
		httpserver.WriteError(w, r, apierr.New(apierr.CodeRateLimited, "too many signup attempts, try again shortly"))
		return
	}

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

// --- account (self-service, pre-organization) ---

func (d apiDeps) handleUpdateProfile(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DisplayName string `json:"display_name"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	u, err := d.identity.UpdateProfile(r.Context(), userIDFromRequest(r), body.DisplayName)
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, u)
}

func (d apiDeps) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)

	// Rate limit by user ID rather than IP — this endpoint re-verifies the
	// caller's current password on every call with no prior throttle, so a
	// stolen/leaked session token let an attacker brute-force the
	// account's real password (useful for credential reuse elsewhere)
	// with no friction at all. Keyed by user ID, not IP, since the caller
	// is already authenticated (an IP-keyed limit would let an attacker
	// spread attempts across many IPs against the same account).
	if d.changePasswordRate != nil && !d.changePasswordRate.Allow(userID.String()) {
		httpserver.WriteError(w, r, apierr.New(apierr.CodeRateLimited, "too many password change attempts, try again shortly"))
		return
	}

	var body struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	sessionToken, _ := r.Context().Value(ctxKeySessionToken{}).(string)
	if err := d.identity.ChangePassword(r.Context(), userID, sessionToken, body.CurrentPassword, body.NewPassword); err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d apiDeps) handleListSessions(w http.ResponseWriter, r *http.Request) {
	sessionToken, _ := r.Context().Value(ctxKeySessionToken{}).(string)
	list, err := d.identity.ListSessions(r.Context(), userIDFromRequest(r), sessionToken)
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, list)
}

func (d apiDeps) handleRevokeSession(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpserver.WriteError(w, r, apierr.Validation("invalid session id"))
		return
	}
	if err := d.identity.RevokeSession(r.Context(), userIDFromRequest(r), id); err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d apiDeps) handleRevokeAllOtherSessions(w http.ResponseWriter, r *http.Request) {
	sessionToken, _ := r.Context().Value(ctxKeySessionToken{}).(string)
	if err := d.identity.RevokeAllOtherSessions(r.Context(), userIDFromRequest(r), sessionToken); err != nil {
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
	userID := userIDFromRequest(r)

	// Rate limit by user rather than IP — organization creation is only
	// reachable once authenticated, so the actor performing it is already
	// known and stable, unlike the pre-auth signup/login endpoints (which
	// key by IP because there's no user identity yet to key by).
	if d.createOrgRate != nil && !d.createOrgRate.Allow(userID.String()) {
		httpserver.WriteError(w, r, apierr.New(apierr.CodeRateLimited, "too many organizations created, try again shortly"))
		return
	}

	var body struct {
		Name string `json:"name"`
		Slug string `json:"slug"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	org, err := d.tenancy.CreateOrganization(r.Context(), userID, body.Name, body.Slug)
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

func (d apiDeps) handleUpdateOrganization(w http.ResponseWriter, r *http.Request) {
	var body tenancy.UpdateOrganizationInput
	if !decodeJSON(w, r, &body) {
		return
	}
	org, err := d.tenancy.UpdateOrganization(r.Context(), mustAuthContext(r), body)
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, org)
}

func (d apiDeps) handleLeaveOrganization(w http.ResponseWriter, r *http.Request) {
	if err := d.tenancy.LeaveOrganization(r.Context(), mustAuthContext(r)); err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- infrastructure ---

func (d apiDeps) handleListNodes(w http.ResponseWriter, r *http.Request) {
	p := httpserver.ParsePagination(r)
	nodes, err := d.infra.List(r.Context(), mustAuthContext(r), p.Limit+1, p.Offset)
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, httpserver.NewPage(nodes, p))
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

func (d apiDeps) handleUpdateNode(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpserver.WriteError(w, r, apierr.Validation("invalid node id"))
		return
	}
	var body infrastructure.UpdateNodeInput
	if !decodeJSON(w, r, &body) {
		return
	}
	n, err := d.infra.UpdateNode(r.Context(), mustAuthContext(r), id, body)
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, n)
}

func (d apiDeps) handleUpdateNodeStatus(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpserver.WriteError(w, r, apierr.Validation("invalid node id"))
		return
	}
	var body struct {
		Status string `json:"status"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	n, err := d.infra.UpdateNodeStatus(r.Context(), mustAuthContext(r), id, body.Status)
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, n)
}

func (d apiDeps) handleDecommissionNode(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpserver.WriteError(w, r, apierr.Validation("invalid node id"))
		return
	}
	n, err := d.infra.DecommissionNode(r.Context(), mustAuthContext(r), id)
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, n)
}

// --- applications ---

func (d apiDeps) handleListApplications(w http.ResponseWriter, r *http.Request) {
	p := httpserver.ParsePagination(r)
	apps, err := d.apps.List(r.Context(), mustAuthContext(r), p.Limit+1, p.Offset)
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, httpserver.NewPage(apps, p))
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

func (d apiDeps) handleUpdateApplication(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpserver.WriteError(w, r, apierr.Validation("invalid application id"))
		return
	}
	var body applications.UpdateInput
	if !decodeJSON(w, r, &body) {
		return
	}
	a, err := d.apps.Update(r.Context(), mustAuthContext(r), id, body)
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, a)
}

func (d apiDeps) handleUpdateApplicationStatus(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpserver.WriteError(w, r, apierr.Validation("invalid application id"))
		return
	}
	var body struct {
		Status string `json:"status"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	a, err := d.apps.UpdateStatus(r.Context(), mustAuthContext(r), id, body.Status)
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, a)
}

func (d apiDeps) handleDeregisterApplication(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpserver.WriteError(w, r, apierr.Validation("invalid application id"))
		return
	}
	a, err := d.apps.Deregister(r.Context(), mustAuthContext(r), id)
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

func (d apiDeps) handleUpdateAPIToken(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpserver.WriteError(w, r, apierr.Validation("invalid token id"))
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	tok, err := d.identity.UpdateAPIToken(r.Context(), mustAuthContext(r), id, body.Name)
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, tok)
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

// handleAdminListAPITokens and handleAdminRevokeAPIToken are the
// organization.manage-scoped counterparts to the two handlers above — they
// operate on every token in the org, not just the caller's own.

func (d apiDeps) handleAdminListAPITokens(w http.ResponseWriter, r *http.Request) {
	tokens, err := d.identity.AdminListAPITokens(r.Context(), mustAuthContext(r))
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, tokens)
}

func (d apiDeps) handleAdminRevokeAPIToken(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpserver.WriteError(w, r, apierr.Validation("invalid token id"))
		return
	}
	if err := d.identity.AdminRevokeAPIToken(r.Context(), mustAuthContext(r), id); err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- RBAC: roles and organization membership ---

func (d apiDeps) handleListRoles(w http.ResponseWriter, r *http.Request) {
	list, err := d.rbac.ListRoles(r.Context(), mustAuthContext(r))
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, list)
}

func (d apiDeps) handleCreateRole(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Permissions []string `json:"permissions"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	role, err := d.rbac.CreateRole(r.Context(), mustAuthContext(r), body.Name, body.Description, body.Permissions)
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusCreated, role)
}

func (d apiDeps) handleUpdateRoleDetails(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpserver.WriteError(w, r, apierr.Validation("invalid role id"))
		return
	}
	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	role, err := d.rbac.UpdateRoleDetails(r.Context(), mustAuthContext(r), id, body.Name, body.Description)
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, role)
}

func (d apiDeps) handleUpdateRolePermissions(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpserver.WriteError(w, r, apierr.Validation("invalid role id"))
		return
	}
	var body struct {
		Permissions []string `json:"permissions"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	role, err := d.rbac.UpdateRolePermissions(r.Context(), mustAuthContext(r), id, body.Permissions)
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, role)
}

func (d apiDeps) handleDeleteRole(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpserver.WriteError(w, r, apierr.Validation("invalid role id"))
		return
	}
	if err := d.rbac.DeleteRole(r.Context(), mustAuthContext(r), id); err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d apiDeps) handleListMembers(w http.ResponseWriter, r *http.Request) {
	list, err := d.rbac.ListMembers(r.Context(), mustAuthContext(r))
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, list)
}

func (d apiDeps) handleAddMember(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email string `json:"email"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	added, err := d.tenancy.AddMember(r.Context(), mustAuthContext(r), body.Email)
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusCreated, added)
}

func (d apiDeps) handleRemoveMember(w http.ResponseWriter, r *http.Request) {
	userID, err := uuid.Parse(chi.URLParam(r, "userID"))
	if err != nil {
		httpserver.WriteError(w, r, apierr.Validation("invalid user id"))
		return
	}
	if err := d.tenancy.RemoveMember(r.Context(), mustAuthContext(r), userID); err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d apiDeps) handleAssignRole(w http.ResponseWriter, r *http.Request) {
	userID, err := uuid.Parse(chi.URLParam(r, "userID"))
	if err != nil {
		httpserver.WriteError(w, r, apierr.Validation("invalid user id"))
		return
	}
	var body struct {
		RoleID string `json:"role_id"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	roleID, err := uuid.Parse(body.RoleID)
	if err != nil {
		httpserver.WriteError(w, r, apierr.Validation("invalid role_id"))
		return
	}
	if err := d.rbac.AssignRole(r.Context(), mustAuthContext(r), userID, roleID); err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d apiDeps) handleRevokeRole(w http.ResponseWriter, r *http.Request) {
	userID, err := uuid.Parse(chi.URLParam(r, "userID"))
	if err != nil {
		httpserver.WriteError(w, r, apierr.Validation("invalid user id"))
		return
	}
	roleID, err := uuid.Parse(chi.URLParam(r, "roleID"))
	if err != nil {
		httpserver.WriteError(w, r, apierr.Validation("invalid role id"))
		return
	}
	if err := d.rbac.RevokeRole(r.Context(), mustAuthContext(r), userID, roleID); err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- service accounts ---

func (d apiDeps) handleListServiceAccounts(w http.ResponseWriter, r *http.Request) {
	list, err := d.identity.ListServiceAccounts(r.Context(), mustAuthContext(r))
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, list)
}

func (d apiDeps) handleCreateServiceAccount(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	sa, err := d.identity.CreateServiceAccount(r.Context(), mustAuthContext(r), body.Name, body.Description)
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusCreated, sa)
}

func (d apiDeps) handleDisableServiceAccount(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpserver.WriteError(w, r, apierr.Validation("invalid service account id"))
		return
	}
	if err := d.identity.DisableServiceAccount(r.Context(), mustAuthContext(r), id); err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d apiDeps) handleDeleteServiceAccount(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpserver.WriteError(w, r, apierr.Validation("invalid service account id"))
		return
	}
	if err := d.identity.DeleteServiceAccount(r.Context(), mustAuthContext(r), id); err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d apiDeps) handleEnableServiceAccount(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpserver.WriteError(w, r, apierr.Validation("invalid service account id"))
		return
	}
	if err := d.identity.EnableServiceAccount(r.Context(), mustAuthContext(r), id); err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d apiDeps) handleUpdateServiceAccount(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpserver.WriteError(w, r, apierr.Validation("invalid service account id"))
		return
	}
	var body identity.UpdateServiceAccountInput
	if !decodeJSON(w, r, &body) {
		return
	}
	sa, err := d.identity.UpdateServiceAccount(r.Context(), mustAuthContext(r), id, body)
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, sa)
}

func (d apiDeps) handleCreateServiceAccountAPIToken(w http.ResponseWriter, r *http.Request) {
	serviceAccountID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpserver.WriteError(w, r, apierr.Validation("invalid service account id"))
		return
	}
	var body struct {
		Name      string     `json:"name"`
		Scopes    []string   `json:"scopes"`
		ExpiresAt *time.Time `json:"expires_at"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	raw, tok, err := d.identity.CreateAPITokenForServiceAccount(r.Context(), mustAuthContext(r), serviceAccountID, body.Name, body.Scopes, body.ExpiresAt)
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusCreated, map[string]any{
		"token": raw,
		"info":  tok,
	})
}

// --- jobs ---

func (d apiDeps) handleListJobs(w http.ResponseWriter, r *http.Request) {
	status := jobs.Status(r.URL.Query().Get("status"))
	p := httpserver.ParsePagination(r)
	list, err := d.jobs.List(r.Context(), mustAuthContext(r), status, p.Limit+1, p.Offset)
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, httpserver.NewPage(list, p))
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

func (d apiDeps) handleRetryJob(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpserver.WriteError(w, r, apierr.Validation("invalid job id"))
		return
	}
	j, err := d.jobs.Retry(r.Context(), mustAuthContext(r), id)
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, j)
}

// --- AI gateway ---

func (d apiDeps) handleListAIUsage(w http.ResponseWriter, r *http.Request) {
	p := httpserver.ParsePagination(r)
	records, err := d.ai.ListUsage(r.Context(), mustAuthContext(r), ai.UsageFilter{
		ProfileKey:  r.URL.Query().Get("profile_key"),
		ProviderKey: r.URL.Query().Get("provider_key"),
		Limit:       p.Limit + 1,
		Offset:      p.Offset,
	})
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, httpserver.NewPage(records, p))
}

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

func (d apiDeps) handleUpdateAIProfile(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpserver.WriteError(w, r, apierr.Validation("invalid profile id"))
		return
	}
	var body ai.UpdateProfileInput
	if !decodeJSON(w, r, &body) {
		return
	}
	p, err := d.ai.UpdateProfile(r.Context(), mustAuthContext(r), id, body)
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, p)
}

func (d apiDeps) handleDeleteAIProfile(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpserver.WriteError(w, r, apierr.Validation("invalid profile id"))
		return
	}
	if err := d.ai.DeleteProfile(r.Context(), mustAuthContext(r), id); err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d apiDeps) handleAIChat(w http.ResponseWriter, r *http.Request) {
	ac := mustAuthContext(r)

	// Rate limit by organization rather than by IP — the AI gateway's real
	// abuse/cost concern is runaway spend against one tenant's usage,
	// which an IP-keyed limiter wouldn't bound (many legitimate calls can
	// share an IP behind NAT; a single compromised or careless integration
	// racking up provider cost is the actual risk here — see
	// docs/SECURITY.md).
	if d.aiChatRate != nil && !d.aiChatRate.Allow(ac.OrganizationID.String()) {
		httpserver.WriteError(w, r, apierr.New(apierr.CodeRateLimited, "too many AI requests for this organization, try again shortly"))
		return
	}

	var body struct {
		ProfileKey string              `json:"profile_key"`
		Messages   []providers.Message `json:"messages"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	result, err := d.ai.Chat(r.Context(), ac, body.ProfileKey, body.Messages)
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

func (d apiDeps) handleDeleteAIProvider(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "key")
	if err := d.ai.DeleteProvider(r.Context(), mustAuthContext(r), key); err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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

func (d apiDeps) handleDeleteAIModel(w http.ResponseWriter, r *http.Request) {
	providerKey := chi.URLParam(r, "providerKey")
	modelIdentifier := chi.URLParam(r, "modelIdentifier")
	if err := d.ai.DeleteModel(r.Context(), mustAuthContext(r), providerKey, modelIdentifier); err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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

func (d apiDeps) handleUpdateSecretDescription(w http.ResponseWriter, r *http.Request) {
	if !d.secretsConfigured(w, r) {
		return
	}
	var body struct {
		Description string `json:"description"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	key := chi.URLParam(r, "key")
	m, err := d.secrets.UpdateDescription(r.Context(), mustAuthContext(r), key, body.Description)
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

// --- tools / approvals ---

func (d apiDeps) handleListTools(w http.ResponseWriter, r *http.Request) {
	list, err := d.tools.List(r.Context(), mustAuthContext(r))
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, list)
}

func (d apiDeps) handleExecuteTool(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ResourceType string         `json:"resource_type"`
		ResourceID   string         `json:"resource_id"`
		Parameters   map[string]any `json:"parameters"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	key := chi.URLParam(r, "key")
	result, err := d.tools.Execute(r.Context(), mustAuthContext(r), key, tools.ExecuteInput{
		ResourceType: body.ResourceType, ResourceID: body.ResourceID, Parameters: body.Parameters,
	})
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	status := http.StatusOK
	if result.Status == "approval_required" {
		status = http.StatusAccepted
	}
	httpserver.WriteJSON(w, status, result)
}

func (d apiDeps) handleListApprovalTTLOverrides(w http.ResponseWriter, r *http.Request) {
	list, err := d.tools.ListApprovalTTLOverrides(r.Context(), mustAuthContext(r))
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, list)
}

func (d apiDeps) handleSetApprovalTTL(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ApprovalTTLSeconds int `json:"approval_ttl_seconds"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	key := chi.URLParam(r, "key")
	s, err := d.tools.SetApprovalTTL(r.Context(), mustAuthContext(r), key, time.Duration(body.ApprovalTTLSeconds)*time.Second)
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, s)
}

func (d apiDeps) handleClearApprovalTTL(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "key")
	if err := d.tools.ClearApprovalTTL(r.Context(), mustAuthContext(r), key); err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d apiDeps) handleListApprovals(w http.ResponseWriter, r *http.Request) {
	list, err := d.tools.ListApprovals(r.Context(), mustAuthContext(r), r.URL.Query().Get("status"))
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, list)
}

func (d apiDeps) handleDecideApproval(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpserver.WriteError(w, r, apierr.Validation("invalid approval id"))
		return
	}
	var body struct {
		Approve bool   `json:"approve"`
		Reason  string `json:"reason"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	a, err := d.tools.DecideApproval(r.Context(), mustAuthContext(r), id, body.Approve, body.Reason)
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, a)
}

func (d apiDeps) handleCancelApproval(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpserver.WriteError(w, r, apierr.Validation("invalid approval id"))
		return
	}
	a, err := d.tools.CancelApproval(r.Context(), mustAuthContext(r), id)
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, a)
}

// --- agents ---
//
// See docs/AGENTS.md: this is agent identity + scoped execution, not an
// autonomous tool-calling loop (rule 38). A caller directs which tool an
// agent uses (handleAgentExecuteTool); the agent never decides that itself.

func (d apiDeps) handleListAgents(w http.ResponseWriter, r *http.Request) {
	list, err := d.agents.List(r.Context(), mustAuthContext(r))
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, list)
}

func (d apiDeps) handleCreateAgent(w http.ResponseWriter, r *http.Request) {
	var body agents.CreateAgentInput
	if !decodeJSON(w, r, &body) {
		return
	}
	a, err := d.agents.CreateAgent(r.Context(), mustAuthContext(r), body)
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusCreated, a)
}

func (d apiDeps) handleGetAgent(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpserver.WriteError(w, r, apierr.Validation("invalid agent id"))
		return
	}
	a, err := d.agents.Get(r.Context(), mustAuthContext(r), id)
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, a)
}

func (d apiDeps) handleUpdateAgent(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpserver.WriteError(w, r, apierr.Validation("invalid agent id"))
		return
	}
	var body agents.UpdateAgentInput
	if !decodeJSON(w, r, &body) {
		return
	}
	a, err := d.agents.Update(r.Context(), mustAuthContext(r), id, body)
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, a)
}

func (d apiDeps) handleDeleteAgent(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpserver.WriteError(w, r, apierr.Validation("invalid agent id"))
		return
	}
	if err := d.agents.Delete(r.Context(), mustAuthContext(r), id); err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d apiDeps) handleEnableAgent(w http.ResponseWriter, r *http.Request) {
	d.setAgentStatus(w, r, true)
}

func (d apiDeps) handleDisableAgent(w http.ResponseWriter, r *http.Request) {
	d.setAgentStatus(w, r, false)
}

func (d apiDeps) setAgentStatus(w http.ResponseWriter, r *http.Request, active bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpserver.WriteError(w, r, apierr.Validation("invalid agent id"))
		return
	}
	a, err := d.agents.SetStatus(r.Context(), mustAuthContext(r), id, active)
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, a)
}

func (d apiDeps) handleRunAgent(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpserver.WriteError(w, r, apierr.Validation("invalid agent id"))
		return
	}
	ac := mustAuthContext(r)

	// Run drives the same ai.Service.Chat cost path as POST /ai/chat — an
	// agent is just another caller of it, so it must be bound by the same
	// per-organization rate limit, or agents.execute becomes a way to
	// bypass ai.chat's cost-control limiter entirely (see docs/SECURITY.md).
	if d.aiChatRate != nil && !d.aiChatRate.Allow(ac.OrganizationID.String()) {
		httpserver.WriteError(w, r, apierr.New(apierr.CodeRateLimited, "too many AI requests for this organization, try again shortly"))
		return
	}

	var body struct {
		Message string `json:"message"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	result, err := d.agents.Run(r.Context(), ac, id, body.Message)
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, result)
}

func (d apiDeps) handleAgentExecuteTool(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpserver.WriteError(w, r, apierr.Validation("invalid agent id"))
		return
	}
	toolKey := chi.URLParam(r, "key")
	var body struct {
		ResourceType string         `json:"resource_type"`
		ResourceID   string         `json:"resource_id"`
		Parameters   map[string]any `json:"parameters"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	result, err := d.agents.ExecuteTool(r.Context(), mustAuthContext(r), id, toolKey, tools.ExecuteInput{
		ResourceType: body.ResourceType, ResourceID: body.ResourceID, Parameters: body.Parameters,
	})
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	status := http.StatusOK
	if result.Status == "approval_required" {
		status = http.StatusAccepted
	}
	httpserver.WriteJSON(w, status, result)
}

// --- audit ---

func (d apiDeps) handleListAudit(w http.ResponseWriter, r *http.Request) {
	ac := mustAuthContext(r)
	p := httpserver.ParsePagination(r)
	records, err := d.audit.Query(r.Context(), ac, audit.QueryFilter{
		OrganizationID: ac.OrganizationID,
		ResourceType:   r.URL.Query().Get("resource_type"),
		Action:         r.URL.Query().Get("action"),
		From:           parseRFC3339Query(r, "from"),
		To:             parseRFC3339Query(r, "to"),
		Limit:          p.Limit + 1,
		Offset:         p.Offset,
	})
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, httpserver.NewPage(records, p))
}

// parseRFC3339Query reads an RFC3339 timestamp from a query parameter,
// treating a missing or unparsable value as unset (zero time.Time) rather
// than a 400 — same "degrade gracefully" philosophy
// httpserver.ParsePagination already applies to ?limit/?offset.
func parseRFC3339Query(r *http.Request, key string) time.Time {
	v := r.URL.Query().Get(key)
	if v == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}
	}
	return t
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
