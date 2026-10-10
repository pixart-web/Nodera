package main

// Security regression suite (spec: SQLi, XSS, CSRF, SSRF, IDOR, cross-tenant,
// permission escalation, secret leakage, path traversal, command injection,
// replay, invalid agent requests). Each test drives the real HTTP router.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/nodera/nodera/internal/nodeagent/protocol"
	"github.com/nodera/nodera/internal/platform/authctx"
)

func memberOf(t *testing.T, e *coreEnv) *coreEnv {
	t.Helper()
	ctx := context.Background()
	email := "sec-member-" + uuid.NewString()[:6] + "@example.com"
	u, err := e.ident.SignUp(ctx, email, "correct horse battery staple 9", "M")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.d.pool.Exec(ctx, `INSERT INTO organization_members (organization_id, user_id) VALUES ($1,$2)`, e.org, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.d.pool.Exec(ctx, `INSERT INTO organization_member_roles (organization_id, user_id, role_id) VALUES ($1,$2,'00000000-0000-0000-0000-000000000003')`, e.org, u.ID); err != nil {
		t.Fatal(err)
	}
	tok, _, err := e.ident.Login(ctx, email, "correct horse battery staple 9", "127.0.0.1", "t")
	if err != nil {
		t.Fatal(err)
	}
	m := *e
	m.token = tok
	return &m
}

func strangerOf(t *testing.T, e *coreEnv) *coreEnv {
	t.Helper()
	ctx := context.Background()
	email := "sec-stranger-" + uuid.NewString()[:6] + "@example.com"
	u, _ := e.ident.SignUp(ctx, email, "correct horse battery staple 9", "S")
	tok, _, _ := e.ident.Login(ctx, email, "correct horse battery staple 9", "127.0.0.1", "t")
	org, err := e.d.tenancy.CreateOrganization(ctx, u.ID, "Stranger "+email, uuid.NewString()[:10])
	if err != nil {
		t.Fatal(err)
	}
	s := *e
	s.token, s.org = tok, org.ID
	return &s
}

func TestSecurity_SQLInjectionPayloadsAreInert(t *testing.T) {
	e, _ := newCoreEnv(t, "sec-sqli-"+uuid.NewString()[:6]+"@example.com")
	stranger := strangerOf(t, e)
	e.do(t, "POST", "/projects", map[string]any{"name": "Victim Site", "kind": "wordpress"})
	stranger.do(t, "POST", "/projects", map[string]any{"name": "Stranger Secret", "kind": "wordpress"})

	payloads := []string{`' OR '1'='1`, `'; DROP TABLE projects; --`, `" OR ""="`, `%' UNION SELECT id,name,email,1,1 FROM users --`, `\`, `'||(SELECT pg_sleep(5))||'`}
	paths := []string{"/projects?q=%s", "/search?q=%s", "/logs?q=%s", "/domains?q=%s", "/clients?q=%s", "/projects?status=%s", "/projects?kind=%s", "/logs?level=%s&source=%s"}
	for _, p := range payloads {
		for _, tmpl := range paths {
			esc := strings.NewReplacer(" ", "%20", "'", "%27", `"`, "%22", "%", "%25", ";", "%3B", "|", "%7C", "\\", "%5C", "(", "%28", ")", "%29", ",", "%2C", "#", "%23", "&", "%26").Replace(p)
			path := strings.ReplaceAll(tmpl, "%s", esc)
			st, body, raw := e.do(t, "GET", path, nil)
			if st >= 500 {
				t.Fatalf("%s -> %d (server error on hostile input): %s", path, st, raw)
			}
			if items, ok := body["items"].([]any); ok {
				for _, it := range items {
					if strings.Contains(fmt.Sprint(it), "Stranger Secret") {
						t.Fatalf("%s leaked another tenant's data", path)
					}
				}
			}
			if hits, ok := body["hits"].([]any); ok {
				for _, h := range hits {
					if strings.Contains(fmt.Sprint(h), "Stranger Secret") {
						t.Fatalf("%s leaked another tenant's data via search", path)
					}
				}
			}
		}
	}
	// Hostile values in body fields are stored as plain data, and the tables survive.
	st, p, _ := e.do(t, "POST", "/projects", map[string]any{"name": `x'); DROP TABLE projects;--`, "kind": "wordpress"})
	if st != 201 {
		t.Fatalf("hostile name should be accepted as data, got %d %v", st, p)
	}
	if st, page, _ := e.do(t, "GET", "/projects", nil); st != 200 || len(page["items"].([]any)) != 2 {
		t.Fatalf("projects table damaged: %d %v", st, page)
	}
}

func TestSecurity_XSSPayloadsAreReturnedAsInertJSON(t *testing.T) {
	e, _ := newCoreEnv(t, "sec-xss-"+uuid.NewString()[:6]+"@example.com")
	payload := `<script>alert(1)</script><img src=x onerror=alert(1)>`
	req, _ := http.NewRequest("POST", e.srv.URL+"/api/v1/clients", strings.NewReader(`{"name":`+mustJSON(payload)+`}`))
	req.Header.Set("Authorization", "Bearer "+e.token)
	req.Header.Set("X-Nodera-Org", e.org.String())
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 201 {
		t.Fatalf("create: %d %s", resp.StatusCode, raw)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("content type %q", ct)
	}
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("nosniff header missing: a browser could sniff the JSON as HTML")
	}
	if bytes.Contains(raw, []byte("<script>")) {
		t.Fatalf("raw markup in a JSON response: %s", raw)
	}
	if resp.Header.Get("Content-Security-Policy") == "" && resp.Header.Get("X-Frame-Options") == "" {
		t.Log("note: security headers present are", resp.Header)
	}
}

func mustJSON(s string) string { b, _ := json.Marshal(s); return string(b) }

func TestSecurity_CSRFProtectsNewMutatingRoutes(t *testing.T) {
	e, _ := newCoreEnv(t, "sec-csrf-"+uuid.NewString()[:6]+"@example.com")
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	email := "sec-csrf-cookie-" + uuid.NewString()[:6] + "@example.com"
	signupAndLogin(t, client, e.srv.URL, email)
	// Create an org via the cookie session (with CSRF) so org-scoped routes are reachable.
	csrf := csrfCookieValue(client, e.srv.URL)
	b, _ := json.Marshal(map[string]string{"name": "CSRF Org", "slug": "csrf-" + uuid.NewString()[:8]})
	req, _ := http.NewRequest("POST", e.srv.URL+"/api/v1/organizations", bytes.NewReader(b))
	req.Header.Set("X-CSRF-Token", csrf)
	req.Header.Set("Content-Type", "application/json")
	resp, _ := client.Do(req)
	var org map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&org)
	resp.Body.Close()
	oid, _ := org["id"].(string)
	if oid == "" {
		t.Fatalf("org creation failed: %v", org)
	}
	for _, p := range []string{"/projects", "/clients", "/domains", "/monitors", "/ai/plans", "/migrations"} {
		req, _ := http.NewRequest("POST", e.srv.URL+"/api/v1"+p, strings.NewReader(`{}`))
		req.Header.Set("X-Nodera-Org", oid)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req) // cookie attached automatically, CSRF header deliberately absent
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 403 {
			t.Errorf("POST %s with cookie but no CSRF header = %d, want 403", p, resp.StatusCode)
		}
	}
}

func TestSecurity_IDORAndCrossTenantAcrossEveryResource(t *testing.T) {
	e, _ := newCoreEnv(t, "sec-idor-"+uuid.NewString()[:6]+"@example.com")
	stranger := strangerOf(t, e)
	ctx := context.Background()

	_, cl, _ := e.do(t, "POST", "/clients", map[string]string{"name": "Owned Client"})
	_, pr, _ := e.do(t, "POST", "/projects", map[string]any{"name": "Owned", "kind": "wordpress"})
	pid := pr["id"].(string)
	e.do(t, "POST", "/projects/"+pid+"/provision", nil)
	e.worker.RunOnce(ctx)
	_, dom, _ := e.do(t, "POST", "/domains", map[string]string{"name": "owned.example.com"})
	_, _, _ = e.do(t, "POST", "/domains/"+dom["id"].(string)+"/certificate", nil)
	e.worker.RunOnce(ctx)
	_, mon, _ := e.do(t, "POST", "/monitors", map[string]any{"kind": "http", "name": "Owned Mon", "target": "https://owned.example.com"})
	_, bk, _ := e.do(t, "POST", "/projects/"+pid+"/backups", map[string]any{"type": "configuration"})
	e.worker.RunOnce(ctx)
	_, mig, _ := e.do(t, "POST", "/migrations", map[string]any{"source_kind": "zip", "target_domain": "owned-mig.example.org"})
	_, _, certsRaw := e.do(t, "GET", "/certificates", nil)
	var certs struct{ Items []map[string]any }
	_ = json.Unmarshal(certsRaw, &certs)
	_, backs, _ := e.do(t, "GET", "/backups", nil)
	bid := backs["items"].([]any)[0].(map[string]any)["id"].(string)
	_ = bk

	gets := []string{
		"/clients/" + cl["id"].(string), "/projects/" + pid, "/projects/" + pid + "/overview", "/projects/" + pid + "/operations",
		"/domains/" + dom["id"].(string), "/domains/" + dom["id"].(string) + "/records", "/backups/" + bid,
		"/migrations/" + mig["id"].(string), "/projects/" + pid + "/backup-policies", "/projects/" + pid + "/wordpress/health",
		"/projects/" + pid + "/container-logs", "/projects/" + pid + "/databases", "/projects/" + pid + "/applications", "/projects/" + pid + "/containers", "/operations/" + uuid.NewString(),
	}
	if len(certs.Items) > 0 {
		gets = append(gets, "/certificates/"+certs.Items[0]["id"].(string))
	}
	for _, p := range gets {
		if st, _, raw := stranger.do(t, "GET", p, nil); st == 200 {
			t.Errorf("IDOR: stranger read %s -> 200 %s", p, truncateStr(string(raw)))
		}
	}
	// Writes and operations against another tenant's objects.
	writes := []struct{ m, p string }{
		{"PUT", "/clients/" + cl["id"].(string)}, {"DELETE", "/clients/" + cl["id"].(string)},
		{"PUT", "/projects/" + pid}, {"DELETE", "/projects/" + pid}, {"POST", "/projects/" + pid + "/provision"},
		{"POST", "/projects/" + pid + "/backups"}, {"POST", "/projects/" + pid + "/deployments"},
		{"POST", "/projects/" + pid + "/wordpress/clone"}, {"POST", "/projects/" + pid + "/wordpress/update"},
		{"DELETE", "/domains/" + dom["id"].(string)}, {"POST", "/domains/" + dom["id"].(string) + "/records"},
		{"POST", "/domains/" + dom["id"].(string) + "/certificate/renew"}, {"POST", "/domains/" + dom["id"].(string) + "/sync"},
		{"POST", "/backups/" + bid + "/verify"}, {"POST", "/backups/" + bid + "/restore"}, {"DELETE", "/backups/" + bid},
		{"POST", "/migrations/" + mig["id"].(string) + "/plan"}, {"POST", "/migrations/" + mig["id"].(string) + "/cutover"},
		{"PATCH", "/monitors/" + mon["id"].(string)}, {"DELETE", "/monitors/" + mon["id"].(string)}, {"POST", "/monitors/" + mon["id"].(string) + "/run"},
	}
	for _, w := range writes {
		body := map[string]any{"name": "pwn", "kind": "wordpress", "type": "files", "value": "1.2.3.4", "image_tag": "6.6", "source": "upload", "enabled": false}
		if st, _, raw := stranger.do(t, w.m, w.p, body); st < 400 && st != 202 {
			t.Errorf("stranger %s %s -> %d %s", w.m, w.p, st, truncateStr(string(raw)))
		} else if st == 202 {
			// 202 is only acceptable when nothing was actually queued for the victim's object.
			var n int
			_ = e.d.pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE organization_id=$1`, stranger.org).Scan(&n)
			var approvals int
			_ = e.d.pool.QueryRow(ctx, `SELECT count(*) FROM approvals WHERE organization_id=$1 AND resource_id=$2`, stranger.org, strings.TrimPrefix(w.p[strings.LastIndex(w.p, "/")+1:], "")).Scan(&approvals)
			if n > 0 {
				t.Errorf("stranger %s %s queued %d jobs", w.m, w.p, n)
			}
		}
	}
	// The victim's data is intact.
	if st, got, _ := e.do(t, "GET", "/projects/"+pid, nil); st != 200 || got["name"] != "Owned" {
		t.Fatalf("victim project changed: %d %v", st, got)
	}
	// Header spoofing: stranger's token with the victim's org id.
	spoof := *stranger
	spoof.org = e.org
	if st, _, _ := spoof.do(t, "GET", "/projects", nil); st == 200 {
		t.Fatal("X-Nodera-Org spoofing granted access")
	}
}

func truncateStr(s string) string {
	if len(s) > 200 {
		return s[:200]
	}
	return s
}

func TestSecurity_MemberCannotEscalate(t *testing.T) {
	e, _ := newCoreEnv(t, "sec-priv-"+uuid.NewString()[:6]+"@example.com")
	member := memberOf(t, e)
	ctx := context.Background()
	_, pr, _ := e.do(t, "POST", "/projects", map[string]any{"name": "Priv", "kind": "wordpress"})
	pid := pr["id"].(string)
	e.do(t, "POST", "/projects/"+pid+"/provision", nil)
	e.worker.RunOnce(ctx)
	_, dom, _ := e.do(t, "POST", "/domains", map[string]string{"name": "priv.example.com"})
	_, backs, _ := e.do(t, "POST", "/projects/"+pid+"/backups", map[string]any{"type": "configuration"})
	_ = backs
	e.worker.RunOnce(ctx)
	_, bl, _ := e.do(t, "GET", "/backups", nil)
	bid := bl["items"].([]any)[0].(map[string]any)["id"].(string)
	did := dom["id"].(string)

	mutating := []struct{ m, p string }{
		{"POST", "/clients"}, {"POST", "/projects"}, {"PUT", "/projects/" + pid}, {"DELETE", "/projects/" + pid},
		{"POST", "/projects/" + pid + "/provision"}, {"POST", "/projects/" + pid + "/backups"}, {"PUT", "/projects/" + pid + "/backup-policies"},
		{"POST", "/backups/" + bid + "/verify"}, {"POST", "/backups/" + bid + "/restore"}, {"DELETE", "/backups/" + bid},
		{"POST", "/domains"}, {"DELETE", "/domains/" + did}, {"POST", "/domains/" + did + "/records"}, {"POST", "/domains/" + did + "/certificate"},
		{"POST", "/projects/" + pid + "/deployments"}, {"POST", "/projects/" + pid + "/wordpress/clone"}, {"POST", "/projects/" + pid + "/wordpress/update"},
		{"POST", "/migrations"}, {"POST", "/monitors"}, {"POST", "/alert-rules"}, {"POST", "/notification-channels"},
		{"PUT", "/retention/log_entries"}, {"PUT", "/feature-flags/migration_engine"},
		{"POST", "/infrastructure/nodes/" + uuid.NewString() + "/agent-registrations"}, {"POST", "/infrastructure/agents/" + uuid.NewString() + "/commands"},
	}
	for _, m := range mutating {
		body := map[string]any{"name": "x", "kind": "wordpress", "type": "files", "value": "1.2.3.4", "source": "upload", "enabled": true, "retention_days": 10, "op": "docker.start", "schedule": "daily"}
		st, _, raw := member.do(t, m.m, m.p, body)
		if st != 403 {
			t.Errorf("member %s %s -> %d (want 403): %s", m.m, m.p, st, truncateStr(string(raw)))
		}
	}
	// Reads that members legitimately have still work.
	for _, p := range []string{"/projects", "/clients", "/backups", "/domains", "/monitors", "/incidents", "/logs", "/dashboard", "/deployments", "/migrations", "/certificates"} {
		if st, _, raw := member.do(t, "GET", p, nil); st != 200 {
			t.Errorf("member GET %s -> %d %s", p, st, truncateStr(string(raw)))
		}
	}
}

func TestSecurity_NoSecretMaterialInAnyResponse(t *testing.T) {
	e, _ := newCoreEnv(t, "sec-leak-"+uuid.NewString()[:6]+"@example.com")
	ctx := context.Background()
	_, pr, _ := e.do(t, "POST", "/projects", map[string]any{"name": "Leaky", "kind": "wordpress"})
	pid := pr["id"].(string)
	_, ref, _ := e.do(t, "POST", "/projects/"+pid+"/provision", nil)
	e.worker.RunOnce(ctx)
	_, dom, _ := e.do(t, "POST", "/domains", map[string]string{"name": "leaky.example.com"})
	e.do(t, "POST", "/domains/"+dom["id"].(string)+"/certificate", nil)
	e.worker.RunOnce(ctx)
	e.do(t, "POST", "/migrations", map[string]any{"source_kind": "ftp", "target_domain": "leaky-mig.example.org", "source_config": map[string]any{"host": "ftp.example.org"}, "credentials": map[string]string{"password": "ftp-pass-LEAKTEST-9981"}})

	// The database password the provisioner generated must never be visible.
	var dbPass string
	_ = e.d.pool.QueryRow(ctx, `SELECT password_secret_key FROM project_databases LIMIT 1`).Scan(&dbPass)
	plain, err := e.secrets.Reveal(ctx, authctx.System(e.org), dbPass)
	if err != nil || plain == "" {
		t.Fatalf("could not read back the generated secret: %v", err)
	}
	needles := []string{plain, "ftp-pass-LEAKTEST-9981", "PRIVATE KEY", "password_hash", "token_hash"}
	paths := []string{"/projects", "/projects/" + pid, "/projects/" + pid + "/overview", "/operations/" + ref["job_id"].(string), "/operations/" + ref["job_id"].(string) + "/logs",
		"/operations/" + ref["job_id"].(string) + "/steps", "/domains", "/certificates", "/migrations", "/backups", "/logs", "/audit", "/dashboard", "/notifications", "/system/info"}
	for _, p := range paths {
		_, _, raw := e.do(t, "GET", p, nil)
		for _, n := range needles {
			if bytes.Contains(raw, []byte(n)) {
				t.Errorf("GET %s leaks %q", p, n)
			}
		}
	}
	// Errors never expose internals.
	_, _, raw := e.do(t, "GET", "/projects/"+uuid.NewString(), nil)
	for _, bad := range []string{"pq:", "pgx", "SQLSTATE", "goroutine", ".go:", "panic"} {
		if bytes.Contains(raw, []byte(bad)) {
			t.Errorf("error response leaks internals (%q): %s", bad, raw)
		}
	}
}

func TestSecurity_PathTraversalAndInjectionThroughTheAPI(t *testing.T) {
	e, _ := newCoreEnv(t, "sec-path-"+uuid.NewString()[:6]+"@example.com")
	ctx := context.Background()
	_, pr, _ := e.do(t, "POST", "/projects", map[string]any{"name": "Pathy", "kind": "wordpress"})
	pid := pr["id"].(string)
	e.do(t, "POST", "/projects/"+pid+"/provision", nil)
	e.worker.RunOnce(ctx)

	b64 := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
	for _, name := range []string{"../../etc/passwd", "/etc/passwd", "a/../../b", "a\\..\\b", "a//b", "\x00"} {
		st, _, raw := e.do(t, "POST", "/projects/"+pid+"/deployments", map[string]any{"source": "upload", "environment": "staging", "files": map[string]string{name: b64("x")}})
		if st != 400 {
			t.Errorf("upload path %q -> %d %s", name, st, truncateStr(string(raw)))
		}
	}
	for _, bad := range []string{"latest", "6.6; rm -rf /", "6.6$(id)", "../6.6", "6.6\n--privileged", "$(reboot)"} {
		if st, _, _ := e.do(t, "POST", "/projects/"+pid+"/wordpress/update", map[string]string{"image_tag": bad}); st != 400 {
			t.Errorf("image tag %q -> %d", bad, st)
		}
	}
	for _, bad := range []string{"main;id", "$(id)", "../../x", "main..", "-delete", "a b"} {
		if st, _, _ := e.do(t, "POST", "/projects/"+pid+"/deployments", map[string]any{"source": "github", "repository": "o/r", "ref": bad, "environment": "staging"}); st != 400 {
			t.Errorf("git ref %q -> %d", bad, st)
		}
	}
	for _, bad := range []string{"o/r;id", "o/r$(id)", "../r", "o/../r", "o r"} {
		if st, _, _ := e.do(t, "POST", "/projects/"+pid+"/deployments", map[string]any{"source": "github", "repository": bad, "environment": "staging"}); st != 400 {
			t.Errorf("repository %q -> %d", bad, st)
		}
	}
	for _, bad := range []string{"a b; rm -rf", "$(id)", "../../etc"} {
		if st, _, _ := e.do(t, "POST", "/monitors", map[string]any{"kind": "container", "name": "c-" + uuid.NewString()[:5], "target": bad}); st != 400 {
			t.Errorf("container target %q -> %d", bad, st)
		}
	}
}

// ---- node agent: invalid and hostile agent requests ----

func TestSecurity_AgentChannelRejectsHostileRequests(t *testing.T) {
	env, svc, ident, ten, exec := newAgentEnv(t)
	acA := ownerOf(t, ident, ten, "sec-agent-a-"+uuid.NewString()[:6]+"@example.com")
	acB := ownerOf(t, ident, ten, "sec-agent-b-"+uuid.NewString()[:6]+"@example.com")
	nodeA, nodeB := insertNode(t, exec, acA.OrganizationID), insertNode(t, exec, acB.OrganizationID)
	_, stA, _ := enrolled(t, env, svc, acA, nodeA)
	aB, stB, _ := enrolled(t, env, svc, acB, nodeB)
	idA, idB := mustUUID(stA.AgentID), mustUUID(stB.AgentID)

	// A command queued for agent A must be invisible to, and unreportable by, agent B.
	q, err := svc.EnqueueAs(env.ctx, acA, idA, "metrics.collect", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if err := aB.Step(env.ctx); err != nil {
		t.Fatalf("agent B step: %v", err)
	}
	body, _ := json.Marshal(protocol.CommandResult{OK: true, Result: json.RawMessage(`{"forged":true}`)})
	resp, _ := signedPost(stB, env.srv.URL, "/agent/v1/commands/"+q.ID.String()+"/result", body, uuid.NewString(), nowUTC())
	if resp.StatusCode < 400 {
		t.Fatalf("agent B reported a result for agent A's command: %d", resp.StatusCode)
	}
	resp.Body.Close()
	var status string
	_ = env.pool.QueryRow(env.ctx, `SELECT status FROM node_agent_commands WHERE id=$1`, q.ID).Scan(&status)
	if status != "pending" {
		t.Fatalf("command status = %q after forged report, want pending", status)
	}
	_ = idB

	// Oversized body, malformed nonce, wrong agent header, garbage signature.
	big := bytes.Repeat([]byte("a"), (1<<20)+10)
	resp, _ = signedPost(stA, env.srv.URL, "/agent/v1/heartbeat", big, uuid.NewString(), nowUTC())
	if resp.StatusCode < 400 {
		t.Fatalf("oversized body accepted: %d", resp.StatusCode)
	}
	resp.Body.Close()
	resp, _ = signedPost(stA, env.srv.URL, "/agent/v1/heartbeat", []byte(`{}`), "short", nowUTC())
	if resp.StatusCode != 401 {
		t.Fatalf("malformed nonce = %d, want 401", resp.StatusCode)
	}
	resp.Body.Close()
	for name, mutate := range map[string]func(*http.Request){
		"wrong agent": func(r *http.Request) { r.Header.Set(protocol.HeaderAgentID, uuid.NewString()) },
		"bad signature": func(r *http.Request) {
			r.Header.Set(protocol.HeaderSignature, base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 64)))
		},
		"no signature": func(r *http.Request) { r.Header.Del(protocol.HeaderSignature) },
		"agent B's id": func(r *http.Request) { r.Header.Set(protocol.HeaderAgentID, stB.AgentID) },
	} {
		req, _ := http.NewRequest("POST", env.srv.URL+"/agent/v1/poll", strings.NewReader(`{}`))
		for k, v := range protocol.SignRequest(ed25519FromState(stA), stA.AgentID, "POST", "/agent/v1/poll", []byte(`{}`), nowUTC(), uuid.NewString()) {
			req.Header.Set(k, v)
		}
		mutate(req)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 401 {
			t.Errorf("%s: %d, want 401", name, resp.StatusCode)
		}
	}
	// Enrolment with a guessed token is refused and rate limited, never an oracle.
	for i := 0; i < 3; i++ {
		b, _ := json.Marshal(protocol.EnrollRequest{Token: "ndr_enr_" + strings.Repeat("A", 43), PublicKeyB64: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))})
		resp, _ := http.Post(env.srv.URL+"/agent/v1/enroll", "application/json", bytes.NewReader(b))
		if resp.StatusCode != 401 {
			t.Errorf("guessed token = %d, want 401", resp.StatusCode)
		}
		resp.Body.Close()
	}
}
