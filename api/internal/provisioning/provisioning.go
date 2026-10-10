// Package provisioning implements the project lifecycle operations
// (project.provision, project.delete) on top of the operation framework and
// the provider interfaces. It never shells out and never assumes a runtime:
// when a required provider (containers, database) is not configured the
// operation fails validation with a clear message instead of pretending.
package provisioning

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nodera/nodera/internal/ops"
	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/platform/authctx"
	"github.com/nodera/nodera/internal/projects"
	"github.com/nodera/nodera/internal/providers"
	"github.com/nodera/nodera/internal/secrets"
)

type Deps struct {
	Pool      *pgxpool.Pool
	Projects  *projects.Service
	Secrets   *secrets.Service
	Providers providers.Set
	// Network is the Docker network containers join (e.g. "proxy-public").
	Network string
	// BaseDomain, when set, adds Traefik routing labels for <slug>.<BaseDomain>.
	BaseDomain string
}

const (
	OpProvision = "project.provision"
	OpDelete    = "project.delete"
)

// Register adds the project lifecycle operations to the engine.
func Register(eng *ops.Engine, d Deps) {
	eng.Register(ops.Definition{
		Name: OpProvision, Permission: "projects.create", NotifyKind: "project.provisioned", TimeoutSeconds: 600,
		Factory: func(_ *ops.Env, orgID, projectID uuid.UUID, payload json.RawMessage) (ops.Operation, error) {
			if projectID == uuid.Nil {
				return nil, apierr.Validation("project_id is required")
			}
			return &provision{d: d, org: orgID, project: projectID}, nil
		},
	})
	eng.Register(ops.Definition{
		Name: OpDelete, Permission: "projects.delete", ToolKey: "project.delete", NotifyKind: "project.deleted", TimeoutSeconds: 600,
		Factory: func(_ *ops.Env, orgID, projectID uuid.UUID, payload json.RawMessage) (ops.Operation, error) {
			if projectID == uuid.Nil {
				return nil, apierr.Validation("project_id is required")
			}
			return &deleteProject{d: d, org: orgID, project: projectID}, nil
		},
	})
}

func randomSecret(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func sysCtx(org uuid.UUID, r *ops.Run) authctx.AuthContext {
	ac := authctx.System(org)
	ac.ActorLabel = "operation:" + OpProvision
	return ac
}

// ---------------- provision ----------------

type provision struct {
	d       Deps
	org     uuid.UUID
	project uuid.UUID
	p       projects.Project
}

func (o *provision) Name() string                             { return OpProvision }
func (o *provision) Rollback(context.Context, *ops.Run) error { return nil }
func (o *provision) Execute(context.Context, *ops.Run) error  { return nil }

func (o *provision) load(ctx context.Context) (projects.Project, error) {
	ac := authctx.System(o.org)
	return o.d.Projects.Get(ctx, ac, o.project)
}

func (o *provision) Validate(ctx context.Context) error {
	p, err := o.load(ctx)
	if err != nil {
		return err
	}
	if p.Status != "provisioning" && p.Status != "failed" {
		return apierr.Conflict("project is " + p.Status + "; only provisioning or failed projects can be provisioned")
	}
	if o.d.Providers.Containers == nil {
		return apierr.New(apierr.CodeValidation, "container provider is not configured on this deployment")
	}
	if o.d.Providers.FS == nil {
		return apierr.New(apierr.CodeValidation, "filesystem provider is not configured on this deployment")
	}
	if p.Kind == "wordpress" && o.d.Providers.DB == nil {
		return apierr.New(apierr.CodeValidation, "database provider is not configured; WordPress projects need one")
	}
	if o.d.Secrets == nil {
		return apierr.New(apierr.CodeValidation, "secrets service is not configured; credentials cannot be stored")
	}
	o.p = p
	return nil
}

func (o *provision) names() (workspace, container, dbName, dbUser, secretKey string) {
	slug := o.p.Slug
	short := strings.ReplaceAll(o.project.String(), "-", "")[:8]
	return "projects/" + slug, "nodera-" + slug, "wp_" + strings.ReplaceAll(slug, "-", "_") + "_" + short[:4], "u_" + short, "project/" + slug + "/db-password"
}

func (o *provision) Steps() []ops.Step {
	return []ops.Step{
		{Name: "validate-project",
			// Undo of the first step runs on every failed provision, so a project
			// never stays in 'provisioning' after a rolled-back attempt.
			Undo: func(ctx context.Context, r *ops.Run) error {
				return o.d.Projects.SetStatus(ctx, o.org, o.project, "failed")
			},
			Run: func(ctx context.Context, r *ops.Run) error {
				p, err := o.load(ctx)
				if err != nil {
					return err
				}
				o.p = p
				if err := o.d.Projects.SetStatus(ctx, o.org, o.project, "provisioning"); err != nil {
					return err
				}
				r.Log("info", "provisioning "+p.Kind+" project "+p.Slug, nil)
				return nil
			}},
		{Name: "prepare-workspace",
			Run: func(ctx context.Context, r *ops.Run) error {
				ws, _, _, _, _ := o.names()
				for _, sub := range []string{"data", "backups", "logs"} {
					if err := o.d.Providers.FS.MkdirAll(ctx, ws+"/"+sub); err != nil {
						return err
					}
				}
				return nil
			},
			Undo: func(ctx context.Context, r *ops.Run) error {
				ws, _, _, _, _ := o.names()
				return o.d.Providers.FS.Remove(ctx, ws)
			}},
		{Name: "create-database",
			Run: func(ctx context.Context, r *ops.Run) error {
				if o.p.Kind != "wordpress" {
					r.Log("info", "application project: no database requested", nil)
					return nil
				}
				_, _, db, user, key := o.names()
				pw, err := randomSecret(24)
				if err != nil {
					return err
				}
				created, err := o.d.Providers.DB.EnsureDatabase(ctx, db, user, pw)
				if err != nil {
					return err
				}
				r.Set("db_created", created)
				if !created {
					r.Log("warn", "database already existed; reusing it (password unchanged)", map[string]any{"database": db})
					return nil
				}
				if _, err := o.d.Secrets.Set(ctx, sysCtx(o.org, r), key, pw, "Database password for project "+o.p.Slug); err != nil {
					return err
				}
				_, err = o.d.Pool.Exec(ctx, `
					INSERT INTO project_databases (organization_id, project_id, name, engine, username, password_secret_key, status)
					VALUES ($1,$2,$3,'mariadb',$4,$5,'ready')
					ON CONFLICT (organization_id, name) DO UPDATE SET status='ready', updated_at=now()`,
					o.org, o.project, db, user, key)
				if err == nil {
					r.Set("db_password", pw) // run-local only; never logged or persisted outside secrets
				}
				return err
			},
			Undo: func(ctx context.Context, r *ops.Run) error {
				if o.p.Kind != "wordpress" {
					return nil
				}
				if v, _ := r.Get("db_created"); v != true { // never drop a database we did not create
					return nil
				}
				_, _, db, user, key := o.names()
				if err := o.d.Providers.DB.DropDatabase(ctx, db, user); err != nil {
					return err
				}
				_, _ = o.d.Pool.Exec(ctx, `DELETE FROM project_databases WHERE organization_id=$1 AND name=$2`, o.org, db)
				return o.d.Secrets.Delete(ctx, authctx.System(o.org), key)
			}},
		{Name: "create-container",
			Run: func(ctx context.Context, r *ops.Run) error {
				spec, err := o.containerSpec(ctx, r)
				if err != nil {
					return err
				}
				info, created, err := o.d.Providers.Containers.Create(ctx, spec)
				if err != nil {
					return err
				}
				r.Set("container_created", created)
				r.Log("info", "container "+info.Name+" ready", map[string]any{"created": created})
				return nil
			},
			Undo: func(ctx context.Context, r *ops.Run) error {
				if v, _ := r.Get("container_created"); v != true {
					return nil
				}
				_, c, _, _, _ := o.names()
				err := o.d.Providers.Containers.Remove(ctx, c)
				if errors.Is(err, providers.ErrNotFound) {
					return nil
				}
				return err
			}},
		{Name: "start-container",
			Run: func(ctx context.Context, r *ops.Run) error {
				_, c, _, _, _ := o.names()
				return o.d.Providers.Containers.Start(ctx, c)
			},
			Undo: func(ctx context.Context, r *ops.Run) error {
				_, c, _, _, _ := o.names()
				err := o.d.Providers.Containers.Stop(ctx, c)
				if errors.Is(err, providers.ErrNotFound) {
					return nil
				}
				return err
			}},
		{Name: "health-check", Run: func(ctx context.Context, r *ops.Run) error {
			_, c, _, _, _ := o.names()
			info, ok, err := o.d.Providers.Containers.Inspect(ctx, c)
			if err != nil {
				return err
			}
			if !ok || info.State != "running" {
				return fmt.Errorf("container is not running (state=%q)", info.State)
			}
			return nil
		}},
		{Name: "register-application",
			Run: func(ctx context.Context, r *ops.Run) error {
				_, c, _, _, _ := o.names()
				var id uuid.UUID
				err := o.d.Pool.QueryRow(ctx, `
					INSERT INTO applications (organization_id, project_id, name, kind, node_id, environment, status)
					VALUES ($1,$2,$3,$4,$5,'production','running')
					ON CONFLICT (organization_id, name) DO UPDATE SET status='running', project_id=EXCLUDED.project_id, updated_at=now()
					RETURNING id`, o.org, o.project, c, appKind(o.p.Kind), o.p.NodeID).Scan(&id)
				if err == nil {
					r.Set("application_id", id.String())
				}
				return err
			},
			Undo: func(ctx context.Context, r *ops.Run) error {
				_, c, _, _, _ := o.names()
				_, err := o.d.Pool.Exec(ctx, `DELETE FROM applications WHERE organization_id=$1 AND name=$2 AND project_id=$3`, o.org, c, o.project)
				return err
			}},
		{Name: "activate-project",
			Run: func(ctx context.Context, r *ops.Run) error {
				if err := o.d.Projects.SetStatus(ctx, o.org, o.project, "active"); err != nil {
					return err
				}
				r.SetResult(map[string]any{"project_id": o.project, "slug": o.p.Slug, "status": "active"})
				return nil
			},
			Undo: func(ctx context.Context, r *ops.Run) error {
				return o.d.Projects.SetStatus(ctx, o.org, o.project, "failed")
			}},
	}
}

func appKind(k string) string {
	if k == "wordpress" {
		return "wordpress"
	}
	return "service"
}

func (o *provision) containerSpec(ctx context.Context, r *ops.Run) (providers.ContainerSpec, error) {
	_, c, db, user, _ := o.names()
	ws, _, _, _, _ := o.names()
	labels := map[string]string{"nodera.managed": "true", "nodera.project": o.project.String(), "nodera.org": o.org.String()}
	spec := providers.ContainerSpec{Name: c, Labels: labels}
	if o.d.Network != "" {
		spec.Networks = []string{o.d.Network}
	}
	if o.d.BaseDomain != "" {
		host := o.p.Slug + "." + o.d.BaseDomain
		labels["traefik.enable"] = "true"
		labels["traefik.http.routers."+c+".rule"] = "Host(`" + host + "`)"
	}
	switch o.p.Kind {
	case "wordpress":
		pw, _ := r.Get("db_password")
		pws, _ := pw.(string)
		if pws == "" {
			// Database pre-existed: read the stored password back (system context).
			_, _, _, _, key := o.names()
			v, err := o.d.Secrets.Reveal(ctx, authctx.System(o.org), key)
			if err != nil {
				return spec, fmt.Errorf("database already existed and its password is not stored: %w", err)
			}
			pws = v
		}
		spec.Image = "wordpress:6-php8.3-apache"
		if img, _ := o.p.Config["wordpress_image"].(string); img != "" {
			spec.Image = img // set by wordpress.update
		}
		spec.Env = map[string]string{
			"WORDPRESS_DB_HOST": "db", "WORDPRESS_DB_NAME": db, "WORDPRESS_DB_USER": user, "WORDPRESS_DB_PASSWORD": pws,
		}
		spec.Volumes = []providers.VolumeMount{{Source: ws + "/data", Target: "/var/www/html"}}
	default:
		img, _ := o.p.Config["image"].(string)
		if img == "" {
			return spec, apierr.Validation("application projects require config.image")
		}
		spec.Image = img
	}
	return spec, nil
}

// ---------------- delete ----------------

type deleteProject struct {
	d       Deps
	org     uuid.UUID
	project uuid.UUID
	p       projects.Project
}

func (o *deleteProject) Name() string                             { return OpDelete }
func (o *deleteProject) Rollback(context.Context, *ops.Run) error { return nil }
func (o *deleteProject) Execute(context.Context, *ops.Run) error  { return nil }

func (o *deleteProject) Validate(ctx context.Context) error {
	p, err := o.d.Projects.Get(ctx, authctx.System(o.org), o.project)
	if err != nil {
		return err
	}
	if p.Status == "deleting" {
		return apierr.Conflict("project is already being deleted")
	}
	o.p = p
	return nil
}

// Steps for deletion are intentionally forward-only: removing runtime
// resources is not reversible, so there is no Undo; a failure leaves the
// project in 'failed' for an operator to retry rather than half-deleted state
// being silently "rolled back" to something that no longer exists.
func (o *deleteProject) Steps() []ops.Step {
	pr := &provision{d: o.d, org: o.org, project: o.project}
	return []ops.Step{
		{Name: "mark-deleting", Run: func(ctx context.Context, r *ops.Run) error {
			p, err := o.d.Projects.Get(ctx, authctx.System(o.org), o.project)
			if err != nil {
				return err
			}
			o.p, pr.p = p, p
			return o.d.Projects.SetStatus(ctx, o.org, o.project, "deleting")
		}},
		{Name: "remove-container", Run: func(ctx context.Context, r *ops.Run) error {
			if o.d.Providers.Containers == nil {
				r.Log("warn", "no container provider configured; skipping", nil)
				return nil
			}
			_, c, _, _, _ := pr.names()
			if err := o.d.Providers.Containers.Remove(ctx, c); err != nil && !errors.Is(err, providers.ErrNotFound) {
				return err
			}
			return nil
		}},
		{Name: "drop-database", Run: func(ctx context.Context, r *ops.Run) error {
			rows, err := o.d.Pool.Query(ctx, `SELECT name, username, password_secret_key FROM project_databases WHERE organization_id=$1 AND project_id=$2 AND status <> 'deleted'`, o.org, o.project)
			if err != nil {
				return err
			}
			type dbrow struct{ name, user, key string }
			var dbs []dbrow
			for rows.Next() {
				var d dbrow
				if err := rows.Scan(&d.name, &d.user, &d.key); err != nil {
					rows.Close()
					return err
				}
				dbs = append(dbs, d)
			}
			rows.Close()
			for _, d := range dbs {
				if o.d.Providers.DB == nil {
					return errors.New("database provider is not configured; cannot drop " + d.name)
				}
				if err := o.d.Providers.DB.DropDatabase(ctx, d.name, d.user); err != nil {
					return err
				}
				if o.d.Secrets != nil {
					_ = o.d.Secrets.Delete(ctx, authctx.System(o.org), d.key)
				}
				if _, err := o.d.Pool.Exec(ctx, `UPDATE project_databases SET status='deleted', updated_at=now() WHERE organization_id=$1 AND name=$2`, o.org, d.name); err != nil {
					return err
				}
			}
			return nil
		}},
		{Name: "remove-workspace", Run: func(ctx context.Context, r *ops.Run) error {
			if o.d.Providers.FS == nil {
				return nil
			}
			ws, _, _, _, _ := pr.names()
			return o.d.Providers.FS.Remove(ctx, ws)
		}},
		{Name: "remove-records", Run: func(ctx context.Context, r *ops.Run) error {
			if _, err := o.d.Pool.Exec(ctx, `DELETE FROM applications WHERE organization_id=$1 AND project_id=$2`, o.org, o.project); err != nil {
				return err
			}
			return o.d.Projects.MarkDeleted(ctx, o.org, o.project)
		}},
	}
}

var _ = pgx.ErrNoRows

// ---- reuse by other engines (e.g. WordPress clone/update) ----

// Provision exposes the provisioning steps so another operation can embed them.
// The target project may be set after construction (SetProject) because a
// clone creates its project in an earlier step.
type Provision struct{ p *provision }

func NewProvision(d Deps, org uuid.UUID) *Provision {
	return &Provision{p: &provision{d: d, org: org}}
}
func (x *Provision) SetProject(id uuid.UUID) { x.p.project = id }
func (x *Provision) Steps() []ops.Step       { return x.p.Steps() }

// ContainerSpec rebuilds the project's container spec, optionally with a
// different image (used by in-place upgrades).
func (x *Provision) ContainerSpec(ctx context.Context, r *ops.Run, image string) (providers.ContainerSpec, error) {
	p, err := x.p.load(ctx)
	if err != nil {
		return providers.ContainerSpec{}, err
	}
	x.p.p = p
	spec, err := x.p.containerSpec(ctx, r)
	if err == nil && image != "" {
		spec.Image = image
	}
	return spec, err
}

// ContainerName is the deterministic container name for a project slug.
func ContainerName(slug string) string { return "nodera-" + slug }
