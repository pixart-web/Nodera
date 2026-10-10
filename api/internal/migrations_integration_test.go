package integration_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"testing"

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
	"github.com/nodera/nodera/internal/sitemig"
	"github.com/nodera/nodera/internal/testhelpers"
	"github.com/nodera/nodera/internal/tools"
)

type migEnv struct {
	h    *testHarness
	eng  *ops.Engine
	w    *jobs.Worker
	proj *projects.Service
	mig  *sitemig.Service
	bk   *backups.Service
	set  providers.Set
	mk   *mock.Handles
	reg  *tools.Registry
	sec  *secrets.Service
}

func newMigEnv(t *testing.T) (*migEnv, context.Context) {
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
	bk := backups.New(pool, h.audit, proj, set)
	bk.Register(eng)
	mig := sitemig.New(pool, h.audit, proj, bk, sec, set)
	mig.Register(eng)
	reg := tools.New(pool, h.audit)
	eng.BridgeTools(reg)
	w := jobs.NewWorker(pool)
	eng.RegisterWorker(w)
	return &migEnv{h: h, eng: eng, w: w, proj: proj, mig: mig, bk: bk, set: set, mk: mk, reg: reg, sec: sec}, context.Background()
}

func makeZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(content))
	}
	_ = zw.Close()
	return buf.Bytes()
}

const oldSite = "http://old-host.example.com"

func wpSiteZip(t *testing.T) []byte {
	sql := "INSERT INTO `wp_options` VALUES (1,'siteurl','" + oldSite + "','yes'),(2,'home','" + oldSite + "','yes')," +
		"(3,'widget_text','a:1:{s:4:\"link\";s:" + itoa(len(oldSite+"/about")) + ":\"" + oldSite + "/about\";}','yes');\n"
	return makeZip(t, map[string]string{
		"public_html/wp-config.php":                            "<?php define('DB_PASSWORD','OLD-SERVER-SECRET'); ",
		"public_html/wp-includes/version.php":                  "<?php $wp_version = '6.5.2';",
		"public_html/index.php":                                "<?php // migrated index",
		"public_html/wp-content/plugins/akismet/a.php":         "x",
		"public_html/wp-content/plugins/wp-rocket/r.php":       "x",
		"public_html/wp-content/themes/twentytwenty/style.css": "x",
		"public_html/wp-content/cache/junk.html":               "cache",
		"database.sql":                                         sql,
	})
}

func itoa(n int) string { return strconv.Itoa(n) }

func (e *migEnv) runOp(t *testing.T, ctx context.Context, ac authctx.AuthContext, op string, id uuid.UUID) string {
	t.Helper()
	ref, err := e.eng.Submit(ctx, ac, ops.SubmitInput{Operation: op, Payload: map[string]any{"migration_id": id}})
	if err != nil {
		return "rejected: " + err.Error()
	}
	e.w.RunOnce(ctx)
	v, _ := e.eng.Get(ctx, ac, ref.JobID)
	return v.Status
}

func (e *migEnv) setup(t *testing.T, ctx context.Context, ac authctx.AuthContext, name, domain string, zipData []byte) (projects.Project, sitemig.Migration) {
	t.Helper()
	p, _ := e.proj.Create(ctx, ac, projects.ProjectInput{Name: name, Kind: "wordpress"})
	ref, _ := e.eng.Submit(ctx, ac, ops.SubmitInput{Operation: provisioning.OpProvision, ProjectID: p.ID})
	e.w.RunOnce(ctx)
	if v, _ := e.eng.Get(ctx, ac, ref.JobID); v.Status != "succeeded" {
		t.Fatalf("provision: %+v", v)
	}
	m, err := e.mig.Create(ctx, ac, sitemig.CreateInput{ProjectID: &p.ID, SourceKind: "zip", TargetDomain: domain})
	if err != nil {
		t.Fatal(err)
	}
	if zipData != nil {
		if m, err = e.mig.UploadSource(ctx, ac, m.ID, bytes.NewReader(zipData)); err != nil {
			t.Fatal(err)
		}
	}
	return p, m
}

func TestMigration_PlanRunCutoverAndRollback(t *testing.T) {
	e, ctx := newMigEnv(t)
	ac, _ := e.h.newOwnerContext(t, ctx, "mig-ok@nodera.dev")
	p, m := e.setup(t, ctx, ac, "Migrated Site", "newsite.example.org", wpSiteZip(t))
	_ = e.set.FS.WriteFile(ctx, "projects/migrated-site/data/index.php", []byte("<?php // ORIGINAL live site"))

	if st := e.runOp(t, ctx, ac, sitemig.OpRun, m.ID); !strings.HasPrefix(st, "rejected") {
		t.Fatalf("run before plan must be rejected, got %s", st)
	}
	if st := e.runOp(t, ctx, ac, sitemig.OpPlan, m.ID); st != "succeeded" {
		t.Fatalf("plan: %s", st)
	}
	m, _ = e.mig.Get(ctx, ac, m.ID)
	if m.Status != "planned" || len(m.Preflight) == 0 || !strings.Contains(string(m.Preflight), `"can_proceed": true`) {
		t.Fatalf("plan result: %s %s", m.Status, m.Preflight)
	}
	if !strings.Contains(string(m.Preflight), "wp-rocket") || !strings.Contains(string(m.Preflight), "WARNING") {
		t.Fatalf("expected a cache-plugin warning: %s", m.Preflight)
	}

	if st := e.runOp(t, ctx, ac, sitemig.OpRun, m.ID); st != "succeeded" {
		t.Fatalf("run: %s", st)
	}
	m, _ = e.mig.Get(ctx, ac, m.ID)
	if m.Status != "ready_for_cutover" || m.HealthScore == nil || *m.HealthScore < 80 {
		t.Fatalf("after run: status=%s score=%v err=%s", m.Status, m.HealthScore, m.Error)
	}
	// The live site is untouched until cutover.
	if b, _ := e.set.FS.ReadFile(ctx, "projects/migrated-site/data/index.php"); string(b) != "<?php // ORIGINAL live site" {
		t.Fatalf("live site changed before cutover: %q", b)
	}
	// Staged DB has rewritten URLs with correct serialized lengths.
	stagedDB := ""
	for name := range e.mk.DB.Data {
		if strings.HasPrefix(name, "mig_") {
			stagedDB = name
		}
	}
	dump := string(e.mk.DB.Data[stagedDB])
	if strings.Contains(dump, "old-host.example.com") || !strings.Contains(dump, "https://newsite.example.org/about") ||
		!strings.Contains(dump, `s:`+itoa(len("https://newsite.example.org/about"))+`:"https://newsite.example.org/about"`) {
		t.Fatalf("staged database not rewritten correctly:\n%s", dump)
	}

	// Cutover must go through approval.
	if st := e.runOp(t, ctx, ac, sitemig.OpCutover, m.ID); !strings.HasPrefix(st, "rejected") {
		t.Fatalf("direct cutover must be rejected, got %s", st)
	}
	res, err := e.reg.Execute(ctx, ac, "migration.cutover", tools.ExecuteInput{ResourceType: "migration", ResourceID: m.ID.String(), Parameters: map[string]any{"project_id": p.ID.String()}})
	if err != nil || res.ApprovalID == nil {
		t.Fatalf("expected approval: %+v %v", res, err)
	}
	if e.w.RunOnce(ctx) {
		t.Fatal("nothing may run before approval")
	}
	if _, err := e.reg.DecideApproval(ctx, ac, *res.ApprovalID, true, "go"); err != nil {
		t.Fatal(err)
	}
	e.w.RunOnce(ctx)
	m, _ = e.mig.Get(ctx, ac, m.ID)
	if m.Status != "completed" || m.SafetyBackupID == nil {
		t.Fatalf("cutover result: status=%s err=%s safety=%v", m.Status, m.Error, m.SafetyBackupID)
	}
	if b, _ := e.set.FS.ReadFile(ctx, "projects/migrated-site/data/index.php"); string(b) != "<?php // migrated index" {
		t.Fatalf("migrated files not live: %q", b)
	}
	if ok, _ := e.set.FS.Exists(ctx, "projects/migrated-site/data/wp-config.php"); ok {
		t.Fatal("the old server's wp-config.php (with its credentials) must never be deployed")
	}
	if ok, _ := e.set.FS.Exists(ctx, "projects/migrated-site/data/wp-content/cache/junk.html"); ok {
		t.Fatal("cache directory should not be migrated")
	}

	// Rollback restores the pre-cutover site from the safety backup.
	if st := e.runOp(t, ctx, ac, sitemig.OpRollback, m.ID); st != "succeeded" {
		t.Fatalf("rollback: %s", st)
	}
	if b, _ := e.set.FS.ReadFile(ctx, "projects/migrated-site/data/index.php"); string(b) != "<?php // ORIGINAL live site" {
		t.Fatalf("rollback did not restore the original site: %q", b)
	}
	if m, _ = e.mig.Get(ctx, ac, m.ID); m.Status != "rolled_back" {
		t.Fatalf("status = %s", m.Status)
	}

	// Secrets never leak into audit/job logs.
	var leaks int
	_ = e.h.pool.QueryRow(ctx, `SELECT count(*) FROM audit_log a WHERE a::text LIKE '%OLD-SERVER-SECRET%'`).Scan(&leaks)
	_ = e.h.pool.QueryRow(ctx, `SELECT count(*) + $1 FROM job_logs l WHERE l::text LIKE '%OLD-SERVER-SECRET%'`, leaks).Scan(&leaks)
	if leaks != 0 {
		t.Fatal("source credentials leaked into logs")
	}
}

func TestMigration_CutoverFailureRestoresSafetyBackup(t *testing.T) {
	e, ctx := newMigEnv(t)
	ac, _ := e.h.newOwnerContext(t, ctx, "mig-fail@nodera.dev")
	p, m := e.setup(t, ctx, ac, "Fragile Site", "fragile.example.org", wpSiteZip(t))
	_ = e.set.FS.WriteFile(ctx, "projects/fragile-site/data/index.php", []byte("ORIGINAL"))
	if e.runOp(t, ctx, ac, sitemig.OpPlan, m.ID) != "succeeded" || e.runOp(t, ctx, ac, sitemig.OpRun, m.ID) != "succeeded" {
		t.Fatal("setup failed")
	}
	e.mk.Faults.FailOnce("db.load", errors.New("database server went away"))
	res, _ := e.reg.Execute(ctx, ac, "migration.cutover", tools.ExecuteInput{ResourceType: "migration", ResourceID: m.ID.String(), Parameters: map[string]any{"project_id": p.ID.String()}})
	_, _ = e.reg.DecideApproval(ctx, ac, *res.ApprovalID, true, "go")
	e.w.RunOnce(ctx)
	m, _ = e.mig.Get(ctx, ac, m.ID)
	if m.Status != "ready_for_cutover" {
		t.Fatalf("a failed cutover must return to ready_for_cutover, got %s (%s)", m.Status, m.Error)
	}
	if b, _ := e.set.FS.ReadFile(ctx, "projects/fragile-site/data/index.php"); string(b) != "ORIGINAL" {
		t.Fatalf("failed cutover must restore the original site, got %q", b)
	}
}

func TestMigration_PreflightBlockersAndHostileArchives(t *testing.T) {
	e, ctx := newMigEnv(t)
	ac, _ := e.h.newOwnerContext(t, ctx, "mig-pre@nodera.dev")
	other, _ := e.h.newOwnerContext(t, ctx, "mig-other@nodera.dev")
	member := e.h.newMemberContext(t, ctx, ac.OrganizationID, "mig-member@nodera.dev")

	// Not WordPress / multisite / no database => blockers, run refused.
	for name, files := range map[string]map[string]string{
		"not wordpress": {"index.html": "hi", "database.sql": "select 1;"},
		"multisite":     {"wp-config.php": "<?php define('MULTISITE', true);", "wp-includes/version.php": "<?php $wp_version = '6.0';", "database.sql": "x"},
		"no database":   {"wp-config.php": "<?php", "wp-includes/version.php": "<?php $wp_version = '6.0';"},
	} {
		_, m := e.setup(t, ctx, ac, "Pre "+name, strings.ReplaceAll(name, " ", "")+".example.org", makeZip(t, files))
		if st := e.runOp(t, ctx, ac, sitemig.OpPlan, m.ID); st != "succeeded" {
			t.Fatalf("%s plan op: %s", name, st)
		}
		m, _ = e.mig.Get(ctx, ac, m.ID)
		if m.Status != "preflight_failed" || !strings.Contains(string(m.Preflight), "BLOCKER") {
			t.Errorf("%s: expected preflight_failed, got %s", name, m.Status)
		}
		if st := e.runOp(t, ctx, ac, sitemig.OpRun, m.ID); !strings.HasPrefix(st, "rejected") {
			t.Errorf("%s: run must be rejected, got %s", name, st)
		}
	}

	// Zip-slip: a path-traversal entry makes the whole archive unusable.
	_, m := e.setup(t, ctx, ac, "Slip", "slip.example.org", nil)
	evil := makeZip(t, map[string]string{"../../etc/cron.d/x": "pwn", "wp-config.php": "<?php", "database.sql": "x"})
	if _, err := e.mig.UploadSource(ctx, ac, m.ID, bytes.NewReader(evil)); err != nil {
		t.Fatal(err)
	}
	e.runOp(t, ctx, ac, sitemig.OpPlan, m.ID)
	if m, _ = e.mig.Get(ctx, ac, m.ID); m.Status != "preflight_failed" {
		t.Fatalf("path-traversal archive must fail preflight, got %s", m.Status)
	}
	// Not a zip / too big.
	if _, err := e.mig.UploadSource(ctx, ac, m.ID, strings.NewReader("this is not a zip")); err == nil {
		t.Fatal("non-zip upload must be rejected")
	}
	if _, err := e.mig.UploadSource(ctx, ac, m.ID, bytes.NewReader(append([]byte("PK\x03\x04"), make([]byte, sitemig.MaxArchiveBytes)...))); err == nil {
		t.Fatal("oversized upload must be rejected")
	}

	// Secrets in source_config are refused; credentials go to the secrets store.
	if _, err := e.mig.Create(ctx, ac, sitemig.CreateInput{SourceKind: "ftp", TargetDomain: "ftp.example.org", SourceConfig: map[string]any{"host": "h", "password": "hunter2"}}); err == nil {
		t.Fatal("password in source_config must be rejected")
	}
	cm, err := e.mig.Create(ctx, ac, sitemig.CreateInput{SourceKind: "ftp", TargetDomain: "ftp2.example.org", SourceConfig: map[string]any{"host": "h"}, Credentials: map[string]string{"password": "hunter2-super-secret"}})
	if err != nil {
		t.Fatal(err)
	}
	var leaks int
	_ = e.h.pool.QueryRow(ctx, `SELECT count(*) FROM site_migrations s WHERE s::text LIKE '%hunter2%'`).Scan(&leaks)
	_ = e.h.pool.QueryRow(ctx, `SELECT count(*) + $1 FROM audit_log a WHERE a::text LIKE '%hunter2%'`, leaks).Scan(&leaks)
	_ = e.h.pool.QueryRow(ctx, `SELECT count(*) + $1 FROM secrets s WHERE s::text LIKE '%hunter2%'`, leaks).Scan(&leaks)
	if leaks != 0 {
		t.Fatal("credentials stored or logged in plaintext")
	}
	// A connector-less source kind fails preflight honestly instead of pretending.
	if st := e.runOp(t, ctx, ac, sitemig.OpPlan, cm.ID); st != "succeeded" {
		t.Fatalf("plan: %s", st)
	}
	if cm, _ = e.mig.Get(ctx, ac, cm.ID); cm.Status != "preflight_failed" || !strings.Contains(string(cm.Preflight), "no connector") {
		t.Fatalf("expected an honest 'no connector' blocker: %s", cm.Preflight)
	}

	// Tenancy / RBAC / duplicate target.
	if _, err := e.mig.Get(ctx, other, m.ID); err == nil {
		t.Fatal("cross-tenant migration read must fail")
	}
	if st := e.runOp(t, ctx, other, sitemig.OpPlan, m.ID); !strings.HasPrefix(st, "rejected") {
		t.Fatalf("cross-tenant plan = %s", st)
	}
	if _, err := e.mig.Create(ctx, member, sitemig.CreateInput{SourceKind: "zip", TargetDomain: "m.example.org"}); err == nil {
		t.Fatal("member must not create migrations")
	}
	if _, err := e.mig.Create(ctx, ac, sitemig.CreateInput{SourceKind: "zip", TargetDomain: "dup.example.org"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.mig.Create(ctx, ac, sitemig.CreateInput{SourceKind: "zip", TargetDomain: "DUP.example.org"}); err == nil {
		t.Fatal("two active migrations to the same domain must conflict")
	}
}
