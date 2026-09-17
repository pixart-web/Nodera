package main

import (
	"encoding/json"
	"log/slog"
	"net"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nodera/nodera/internal/audit"
	"github.com/nodera/nodera/internal/identity"
	"github.com/nodera/nodera/internal/infrastructure"
	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/platform/httpserver"
	"github.com/nodera/nodera/internal/tenancy"
)

type apiDeps struct {
	log      *slog.Logger
	identity *identity.Service
	tenancy  *tenancy.Service
	audit    *audit.Service
	infra    *infrastructure.Service
	pool     *pgxpool.Pool
}

func newRouter(d apiDeps) http.Handler {
	r := chi.NewRouter()
	r.Use(httpserver.WithRequestID(d.log))
	r.Use(httpserver.Recover)
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
