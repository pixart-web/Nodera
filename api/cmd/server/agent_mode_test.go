package main

// ProviderMode "agent": the API holds no Docker socket and drives containers
// and the database through the local Node Agent. These tests run the real
// signed-command path (API service -> Postgres queue -> agent poll -> executor)
// with mock container/database backends and a real shared filesystem.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nodera/nodera/internal/nodeagent/agent"
	"github.com/nodera/nodera/internal/nodeagent/remote"
	"github.com/nodera/nodera/internal/platform/netpolicy"
	"github.com/nodera/nodera/internal/providers"
	"github.com/nodera/nodera/internal/providers/local"
	"github.com/nodera/nodera/internal/providers/mock"
)

func TestAgentModeEndToEnd(t *testing.T) {
	env, svc, ident, ten, exec := newAgentEnv(t)
	ac := ownerOf(t, ident, ten, "agent-mode-"+uuid.NewString()[:6]+"@example.com")
	node := insertNode(t, exec, ac.OrganizationID)

	reg, err := svc.CreateRegistration(env.ctx, ac, node)
	if err != nil {
		t.Fatal(err)
	}
	st, err := agent.Enroll(env.ctx, env.srv.URL, reg.Token, nil)
	if err != nil {
		t.Fatal(err)
	}
	// API and agent share ONE filesystem root (same absolute path), as in production.
	shared, err := local.NewSet(t.TempDir(), netpolicy.Policy{})
	if err != nil {
		t.Fatal(err)
	}
	set, h := mock.NewSet()
	set.FS = shared.FS
	ag, err := agent.New(st, set, nil)
	if err != nil {
		t.Fatal(err)
	}
	ag.SetGuard([]string{"traefik"})
	runCtx, stop := context.WithCancel(env.ctx)
	done := make(chan struct{})
	go func() { ag.Run(runCtx, 50*time.Millisecond, nil); close(done) }()
	t.Cleanup(func() { stop(); <-done })

	agentID := uuid.MustParse(st.AgentID)
	containers, db := remote.LocalNode(svc, agentID, shared.FS)
	ctx, cancel := context.WithTimeout(env.ctx, 30*time.Second)
	defer cancel()

	root, _ := shared.FS.AbsPath(".")
	_, created, err := containers.Create(ctx, providers.ContainerSpec{
		Name: "nodera-blog", Image: "wordpress:6", Env: map[string]string{"WORDPRESS_DB_PASSWORD": "super-secret-db-password"},
		Volumes: []providers.VolumeMount{{Source: root + "/projects/blog/data", Target: "/var/www/html"}},
	})
	if err != nil || !created {
		t.Fatalf("create through the agent: created=%v err=%v", created, err)
	}
	if _, ok := h.Containers.M["nodera-blog"]; !ok {
		t.Fatal("container was not created on the agent's backend")
	}

	// Secrets in command parameters must not remain in Postgres once finished.
	var params string
	if err := env.pool.QueryRow(ctx, `SELECT params::text FROM node_agent_commands WHERE op='docker.create' ORDER BY created_at DESC LIMIT 1`).Scan(&params); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(params, "super-secret-db-password") {
		t.Fatalf("container environment (secret) is still stored in node_agent_commands: %s", params)
	}

	// Database: ensure, dump (streams back through the shared directory), load.
	pw := "abcdefghijklmnop0123"
	if ok, err := db.EnsureDatabase(ctx, "wp_blog", "u_blog", pw); err != nil || !ok {
		t.Fatalf("ensure: %v %v", ok, err)
	}
	if err := env.pool.QueryRow(ctx, `SELECT params::text FROM node_agent_commands WHERE op='database.ensure' ORDER BY created_at DESC LIMIT 1`).Scan(&params); err != nil || strings.Contains(params, pw) {
		t.Fatalf("database password still stored in node_agent_commands: %v %s", err, params)
	}
	var dump strings.Builder
	if err := db.Dump(ctx, "wp_blog", &dump); err != nil {
		t.Fatalf("dump: %v", err)
	}
	if err := db.Load(ctx, "wp_blog", strings.NewReader(dump.String())); err != nil {
		t.Fatalf("load: %v", err)
	}
	if left, _ := shared.FS.List(ctx, ".tmp-db"); len(left) != 0 {
		t.Fatalf("temporary dump files must be removed: %v", left)
	}

	// Control plane protection through the full path.
	for _, name := range []string{"nodera-cp-api", "traefik"} {
		if err := containers.Remove(ctx, name); err == nil {
			t.Errorf("removing %q through the agent must be refused", name)
		}
	}
	if _, _, err := containers.Create(ctx, providers.ContainerSpec{Name: "x", Image: "alpine",
		Volumes: []providers.VolumeMount{{Source: "/var/run/docker.sock", Target: "/s"}}}); err == nil {
		t.Error("mounting the Docker socket must be refused")
	}
	if err := containers.Remove(ctx, "nodera-blog"); err != nil {
		t.Errorf("an ordinary workload can still be removed: %v", err)
	}
}

func TestAgentModeWithoutAgentFailsHonestly(t *testing.T) {
	env, svc, _, _, _ := newAgentEnv(t)
	shared, _ := local.NewSet(t.TempDir(), netpolicy.Policy{})
	containers, _ := remote.LocalNode(svc, uuid.New(), shared.FS) // an id that is not enrolled
	ctx, cancel := context.WithTimeout(env.ctx, 5*time.Second)
	defer cancel()
	if _, _, err := containers.Inspect(ctx, "x"); err == nil || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("a missing agent must produce a clear error, got %v", err)
	}
}
