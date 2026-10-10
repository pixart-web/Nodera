package integration_test

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nodera/nodera/internal/backups"
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

type bkEnv struct {
	h    *testHarness
	eng  *ops.Engine
	w    *jobs.Worker
	proj *projects.Service
	bk   *backups.Service
	set  providers.Set
	mk   *mock.Handles
	dir  string
	reg  *tools.Registry
}

// newBkEnv uses the REAL local filesystem + tar.gz backup provider and a mock
// database/container provider, so archives are genuine files on disk.
func newBkEnv(t *testing.T) (*bkEnv, context.Context) {
	t.Helper()
	pool := testhelpers.RequirePool(t)
	h := newHarness(pool)
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	sec, err := secrets.New(pool, h.audit, h.platform, base64.StdEncoding.EncodeToString(key))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	set, err := local.NewSet(dir, netpolicy.Policy{Level: netpolicy.PublicOnly})
	if err != nil {
		t.Fatal(err)
	}
	msets, mk := mock.NewSet()
	set.Containers, set.DB = msets.Containers, msets.DB
	proj := projects.New(pool, h.audit)
	eng := ops.New(pool, h.audit)
	provisioning.Register(eng, provisioning.Deps{Pool: pool, Projects: proj, Secrets: sec, Providers: set})
	bk := backups.New(pool, h.audit, proj, set)
	bk.Register(eng)
	reg := tools.New(pool, h.audit)
	eng.BridgeTools(reg)
	w := jobs.NewWorker(pool)
	eng.RegisterWorker(w)
	return &bkEnv{h: h, eng: eng, w: w, proj: proj, bk: bk, set: set, mk: mk, dir: dir, reg: reg}, context.Background()
}

func (e *bkEnv) provisioned(t *testing.T, ctx context.Context, ac_ctx authctx.AuthContext, name string) projects.Project {
	t.Helper()
	p, err := e.proj.Create(ctx, ac_ctx, projects.ProjectInput{Name: name, Kind: "wordpress"})
	if err != nil {
		t.Fatal(err)
	}
	ref, err := e.eng.Submit(ctx, ac_ctx, ops.SubmitInput{Operation: provisioning.OpProvision, ProjectID: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	e.w.RunOnce(ctx)
	if v, _ := e.eng.Get(ctx, ac_ctx, ref.JobID); v.Status != "succeeded" {
		t.Fatalf("provision failed: %+v", v)
	}
	return p
}

func (e *bkEnv) backup(t *testing.T, ctx context.Context, ac authctx.AuthContext, pid uuid.UUID, typ string) backups.Backup {
	t.Helper()
	ref, err := e.eng.Submit(ctx, ac, ops.SubmitInput{Operation: backups.OpCreate, ProjectID: pid, Payload: backups.CreatePayload{Type: typ}})
	if err != nil {
		t.Fatal(err)
	}
	e.w.RunOnce(ctx)
	v, _ := e.eng.Get(ctx, ac, ref.JobID)
	if v.Status != "succeeded" {
		t.Fatalf("backup op: %+v", v)
	}
	list, _ := e.bk.List(ctx, ac, &pid, 50, 0)
	if len(list) == 0 {
		t.Fatal("no backup recorded")
	}
	return list[0]
}

func (e *bkEnv) restore(t *testing.T, ctx context.Context, ac authctx.AuthContext, b backups.Backup) (string, error) {
	t.Helper()
	res, err := e.reg.Execute(ctx, ac, "backup.restore", tools.ExecuteInput{ResourceType: "backup", ResourceID: b.ID.String(), Parameters: map[string]any{"project_id": b.ProjectID.String()}})
	if err != nil || res.ApprovalID == nil {
		t.Fatalf("restore request: %+v %v", res, err)
	}
	if _, err := e.reg.DecideApproval(ctx, ac, *res.ApprovalID, true, "test"); err != nil {
		return "", err
	}
	e.w.RunOnce(ctx)
	var st string
	_ = e.h.pool.QueryRow(ctx, `SELECT status FROM jobs WHERE operation='backup.restore' AND organization_id=$1 ORDER BY created_at DESC LIMIT 1`, ac.OrganizationID).Scan(&st)
	return st, nil
}

func TestBackup_CreateVerifyRestoreRoundTrip(t *testing.T) {
	e, ctx := newBkEnv(t)
	ac, _ := e.h.newOwnerContext(t, ctx, "bk-ok@nodera.dev")
	p := e.provisioned(t, ctx, ac, "Backup Site")

	_ = e.set.FS.WriteFile(ctx, "projects/backup-site/data/index.php", []byte("<?php // v1"))
	e.mk.DB.Data["wp_backup_site"] = nil
	b := e.backup(t, ctx, ac, p.ID, "full")
	if b.Status != "completed" || b.Checksum == "" || b.SizeBytes == 0 || b.VerifiedAt == nil || b.RetentionUntil == nil {
		t.Fatalf("unexpected backup %+v", b)
	}

	// Site changes after the backup, then we restore.
	_ = e.set.FS.WriteFile(ctx, "projects/backup-site/data/index.php", []byte("<?php // v2 BROKEN"))
	st, err := e.restore(t, ctx, ac, b)
	if err != nil || st != "succeeded" {
		t.Fatalf("restore status=%q err=%v", st, err)
	}
	got, _ := e.set.FS.ReadFile(ctx, "projects/backup-site/data/index.php")
	if string(got) != "<?php // v1" {
		t.Fatalf("file not restored, got %q", got)
	}
	var safety *uuid.UUID
	_ = e.h.pool.QueryRow(ctx, `SELECT safety_backup_id FROM restores WHERE backup_id=$1`, b.ID).Scan(&safety)
	if safety == nil {
		t.Fatal("restore must record its safety snapshot")
	}
}

func TestBackup_FailedRestoreReturnsToSafetySnapshot(t *testing.T) {
	e, ctx := newBkEnv(t)
	ac, _ := e.h.newOwnerContext(t, ctx, "bk-rb@nodera.dev")
	p := e.provisioned(t, ctx, ac, "Rollback Site")
	_ = e.set.FS.WriteFile(ctx, "projects/rollback-site/data/a.txt", []byte("original"))
	b := e.backup(t, ctx, ac, p.ID, "full")

	_ = e.set.FS.WriteFile(ctx, "projects/rollback-site/data/a.txt", []byte("live data written after the backup"))
	// The database load fails mid-restore (files may already be half-written).
	e.mk.Faults.FailOnce("db.load", errors.New("disk full"))
	st, err := e.restore(t, ctx, ac, b)
	if err != nil || st != "failed" {
		t.Fatalf("restore should fail, got status=%q err=%v", st, err)
	}
	got, _ := e.set.FS.ReadFile(ctx, "projects/rollback-site/data/a.txt")
	if string(got) != "live data written after the backup" {
		t.Fatalf("rollback must restore the pre-restore state, got %q", got)
	}
}

func TestBackup_CorruptionDetectedAndRestoreRefused(t *testing.T) {
	e, ctx := newBkEnv(t)
	ac, _ := e.h.newOwnerContext(t, ctx, "bk-corrupt@nodera.dev")
	p := e.provisioned(t, ctx, ac, "Corrupt Site")
	_ = e.set.FS.WriteFile(ctx, "projects/corrupt-site/data/a.txt", []byte("x"))
	b := e.backup(t, ctx, ac, p.ID, "files")

	files, _ := filepath.Glob(filepath.Join(e.dir, "backups", "*.tar.gz"))
	if len(files) == 0 {
		t.Fatal("expected an archive on disk")
	}
	if err := os.WriteFile(files[0], []byte("tampered"), 0o640); err != nil {
		t.Fatal(err)
	}
	ref, err := e.eng.Submit(ctx, ac, ops.SubmitInput{Operation: backups.OpVerify, Payload: map[string]any{"backup_id": b.ID}})
	if err != nil {
		t.Fatal(err)
	}
	e.w.RunOnce(ctx)
	if v, _ := e.eng.Get(ctx, ac, ref.JobID); v.Status != "failed" {
		t.Fatalf("verify of tampered backup should fail: %+v", v)
	}
	if got, _ := e.bk.Get(ctx, ac, b.ID); got.Status != "corrupt" {
		t.Fatalf("status = %s, want corrupt", got.Status)
	}
	// The corrupt backup is no longer 'completed', so the gateway handler's
	// validation refuses to even enqueue the restore.
	res, err := e.reg.Execute(ctx, ac, "backup.restore", tools.ExecuteInput{ResourceType: "backup", ResourceID: b.ID.String(), Parameters: map[string]any{"project_id": b.ProjectID.String()}})
	if err != nil || res.ApprovalID == nil {
		t.Fatalf("restore request: %+v %v", res, err)
	}
	if a, err := e.reg.DecideApproval(ctx, ac, *res.ApprovalID, true, "x"); err == nil && a.Status == "executed" {
		t.Fatalf("approving a restore of a corrupt backup must not execute: %+v", a)
	}
	if e.w.RunOnce(ctx) {
		t.Fatal("no restore job may be queued for a corrupt backup")
	}
	var n int
	_ = e.h.pool.QueryRow(ctx, `SELECT count(*) FROM restores WHERE backup_id=$1 AND status='completed'`, b.ID).Scan(&n)
	if n != 0 {
		t.Fatal("a corrupt backup must never be restored")
	}
}

func TestBackup_RetentionPoliciesAndTenancy(t *testing.T) {
	e, ctx := newBkEnv(t)
	ac, _ := e.h.newOwnerContext(t, ctx, "bk-ret@nodera.dev")
	other, _ := e.h.newOwnerContext(t, ctx, "bk-other@nodera.dev")
	member := e.h.newMemberContext(t, ctx, ac.OrganizationID, "bk-member@nodera.dev")
	p := e.provisioned(t, ctx, ac, "Retention Site")
	_ = e.set.FS.WriteFile(ctx, "projects/retention-site/data/a.txt", []byte("x"))

	// Policy -> scheduler creates exactly one backup per period, even if run twice.
	if _, err := e.bk.SavePolicy(ctx, ac, p.ID, backups.PolicyInput{Schedule: "daily", Type: "files", RetentionDays: 1}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	n1, err := e.bk.RunDuePolicies(ctx, now)
	if err != nil || n1 != 1 {
		t.Fatalf("first scheduler run n=%d err=%v", n1, err)
	}
	e.h.pool.Exec(ctx, `UPDATE backup_policies SET last_run_at = NULL`)
	if n2, _ := e.bk.RunDuePolicies(ctx, now); n2 != 0 {
		t.Fatalf("same period must not enqueue twice, got %d", n2)
	}
	e.w.RunOnce(ctx)
	list, _ := e.bk.List(ctx, ac, &p.ID, 50, 0)
	if len(list) != 1 || list[0].Status != "completed" {
		t.Fatalf("scheduled backup missing: %+v", list)
	}

	// Retention deletes expired backups and their artifacts.
	if n, _ := e.bk.EnforceRetention(ctx, now); n != 0 {
		t.Fatalf("nothing is expired yet, deleted %d", n)
	}
	if n, _ := e.bk.EnforceRetention(ctx, now.Add(48*time.Hour)); n != 1 {
		t.Fatalf("expired backup should be removed, deleted %d", n)
	}
	files, _ := filepath.Glob(filepath.Join(e.dir, "backups", "*.tar.gz"))
	if len(files) != 0 {
		t.Fatalf("artifact survived retention: %v", files)
	}

	// Tenancy & RBAC.
	if _, err := e.bk.Get(ctx, other, list[0].ID); err == nil {
		t.Fatal("cross-tenant backup read must fail")
	}
	if _, err := e.eng.Submit(ctx, other, ops.SubmitInput{Operation: backups.OpCreate, ProjectID: p.ID, Payload: backups.CreatePayload{Type: "files"}}); err == nil {
		t.Fatal("cross-tenant backup create must fail")
	}
	if _, err := e.eng.Submit(ctx, member, ops.SubmitInput{Operation: backups.OpCreate, ProjectID: p.ID, Payload: backups.CreatePayload{Type: "files"}}); err == nil {
		t.Fatal("member must not create backups")
	}
	if _, err := e.bk.SavePolicy(ctx, member, p.ID, backups.PolicyInput{Schedule: "daily", Type: "files"}); err == nil {
		t.Fatal("member must not save policies")
	}
	if _, err := e.bk.SavePolicy(ctx, other, p.ID, backups.PolicyInput{Schedule: "daily", Type: "files"}); err == nil {
		t.Fatal("cross-tenant policy must fail")
	}
	_ = strings.TrimSpace
}
