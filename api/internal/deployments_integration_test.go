package integration_test

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/nodera/nodera/internal/deployments"
	"github.com/nodera/nodera/internal/jobs"
	"github.com/nodera/nodera/internal/ops"
	"github.com/nodera/nodera/internal/platform/authctx"
	"github.com/nodera/nodera/internal/platform/netpolicy"
	"github.com/nodera/nodera/internal/projects"
	"github.com/nodera/nodera/internal/providers"
	"github.com/nodera/nodera/internal/providers/local"
	"github.com/nodera/nodera/internal/providers/mock"
	"github.com/nodera/nodera/internal/provisioning"
	"github.com/nodera/nodera/internal/secrets"
	"github.com/nodera/nodera/internal/testhelpers"
	"github.com/nodera/nodera/internal/tools"
)

type depEnv struct {
	h    *testHarness
	eng  *ops.Engine
	w    *jobs.Worker
	proj *projects.Service
	dep  *deployments.Service
	set  providers.Set
	mk   *mock.Handles
	reg  *tools.Registry
}

func newDepEnv(t *testing.T) (*depEnv, context.Context) {
	t.Helper()
	pool := testhelpers.RequirePool(t)
	h := newHarness(pool)
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	sec, _ := secrets.New(pool, h.audit, h.platform, base64.StdEncoding.EncodeToString(key))
	set, err := local.NewSet(t.TempDir(), netpolicy.Policy{Level: netpolicy.PublicOnly})
	if err != nil {
		t.Fatal(err)
	}
	m, mk := mock.NewSet()
	set.Containers, set.DB, set.Git = m.Containers, m.DB, m.Git
	proj := projects.New(pool, h.audit)
	eng := ops.New(pool, h.audit)
	provisioning.Register(eng, provisioning.Deps{Pool: pool, Projects: proj, Secrets: sec, Providers: set})
	dep := deployments.New(pool, h.audit, proj, set)
	dep.Register(eng)
	reg := tools.New(pool, h.audit)
	eng.BridgeTools(reg)
	w := jobs.NewWorker(pool)
	eng.RegisterWorker(w)
	return &depEnv{h: h, eng: eng, w: w, proj: proj, dep: dep, set: set, mk: mk, reg: reg}, context.Background()
}

func (e *depEnv) project(t *testing.T, ctx context.Context, ac authctx.AuthContext, name string) projects.Project {
	t.Helper()
	p, err := e.proj.Create(ctx, ac, projects.ProjectInput{Name: name, Kind: "wordpress"})
	if err != nil {
		t.Fatal(err)
	}
	ref, _ := e.eng.Submit(ctx, ac, ops.SubmitInput{Operation: provisioning.OpProvision, ProjectID: p.ID})
	e.w.RunOnce(ctx)
	if v, _ := e.eng.Get(ctx, ac, ref.JobID); v.Status != "succeeded" {
		t.Fatalf("provision: %+v", v)
	}
	return p
}

func upload(files map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range files {
		out[k] = base64.StdEncoding.EncodeToString([]byte(v))
	}
	return out
}

func (e *depEnv) deploy(t *testing.T, ctx context.Context, ac authctx.AuthContext, pid uuid.UUID, p deployments.Payload) string {
	t.Helper()
	ref, err := e.eng.Submit(ctx, ac, ops.SubmitInput{Operation: deployments.OpDeploy, ProjectID: pid, Payload: p})
	if err != nil {
		return "rejected: " + err.Error()
	}
	e.w.RunOnce(ctx)
	v, _ := e.eng.Get(ctx, ac, ref.JobID)
	return v.Status
}

func TestDeploy_UploadGitRollbackAndAutoRevert(t *testing.T) {
	e, ctx := newDepEnv(t)
	ac, _ := e.h.newOwnerContext(t, ctx, "dep-ok@nodera.dev")
	p := e.project(t, ctx, ac, "Deploy Site")
	read := func() string {
		b, _ := e.set.FS.ReadFile(ctx, "projects/deploy-site/app/index.php")
		return string(b)
	}

	if st := e.deploy(t, ctx, ac, p.ID, deployments.Payload{Source: "upload", Environment: "staging", Files: upload(map[string]string{"index.php": "v1", "lib/a.php": "a"})}); st != "succeeded" {
		t.Fatalf("v1: %s", st)
	}
	if read() != "v1" {
		t.Fatalf("live = %q", read())
	}
	// Git deployment records the resolved commit.
	if st := e.deploy(t, ctx, ac, p.ID, deployments.Payload{Source: "github", Repository: "demo/site", Ref: "main", Environment: "staging"}); st != "succeeded" {
		t.Fatalf("git: %s", st)
	}
	list, _ := e.dep.List(ctx, ac, &p.ID, 10, 0)
	if len(list) != 2 || list[0].CommitSHA != "mockc0ffee" || list[0].Stage != "COMPLETE" || list[0].PreviousID == nil {
		t.Fatalf("unexpected deployments: %+v", list)
	}
	gitLive := read()

	// A failing health check automatically puts the previous release back.
	e.mk.Faults.FailOn("container.inspect", errors.New("daemon gone"))
	e.mk.Containers.M["nodera-deploy-site"] = providers.ContainerInfo{Name: "nodera-deploy-site", State: "stopped"}
	if st := e.deploy(t, ctx, ac, p.ID, deployments.Payload{Source: "upload", Environment: "staging", Files: upload(map[string]string{"index.php": "v3-broken"})}); st != "failed" {
		t.Fatalf("v3 should fail health check, got %s", st)
	}
	if read() != gitLive {
		t.Fatalf("failed deploy must leave the previous release live; got %q want %q", read(), gitLive)
	}
	last, _ := e.dep.List(ctx, ac, &p.ID, 1, 0)
	if last[0].Status != "failed" {
		t.Fatalf("deployment row = %+v", last[0])
	}
	e.mk.Faults.Clear()
	e.mk.Containers.M["nodera-deploy-site"] = providers.ContainerInfo{Name: "nodera-deploy-site", State: "running"}

	// Explicit rollback to the first release.
	first := list[1].ID
	ref, err := e.eng.Submit(ctx, ac, ops.SubmitInput{Operation: deployments.OpRollback, ProjectID: p.ID, Payload: deployments.Payload{RollbackTo: first}})
	if err != nil {
		t.Fatal(err)
	}
	e.w.RunOnce(ctx)
	if v, _ := e.eng.Get(ctx, ac, ref.JobID); v.Status != "succeeded" {
		t.Fatalf("rollback: %+v", v)
	}
	if read() != "v1" {
		t.Fatalf("after rollback live = %q, want v1", read())
	}
}

func TestDeploy_ValidationProductionApprovalAndTenancy(t *testing.T) {
	e, ctx := newDepEnv(t)
	ac, _ := e.h.newOwnerContext(t, ctx, "dep-val@nodera.dev")
	other, _ := e.h.newOwnerContext(t, ctx, "dep-other@nodera.dev")
	member := e.h.newMemberContext(t, ctx, ac.OrganizationID, "dep-member@nodera.dev")
	p := e.project(t, ctx, ac, "Val Site")

	bad := []deployments.Payload{
		{Source: "upload", Environment: "staging", Files: upload(map[string]string{"../../etc/passwd": "x"})},
		{Source: "upload", Environment: "staging", Files: upload(map[string]string{"/abs": "x"})},
		{Source: "upload", Environment: "staging"},
		{Source: "github", Repository: "not a repo", Environment: "staging"},
		{Source: "github", Repository: "a/b", Ref: "main;rm -rf /", Environment: "staging"},
		{Source: "github", Repository: "a/b", Ref: "../x", Environment: "staging"},
		{Source: "ftp", Environment: "staging"},
	}
	for i, b := range bad {
		if st := e.deploy(t, ctx, ac, p.ID, b); !strings.HasPrefix(st, "rejected") {
			t.Errorf("payload %d should be rejected, got %s", i, st)
		}
	}
	// Corrupt base64 passes validation but must fail cleanly and leave nothing live.
	if st := e.deploy(t, ctx, ac, p.ID, deployments.Payload{Source: "upload", Environment: "staging", Files: map[string]string{"a": "!!!notbase64"}}); st != "failed" {
		t.Errorf("bad base64 should fail, got %s", st)
	}
	if ok, _ := e.set.FS.Exists(ctx, "projects/val-site/app"); ok {
		t.Error("failed deploy left a live directory")
	}

	// Production cannot be deployed directly...
	if st := e.deploy(t, ctx, ac, p.ID, deployments.Payload{Source: "upload", Environment: "production", Files: upload(map[string]string{"index.php": "x"})}); !strings.HasPrefix(st, "rejected") {
		t.Fatalf("direct production deploy must be rejected, got %s", st)
	}
	if _, err := e.eng.Submit(ctx, ac, ops.SubmitInput{Operation: deployments.OpProduction, ProjectID: p.ID, Payload: deployments.Payload{Source: "upload", Environment: "production", Files: upload(map[string]string{"index.php": "x"})}}); err == nil {
		t.Fatal("production operation must be gateway-only")
	}
	// ...it needs approval, then runs.
	res, err := e.reg.Execute(ctx, ac, "deployment.production", tools.ExecuteInput{ResourceType: "project", ResourceID: p.ID.String(),
		Parameters: map[string]any{"source": "upload", "environment": "production", "files": map[string]any{"index.php": base64.StdEncoding.EncodeToString([]byte("prod-1"))}}})
	if err != nil || res.ApprovalID == nil {
		t.Fatalf("expected approval: %+v %v", res, err)
	}
	if e.w.RunOnce(ctx) {
		t.Fatal("nothing may deploy before approval")
	}
	if _, err := e.reg.DecideApproval(ctx, ac, *res.ApprovalID, true, "ship it"); err != nil {
		t.Fatal(err)
	}
	e.w.RunOnce(ctx)
	if b, _ := e.set.FS.ReadFile(ctx, "projects/val-site/app/index.php"); string(b) != "prod-1" {
		t.Fatalf("production release not live: %q", b)
	}

	// Tenancy and RBAC.
	if st := e.deploy(t, ctx, other, p.ID, deployments.Payload{Source: "upload", Environment: "staging", Files: upload(map[string]string{"a": "b"})}); !strings.HasPrefix(st, "rejected") {
		t.Fatalf("cross-tenant deploy = %s", st)
	}
	if st := e.deploy(t, ctx, member, p.ID, deployments.Payload{Source: "upload", Environment: "staging", Files: upload(map[string]string{"a": "b"})}); !strings.HasPrefix(st, "rejected") {
		t.Fatalf("member deploy = %s", st)
	}
	if l, _ := e.dep.List(ctx, other, nil, 10, 0); len(l) != 0 {
		t.Fatal("cross-tenant deployment list leaked rows")
	}
}
