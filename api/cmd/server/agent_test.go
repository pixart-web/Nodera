package main

// End-to-end tests for the Node Agent channel: a real agent.Agent enrols
// against the real router (httptest) and executes signed commands through a
// mock container provider. Security-negative cases (replay, tampering,
// revocation, token reuse, allowlist, cross-tenant, RBAC) are covered here.

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nodera/nodera/internal/audit"
	"github.com/nodera/nodera/internal/identity"
	"github.com/nodera/nodera/internal/nodeagent"
	"github.com/nodera/nodera/internal/nodeagent/agent"
	"github.com/nodera/nodera/internal/nodeagent/protocol"
	"github.com/nodera/nodera/internal/platform/authctx"
	"github.com/nodera/nodera/internal/platform/logger"
	"github.com/nodera/nodera/internal/platformauth"
	"github.com/nodera/nodera/internal/providers/mock"
	"github.com/nodera/nodera/internal/rbac"
	"github.com/nodera/nodera/internal/tenancy"
	"github.com/nodera/nodera/internal/testhelpers"
)

type agentEnv struct {
	srv  *httptest.Server
	ctx  context.Context
	pool *pgxpool.Pool
}

func newAgentEnv(t *testing.T) (*agentEnv, *nodeagent.Service, *identity.Service, *tenancy.Service, func(string, ...any)) {
	t.Helper()
	pool := testhelpers.RequirePool(t)
	auditSvc := audit.New(pool)
	rbacSvc := rbac.New(pool, auditSvc)
	identitySvc := identity.New(pool, rbacSvc, 24*time.Hour, auditSvc)
	tenancySvc := tenancy.New(pool, identitySvc, auditSvc)
	platformSvc := platformauth.New(pool, auditSvc)
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	svc := nodeagent.New(pool, auditSvc, key)
	deps := apiDeps{
		log: logger.New("test"), identity: identitySvc, tenancy: tenancySvc, audit: auditSvc,
		rbac: rbacSvc, platform: platformSvc, pool: pool, sessionTTL: 24 * time.Hour,
		nodeagent: svc,
	}
	srv := httptest.NewServer(newRouter(deps))
	t.Cleanup(srv.Close)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
			t.Fatalf("exec %q: %v", sql, err)
		}
	}
	return &agentEnv{srv: srv, ctx: context.Background(), pool: pool}, svc, identitySvc, tenancySvc, exec
}

func ownerOf(t *testing.T, ident *identity.Service, ten *tenancy.Service, email string) authctx.AuthContext {
	t.Helper()
	ctx := context.Background()
	u, err := ident.SignUp(ctx, email, "correct horse battery staple 9", "Agent Test")
	if err != nil {
		t.Fatalf("signup: %v", err)
	}
	tok, _, err := ident.Login(ctx, email, "correct horse battery staple 9", "127.0.0.1", "t")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	org, err := ten.CreateOrganization(ctx, u.ID, "Org "+email, uuid.NewString()[:12])
	if err != nil {
		t.Fatalf("org: %v", err)
	}
	ac, err := ident.AuthContextForSession(ctx, tok, org.ID, "corr")
	if err != nil {
		t.Fatalf("authctx: %v", err)
	}
	return ac
}

func insertNode(t *testing.T, exec func(string, ...any), orgID uuid.UUID) uuid.UUID {
	id := uuid.New()
	exec(`INSERT INTO nodes (id, organization_id, hostname, provider) VALUES ($1,$2,$3,'local')`, id, orgID, "n-"+id.String()[:8])
	return id
}

func enrolled(t *testing.T, env *agentEnv, svc *nodeagent.Service, ac authctx.AuthContext, node uuid.UUID) (*agent.Agent, agent.State, *mock.Handles) {
	t.Helper()
	reg, err := svc.CreateRegistration(env.ctx, ac, node)
	if err != nil {
		t.Fatalf("registration: %v", err)
	}
	st, err := agent.Enroll(env.ctx, env.srv.URL, reg.Token, nil)
	if err != nil {
		t.Fatalf("enroll: %v", err)
	}
	set, h := mock.NewSet()
	a, err := agent.New(st, set, nil)
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}
	return a, st, h
}

func signedPost(st agent.State, base, path string, body []byte, nonce string, now time.Time) (*http.Response, error) {
	seed, _ := base64.StdEncoding.DecodeString(st.PrivateSeedB64)
	priv := ed25519.NewKeyFromSeed(seed)
	req, _ := http.NewRequest(http.MethodPost, base+path, bytes.NewReader(body))
	for k, v := range protocol.SignRequest(priv, st.AgentID, http.MethodPost, path, body, now, nonce) {
		req.Header.Set(k, v)
	}
	req.Header.Set("Content-Type", "application/json")
	return http.DefaultClient.Do(req)
}

func TestAgentEnrolHeartbeatAndSignedCommand(t *testing.T) {
	env, svc, ident, ten, exec := newAgentEnv(t)
	ac := ownerOf(t, ident, ten, "agent-owner-"+uuid.NewString()[:6]+"@example.com")
	node := insertNode(t, exec, ac.OrganizationID)
	a, st, h := enrolled(t, env, svc, ac, node)

	// Token is single use.
	node2 := insertNode(t, exec, ac.OrganizationID)
	reg2, _ := svc.CreateRegistration(env.ctx, ac, node2)
	if _, err := agent.Enroll(env.ctx, env.srv.URL, reg2.Token, nil); err != nil {
		t.Fatalf("fresh token should enrol: %v", err)
	}
	// One live agent per node: a fresh token for an already-enrolled node is refused.
	reg3, _ := svc.CreateRegistration(env.ctx, ac, node)
	if _, err := agent.Enroll(env.ctx, env.srv.URL, reg3.Token, nil); err == nil {
		t.Fatal("second agent on the same node must be refused until the first is revoked")
	}
	if _, err := agent.Enroll(env.ctx, env.srv.URL, reg2.Token, nil); err == nil {
		t.Fatal("reused enrolment token must be rejected")
	}

	if err := a.Step(env.ctx); err != nil {
		t.Fatalf("step: %v", err)
	}
	var status string
	if err := env.pool.QueryRow(env.ctx, `SELECT status FROM nodes WHERE id=$1`, node).Scan(&status); err != nil || status != "online" {
		t.Fatalf("node status = %q err=%v, want online", status, err)
	}

	id, _ := uuid.Parse(st.AgentID)
	q, err := svc.EnqueueAs(env.ctx, ac, id, "docker.create", map[string]any{"name": "web1", "image": "nginx:1.27"})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := a.Step(env.ctx); err != nil {
		t.Fatalf("step 2: %v", err)
	}
	res, err := svc.WaitResult(env.ctx, ac.OrganizationID, q.ID, 3*time.Second)
	if err != nil || !res.OK {
		t.Fatalf("command result = %+v err=%v", res, err)
	}
	if _, ok := h.Containers.M["web1"]; !ok {
		t.Fatal("container was not created through the agent")
	}
}

func TestAgentRejectsDisallowedAndUnsafeCommands(t *testing.T) {
	env, svc, ident, ten, exec := newAgentEnv(t)
	ac := ownerOf(t, ident, ten, "agent-allow-"+uuid.NewString()[:6]+"@example.com")
	node := insertNode(t, exec, ac.OrganizationID)
	_, st, _ := enrolled(t, env, svc, ac, node)
	id, _ := uuid.Parse(st.AgentID)

	for name, c := range map[string]struct {
		op     string
		params map[string]any
	}{
		"shell":          {"shell.exec", map[string]any{"cmd": "id"}},
		"traversal":      {"filesystem.read", map[string]any{"path": "../../etc/passwd"}},
		"absolute":       {"filesystem.write", map[string]any{"path": "/etc/cron.d/x"}},
		"image injected": {"docker.create", map[string]any{"name": "a", "image": "--privileged"}},
		"bad name":       {"docker.stop", map[string]any{"name": "a;rm -rf /"}},
	} {
		if _, err := svc.EnqueueAs(env.ctx, ac, id, c.op, c.params); err == nil {
			t.Errorf("%s: expected rejection at enqueue", name)
		}
	}
}

func TestAgentReplayTamperAndRevocation(t *testing.T) {
	env, svc, ident, ten, exec := newAgentEnv(t)
	ac := ownerOf(t, ident, ten, "agent-replay-"+uuid.NewString()[:6]+"@example.com")
	node := insertNode(t, exec, ac.OrganizationID)
	_, st, _ := enrolled(t, env, svc, ac, node)

	hb, _ := json.Marshal(protocol.Heartbeat{Version: "x", Status: "online"})
	now := time.Now()
	nonce := uuid.NewString()
	r1, err := signedPost(st, env.srv.URL, "/agent/v1/heartbeat", hb, nonce, now)
	if err != nil || r1.StatusCode != http.StatusNoContent {
		t.Fatalf("first request: %v %v", r1, err)
	}
	r1.Body.Close()
	r2, _ := signedPost(st, env.srv.URL, "/agent/v1/heartbeat", hb, nonce, now)
	if r2.StatusCode == http.StatusNoContent {
		t.Fatal("replayed nonce must be rejected")
	}
	r2.Body.Close()

	// Stale timestamp.
	r3, _ := signedPost(st, env.srv.URL, "/agent/v1/heartbeat", hb, uuid.NewString(), now.Add(-10*time.Minute))
	if r3.StatusCode == http.StatusNoContent {
		t.Fatal("stale timestamp must be rejected")
	}
	r3.Body.Close()

	// Tampered body: sign one body, send another.
	seed, _ := base64.StdEncoding.DecodeString(st.PrivateSeedB64)
	priv := ed25519.NewKeyFromSeed(seed)
	req, _ := http.NewRequest(http.MethodPost, env.srv.URL+"/agent/v1/heartbeat", bytes.NewReader([]byte(`{"version":"evil"}`)))
	for k, v := range protocol.SignRequest(priv, st.AgentID, http.MethodPost, "/agent/v1/heartbeat", hb, now, uuid.NewString()) {
		req.Header.Set(k, v)
	}
	r4, _ := http.DefaultClient.Do(req)
	if r4.StatusCode == http.StatusNoContent {
		t.Fatal("tampered body must be rejected")
	}
	r4.Body.Close()

	// Unsigned.
	r5, _ := http.Post(env.srv.URL+"/agent/v1/poll", "application/json", bytes.NewReader([]byte("{}")))
	if r5.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unsigned poll = %d, want 401", r5.StatusCode)
	}
	r5.Body.Close()

	// Revocation locks the agent out.
	id, _ := uuid.Parse(st.AgentID)
	if err := svc.Revoke(env.ctx, ac, id); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	r6, _ := signedPost(st, env.srv.URL, "/agent/v1/heartbeat", hb, uuid.NewString(), time.Now())
	if r6.StatusCode == http.StatusNoContent {
		t.Fatal("revoked agent must be rejected")
	}
	r6.Body.Close()
}

func TestAgentExpiredTokenAndTenantAndRBAC(t *testing.T) {
	env, svc, ident, ten, exec := newAgentEnv(t)
	acA := ownerOf(t, ident, ten, "agent-a-"+uuid.NewString()[:6]+"@example.com")
	acB := ownerOf(t, ident, ten, "agent-b-"+uuid.NewString()[:6]+"@example.com")
	nodeA := insertNode(t, exec, acA.OrganizationID)

	// Expired token.
	reg, _ := svc.CreateRegistration(env.ctx, acA, nodeA)
	exec(`UPDATE node_agent_registrations SET expires_at = now() - interval '1 minute' WHERE node_id = $1`, nodeA)
	if _, err := agent.Enroll(env.ctx, env.srv.URL, reg.Token, nil); err == nil {
		t.Fatal("expired token must be rejected")
	}

	// Cross-tenant: org B cannot mint a token for org A's node nor command its agents.
	if _, err := svc.CreateRegistration(env.ctx, acB, nodeA); err == nil {
		t.Fatal("cross-tenant registration must fail")
	}
	_, st, _ := enrolled(t, env, svc, acA, nodeA)
	id, _ := uuid.Parse(st.AgentID)
	if _, err := svc.EnqueueAs(env.ctx, acB, id, "metrics.collect", map[string]any{}); err == nil {
		t.Fatal("cross-tenant enqueue must fail")
	}
	if err := svc.Revoke(env.ctx, acB, id); err == nil {
		t.Fatal("cross-tenant revoke must fail")
	}
	if list, _ := svc.List(env.ctx, acB); len(list) != 0 {
		t.Fatal("cross-tenant list leaked agents")
	}

	// RBAC: a read-only member cannot manage agents.
	u, err := ident.SignUp(env.ctx, "agent-m-"+uuid.NewString()[:6]+"@example.com", "correct horse battery staple 9", "M")
	if err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO organization_members (organization_id, user_id) VALUES ($1,$2)`, acA.OrganizationID, u.ID)
	exec(`INSERT INTO organization_member_roles (organization_id, user_id, role_id) VALUES ($1,$2,'00000000-0000-0000-0000-000000000003')`, acA.OrganizationID, u.ID)
	member := acA
	member.ActorID = u.ID
	member.Permissions = nil
	if _, err := svc.CreateRegistration(env.ctx, member, nodeA); err == nil {
		t.Fatal("member without infrastructure.manage must be denied")
	}
}

func mustUUID(s string) uuid.UUID { id, _ := uuid.Parse(s); return id }

func nowUTC() time.Time { return time.Now().UTC() }

func ed25519FromState(st agent.State) ed25519.PrivateKey {
	seed, _ := base64.StdEncoding.DecodeString(st.PrivateSeedB64)
	return ed25519.NewKeyFromSeed(seed)
}
