package main

// HTTP-level tests for the core control-plane API (clients, projects,
// operations, dangerous-op approval) against the real router, real Postgres
// and the in-memory mock provider set.

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nodera/nodera/internal/audit"
	"github.com/nodera/nodera/internal/backups"
	"github.com/nodera/nodera/internal/deployments"
	"github.com/nodera/nodera/internal/identity"
	"github.com/nodera/nodera/internal/jobs"
	"github.com/nodera/nodera/internal/logs"
	"github.com/nodera/nodera/internal/monitoring"
	"github.com/nodera/nodera/internal/network"
	"github.com/nodera/nodera/internal/notifications"
	"github.com/nodera/nodera/internal/ops"
	"github.com/nodera/nodera/internal/platform/logger"
	"github.com/nodera/nodera/internal/platform/netpolicy"
	"github.com/nodera/nodera/internal/platformauth"
	"github.com/nodera/nodera/internal/projects"
	"github.com/nodera/nodera/internal/providers/mock"
	"github.com/nodera/nodera/internal/provisioning"
	"github.com/nodera/nodera/internal/rbac"
	"github.com/nodera/nodera/internal/secrets"
	"github.com/nodera/nodera/internal/sitemig"
	"github.com/nodera/nodera/internal/tenancy"
	"github.com/nodera/nodera/internal/testhelpers"
	"github.com/nodera/nodera/internal/tools"
	"github.com/nodera/nodera/internal/wordpress"
)

type coreEnv struct {
	srv    *httptest.Server
	worker *jobs.Worker
	token  string
	org    uuid.UUID
	ident  *identity.Service
	d      apiDeps
}

func newCoreEnv(t *testing.T, email string) (*coreEnv, apiDeps) {
	t.Helper()
	pool := testhelpers.RequirePool(t)
	auditSvc := audit.New(pool)
	rbacSvc := rbac.New(pool, auditSvc)
	identitySvc := identity.New(pool, rbacSvc, 24*time.Hour, auditSvc)
	tenancySvc := tenancy.New(pool, identitySvc, auditSvc)
	platformSvc := platformauth.New(pool, auditSvc)
	auditSvc.SetPlatformAuthorizer(platformSvc)
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	sec, err := secrets.New(pool, auditSvc, platformSvc, base64.StdEncoding.EncodeToString(key))
	if err != nil {
		t.Fatal(err)
	}
	set, _ := mock.NewSet()
	proj := projects.New(pool, auditSvc)
	eng := ops.New(pool, auditSvc)
	provisioning.Register(eng, provisioning.Deps{Pool: pool, Projects: proj, Secrets: sec, Providers: set})
	bk := backups.New(pool, auditSvc, proj, set)
	bk.Register(eng)
	nw := network.New(pool, auditSvc, sec, set)
	nw.Register(eng)
	dep := deployments.New(pool, auditSvc, proj, set)
	dep.Register(eng)
	mig := sitemig.New(pool, auditSvc, proj, bk, sec, set)
	mig.Register(eng)
	wp := wordpress.New(pool, proj, bk, provisioning.Deps{Pool: pool, Projects: proj, Secrets: sec, Providers: set})
	wp.Register(eng)
	notif := notifications.New(pool, auditSvc, netpolicy.Policy{Level: netpolicy.PublicOnly})
	eng.SetNotifier(notif)
	monSvc := monitoring.New(pool, auditSvc, set, notif)
	logSvc := logs.New(pool, set)
	toolsSvc := tools.New(pool, auditSvc)
	eng.BridgeTools(toolsSvc)
	worker := jobs.NewWorker(pool)
	eng.RegisterWorker(worker)

	d := apiDeps{
		log: logger.New("test"), identity: identitySvc, tenancy: tenancySvc, audit: auditSvc, rbac: rbacSvc,
		platform: platformSvc, pool: pool, sessionTTL: 24 * time.Hour,
		projects: proj, backups: bk, network: nw, deployments: dep, monitoring: monSvc, notifications: notif, logs: logSvc, migrations: mig, wordpress: wp, ops: eng, prov: set, providerMode: "mock", environment: "test", tools: toolsSvc,
	}
	srv := httptest.NewServer(newRouter(d))
	t.Cleanup(srv.Close)

	ctx := context.Background()
	u, err := identitySvc.SignUp(ctx, email, "correct horse battery staple 9", "Core Test")
	if err != nil {
		t.Fatal(err)
	}
	tok, _, err := identitySvc.Login(ctx, email, "correct horse battery staple 9", "127.0.0.1", "t")
	if err != nil {
		t.Fatal(err)
	}
	org, err := tenancySvc.CreateOrganization(ctx, u.ID, "Core "+email, uuid.NewString()[:10])
	if err != nil {
		t.Fatal(err)
	}
	return &coreEnv{srv: srv, worker: worker, token: tok, org: org.ID, ident: identitySvc, d: d}, d
}

func (e *coreEnv) do(t *testing.T, method, path string, body any) (int, map[string]any, []byte) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.srv.URL+"/api/v1"+path, rd)
	req.Header.Set("Authorization", "Bearer "+e.token)
	req.Header.Set("X-Nodera-Org", e.org.String())
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return resp.StatusCode, m, raw
}

func TestCoreAPI_ProjectLifecycle(t *testing.T) {
	e, _ := newCoreEnv(t, "core-api-"+uuid.NewString()[:6]+"@example.com")

	st, client, _ := e.do(t, "POST", "/clients", map[string]string{"name": "Acme Corp", "contact_email": "ops@acme.test"})
	if st != 201 {
		t.Fatalf("create client: %d %v", st, client)
	}
	st, proj, _ := e.do(t, "POST", "/projects", map[string]any{"name": "Acme Blog", "kind": "wordpress", "client_id": client["id"]})
	if st != 201 || proj["status"] != "provisioning" || proj["slug"] != "acme-blog" {
		t.Fatalf("create project: %d %v", st, proj)
	}
	pid := proj["id"].(string)

	st, ref, _ := e.do(t, "POST", "/projects/"+pid+"/provision", nil)
	if st != 202 {
		t.Fatalf("provision: %d %v", st, ref)
	}
	if !e.worker.RunOnce(context.Background()) {
		t.Fatal("expected provisioning job")
	}
	jid := ref["job_id"].(string)
	st, op, _ := e.do(t, "GET", "/operations/"+jid, nil)
	if st != 200 || op["status"] != "succeeded" {
		t.Fatalf("operation: %d %v", st, op)
	}
	_, _, rawSteps := e.do(t, "GET", "/operations/"+jid+"/steps", nil)
	var steps []map[string]any
	_ = json.Unmarshal(rawSteps, &steps)
	if len(steps) != 8 {
		t.Fatalf("expected 8 steps, got %d: %s", len(steps), rawSteps)
	}
	_, _, rawLogs := e.do(t, "GET", "/operations/"+jid+"/logs", nil)
	var logs []map[string]any
	_ = json.Unmarshal(rawLogs, &logs)
	if len(logs) == 0 {
		t.Fatal("expected operation logs")
	}

	st, got, _ := e.do(t, "GET", "/projects/"+pid, nil)
	if st != 200 || got["status"] != "active" {
		t.Fatalf("project after provision: %d %v", st, got)
	}
	st, page, _ := e.do(t, "GET", "/projects?q=acme&limit=1", nil)
	items, _ := page["items"].([]any)
	if st != 200 || len(items) != 1 || page["has_more"] != false {
		t.Fatalf("search/paging: %d %v", st, page)
	}
	st, ov, _ := e.do(t, "GET", "/projects/"+pid+"/overview", nil)
	if st != 200 || ov["counts"].(map[string]any)["applications"].(float64) != 1 {
		t.Fatalf("overview: %d %v", st, ov)
	}

	// Deleting needs approval: 202 + approval id, and the project is untouched.
	st, del, _ := e.do(t, "DELETE", "/projects/"+pid, nil)
	if st != 202 || del["status"] != "approval_required" || del["approval_id"] == nil {
		t.Fatalf("delete: %d %v", st, del)
	}
	if st, got, _ := e.do(t, "GET", "/projects/"+pid, nil); st != 200 || got["status"] != "active" {
		t.Fatal("project must survive until approval")
	}
}

func TestCoreAPI_ErrorsAuthAndIsolation(t *testing.T) {
	e, _ := newCoreEnv(t, "core-err-"+uuid.NewString()[:6]+"@example.com")

	st, body, _ := e.do(t, "GET", "/projects/not-a-uuid", nil)
	errObj, _ := body["error"].(map[string]any)
	if st != 400 || errObj["code"] != "VALIDATION_ERROR" || errObj["request_id"] == nil {
		t.Fatalf("expected structured 400, got %d %v", st, body)
	}
	if st, _, _ := e.do(t, "GET", "/projects/"+uuid.NewString(), nil); st != 404 {
		t.Fatalf("unknown project = %d, want 404", st)
	}
	if st, _, _ := e.do(t, "POST", "/projects", map[string]any{"name": "x", "kind": "bogus"}); st != 400 {
		t.Fatalf("invalid kind = %d, want 400", st)
	}

	// No token -> 401.
	resp, _ := http.Get(e.srv.URL + "/api/v1/projects")
	if resp.StatusCode != 401 {
		t.Fatalf("unauthenticated = %d, want 401", resp.StatusCode)
	}
	resp.Body.Close()

	// A second tenant cannot see or touch the first tenant's data.
	_, proj, _ := e.do(t, "POST", "/projects", map[string]any{"name": "Secret Site", "kind": "wordpress"})
	other := *e
	ctx := context.Background()
	email := "core-other-" + uuid.NewString()[:6] + "@example.com"
	u, _ := e.ident.SignUp(ctx, email, "correct horse battery staple 9", "O")
	tok, _, _ := e.ident.Login(ctx, email, "correct horse battery staple 9", "127.0.0.1", "t")
	org, _ := e.d.tenancy.CreateOrganization(ctx, u.ID, "Other "+email, uuid.NewString()[:10])
	other.token, other.org = tok, org.ID
	pid := proj["id"].(string)
	if st, _, _ := other.do(t, "GET", "/projects/"+pid, nil); st != 404 {
		t.Fatalf("cross-tenant read = %d, want 404", st)
	}
	if st, _, _ := other.do(t, "POST", "/projects/"+pid+"/provision", nil); st != 404 {
		t.Fatalf("cross-tenant provision = %d, want 404", st)
	}
	if st, _, _ := other.do(t, "DELETE", "/projects/"+pid, nil); st != 202 && st != 404 {
		t.Fatalf("cross-tenant delete = %d", st)
	}
	// A tenant header for an org the user does not belong to is refused.
	spoof := *e
	spoof.org = org.ID
	if st, _, _ := spoof.do(t, "GET", "/projects", nil); st != 403 && st != 404 {
		t.Fatalf("org spoofing = %d, want 403/404", st)
	}
}

func TestInfraAPI_DomainsSSLBackups(t *testing.T) {
	e, _ := newCoreEnv(t, "infra-api-"+uuid.NewString()[:6]+"@example.com")
	ctx := context.Background()

	st, dom, _ := e.do(t, "POST", "/domains", map[string]string{"name": "Shop.Example.com"})
	if st != 201 || dom["name"] != "shop.example.com" {
		t.Fatalf("add domain: %d %v", st, dom)
	}
	did := dom["id"].(string)
	if st, _, _ := e.do(t, "POST", "/domains", map[string]string{"name": "not a domain"}); st != 400 {
		t.Fatalf("invalid domain = %d", st)
	}
	if st, _, _ := e.do(t, "POST", "/domains/"+did+"/records", map[string]any{"type": "A", "name": "@", "value": "198.51.100.7"}); st != 200 {
		t.Fatalf("add record = %d", st)
	}
	if st, _, _ := e.do(t, "POST", "/domains/"+did+"/records", map[string]any{"type": "A", "name": "@", "value": "nope"}); st != 400 {
		t.Fatalf("bad record = %d", st)
	}
	st, ref, _ := e.do(t, "POST", "/domains/"+did+"/certificate", nil)
	if st != 202 {
		t.Fatalf("issue = %d %v", st, ref)
	}
	e.worker.RunOnce(ctx)
	st, page, _ := e.do(t, "GET", "/certificates", nil)
	items, _ := page["items"].([]any)
	if st != 200 || len(items) != 1 || items[0].(map[string]any)["status"] != "valid" {
		t.Fatalf("certificates: %d %v", st, page)
	}
	if st, _, raw := e.do(t, "GET", "/certificates", nil); st != 200 || bytes.Contains(raw, []byte("PRIVATE KEY")) {
		t.Fatal("private key material must never be returned by the API")
	}

	// Removing a domain is a gateway action: 202 + approval, domain still there.
	st, del, _ := e.do(t, "DELETE", "/domains/"+did, nil)
	if st != 202 || del["approval_id"] == nil {
		t.Fatalf("remove domain: %d %v", st, del)
	}
	if st, _, _ := e.do(t, "GET", "/domains/"+did, nil); st != 200 {
		t.Fatal("domain must remain until approval")
	}
	if st, _, _ := e.do(t, "GET", "/backups/"+uuid.NewString(), nil); st != 404 {
		t.Fatalf("unknown backup = %d", st)
	}
}

func (e *coreEnv) raw(t *testing.T, method, path string, body []byte) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, e.srv.URL+"/api/v1"+path, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+e.token)
	req.Header.Set("X-Nodera-Org", e.org.String())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return resp.StatusCode, m
}

func TestDeliveryAPI_DeployMigrateWordPress(t *testing.T) {
	e, _ := newCoreEnv(t, "delivery-api-"+uuid.NewString()[:6]+"@example.com")
	ctx := context.Background()
	_, proj, _ := e.do(t, "POST", "/projects", map[string]any{"name": "Delivery", "kind": "wordpress"})
	pid := proj["id"].(string)
	e.do(t, "POST", "/projects/"+pid+"/provision", nil)
	e.worker.RunOnce(ctx)

	// staging deploy runs; production deploy asks for approval.
	files := map[string]string{"index.php": base64.StdEncoding.EncodeToString([]byte("hi"))}
	st, ref, _ := e.do(t, "POST", "/projects/"+pid+"/deployments", map[string]any{"source": "upload", "environment": "staging", "files": files})
	if st != 202 {
		t.Fatalf("staging deploy: %d %v", st, ref)
	}
	e.worker.RunOnce(ctx)
	st, page, _ := e.do(t, "GET", "/deployments?project_id="+pid, nil)
	items, _ := page["items"].([]any)
	if st != 200 || len(items) != 1 || items[0].(map[string]any)["status"] != "succeeded" {
		t.Fatalf("deployments: %d %v", st, page)
	}
	st, prod, _ := e.do(t, "POST", "/projects/"+pid+"/deployments", map[string]any{"source": "upload", "files": files})
	if st != 202 || prod["approval_id"] == nil {
		t.Fatalf("production deploy must need approval: %d %v", st, prod)
	}
	if st, _, _ := e.do(t, "POST", "/projects/"+pid+"/deployments", map[string]any{"source": "ftp", "environment": "staging"}); st != 400 {
		t.Fatalf("invalid source = %d", st)
	}

	// migration: create -> upload zip -> plan.
	st, mig, _ := e.do(t, "POST", "/migrations", map[string]any{"project_id": pid, "source_kind": "zip", "target_domain": "moved.example.org"})
	if st != 201 {
		t.Fatalf("create migration: %d %v", st, mig)
	}
	mid := mig["id"].(string)
	var zb bytes.Buffer
	zw := zip.NewWriter(&zb)
	for n, c := range map[string]string{"wp-config.php": "<?php", "wp-includes/version.php": "<?php $wp_version='6.5';", "index.php": "x", "database.sql": "INSERT INTO t VALUES ('http://o.example.com');"} {
		w, _ := zw.Create(n)
		w.Write([]byte(c))
	}
	zw.Close()
	if st, _ := e.raw(t, "PUT", "/migrations/"+mid+"/source", zb.Bytes()); st != 200 {
		t.Fatalf("upload source = %d", st)
	}
	if st, _ := e.raw(t, "PUT", "/migrations/"+mid+"/source", []byte("not a zip")); st != 400 {
		t.Fatalf("non-zip upload = %d, want 400", st)
	}
	if st, _, _ := e.do(t, "POST", "/migrations/"+mid+"/plan", nil); st != 202 {
		t.Fatalf("plan = %d", st)
	}
	e.worker.RunOnce(ctx)
	st, got, _ := e.do(t, "GET", "/migrations/"+mid, nil)
	if st != 200 || got["status"] != "planned" {
		t.Fatalf("migration after plan: %d %v", st, got)
	}
	st, cut, _ := e.do(t, "POST", "/migrations/"+mid+"/cutover", nil)
	if st != 202 && st != 409 {
		t.Fatalf("cutover request = %d %v", st, cut)
	}

	// wordpress health
	st, health, _ := e.do(t, "GET", "/projects/"+pid+"/wordpress/health", nil)
	if st != 200 || health["checks"] == nil {
		t.Fatalf("health: %d %v", st, health)
	}
	if st, _, _ := e.do(t, "POST", "/projects/"+pid+"/wordpress/update", map[string]string{"image_tag": "latest; rm"}); st != 400 {
		t.Fatalf("bad image tag = %d", st)
	}
}

func TestObserveAPI_MonitorsIncidentsNotificationsLogs(t *testing.T) {
	e, _ := newCoreEnv(t, "observe-api-"+uuid.NewString()[:6]+"@example.com")
	st, mon, _ := e.do(t, "POST", "/monitors", map[string]any{"kind": "http", "name": "Home", "target": "https://home.example.com"})
	if st != 201 {
		t.Fatalf("create monitor: %d %v", st, mon)
	}
	if st, _, _ := e.do(t, "POST", "/monitors", map[string]any{"kind": "http", "name": "Bad", "target": "javascript:alert(1)"}); st != 400 {
		t.Fatalf("bad target = %d", st)
	}
	st, run, _ := e.do(t, "POST", "/monitors/"+mon["id"].(string)+"/run", nil)
	if st != 200 || run["last_status"] != "ok" {
		t.Fatalf("run monitor: %d %v", st, run)
	}
	st, rule, _ := e.do(t, "POST", "/alert-rules", map[string]any{"name": "Down", "condition": "http_failure", "severity": "critical"})
	if st != 201 {
		t.Fatalf("create rule: %d %v", st, rule)
	}
	if st, _, _ := e.do(t, "POST", "/incidents/"+uuid.NewString()+"/acknowledge", nil); st != 404 {
		t.Fatalf("unknown incident = %d", st)
	}
	if st, _, _ := e.do(t, "POST", "/incidents/"+uuid.NewString()+"/explode", nil); st != 400 && st != 404 {
		t.Fatalf("bad action = %d", st)
	}
	st, cnt, _ := e.do(t, "GET", "/notifications/unread-count", nil)
	if st != 200 || cnt["unread"] == nil {
		t.Fatalf("unread-count: %d %v", st, cnt)
	}
	if st, _, _ := e.do(t, "POST", "/notification-channels", map[string]any{"kind": "webhook", "name": "evil", "target": "http://169.254.169.254/"}); st != 400 {
		t.Fatalf("SSRF webhook = %d, want 400", st)
	}
	if st, page, _ := e.do(t, "GET", "/logs?level=error&limit=5", nil); st != 200 || page["items"] == nil {
		t.Fatalf("logs: %d %v", st, page)
	}
	if st, _, _ := e.do(t, "PUT", "/retention/audit_log", map[string]int{"retention_days": 3}); st != 400 {
		t.Fatalf("audit retention too short = %d", st)
	}
	if st, _, _ := e.do(t, "PUT", "/retention/log_entries", map[string]int{"retention_days": 14}); st != 204 {
		t.Fatalf("set retention = %d", st)
	}
}
