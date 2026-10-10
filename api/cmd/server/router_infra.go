package main

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/nodera/nodera/internal/backups"
	"github.com/nodera/nodera/internal/network"
	"github.com/nodera/nodera/internal/ops"
	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/platform/httpserver"
	"github.com/nodera/nodera/internal/tools"
)

// submit enqueues a normal (non-dangerous) operation and answers 202.
func (d apiDeps) submit(w http.ResponseWriter, r *http.Request, op string, project uuid.UUID, payload any) {
	ref, err := d.ops.Submit(r.Context(), mustAuthContext(r), ops.SubmitInput{
		Operation: op, ProjectID: project, Payload: payload, IdempotencyKey: r.Header.Get("Idempotency-Key"),
	})
	reply(w, r, http.StatusAccepted, ref, err)
}

// gateway routes a dangerous action through the Tool Gateway (approval).
func (d apiDeps) gateway(w http.ResponseWriter, r *http.Request, tool, resType string, id uuid.UUID, params map[string]any) {
	if !d.allowDangerous(w, r) {
		return
	}
	res, err := d.tools.Execute(r.Context(), mustAuthContext(r), tool, tools.ExecuteInput{ResourceType: resType, ResourceID: id.String(), Parameters: params})
	reply(w, r, http.StatusAccepted, res, err)
}

func (d apiDeps) mountInfra(r chi.Router) {
	// backups
	r.Get("/backups", d.handleListBackups)
	r.Get("/backups/{id}", d.handleGetBackup)
	r.Post("/backups/{id}/verify", d.handleVerifyBackup)
	r.Post("/backups/{id}/restore", d.handleRestoreBackup)
	r.Delete("/backups/{id}", d.handleDeleteBackup)
	r.Post("/projects/{id}/backups", d.handleCreateBackup)
	r.Get("/projects/{id}/backup-policies", d.handleListBackupPolicies)
	r.Put("/projects/{id}/backup-policies", d.handleSaveBackupPolicy)
	r.Delete("/backup-policies/{id}", d.handleDeleteBackupPolicy)

	// domains & dns
	r.Get("/domains", d.handleListDomains)
	r.Post("/domains", d.handleAddDomain)
	r.Get("/domains/{id}", d.handleGetDomain)
	r.Delete("/domains/{id}", d.handleRemoveDomain)
	r.Get("/domains/{id}/records", d.handleListRecords)
	r.Post("/domains/{id}/records", d.handleUpsertRecord)
	r.Delete("/domains/{id}/records/{rid}", d.handleDeleteRecord)
	r.Post("/domains/{id}/check-propagation", d.handleCheckPropagation)
	r.Post("/domains/{id}/sync", d.handleSyncDomain)

	// ssl
	r.Get("/certificates", d.handleListCertificates)
	r.Get("/certificates/{id}", d.handleGetCertificate)
	r.Post("/domains/{id}/certificate", d.handleIssueCertificate)
	r.Post("/domains/{id}/certificate/renew", d.handleRenewCertificate)
	r.Post("/domains/{id}/certificate/revoke", d.handleRevokeCertificate)
}

// ---- backups ----

func (d apiDeps) handleListBackups(w http.ResponseWriter, r *http.Request) {
	p := httpserver.ParsePagination(r)
	items, err := d.backups.List(r.Context(), mustAuthContext(r), queryUUID(r, "project_id"), p.Limit+1, p.Offset)
	replyPage(w, r, p, items, err)
}

func (d apiDeps) handleGetBackup(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	b, err := d.backups.Get(r.Context(), mustAuthContext(r), id)
	reply(w, r, http.StatusOK, b, err)
}

func (d apiDeps) handleCreateBackup(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var in backups.CreatePayload
	if !decodeJSON(w, r, &in) {
		return
	}
	in.PolicyID, in.Reason = nil, "" // not client-settable
	d.submit(w, r, backups.OpCreate, id, in)
}

func (d apiDeps) handleVerifyBackup(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	d.submit(w, r, backups.OpVerify, uuid.Nil, map[string]any{"backup_id": id})
}

func (d apiDeps) handleRestoreBackup(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	b, err := d.backups.Get(r.Context(), mustAuthContext(r), id)
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	d.gateway(w, r, "backup.restore", "backup", id, map[string]any{"project_id": b.ProjectID.String()})
}

func (d apiDeps) handleDeleteBackup(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	b, err := d.backups.Get(r.Context(), mustAuthContext(r), id)
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	d.gateway(w, r, "backup.delete", "backup", id, map[string]any{"project_id": b.ProjectID.String()})
}

func (d apiDeps) handleListBackupPolicies(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	items, err := d.backups.ListPolicies(r.Context(), mustAuthContext(r), id)
	reply(w, r, http.StatusOK, items, err)
}

func (d apiDeps) handleSaveBackupPolicy(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var in backups.PolicyInput
	if !decodeJSON(w, r, &in) {
		return
	}
	p, err := d.backups.SavePolicy(r.Context(), mustAuthContext(r), id, in)
	reply(w, r, http.StatusOK, p, err)
}

func (d apiDeps) handleDeleteBackupPolicy(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	noContent(w, r, d.backups.DeletePolicy(r.Context(), mustAuthContext(r), id))
}

// ---- domains ----

func (d apiDeps) handleListDomains(w http.ResponseWriter, r *http.Request) {
	p := httpserver.ParsePagination(r)
	items, err := d.network.ListDomains(r.Context(), mustAuthContext(r), queryUUID(r, "project_id"), r.URL.Query().Get("q"), p.Limit+1, p.Offset)
	replyPage(w, r, p, items, err)
}

func (d apiDeps) handleAddDomain(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name      string     `json:"name"`
		ProjectID *uuid.UUID `json:"project_id"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	dom, err := d.network.AddDomain(r.Context(), mustAuthContext(r), in.Name, in.ProjectID)
	reply(w, r, http.StatusCreated, dom, err)
}

func (d apiDeps) handleGetDomain(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	dom, err := d.network.GetDomain(r.Context(), mustAuthContext(r), id)
	reply(w, r, http.StatusOK, dom, err)
}

func (d apiDeps) handleRemoveDomain(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	dom, err := d.network.GetDomain(r.Context(), mustAuthContext(r), id)
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	params := map[string]any{}
	if dom.ProjectID != nil {
		params["project_id"] = dom.ProjectID.String()
	}
	d.gateway(w, r, "domain.remove", "domain", id, params)
}

func (d apiDeps) handleListRecords(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	items, err := d.network.ListRecords(r.Context(), mustAuthContext(r), id)
	reply(w, r, http.StatusOK, items, err)
}

func (d apiDeps) handleUpsertRecord(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var in network.RecordInput
	if !decodeJSON(w, r, &in) {
		return
	}
	rec, err := d.network.UpsertRecord(r.Context(), mustAuthContext(r), id, in)
	reply(w, r, http.StatusOK, rec, err)
}

func (d apiDeps) handleDeleteRecord(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	rid, ok := pathID(w, r, "rid")
	if !ok {
		return
	}
	noContent(w, r, d.network.DeleteRecord(r.Context(), mustAuthContext(r), id, rid))
}

func (d apiDeps) handleCheckPropagation(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	res, err := d.network.CheckPropagation(r.Context(), mustAuthContext(r), id)
	reply(w, r, http.StatusOK, res, err)
}

func (d apiDeps) handleSyncDomain(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	n, err := d.network.Sync(r.Context(), mustAuthContext(r), id)
	reply(w, r, http.StatusOK, map[string]int{"synced": n}, err)
}

// ---- ssl ----

func (d apiDeps) handleListCertificates(w http.ResponseWriter, r *http.Request) {
	p := httpserver.ParsePagination(r)
	items, err := d.network.ListCertificates(r.Context(), mustAuthContext(r), queryUUID(r, "project_id"), p.Limit+1, p.Offset)
	replyPage(w, r, p, items, err)
}

func (d apiDeps) handleGetCertificate(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	c, err := d.network.GetCertificate(r.Context(), mustAuthContext(r), id)
	reply(w, r, http.StatusOK, c, err)
}

func (d apiDeps) certOp(op string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r, "id")
		if !ok {
			return
		}
		d.submit(w, r, op, uuid.Nil, map[string]any{"domain_id": id})
	}
}

func (d apiDeps) handleIssueCertificate(w http.ResponseWriter, r *http.Request) {
	d.certOp(network.OpIssue)(w, r)
}
func (d apiDeps) handleRenewCertificate(w http.ResponseWriter, r *http.Request) {
	d.certOp(network.OpRenew)(w, r)
}
func (d apiDeps) handleRevokeCertificate(w http.ResponseWriter, r *http.Request) {
	d.certOp(network.OpRevoke)(w, r)
}

var _ = apierr.Validation

// allowDangerous applies the tighter budget for requests that create approvals
// for destructive or production-affecting operations.
func (d apiDeps) allowDangerous(w http.ResponseWriter, r *http.Request) bool {
	if d.dangerRate == nil {
		return true
	}
	ac := mustAuthContext(r)
	if !d.dangerRate.Allow(ac.OrganizationID.String() + ":" + ac.ActorID.String()) {
		w.Header().Set("Retry-After", "3600")
		httpserver.WriteError(w, r, apierr.New(apierr.CodeRateLimited, "too many dangerous operation requests; try again later"))
		return false
	}
	return true
}
