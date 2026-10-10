// Package wordpress implements WordPress-specific operations on top of the
// project, provisioning and backup engines: clone, in-place upgrade, and a
// health report. Installation is project provisioning (project.provision with
// kind=wordpress); deletion is project.delete.
//
// No command is ever executed inside a site container: upgrades replace the
// container image (keeping the data volume), clones copy files and database
// through the provider interfaces.
package wordpress

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nodera/nodera/internal/backups"
	"github.com/nodera/nodera/internal/network"
	"github.com/nodera/nodera/internal/ops"
	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/platform/authctx"
	"github.com/nodera/nodera/internal/projects"
	"github.com/nodera/nodera/internal/providers"
	"github.com/nodera/nodera/internal/provisioning"
	"github.com/nodera/nodera/internal/rbac"
	"github.com/nodera/nodera/internal/sitemig"
)

const (
	OpClone  = "wordpress.clone"
	OpUpdate = "wordpress.update"
)

var imageTagRe = regexp.MustCompile(`^[0-9]+(\.[0-9]+){0,2}(-php[0-9]+\.[0-9]+)?(-apache|-fpm)?$`)

type Service struct {
	pool      *pgxpool.Pool
	projects  *projects.Service
	backups   *backups.Service
	prov      provisioning.Deps
	providers providers.Set
}

func New(pool *pgxpool.Pool, p *projects.Service, b *backups.Service, d provisioning.Deps) *Service {
	return &Service{pool: pool, projects: p, backups: b, prov: d, providers: d.Providers}
}

func (s *Service) Register(eng *ops.Engine) {
	eng.Register(ops.Definition{
		Name: OpClone, Permission: "wordpress.manage", NotifyKind: "wordpress.cloned", TimeoutSeconds: 1800,
		Factory: func(_ *ops.Env, org, project uuid.UUID, payload json.RawMessage) (ops.Operation, error) {
			var p ClonePayload
			_ = json.Unmarshal(payload, &p)
			return &cloneOp{s: s, org: org, source: project, in: p}, nil
		},
	})
	eng.Register(ops.Definition{
		Name: OpUpdate, Permission: "wordpress.manage", NotifyKind: "wordpress.updated", TimeoutSeconds: 1800,
		Factory: func(_ *ops.Env, org, project uuid.UUID, payload json.RawMessage) (ops.Operation, error) {
			var p UpdatePayload
			_ = json.Unmarshal(payload, &p)
			return &updateOp{s: s, org: org, project: project, in: p}, nil
		},
	})
}

type ClonePayload struct {
	Name   string `json:"name"`
	Domain string `json:"domain,omitempty"` // URLs are rewritten to https://<domain> when set
}
type UpdatePayload struct {
	ImageTag string `json:"image_tag"` // e.g. 6.6-php8.3-apache
}

// ---------------- clone ----------------

type cloneOp struct {
	s      *Service
	org    uuid.UUID
	source uuid.UUID
	in     ClonePayload
	src    projects.Project
}

func (o *cloneOp) Name() string                             { return OpClone }
func (o *cloneOp) Rollback(context.Context, *ops.Run) error { return nil }
func (o *cloneOp) Execute(context.Context, *ops.Run) error  { return nil }

func (o *cloneOp) Validate(ctx context.Context) error {
	if o.source == uuid.Nil {
		return apierr.Validation("project_id is required")
	}
	o.in.Name = strings.TrimSpace(o.in.Name)
	if o.in.Name == "" || len(o.in.Name) > 120 {
		return apierr.Validation("name is required for the clone")
	}
	if o.in.Domain != "" {
		d, err := network.NormalizeDomain(o.in.Domain)
		if err != nil {
			return err
		}
		o.in.Domain = d
	}
	p, err := o.s.projects.Get(ctx, authctx.System(o.org), o.source)
	if err != nil {
		return err
	}
	if p.Kind != "wordpress" || p.Status != "active" {
		return apierr.Conflict("only an active WordPress project can be cloned")
	}
	if o.s.providers.FS == nil || o.s.providers.DB == nil || o.s.providers.Containers == nil {
		return apierr.New(apierr.CodeValidation, "filesystem, database and container providers are required to clone a site")
	}
	o.src = p
	return nil
}

func (o *cloneOp) Steps() []ops.Step {
	prov := provisioning.NewProvision(o.s.prov, o.org)
	var newID uuid.UUID
	steps := []ops.Step{{
		Name: "create-clone-project",
		Run: func(ctx context.Context, r *ops.Run) error {
			p, err := o.s.projects.Create(ctx, authctx.System(o.org), projects.ProjectInput{
				Name: o.in.Name, Kind: "wordpress", ClientID: o.src.ClientID, NodeID: o.src.NodeID,
				Description: "Clone of " + o.src.Name, Config: map[string]any{"cloned_from": o.src.ID.String()},
			})
			if err != nil {
				return err
			}
			newID = p.ID
			prov.SetProject(p.ID)
			r.Set("clone_project_id", p.ID.String())
			return nil
		},
		Undo: func(ctx context.Context, r *ops.Run) error {
			if newID != uuid.Nil {
				return o.s.projects.MarkDeleted(ctx, o.org, newID)
			}
			return nil
		},
	}}
	steps = append(steps, prov.Steps()...)
	steps = append(steps,
		ops.Step{Name: "clone-files", Run: func(ctx context.Context, r *ops.Run) error {
			src, dst := projects.WorkspacePath(o.src.Slug)+"/data", ""
			p, err := o.s.projects.Get(ctx, authctx.System(o.org), newID)
			if err != nil {
				return err
			}
			dst = projects.WorkspacePath(p.Slug) + "/data"
			n, err := copyTree(ctx, o.s.providers.FS, src, dst)
			r.Log("info", fmt.Sprintf("copied %d files", n), nil)
			return err
		}},
		ops.Step{Name: "clone-database", Run: func(ctx context.Context, r *ops.Run) error {
			srcDB, err := o.s.dbName(ctx, o.source)
			if err != nil {
				return err
			}
			dstDB, err := o.s.dbName(ctx, newID)
			if err != nil {
				return err
			}
			var buf bytes.Buffer
			if err := o.s.providers.DB.Dump(ctx, srcDB, &buf); err != nil {
				return err
			}
			dump := buf.String()
			if old, _ := o.src.Config["primary_domain"].(string); old != "" && o.in.Domain != "" {
				var n int
				dump, n = sitemig.RewriteURLs(dump, "https://"+old, "https://"+o.in.Domain)
				dump, _ = sitemig.RewriteURLs(dump, "http://"+old, "https://"+o.in.Domain)
				r.Log("info", fmt.Sprintf("rewrote URLs in %d values", n), nil)
			}
			return o.s.providers.DB.Load(ctx, dstDB, strings.NewReader(dump))
		}},
		ops.Step{Name: "restart-clone", Run: func(ctx context.Context, r *ops.Run) error {
			p, err := o.s.projects.Get(ctx, authctx.System(o.org), newID)
			if err != nil {
				return err
			}
			if err := o.s.providers.Containers.Restart(ctx, provisioning.ContainerName(p.Slug)); err != nil {
				return err
			}
			if o.in.Domain != "" {
				cfg := p.Config
				cfg["primary_domain"] = o.in.Domain
				if _, err := o.s.projects.Update(ctx, authctx.System(o.org), newID, projects.UpdateInput{Config: cfg}); err != nil {
					return err
				}
			}
			r.SetResult(map[string]any{"project_id": newID, "slug": p.Slug})
			return nil
		}},
	)
	return steps
}

func (s *Service) dbName(ctx context.Context, project uuid.UUID) (string, error) {
	var n string
	err := s.pool.QueryRow(ctx, `SELECT name FROM project_databases WHERE project_id=$1 AND status='ready' ORDER BY created_at LIMIT 1`, project).Scan(&n)
	if err != nil {
		return "", errors.New("project has no ready database")
	}
	return n, nil
}

func copyTree(ctx context.Context, fs providers.FilesystemProvider, src, dst string) (int, error) {
	n := 0
	entries, err := fs.List(ctx, src)
	if err != nil {
		return 0, nil // nothing to copy
	}
	for _, e := range entries {
		rel := strings.TrimPrefix(strings.TrimPrefix(e.Path, src), "/")
		if e.IsDir {
			c, err := copyTree(ctx, fs, e.Path, dst+"/"+rel)
			n += c
			if err != nil {
				return n, err
			}
			continue
		}
		data, err := fs.ReadFile(ctx, e.Path)
		if err != nil {
			return n, err
		}
		if err := fs.WriteFile(ctx, dst+"/"+rel, data); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// ---------------- update ----------------

type updateOp struct {
	s       *Service
	org     uuid.UUID
	project uuid.UUID
	in      UpdatePayload
	p       projects.Project
}

func (o *updateOp) Name() string                             { return OpUpdate }
func (o *updateOp) Rollback(context.Context, *ops.Run) error { return nil }
func (o *updateOp) Execute(context.Context, *ops.Run) error  { return nil }
func (o *updateOp) Validate(ctx context.Context) error {
	if o.project == uuid.Nil {
		return apierr.Validation("project_id is required")
	}
	if !imageTagRe.MatchString(o.in.ImageTag) {
		return apierr.Validation("image_tag must look like 6.6-php8.3-apache")
	}
	p, err := o.s.projects.Get(ctx, authctx.System(o.org), o.project)
	if err != nil {
		return err
	}
	if p.Kind != "wordpress" || p.Status != "active" {
		return apierr.Conflict("only an active WordPress project can be updated")
	}
	if o.s.providers.Containers == nil {
		return apierr.New(apierr.CodeValidation, "container provider is not configured on this deployment")
	}
	if o.s.backups == nil {
		return apierr.New(apierr.CodeValidation, "backup service is required for a safe update")
	}
	o.p = p
	return nil
}

func (o *updateOp) Steps() []ops.Step {
	prov := provisioning.NewProvision(o.s.prov, o.org)
	prov.SetProject(o.project)
	name := provisioning.ContainerName(o.p.Slug)
	var safety uuid.UUID
	var oldSpec providers.ContainerSpec
	recreate := func(ctx context.Context, r *ops.Run, spec providers.ContainerSpec) error {
		c := o.s.providers.Containers
		if err := c.Remove(ctx, name); err != nil && !errors.Is(err, providers.ErrNotFound) {
			return err
		}
		if _, _, err := c.Create(ctx, spec); err != nil {
			return err
		}
		return c.Start(ctx, name)
	}
	return []ops.Step{
		{Name: "safety-backup", Run: func(ctx context.Context, r *ops.Run) error {
			id, err := o.s.backups.SnapshotNow(ctx, r, o.org, o.project, "full", 14, "pre-update safety backup")
			if err != nil {
				return fmt.Errorf("cannot update without a safety backup: %w", err)
			}
			safety = id
			return nil
		}},
		{Name: "capture-current-spec", Run: func(ctx context.Context, r *ops.Run) error {
			spec, err := prov.ContainerSpec(ctx, r, "")
			oldSpec = spec
			return err
		}},
		{Name: "replace-container",
			Run: func(ctx context.Context, r *ops.Run) error {
				spec, err := prov.ContainerSpec(ctx, r, "wordpress:"+o.in.ImageTag)
				if err != nil {
					return err
				}
				r.Log("info", "replacing container image with "+spec.Image+" (data volume preserved)", nil)
				return recreate(ctx, r, spec)
			},
			Undo: func(ctx context.Context, r *ops.Run) error {
				r.Log("warning", "update failed: restoring the previous image", nil)
				if oldSpec.Image == "" {
					return nil
				}
				if err := recreate(ctx, r, oldSpec); err != nil {
					return err
				}
				if safety != uuid.Nil {
					return o.s.backups.RestoreNow(ctx, o.org, o.project, safety)
				}
				return nil
			}},
		{Name: "health-check", Run: func(ctx context.Context, r *ops.Run) error {
			info, ok, err := o.s.providers.Containers.Inspect(ctx, name)
			if err != nil {
				return err
			}
			if !ok || info.State != "running" {
				return fmt.Errorf("container is %q after the update", info.State)
			}
			return nil
		}},
		{Name: "record", Run: func(ctx context.Context, r *ops.Run) error {
			cfg := o.p.Config
			if cfg == nil {
				cfg = map[string]any{}
			}
			cfg["wordpress_image"] = "wordpress:" + o.in.ImageTag
			_, err := o.s.projects.Update(ctx, authctx.System(o.org), o.project, projects.UpdateInput{Config: cfg})
			r.SetResult(map[string]any{"image": "wordpress:" + o.in.ImageTag, "safety_backup_id": safety})
			return err
		}},
	}
}

// ---------------- health ----------------

type HealthCheck struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"` // PASS | WARNING | FAIL
	Detail string `json:"detail,omitempty"`
}
type Health struct {
	Healthy bool          `json:"healthy"`
	Score   int           `json:"score"`
	Checks  []HealthCheck `json:"checks"`
}

// Health runs read-only checks and reports exactly what it could and could not
// verify; an unavailable provider is a WARNING, never a silent PASS.
func (s *Service) Health(ctx context.Context, ac authctx.AuthContext, projectID uuid.UUID) (Health, error) {
	if err := rbac.Require(ac, "wordpress.read"); err != nil {
		return Health{}, err
	}
	p, err := s.projects.Get(ctx, ac, projectID)
	if err != nil {
		return Health{}, err
	}
	if p.Kind != "wordpress" {
		return Health{}, apierr.Validation("not a WordPress project")
	}
	h := Health{}
	add := func(id, title, status, detail string) {
		h.Checks = append(h.Checks, HealthCheck{ID: id, Title: title, Status: status, Detail: detail})
	}
	if p.Status == "active" {
		add("project", "Project is active", "PASS", "")
	} else {
		add("project", "Project is active", "FAIL", "status is "+p.Status)
	}
	if s.providers.Containers == nil {
		add("container", "Container is running", "WARNING", "container provider not configured; could not verify")
	} else if info, ok, err := s.providers.Containers.Inspect(ctx, provisioning.ContainerName(p.Slug)); err != nil {
		add("container", "Container is running", "WARNING", err.Error())
	} else if !ok {
		add("container", "Container is running", "FAIL", "container does not exist")
	} else if info.State != "running" {
		add("container", "Container is running", "FAIL", "state is "+info.State)
	} else {
		add("container", "Container is running", "PASS", info.Image)
	}
	if s.providers.FS != nil {
		if ok, _ := s.providers.FS.Exists(ctx, projects.WorkspacePath(p.Slug)+"/data"); ok {
			add("files", "Site files present", "PASS", "")
		} else {
			add("files", "Site files present", "FAIL", "workspace data directory is missing")
		}
	}
	if s.providers.DB != nil {
		if name, err := s.dbName(ctx, projectID); err != nil {
			add("database", "Database registered", "FAIL", err.Error())
		} else if ok, _ := s.providers.DB.Exists(ctx, name); ok {
			add("database", "Database exists", "PASS", "")
		} else {
			add("database", "Database exists", "FAIL", "database is missing on the server")
		}
	} else {
		add("database", "Database exists", "WARNING", "database provider not configured; could not verify")
	}
	var recent int
	_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM backups WHERE project_id=$1 AND status='completed' AND created_at > now() - interval '8 days'`, projectID).Scan(&recent)
	if recent > 0 {
		add("backup", "Recent backup exists", "PASS", "")
	} else {
		add("backup", "Recent backup exists", "WARNING", "no completed backup in the last 8 days")
	}
	if u, _ := p.Config["health_url"].(string); u != "" && s.providers.Monitoring != nil {
		if res, err := s.providers.Monitoring.CheckHTTP(ctx, u, 200); err != nil || !res.OK {
			d := res.Detail
			if err != nil {
				d = err.Error()
			}
			add("http", "Site responds over HTTP", "FAIL", d)
		} else {
			add("http", "Site responds over HTTP", "PASS", fmt.Sprintf("%d ms", res.LatencyMs))
		}
	}
	pass, total := 0, 0
	h.Healthy = true
	for _, c := range h.Checks {
		total++
		switch c.Status {
		case "PASS":
			pass++
		case "WARNING":
			pass += 0
		case "FAIL":
			h.Healthy = false
		}
	}
	if total > 0 {
		h.Score = pass * 100 / total
	}
	return h, nil
}
