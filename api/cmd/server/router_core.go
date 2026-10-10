package main

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/nodera/nodera/internal/ops"
	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/platform/httpserver"
	"github.com/nodera/nodera/internal/projects"
	infraproviders "github.com/nodera/nodera/internal/providers"
	"github.com/nodera/nodera/internal/provisioning"
	"github.com/nodera/nodera/internal/tools"
)

func pathID(w http.ResponseWriter, r *http.Request, name string) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, name))
	if err != nil {
		httpserver.WriteError(w, r, apierr.Validation("invalid "+name))
		return uuid.Nil, false
	}
	return id, true
}

// reply writes v with status, or the structured error when err != nil.
func reply(w http.ResponseWriter, r *http.Request, status int, v any, err error) {
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, status, v)
}

func replyPage[T any](w http.ResponseWriter, r *http.Request, p httpserver.Pagination, items []T, err error) {
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, httpserver.NewPage(items, p))
}

func noContent(w http.ResponseWriter, r *http.Request, err error) {
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func queryUUID(r *http.Request, key string) *uuid.UUID {
	if v := r.URL.Query().Get(key); v != "" {
		if id, err := uuid.Parse(v); err == nil {
			return &id
		}
	}
	return nil
}

func queryInt(r *http.Request, key string, def int) int {
	if n, err := strconv.Atoi(r.URL.Query().Get(key)); err == nil {
		return n
	}
	return def
}

// mountCore registers clients, projects, operations and system info.
func (d apiDeps) mountCore(r chi.Router) {
	r.Get("/system/info", d.handleSystemInfo)

	r.Get("/clients", d.handleListClients)
	r.Post("/clients", d.handleCreateClient)
	r.Get("/clients/{id}", d.handleGetClient)
	r.Put("/clients/{id}", d.handleUpdateClient)
	r.Delete("/clients/{id}", d.handleDeleteClient)

	r.Get("/projects", d.handleListProjects)
	r.Post("/projects", d.handleCreateProject)
	r.Get("/projects/{id}", d.handleGetProject)
	r.Put("/projects/{id}", d.handleUpdateProject)
	r.Delete("/projects/{id}", d.handleDeleteProject)
	r.Get("/projects/{id}/overview", d.handleProjectOverview)
	r.Post("/projects/{id}/provision", d.handleProvisionProject)
	r.Get("/projects/{id}/operations", d.handleProjectOperations)
	r.Get("/projects/{id}/databases", d.handleProjectDatabases)
	r.Get("/projects/{id}/applications", d.handleProjectApplications)
	r.Get("/projects/{id}/containers", d.handleProjectContainers)

	r.Get("/operations", d.handleListOperations)
	r.Get("/operations/{id}", d.handleGetOperation)
	r.Get("/operations/{id}/steps", d.handleOperationSteps)
	r.Get("/operations/{id}/logs", d.handleOperationLogs)
	r.Post("/operations/{id}/cancel", d.handleCancelOperation)
}

func (d apiDeps) handleSystemInfo(w http.ResponseWriter, r *http.Request) {
	mustAuthContext(r)
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{
		"provider_mode": d.providerMode,
		"capabilities":  capabilityReport(d.providerMode, d.prov),
		"demo":          d.providerMode == "mock",
		"environment":   d.environment,
	})
}

// ---- clients ----

func (d apiDeps) handleListClients(w http.ResponseWriter, r *http.Request) {
	p := httpserver.ParsePagination(r)
	items, err := d.projects.ListClients(r.Context(), mustAuthContext(r), r.URL.Query().Get("q"), p.Limit+1, p.Offset)
	replyPage(w, r, p, items, err)
}

func (d apiDeps) handleCreateClient(w http.ResponseWriter, r *http.Request) {
	var in projects.ClientInput
	if !decodeJSON(w, r, &in) {
		return
	}
	c, err := d.projects.CreateClient(r.Context(), mustAuthContext(r), in)
	reply(w, r, http.StatusCreated, c, err)
}

func (d apiDeps) handleGetClient(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	c, err := d.projects.GetClient(r.Context(), mustAuthContext(r), id)
	reply(w, r, http.StatusOK, c, err)
}

func (d apiDeps) handleUpdateClient(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var in projects.ClientInput
	if !decodeJSON(w, r, &in) {
		return
	}
	c, err := d.projects.UpdateClient(r.Context(), mustAuthContext(r), id, in)
	reply(w, r, http.StatusOK, c, err)
}

func (d apiDeps) handleDeleteClient(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	noContent(w, r, d.projects.DeleteClient(r.Context(), mustAuthContext(r), id))
}

// ---- projects ----

func (d apiDeps) handleListProjects(w http.ResponseWriter, r *http.Request) {
	p := httpserver.ParsePagination(r)
	q := r.URL.Query()
	items, err := d.projects.List(r.Context(), mustAuthContext(r), projects.ListFilter{
		Search: q.Get("q"), Status: q.Get("status"), Kind: q.Get("kind"), ClientID: queryUUID(r, "client_id"),
		Limit: p.Limit + 1, Offset: p.Offset,
	})
	replyPage(w, r, p, items, err)
}

func (d apiDeps) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	var in projects.ProjectInput
	if !decodeJSON(w, r, &in) {
		return
	}
	p, err := d.projects.Create(r.Context(), mustAuthContext(r), in)
	reply(w, r, http.StatusCreated, p, err)
}

func (d apiDeps) handleGetProject(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	p, err := d.projects.Get(r.Context(), mustAuthContext(r), id)
	reply(w, r, http.StatusOK, p, err)
}

func (d apiDeps) handleUpdateProject(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var in projects.UpdateInput
	if !decodeJSON(w, r, &in) {
		return
	}
	p, err := d.projects.Update(r.Context(), mustAuthContext(r), id, in)
	reply(w, r, http.StatusOK, p, err)
}

func (d apiDeps) handleProjectOverview(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	o, err := d.projects.Overview(r.Context(), mustAuthContext(r), id)
	reply(w, r, http.StatusOK, o, err)
}

// handleDeleteProject goes through the Tool Gateway: it answers 202 with an
// approval id (the usual case) and nothing is deleted until a human approves.
func (d apiDeps) handleDeleteProject(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if !d.allowDangerous(w, r) {
		return
	}
	res, err := d.tools.Execute(r.Context(), mustAuthContext(r), "project.delete", tools.ExecuteInput{ResourceType: "project", ResourceID: id.String()})
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusAccepted, res)
}

func (d apiDeps) handleProvisionProject(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	ref, err := d.ops.Submit(r.Context(), mustAuthContext(r), ops.SubmitInput{
		Operation: provisioning.OpProvision, ProjectID: id, IdempotencyKey: r.Header.Get("Idempotency-Key"),
	})
	reply(w, r, http.StatusAccepted, ref, err)
}

// ---- operations ----

func (d apiDeps) handleListOperations(w http.ResponseWriter, r *http.Request) {
	p := httpserver.ParsePagination(r)
	var pid uuid.UUID
	if q := queryUUID(r, "project_id"); q != nil {
		pid = *q
	}
	items, err := d.ops.List(r.Context(), mustAuthContext(r), pid, p.Limit+1, p.Offset)
	replyPage(w, r, p, items, err)
}

func (d apiDeps) handleProjectOperations(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if _, err := d.projects.Get(r.Context(), mustAuthContext(r), id); err != nil {
		httpserver.WriteError(w, r, err) // 404 for another tenant's project, not an empty 200
		return
	}
	p := httpserver.ParsePagination(r)
	items, err := d.ops.List(r.Context(), mustAuthContext(r), id, p.Limit+1, p.Offset)
	replyPage(w, r, p, items, err)
}

func (d apiDeps) handleGetOperation(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	v, err := d.ops.Get(r.Context(), mustAuthContext(r), id)
	reply(w, r, http.StatusOK, v, err)
}

func (d apiDeps) handleOperationSteps(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	s, err := d.ops.Steps(r.Context(), mustAuthContext(r), id)
	if s == nil {
		s = []ops.StepView{}
	}
	reply(w, r, http.StatusOK, s, err)
}

func (d apiDeps) handleOperationLogs(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	l, err := d.ops.Logs(r.Context(), mustAuthContext(r), id, int64(queryInt(r, "after", 0)), queryInt(r, "limit", 200))
	if l == nil {
		l = []ops.LogView{}
	}
	reply(w, r, http.StatusOK, l, err)
}

func (d apiDeps) handleCancelOperation(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	noContent(w, r, d.ops.RequestCancel(r.Context(), mustAuthContext(r), id))
}

func (d apiDeps) handleProjectDatabases(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	items, err := d.projects.Databases(r.Context(), mustAuthContext(r), id)
	reply(w, r, http.StatusOK, items, err)
}

func (d apiDeps) handleProjectApplications(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	items, err := d.projects.Applications(r.Context(), mustAuthContext(r), id)
	reply(w, r, http.StatusOK, items, err)
}

// handleProjectContainers reports the project's containers as the provider
// sees them right now. With no container provider it says so (empty list plus
// available=false) instead of inventing containers.
func (d apiDeps) handleProjectContainers(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	ac := mustAuthContext(r)
	if _, err := d.projects.Get(r.Context(), ac, id); err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	if d.prov.Containers == nil {
		httpserver.WriteJSON(w, http.StatusOK, map[string]any{"available": false, "containers": []any{}})
		return
	}
	list, err := d.prov.Containers.List(r.Context(), map[string]string{"nodera.project": id.String()})
	if err != nil {
		httpserver.WriteError(w, r, apierr.Wrap(apierr.CodeInternal, "failed to list containers", err))
		return
	}
	if list == nil {
		list = []infraproviders.ContainerInfo{}
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"available": true, "containers": list})
}
