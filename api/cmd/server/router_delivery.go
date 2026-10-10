package main

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/nodera/nodera/internal/deployments"
	"github.com/nodera/nodera/internal/ops"
	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/platform/httpserver"
	"github.com/nodera/nodera/internal/sitemig"
	"github.com/nodera/nodera/internal/wordpress"
)

func (d apiDeps) mountDelivery(r chi.Router) {
	// deployments
	r.Get("/deployments", d.handleListDeployments)
	r.Get("/deployments/{id}", d.handleGetDeployment)
	r.Post("/projects/{id}/deployments", d.handleCreateDeployment)
	r.Post("/projects/{id}/deployments/{did}/rollback", d.handleRollbackDeployment)
	r.Get("/git/repositories", d.handleGitRepositories)

	// migrations
	r.Get("/migrations", d.handleListMigrations)
	r.Post("/migrations", d.handleCreateMigration)
	r.Get("/migrations/{id}", d.handleGetMigration)
	r.Put("/migrations/{id}/source", d.handleUploadMigrationSource)
	r.Post("/migrations/{id}/plan", d.migrationOp(sitemig.OpPlan))
	r.Post("/migrations/{id}/run", d.migrationOp(sitemig.OpRun))
	r.Post("/migrations/{id}/cutover", d.handleMigrationCutover)
	r.Post("/migrations/{id}/rollback", d.migrationOp(sitemig.OpRollback))

	// wordpress
	r.Post("/projects/{id}/wordpress/clone", d.handleWPClone)
	r.Post("/projects/{id}/wordpress/update", d.handleWPUpdate)
	r.Get("/projects/{id}/wordpress/health", d.handleWPHealth)
}

// ---- deployments ----

func (d apiDeps) handleListDeployments(w http.ResponseWriter, r *http.Request) {
	p := httpserver.ParsePagination(r)
	items, err := d.deployments.List(r.Context(), mustAuthContext(r), queryUUID(r, "project_id"), p.Limit+1, p.Offset)
	replyPage(w, r, p, items, err)
}

func (d apiDeps) handleGetDeployment(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	dep, err := d.deployments.Get(r.Context(), mustAuthContext(r), id)
	reply(w, r, http.StatusOK, dep, err)
}

// handleCreateDeployment starts a deployment. Production goes through the Tool
// Gateway (approval); other environments are submitted directly.
func (d apiDeps) handleCreateDeployment(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var in deployments.Payload
	if !decodeJSON(w, r, &in) {
		return
	}
	in.RollbackTo = uuid.Nil
	if in.Environment == "" {
		in.Environment = "production"
	}
	if in.Environment == "production" {
		params := map[string]any{"source": in.Source, "repository": in.Repository, "ref": in.Ref, "environment": in.Environment}
		if len(in.Files) > 0 {
			params["files"] = in.Files
		}
		d.gateway(w, r, "deployment.production", "project", id, params)
		return
	}
	d.submit(w, r, deployments.OpDeploy, id, in)
}

func (d apiDeps) handleRollbackDeployment(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	did, ok := pathID(w, r, "did")
	if !ok {
		return
	}
	d.submit(w, r, deployments.OpRollback, id, deployments.Payload{RollbackTo: did})
}

func (d apiDeps) handleGitRepositories(w http.ResponseWriter, r *http.Request) {
	repos, err := d.deployments.Repositories(r.Context(), mustAuthContext(r))
	reply(w, r, http.StatusOK, repos, err)
}

// ---- migrations ----

func (d apiDeps) handleListMigrations(w http.ResponseWriter, r *http.Request) {
	p := httpserver.ParsePagination(r)
	items, err := d.migrations.List(r.Context(), mustAuthContext(r), queryUUID(r, "project_id"), p.Limit+1, p.Offset)
	replyPage(w, r, p, items, err)
}

func (d apiDeps) handleCreateMigration(w http.ResponseWriter, r *http.Request) {
	var in sitemig.CreateInput
	if !decodeJSON(w, r, &in) {
		return
	}
	m, err := d.migrations.Create(r.Context(), mustAuthContext(r), in)
	reply(w, r, http.StatusCreated, m, err)
}

func (d apiDeps) handleGetMigration(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	m, err := d.migrations.Get(r.Context(), mustAuthContext(r), id)
	reply(w, r, http.StatusOK, m, err)
}

// handleUploadMigrationSource accepts the raw zip body (bounded).
func (d apiDeps) handleUploadMigrationSource(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	body := http.MaxBytesReader(w, r.Body, sitemig.MaxArchiveBytes+1024)
	defer body.Close()
	m, err := d.migrations.UploadSource(r.Context(), mustAuthContext(r), id, body)
	reply(w, r, http.StatusOK, m, err)
}

func (d apiDeps) migrationOp(op string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r, "id")
		if !ok {
			return
		}
		m, err := d.migrations.Get(r.Context(), mustAuthContext(r), id)
		if err != nil {
			httpserver.WriteError(w, r, err)
			return
		}
		var pid uuid.UUID
		if m.ProjectID != nil {
			pid = *m.ProjectID
		}
		d.submit(w, r, op, pid, map[string]any{"migration_id": id})
	}
}

func (d apiDeps) handleMigrationCutover(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	m, err := d.migrations.Get(r.Context(), mustAuthContext(r), id)
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	params := map[string]any{}
	if m.ProjectID != nil {
		params["project_id"] = m.ProjectID.String()
	}
	d.gateway(w, r, "migration.cutover", "migration", id, params)
}

// ---- wordpress ----

func (d apiDeps) handleWPClone(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var in wordpress.ClonePayload
	if !decodeJSON(w, r, &in) {
		return
	}
	d.submit(w, r, wordpress.OpClone, id, in)
}

func (d apiDeps) handleWPUpdate(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var in wordpress.UpdatePayload
	if !decodeJSON(w, r, &in) {
		return
	}
	d.submit(w, r, wordpress.OpUpdate, id, in)
}

func (d apiDeps) handleWPHealth(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	h, err := d.wordpress.Health(r.Context(), mustAuthContext(r), id)
	reply(w, r, http.StatusOK, h, err)
}

var _ = ops.SubmitInput{}
var _ = apierr.Validation
