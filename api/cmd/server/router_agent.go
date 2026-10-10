package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/nodera/nodera/internal/nodeagent"
	"github.com/nodera/nodera/internal/nodeagent/protocol"
	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/platform/httpserver"
)

type ctxKeyAgent struct{}

const maxAgentBody = 1 << 20 // 1 MiB

// agentAuth authenticates a signed Node Agent request. It reads the body once
// (bounded), verifies method+path+body-hash signature, consumes the nonce
// (replay protection) and applies a per-agent rate limit.
func (d apiDeps) agentAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxAgentBody))
		if err != nil {
			httpserver.WriteError(w, r, apierr.Validation("request body too large or unreadable"))
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		a, err := d.nodeagent.Authenticate(r.Context(), r.Header.Get, r.Method, r.URL.Path, body)
		if err != nil {
			httpserver.WriteError(w, r, err)
			return
		}
		if d.agentRate != nil && !d.agentRate.Allow(a.ID.String()) {
			httpserver.WriteError(w, r, apierr.New(apierr.CodeRateLimited, "agent request rate exceeded"))
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKeyAgent{}, a)))
	})
}

func agentFrom(r *http.Request) nodeagent.Agent {
	a, _ := r.Context().Value(ctxKeyAgent{}).(nodeagent.Agent)
	return a
}

// mountAgentProtocol registers the unversioned-by-org agent protocol under
// /agent/v1 (its own version axis, independent of /api/v1).
func (d apiDeps) mountAgentProtocol(r chi.Router) {
	r.Route("/agent/v1", func(r chi.Router) {
		r.Post("/enroll", d.handleAgentEnroll)
		r.Group(func(r chi.Router) {
			r.Use(d.agentAuth)
			r.Post("/heartbeat", d.handleAgentHeartbeat)
			r.Post("/poll", d.handleAgentPoll)
			r.Post("/commands/{id}/result", d.handleAgentResult)
		})
	})
}

func (d apiDeps) handleAgentEnroll(w http.ResponseWriter, r *http.Request) {
	if d.agentEnrollRate != nil && !d.agentEnrollRate.Allow(clientIP(r)) {
		httpserver.WriteError(w, r, apierr.New(apierr.CodeRateLimited, "too many enrolment attempts"))
		return
	}
	var req protocol.EnrollRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	res, err := d.nodeagent.Enroll(r.Context(), req)
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, res)
}

func (d apiDeps) handleAgentHeartbeat(w http.ResponseWriter, r *http.Request) {
	var hb protocol.Heartbeat
	if !decodeJSON(w, r, &hb) {
		return
	}
	if err := d.nodeagent.Heartbeat(r.Context(), agentFrom(r), hb); err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d apiDeps) handleAgentPoll(w http.ResponseWriter, r *http.Request) {
	cmds, err := d.nodeagent.Poll(r.Context(), agentFrom(r))
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	if cmds == nil {
		cmds = []protocol.Command{}
	}
	httpserver.WriteJSON(w, http.StatusOK, cmds)
}

func (d apiDeps) handleAgentResult(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpserver.WriteError(w, r, apierr.Validation("invalid command id"))
		return
	}
	var res protocol.CommandResult
	if !decodeJSON(w, r, &res) {
		return
	}
	if err := d.nodeagent.ReportResult(r.Context(), agentFrom(r), id, res); err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- organization-facing management (session/API-token authenticated) ----

func (d apiDeps) mountAgentManagement(r chi.Router) {
	r.Post("/infrastructure/nodes/{id}/agent-registrations", d.handleCreateAgentRegistration)
	r.Get("/infrastructure/agents", d.handleListNodeAgents)
	r.Post("/infrastructure/agents/{id}/revoke", d.handleRevokeNodeAgent)
	r.Post("/infrastructure/agents/{id}/commands", d.handleQueueAgentCommand)
}

func (d apiDeps) handleCreateAgentRegistration(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpserver.WriteError(w, r, apierr.Validation("invalid node id"))
		return
	}
	reg, err := d.nodeagent.CreateRegistration(r.Context(), mustAuthContext(r), id)
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusCreated, reg)
}

func (d apiDeps) handleListNodeAgents(w http.ResponseWriter, r *http.Request) {
	list, err := d.nodeagent.List(r.Context(), mustAuthContext(r))
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	if list == nil {
		list = []nodeagent.Agent{}
	}
	httpserver.WriteJSON(w, http.StatusOK, list)
}

func (d apiDeps) handleRevokeNodeAgent(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpserver.WriteError(w, r, apierr.Validation("invalid agent id"))
		return
	}
	if err := d.nodeagent.Revoke(r.Context(), mustAuthContext(r), id); err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d apiDeps) handleQueueAgentCommand(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpserver.WriteError(w, r, apierr.Validation("invalid agent id"))
		return
	}
	var body struct {
		Op     string          `json:"op"`
		Params json.RawMessage `json:"params"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Op) == "" {
		httpserver.WriteError(w, r, apierr.Validation("op is required"))
		return
	}
	var params any = map[string]any{}
	if len(body.Params) > 0 {
		params = body.Params
	}
	q, err := d.nodeagent.EnqueueAs(r.Context(), mustAuthContext(r), id, body.Op, params)
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusAccepted, q)
}
