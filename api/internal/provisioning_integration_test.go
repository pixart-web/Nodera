package integration_test

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/nodera/nodera/internal/jobs"
	"github.com/nodera/nodera/internal/ops"
	"github.com/nodera/nodera/internal/projects"
	"github.com/nodera/nodera/internal/providers"
	"github.com/nodera/nodera/internal/providers/mock"
	"github.com/nodera/nodera/internal/provisioning"
	"github.com/nodera/nodera/internal/secrets"
	"github.com/nodera/nodera/internal/testhelpers"
	"github.com/nodera/nodera/internal/tools"
)

type provEnv struct {
	h    *testHarness
	eng  *ops.Engine
	w    *jobs.Worker
	proj *projects.Service
	sec  *secrets.Service
	mk   *mock.Handles
	set  providers.Set
}

func newProvEnv(t *testing.T) (*provEnv, context.Context) {
	t.Helper()
	pool := testhelpers.RequirePool(t)
	h := newHarness(pool)
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	sec, err := secrets.New(pool, h.audit, h.platform, base64.StdEncoding.EncodeToString(key))
	if err != nil {
		t.Fatal(err)
	}
	set, mk := mock.NewSet()
	proj := projects.New(pool, h.audit)
	eng := ops.New(pool, h.audit)
	provisioning.Register(eng, provisioning.Deps{Pool: pool, Projects: proj, Secrets: sec, Providers: set, Network: "proxy-public", BaseDomain: "apps.example.test"})
	w := jobs.NewWorker(pool)
	eng.RegisterWorker(w)
	return &provEnv{h: h, eng: eng, w: w, proj: proj, sec: sec, mk: mk, set: set}, context.Background()
}

func TestProvision_WordPressSuccess(t *testing.T) {
	e, ctx := newProvEnv(t)
	ac, _ := e.h.newOwnerContext(t, ctx, "prov-ok@nodera.dev")
	p, err := e.proj.Create(ctx, ac, projects.ProjectInput{Name: "Acme Blog", Kind: "wordpress"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Slug != "acme-blog" || p.Status != "provisioning" {
		t.Fatalf("unexpected project %+v", p)
	}
	ref, err := e.eng.Submit(ctx, ac, ops.SubmitInput{Operation: provisioning.OpProvision, ProjectID: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	e.w.RunOnce(ctx)
	v, _ := e.eng.Get(ctx, ac, ref.JobID)
	if v.Status != "succeeded" {
		logs, _ := e.eng.Logs(ctx, ac, ref.JobID, 0, 100)
		t.Fatalf("status=%s err=%v logs=%v", v.Status, v.Error, logs)
	}
	p, _ = e.proj.Get(ctx, ac, p.ID)
	if p.Status != "active" {
		t.Fatalf("project status %s", p.Status)
	}
	if info, ok, _ := e.set.Containers.Inspect(ctx, "nodera-acme-blog"); !ok || info.State != "running" {
		t.Fatalf("container not running: %+v", info)
	}
	// Credentials are stored encrypted and never appear in job logs or the audit trail.
	pw, err := e.sec.Reveal(ctx, ac, "project/acme-blog/db-password")
	if err != nil || pw == "" {
		t.Fatalf("db password secret missing: %v", err)
	}
	logs, _ := e.eng.Logs(ctx, ac, ref.JobID, 0, 500)
	for _, l := range logs {
		if strings.Contains(l.Message, pw) {
			t.Fatal("database password leaked into operation logs")
		}
	}
	var leaks int
	if err := e.h.pool.QueryRow(ctx, `SELECT count(*) FROM audit_log a WHERE a::text LIKE '%' || $1 || '%'`, pw).Scan(&leaks); err != nil || leaks != 0 {
		t.Fatalf("database password leaked into audit log (rows=%d err=%v)", leaks, err)
	}
	if err := e.h.pool.QueryRow(ctx, `SELECT count(*) FROM job_logs l WHERE l::text LIKE '%' || $1 || '%'`, pw).Scan(&leaks); err != nil || leaks != 0 {
		t.Fatalf("database password leaked into job logs (rows=%d err=%v)", leaks, err)
	}
	ov, err := e.proj.Overview(ctx, ac, p.ID)
	if err != nil || ov.Counts["applications"] != 1 {
		t.Fatalf("overview %+v err=%v", ov, err)
	}
}

func TestProvision_FailureRollsBackEverything(t *testing.T) {
	e, ctx := newProvEnv(t)
	ac, _ := e.h.newOwnerContext(t, ctx, "prov-fail@nodera.dev")
	p, _ := e.proj.Create(ctx, ac, projects.ProjectInput{Name: "Broken", Kind: "wordpress"})
	e.mk.Faults.FailOn("container.start", errors.New("docker daemon unreachable"))
	ref, _ := e.eng.Submit(ctx, ac, ops.SubmitInput{Operation: provisioning.OpProvision, ProjectID: p.ID})
	e.w.RunOnce(ctx)
	v, _ := e.eng.Get(ctx, ac, ref.JobID)
	if v.Status != "failed" || !v.RolledBack {
		t.Fatalf("expected failed+rolled back, got %+v", v)
	}
	if _, ok, _ := e.set.Containers.Inspect(ctx, "nodera-broken"); ok {
		t.Fatal("container leaked after rollback")
	}
	if len(e.mk.DB.Names) != 0 {
		t.Fatalf("database leaked after rollback: %v", e.mk.DB.Names)
	}
	if ex, _ := e.set.FS.Exists(ctx, "projects/broken"); ex {
		t.Fatal("workspace leaked after rollback")
	}
	p, _ = e.proj.Get(ctx, ac, p.ID)
	if p.Status != "failed" {
		t.Fatalf("project status = %s, want failed", p.Status)
	}
	// Retry succeeds once the fault clears.
	e.mk.Faults.Clear()
	ref2, err := e.eng.Submit(ctx, ac, ops.SubmitInput{Operation: provisioning.OpProvision, ProjectID: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	e.w.RunOnce(ctx)
	if v2, _ := e.eng.Get(ctx, ac, ref2.JobID); v2.Status != "succeeded" {
		t.Fatalf("retry failed: %+v", v2)
	}
}

func TestProvision_TenantIsolationAndPermissions(t *testing.T) {
	e, ctx := newProvEnv(t)
	acA, _ := e.h.newOwnerContext(t, ctx, "prov-a@nodera.dev")
	acB, _ := e.h.newOwnerContext(t, ctx, "prov-b@nodera.dev")
	member := e.h.newMemberContext(t, ctx, acA.OrganizationID, "prov-m@nodera.dev")
	p, _ := e.proj.Create(ctx, acA, projects.ProjectInput{Name: "Private", Kind: "wordpress"})

	if _, err := e.proj.Get(ctx, acB, p.ID); err == nil {
		t.Fatal("cross-tenant project read must fail")
	}
	if l, _ := e.proj.List(ctx, acB, projects.ListFilter{}); len(l) != 0 {
		t.Fatal("cross-tenant list leaked projects")
	}
	if _, err := e.eng.Submit(ctx, acB, ops.SubmitInput{Operation: provisioning.OpProvision, ProjectID: p.ID}); err == nil {
		t.Fatal("cross-tenant provision must fail")
	}
	if _, err := e.proj.Create(ctx, member, projects.ProjectInput{Name: "X", Kind: "wordpress"}); err == nil {
		t.Fatal("member must not create projects")
	}
	if _, err := e.proj.Get(ctx, member, p.ID); err != nil {
		t.Fatalf("member should read projects: %v", err)
	}
	// Duplicate slug and foreign client are rejected.
	if _, err := e.proj.Create(ctx, acA, projects.ProjectInput{Name: "Private", Kind: "wordpress"}); err == nil {
		t.Fatal("duplicate slug must conflict")
	}
	cB, _ := e.proj.CreateClient(ctx, acB, projects.ClientInput{Name: "B's client"})
	if _, err := e.proj.Create(ctx, acA, projects.ProjectInput{Name: "Other", Kind: "wordpress", ClientID: &cB.ID}); err == nil {
		t.Fatal("project must not reference another tenant's client")
	}
}

func TestDeleteProject_RemovesResources(t *testing.T) {
	e, ctx := newProvEnv(t)
	ac, _ := e.h.newOwnerContext(t, ctx, "prov-del@nodera.dev")
	p, _ := e.proj.Create(ctx, ac, projects.ProjectInput{Name: "Doomed", Kind: "wordpress"})
	ref, _ := e.eng.Submit(ctx, ac, ops.SubmitInput{Operation: provisioning.OpProvision, ProjectID: p.ID})
	e.w.RunOnce(ctx)
	if v, _ := e.eng.Get(ctx, ac, ref.JobID); v.Status != "succeeded" {
		t.Fatalf("setup failed: %+v", v)
	}
	// Direct submission of a dangerous operation is refused: it must use the gateway.
	if _, err := e.eng.Submit(ctx, ac, ops.SubmitInput{Operation: provisioning.OpDelete, ProjectID: p.ID}); err == nil {
		t.Fatal("dangerous operation must not be submittable without the approval gateway")
	}
	reg := tools.New(e.h.pool, e.h.audit)
	e.eng.BridgeTools(reg)
	res, err := reg.Execute(ctx, ac, "project.delete", tools.ExecuteInput{ResourceType: "project", ResourceID: p.ID.String()})
	if err != nil || res.Status != "approval_required" || res.ApprovalID == nil {
		t.Fatalf("expected approval_required, got %+v err=%v", res, err)
	}
	if e.w.RunOnce(ctx) {
		t.Fatal("nothing may run before approval")
	}
	if _, err := e.proj.Get(ctx, ac, p.ID); err != nil {
		t.Fatal("project must still exist before approval")
	}
	if _, err := reg.DecideApproval(ctx, ac, *res.ApprovalID, true, "ok"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	e.w.RunOnce(ctx)
	var delJob string
	if err := e.h.pool.QueryRow(ctx, `SELECT status FROM jobs WHERE operation=$1 AND organization_id=$2`, provisioning.OpDelete, ac.OrganizationID).Scan(&delJob); err != nil || delJob != "succeeded" {
		t.Fatalf("delete job status=%q err=%v", delJob, err)
	}
	if _, err := e.proj.Get(ctx, ac, p.ID); err == nil {
		t.Fatal("deleted project must not be readable")
	}
	if _, ok, _ := e.set.Containers.Inspect(ctx, "nodera-doomed"); ok {
		t.Fatal("container survived deletion")
	}
	if len(e.mk.DB.Names) != 0 {
		t.Fatal("database survived deletion")
	}
	// The slug is reusable after soft delete.
	if _, err := e.proj.Create(ctx, ac, projects.ProjectInput{Name: "Doomed", Kind: "wordpress"}); err != nil {
		t.Fatalf("slug should be reusable after delete: %v", err)
	}
}
