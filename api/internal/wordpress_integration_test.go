package integration_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nodera/nodera/internal/ops"
	"github.com/nodera/nodera/internal/platform/authctx"
	"github.com/nodera/nodera/internal/projects"
	"github.com/nodera/nodera/internal/provisioning"
	"github.com/nodera/nodera/internal/wordpress"
)

func TestWordPress_CloneUpdateHealth(t *testing.T) {
	e, ctx := newMigEnv(t)
	ac, _ := e.h.newOwnerContext(t, ctx, "wp-ok@nodera.dev")
	other, _ := e.h.newOwnerContext(t, ctx, "wp-other@nodera.dev")
	member := e.h.newMemberContext(t, ctx, ac.OrganizationID, "wp-member@nodera.dev")
	wp := wordpress.New(e.h.pool, e.proj, e.bk, provisioning.Deps{Pool: e.h.pool, Projects: e.proj, Secrets: e.sec, Providers: e.set})
	wp.Register(e.eng)
	e.eng.RegisterWorker(e.w) // re-register so the new definitions are known to the worker

	p, _ := e.setup(t, ctx, ac, "Origin Site", "origin.example.org", nil)
	_ = e.set.FS.WriteFile(ctx, "projects/origin-site/data/wp-content/uploads/a.jpg", []byte("IMG"))
	_ = e.set.FS.WriteFile(ctx, "projects/origin-site/data/index.php", []byte("<?php // origin"))
	// Seed the origin database with a URL that must be rewritten in the clone.
	var srcDB string
	_ = e.h.pool.QueryRow(ctx, `SELECT name FROM project_databases WHERE project_id=$1`, p.ID).Scan(&srcDB)
	e.mk.DB.Data[srcDB] = []byte("INSERT INTO wp_options VALUES (1,'siteurl','https://origin.example.org','yes');")
	_, _ = e.proj.Update(ctx, ac, p.ID, projectsUpdate(map[string]any{"primary_domain": "origin.example.org"}))

	// ---- clone ----
	ref, err := e.eng.Submit(ctx, ac, ops.SubmitInput{Operation: wordpress.OpClone, ProjectID: p.ID, Payload: wordpress.ClonePayload{Name: "Staging Copy", Domain: "staging.example.org"}})
	if err != nil {
		t.Fatal(err)
	}
	e.w.RunOnce(ctx)
	v, _ := e.eng.Get(ctx, ac, ref.JobID)
	if v.Status != "succeeded" {
		t.Fatalf("clone: %+v", v)
	}
	clone, err := lookupBySlug(t, e, ctx, ac, "staging-copy")
	if err != nil || clone.Status != "active" {
		t.Fatalf("clone project: %+v %v", clone, err)
	}
	if b, _ := e.set.FS.ReadFile(ctx, "projects/staging-copy/data/wp-content/uploads/a.jpg"); string(b) != "IMG" {
		t.Fatal("uploads were not cloned")
	}
	var cloneDB string
	_ = e.h.pool.QueryRow(ctx, `SELECT name FROM project_databases WHERE project_id=$1`, clone.ID).Scan(&cloneDB)
	dump := string(e.mk.DB.Data[cloneDB])
	if !strings.Contains(dump, "https://staging.example.org") || strings.Contains(dump, "origin.example.org") {
		t.Fatalf("clone database not rewritten: %s", dump)
	}
	if cloneDB == srcDB {
		t.Fatal("clone must have its own database")
	}
	if clone.Config["primary_domain"] != "staging.example.org" {
		t.Fatalf("clone config: %+v", clone.Config)
	}

	// A failing clone leaves nothing behind.
	e.mk.Faults.FailOnce("container.create", errors.New("out of memory"))
	ref, _ = e.eng.Submit(ctx, ac, ops.SubmitInput{Operation: wordpress.OpClone, ProjectID: p.ID, Payload: wordpress.ClonePayload{Name: "Doomed Copy"}})
	e.w.RunOnce(ctx)
	if v, _ := e.eng.Get(ctx, ac, ref.JobID); v.Status != "failed" || !v.RolledBack {
		t.Fatalf("expected failed+rolled back: %+v", v)
	}
	if _, err := lookupBySlug(t, e, ctx, ac, "doomed-copy"); err == nil {
		t.Fatal("failed clone left a project behind")
	}

	// ---- update ----
	run := func(tag string) string {
		ref, err := e.eng.Submit(ctx, ac, ops.SubmitInput{Operation: wordpress.OpUpdate, ProjectID: p.ID, Payload: wordpress.UpdatePayload{ImageTag: tag}})
		if err != nil {
			return "rejected"
		}
		e.w.RunOnce(ctx)
		v, _ := e.eng.Get(ctx, ac, ref.JobID)
		return v.Status
	}
	if run("6.6-php8.3-apache") != "succeeded" {
		t.Fatal("update failed")
	}
	if info, _, _ := e.set.Containers.Inspect(ctx, "nodera-origin-site"); info.Image != "wordpress:6.6-php8.3-apache" || info.State != "running" {
		t.Fatalf("container after update: %+v", info)
	}
	// Failing start => previous image is restored.
	e.mk.Faults.FailOnce("container.start", errors.New("image crashes on boot"))
	if run("6.7-php8.3-apache") != "failed" {
		t.Fatal("broken update should fail")
	}
	if info, _, _ := e.set.Containers.Inspect(ctx, "nodera-origin-site"); info.Image != "wordpress:6.6-php8.3-apache" || info.State != "running" {
		t.Fatalf("rollback must restore the previous image, got %+v", info)
	}
	for _, bad := range []string{"latest", "6.6; rm -rf /", "../x", ""} {
		if run(bad) != "rejected" {
			t.Errorf("image tag %q should be rejected", bad)
		}
	}

	// ---- health ----
	h, err := wp.Health(ctx, ac, p.ID)
	if err != nil || !h.Healthy || h.Score == 0 {
		t.Fatalf("health: %+v %v", h, err)
	}
	e.set.Containers.Stop(ctx, "nodera-origin-site")
	h, _ = wp.Health(ctx, ac, p.ID)
	if h.Healthy {
		t.Fatalf("stopped container must be unhealthy: %+v", h)
	}
	if _, err := wp.Health(ctx, other, p.ID); err == nil {
		t.Fatal("cross-tenant health must fail")
	}
	if _, err := wp.Health(ctx, member, p.ID); err != nil {
		t.Fatalf("member can read health: %v", err)
	}
	if _, err := e.eng.Submit(ctx, member, ops.SubmitInput{Operation: wordpress.OpClone, ProjectID: p.ID, Payload: wordpress.ClonePayload{Name: "x"}}); err == nil {
		t.Fatal("member must not clone")
	}
}

func projectsUpdate(cfg map[string]any) projects.UpdateInput {
	return projects.UpdateInput{Config: cfg}
}

func lookupBySlug(t *testing.T, e *migEnv, ctx context.Context, ac authctx.AuthContext, slug string) (projects.Project, error) {
	t.Helper()
	list, _ := e.proj.List(ctx, ac, projects.ListFilter{Search: slug})
	for _, p := range list {
		if p.Slug == slug {
			return p, nil
		}
	}
	return projects.Project{}, errors.New("not found")
}
