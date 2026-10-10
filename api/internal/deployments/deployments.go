// Package deployments implements the deployment engine: PRECHECK -> FETCH ->
// BUILD -> TEST -> DEPLOY -> HEALTH_CHECK -> COMPLETE as persisted operation
// steps, with automatic rollback to the previous release on failure and an
// explicit rollback operation. Sources: GitHub/Git (via GitProvider) and
// direct upload. Production deployments require approval (Tool Gateway).
//
// There is deliberately no build-command execution: arbitrary commands are
// not accepted (no generic shell). The BUILD stage is recorded as skipped
// unless a real builder is configured; the pipeline never claims a build ran.
package deployments

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nodera/nodera/internal/audit"
	"github.com/nodera/nodera/internal/ops"
	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/platform/authctx"
	"github.com/nodera/nodera/internal/platform/logger"
	"github.com/nodera/nodera/internal/projects"
	"github.com/nodera/nodera/internal/providers"
	"github.com/nodera/nodera/internal/rbac"
)

const (
	OpDeploy     = "deployment.deploy"     // non-production environments
	OpProduction = "deployment.production" // production: approval required
	OpRollback   = "deployment.rollback"

	maxFiles     = 20000
	maxBytes     = 200 << 20
	keepReleases = 5
)

var (
	repoRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}/[A-Za-z0-9._-]{1,100}$`)
	refRe  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,199}$`)
	envRe  = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)
)

type AuditRecorder interface {
	Record(ctx context.Context, ac authctx.AuthContext, e audit.Entry) error
}

type Service struct {
	pool      *pgxpool.Pool
	audit     AuditRecorder
	projects  *projects.Service
	providers providers.Set
}

func New(pool *pgxpool.Pool, a AuditRecorder, p *projects.Service, set providers.Set) *Service {
	return &Service{pool: pool, audit: a, projects: p, providers: set}
}

// Payload is the deployment request (also the operation payload).
type Payload struct {
	Source      string            `json:"source"` // github | git | upload
	Repository  string            `json:"repository,omitempty"`
	Ref         string            `json:"ref,omitempty"`
	Environment string            `json:"environment,omitempty"`
	Files       map[string]string `json:"files,omitempty"` // upload: path -> base64 content
	// RollbackTo is used by the rollback operation: the deployment to restore.
	RollbackTo uuid.UUID `json:"rollback_to,omitempty"`
}

func (p *Payload) normalise() error {
	if p.Environment == "" {
		p.Environment = "production"
	}
	if !envRe.MatchString(p.Environment) {
		return apierr.Validation("invalid environment name")
	}
	if p.Ref == "" {
		p.Ref = "main"
	}
	switch p.Source {
	case "github", "git":
		if !repoRe.MatchString(p.Repository) {
			return apierr.Validation("repository must look like owner/name")
		}
		if !refRe.MatchString(p.Ref) || strings.Contains(p.Ref, "..") || strings.HasSuffix(p.Ref, "/") {
			return apierr.Validation("invalid git ref")
		}
	case "upload":
		if len(p.Files) == 0 {
			return apierr.Validation("upload deployments need at least one file")
		}
		if len(p.Files) > maxFiles {
			return apierr.Validation("too many files")
		}
		for name := range p.Files {
			if err := safePath(name); err != nil {
				return apierr.Validation("invalid file path " + name + ": " + err.Error())
			}
		}
	default:
		return apierr.Validation("source must be github, git or upload")
	}
	return nil
}

func safePath(p string) error {
	if p == "" || strings.HasPrefix(p, "/") || strings.ContainsRune(p, 0) || strings.Contains(p, `\`) {
		return errors.New("must be a relative path")
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." || seg == "" {
			return errors.New("must not contain empty or .. segments")
		}
	}
	return nil
}

func (s *Service) Register(eng *ops.Engine) {
	mk := func(name, perm, tool, notify string, rollback bool) {
		eng.Register(ops.Definition{
			Name: name, Permission: perm, ToolKey: tool, NotifyKind: notify, TimeoutSeconds: 1800,
			Factory: func(_ *ops.Env, org, project uuid.UUID, payload json.RawMessage) (ops.Operation, error) {
				var p Payload
				if err := json.Unmarshal(payload, &p); err != nil {
					return nil, apierr.Validation("invalid payload")
				}
				return &deployOp{s: s, org: org, project: project, in: p, name: name, rollback: rollback}, nil
			},
		})
	}
	mk(OpDeploy, "deployments.create", "", "deployment.completed", false)
	mk(OpProduction, "deployments.create", "deployment.production", "deployment.completed", false)
	mk(OpRollback, "deployments.rollback", "", "deployment.rolled_back", true)
}

// ---------------- records ----------------

type Deployment struct {
	ID          uuid.UUID  `json:"id"`
	ProjectID   uuid.UUID  `json:"project_id"`
	Source      string     `json:"source"`
	Repository  string     `json:"repository,omitempty"`
	Ref         string     `json:"ref"`
	CommitSHA   string     `json:"commit_sha,omitempty"`
	Environment string     `json:"environment"`
	Status      string     `json:"status"`
	Stage       string     `json:"stage"`
	FileCount   int        `json:"file_count"`
	PreviousID  *uuid.UUID `json:"previous_deployment_id,omitempty"`
	JobID       *uuid.UUID `json:"job_id,omitempty"`
	Error       string     `json:"error,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
}

const depCols = `id, project_id, source, COALESCE(repository,''), ref, COALESCE(commit_sha,''), environment, status, stage, file_count, previous_deployment_id, job_id, COALESCE(error,''), created_at, finished_at`

func scanDep(row pgx.Row) (Deployment, error) {
	var d Deployment
	err := row.Scan(&d.ID, &d.ProjectID, &d.Source, &d.Repository, &d.Ref, &d.CommitSHA, &d.Environment, &d.Status, &d.Stage, &d.FileCount, &d.PreviousID, &d.JobID, &d.Error, &d.CreatedAt, &d.FinishedAt)
	return d, err
}

func (s *Service) List(ctx context.Context, ac authctx.AuthContext, projectID *uuid.UUID, limit, offset int) ([]Deployment, error) {
	if err := rbac.Require(ac, "deployments.read"); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `SELECT `+depCols+` FROM deployments WHERE organization_id=$1 AND ($2::uuid IS NULL OR project_id=$2)
		ORDER BY created_at DESC LIMIT $3 OFFSET $4`, ac.OrganizationID, projectID, limit, offset)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeInternal, "failed to list deployments", err)
	}
	defer rows.Close()
	out := []Deployment{}
	for rows.Next() {
		d, err := scanDep(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Service) Get(ctx context.Context, ac authctx.AuthContext, id uuid.UUID) (Deployment, error) {
	if err := rbac.Require(ac, "deployments.read"); err != nil {
		return Deployment{}, err
	}
	d, err := scanDep(s.pool.QueryRow(ctx, `SELECT `+depCols+` FROM deployments WHERE id=$1 AND organization_id=$2`, id, ac.OrganizationID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Deployment{}, apierr.NotFound("deployment")
	}
	return d, err
}

// ---------------- operation ----------------

type deployOp struct {
	s        *Service
	org      uuid.UUID
	project  uuid.UUID
	in       Payload
	name     string
	rollback bool
	p        projects.Project
}

func (o *deployOp) Name() string                             { return o.name }
func (o *deployOp) Rollback(context.Context, *ops.Run) error { return nil }
func (o *deployOp) Execute(context.Context, *ops.Run) error  { return nil }

func (o *deployOp) Validate(ctx context.Context) error {
	if o.project == uuid.Nil {
		return apierr.Validation("project_id is required")
	}
	if o.s.providers.FS == nil {
		return apierr.New(apierr.CodeValidation, "filesystem provider is not configured on this deployment")
	}
	p, err := o.s.projects.Get(ctx, authctx.System(o.org), o.project)
	if err != nil {
		return err
	}
	o.p = p
	if p.Status != "active" {
		return apierr.Conflict("project is " + p.Status + "; deployments need an active project")
	}
	if o.rollback {
		if o.in.RollbackTo == uuid.Nil {
			return apierr.Validation("rollback_to is required")
		}
		return nil
	}
	if err := o.in.normalise(); err != nil {
		return err
	}
	if (o.name == OpDeploy) != (o.in.Environment != "production") {
		return apierr.Validation("production deployments must be requested through the approval gateway; other environments use the direct endpoint")
	}
	if o.in.Source != "upload" && o.s.providers.Git == nil {
		return apierr.New(apierr.CodeValidation, "git provider is not configured on this deployment; use an upload deployment or configure GitHub")
	}
	var n int
	_ = o.s.pool.QueryRow(ctx, `SELECT count(*) FROM deployments WHERE project_id=$1 AND environment=$2 AND status IN ('queued','running')`, o.project, o.in.Environment).Scan(&n)
	if n > 0 {
		return apierr.Conflict("another deployment is already running for this project and environment")
	}
	return nil
}

func (o *deployOp) workspace() string { return projects.WorkspacePath(o.p.Slug) }
func (o *deployOp) liveDir() string   { return o.workspace() + "/app" }

func (s *Service) setStage(ctx context.Context, id uuid.UUID, stage string) {
	_, _ = s.pool.Exec(ctx, `UPDATE deployments SET stage=$2 WHERE id=$1`, id, stage)
}

// copyTree copies every file under src to dst (both relative to the FS root).
func (s *Service) copyTree(ctx context.Context, src, dst string) (files int, bytes int64, err error) {
	fs := s.providers.FS
	var walk func(dir string) error
	walk = func(dir string) error {
		entries, err := fs.List(ctx, dir)
		if err != nil {
			return err
		}
		for _, e := range entries {
			rel := strings.TrimPrefix(strings.TrimPrefix(e.Path, src), "/")
			if e.IsDir {
				if err := walk(e.Path); err != nil {
					return err
				}
				continue
			}
			data, err := fs.ReadFile(ctx, e.Path)
			if err != nil {
				return err
			}
			files++
			bytes += int64(len(data))
			if files > maxFiles || bytes > maxBytes {
				return errors.New("release exceeds the size limit")
			}
			if err := fs.WriteFile(ctx, path.Join(dst, rel), data); err != nil {
				return err
			}
		}
		return nil
	}
	if ok, _ := fs.Exists(ctx, src); !ok {
		return 0, 0, nil
	}
	err = walk(src)
	return
}

func (o *deployOp) Steps() []ops.Step {
	if o.rollback {
		return o.rollbackSteps()
	}
	var depID, prevID uuid.UUID
	var release string
	liveBackedUp := false
	fs := func() providers.FilesystemProvider { return o.s.providers.FS }

	return []ops.Step{
		{Name: "PRECHECK",
			Run: func(ctx context.Context, r *ops.Run) error {
				var prev *uuid.UUID
				_ = o.s.pool.QueryRow(ctx, `SELECT id FROM deployments WHERE project_id=$1 AND environment=$2 AND status='succeeded' ORDER BY created_at DESC LIMIT 1`,
					o.project, o.in.Environment).Scan(&prev)
				if prev != nil {
					prevID = *prev
				}
				err := o.s.pool.QueryRow(ctx, `INSERT INTO deployments (organization_id, project_id, source, repository, ref, environment, status, stage, previous_deployment_id, job_id)
					VALUES ($1,$2,$3,NULLIF($4,''),$5,$6,'running','PRECHECK',$7,$8) RETURNING id`,
					o.org, o.project, o.in.Source, o.in.Repository, o.in.Ref, o.in.Environment, prev, r.JobID).Scan(&depID)
				if err != nil {
					return err
				}
				release = o.workspace() + "/releases/" + depID.String()
				r.Set("deployment_id", depID.String())
				r.Log("info", fmt.Sprintf("deploying %s to %s (%s)", o.in.Source, o.p.Slug, o.in.Environment), nil)
				return nil
			},
			Undo: func(ctx context.Context, r *ops.Run) error {
				if depID != uuid.Nil {
					_, _ = o.s.pool.Exec(ctx, `UPDATE deployments SET status='failed', finished_at=now() WHERE id=$1 AND status='running'`, depID)
				}
				return nil
			}},
		{Name: "FETCH",
			Run: func(ctx context.Context, r *ops.Run) error {
				o.s.setStage(ctx, depID, "FETCH")
				switch o.in.Source {
				case "upload":
					total := 0
					for name, b64 := range o.in.Files {
						data, err := base64.StdEncoding.DecodeString(b64)
						if err != nil {
							return fmt.Errorf("file %s is not valid base64", name)
						}
						total += len(data)
						if total > maxBytes {
							return errors.New("upload exceeds the size limit")
						}
						if err := fs().WriteFile(ctx, path.Join(release, name), data); err != nil {
							return err
						}
					}
				default:
					sha, err := o.s.providers.Git.Fetch(ctx, o.in.Repository, o.in.Ref, fs(), release)
					if err != nil {
						return fmt.Errorf("fetching %s@%s: %w", o.in.Repository, o.in.Ref, err)
					}
					_, _ = o.s.pool.Exec(ctx, `UPDATE deployments SET commit_sha=$2 WHERE id=$1`, depID, sha)
					r.Log("info", "fetched "+o.in.Repository+" at "+sha, nil)
				}
				return nil
			},
			Undo: func(ctx context.Context, r *ops.Run) error { return fs().Remove(ctx, release) }},
		{Name: "BUILD", Run: func(ctx context.Context, r *ops.Run) error {
			o.s.setStage(ctx, depID, "BUILD")
			r.Log("info", "no builder configured: BUILD skipped (static/PHP release deployed as-is)", nil)
			return nil
		}},
		{Name: "TEST", Run: func(ctx context.Context, r *ops.Run) error {
			o.s.setStage(ctx, depID, "TEST")
			entries, err := fs().List(ctx, release)
			if err != nil || len(entries) == 0 {
				return errors.New("release is empty: refusing to deploy nothing")
			}
			var size int64
			for _, e := range entries {
				size += e.Size
			}
			_, _ = o.s.pool.Exec(ctx, `UPDATE deployments SET file_count=$2 WHERE id=$1`, depID, len(entries))
			r.Log("success", fmt.Sprintf("release validated: %d top-level entries, %d bytes", len(entries), size), nil)
			return nil
		}},
		{Name: "DEPLOY",
			Run: func(ctx context.Context, r *ops.Run) error {
				o.s.setStage(ctx, depID, "DEPLOY")
				// Keep a copy of what is live so a failure can put it back.
				if ok, _ := fs().Exists(ctx, o.liveDir()); ok {
					if _, _, err := o.s.copyTree(ctx, o.liveDir(), o.workspace()+"/releases/_previous-"+depID.String()); err != nil {
						return fmt.Errorf("could not preserve the live release: %w", err)
					}
					liveBackedUp = true
					if err := fs().Remove(ctx, o.liveDir()); err != nil {
						return err
					}
				}
				n, _, err := o.s.copyTree(ctx, release, o.liveDir())
				if err != nil {
					return err
				}
				r.Log("info", fmt.Sprintf("activated release (%d files)", n), nil)
				return nil
			},
			Undo: func(ctx context.Context, r *ops.Run) error {
				if !liveBackedUp {
					return fs().Remove(ctx, o.liveDir())
				}
				r.Log("warning", "restoring the previous live release", nil)
				if err := fs().Remove(ctx, o.liveDir()); err != nil {
					return err
				}
				_, _, err := o.s.copyTree(ctx, o.workspace()+"/releases/_previous-"+depID.String(), o.liveDir())
				return err
			}},
		{Name: "HEALTH_CHECK", Run: func(ctx context.Context, r *ops.Run) error {
			o.s.setStage(ctx, depID, "HEALTH_CHECK")
			if o.s.providers.Containers != nil {
				info, ok, err := o.s.providers.Containers.Inspect(ctx, "nodera-"+o.p.Slug)
				if err != nil {
					return err
				}
				if ok && info.State != "running" {
					return fmt.Errorf("application container is %q after deploy", info.State)
				}
				if !ok {
					r.Log("warning", "no application container found; skipping runtime check", nil)
				}
			} else {
				r.Log("warning", "no container provider; runtime health could not be checked", nil)
			}
			if u, _ := o.p.Config["health_url"].(string); u != "" && o.s.providers.Monitoring != nil {
				res, err := o.s.providers.Monitoring.CheckHTTP(ctx, u, 200)
				if err != nil {
					return err
				}
				if !res.OK {
					return fmt.Errorf("health check failed: %s", res.Detail)
				}
			}
			return nil
		}},
		{Name: "COMPLETE", Run: func(ctx context.Context, r *ops.Run) error {
			_, err := o.s.pool.Exec(ctx, `UPDATE deployments SET status='succeeded', stage='COMPLETE', finished_at=now() WHERE id=$1`, depID)
			if err != nil {
				return err
			}
			o.s.prune(ctx, o.org, o.project, o.workspace(), depID)
			_ = o.s.providers.FS.Remove(ctx, o.workspace()+"/releases/_previous-"+depID.String())
			r.SetResult(map[string]any{"deployment_id": depID, "previous_deployment_id": prevID})
			return nil
		}},
	}
}

// prune removes old release directories beyond keepReleases.
func (s *Service) prune(ctx context.Context, org, project uuid.UUID, ws string, keep uuid.UUID) {
	rows, err := s.pool.Query(ctx, `SELECT id FROM deployments WHERE organization_id=$1 AND project_id=$2 AND status IN ('succeeded','rolled_back') ORDER BY created_at DESC OFFSET $3`, org, project, keepReleases)
	if err != nil {
		return
	}
	var old []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if rows.Scan(&id) == nil && id != keep {
			old = append(old, id)
		}
	}
	rows.Close()
	sort.Slice(old, func(i, j int) bool { return old[i].String() < old[j].String() })
	for _, id := range old {
		_ = s.providers.FS.Remove(ctx, ws+"/releases/"+id.String())
	}
}

func (o *deployOp) rollbackSteps() []ops.Step {
	var target Deployment
	var newID uuid.UUID
	return []ops.Step{
		{Name: "locate-release", Run: func(ctx context.Context, r *ops.Run) error {
			d, err := scanDep(o.s.pool.QueryRow(ctx, `SELECT `+depCols+` FROM deployments WHERE id=$1 AND organization_id=$2 AND project_id=$3`, o.in.RollbackTo, o.org, o.project))
			if err != nil {
				return apierr.NotFound("deployment")
			}
			if d.Status != "succeeded" && d.Status != "rolled_back" {
				return fmt.Errorf("deployment %s never completed (%s); it cannot be restored", d.ID, d.Status)
			}
			if ok, _ := o.s.providers.FS.Exists(ctx, o.workspace()+"/releases/"+d.ID.String()); !ok {
				return errors.New("that release is no longer on disk (pruned); redeploy it from source instead")
			}
			target = d
			return nil
		}},
		{Name: "activate-release",
			Run: func(ctx context.Context, r *ops.Run) error {
				fs := o.s.providers.FS
				if ok, _ := fs.Exists(ctx, o.liveDir()); ok {
					if _, _, err := o.s.copyTree(ctx, o.liveDir(), o.workspace()+"/releases/_pre-rollback-"+o.in.RollbackTo.String()); err != nil {
						return err
					}
					if err := fs.Remove(ctx, o.liveDir()); err != nil {
						return err
					}
				}
				_, _, err := o.s.copyTree(ctx, o.workspace()+"/releases/"+target.ID.String(), o.liveDir())
				return err
			},
			Undo: func(ctx context.Context, r *ops.Run) error {
				fs := o.s.providers.FS
				if ok, _ := fs.Exists(ctx, o.workspace()+"/releases/_pre-rollback-"+o.in.RollbackTo.String()); !ok {
					return nil
				}
				_ = fs.Remove(ctx, o.liveDir())
				_, _, err := o.s.copyTree(ctx, o.workspace()+"/releases/_pre-rollback-"+o.in.RollbackTo.String(), o.liveDir())
				return err
			}},
		{Name: "record", Run: func(ctx context.Context, r *ops.Run) error {
			if _, err := o.s.pool.Exec(ctx, `UPDATE deployments SET status='rolled_back', finished_at=COALESCE(finished_at, now())
				WHERE project_id=$1 AND environment=$2 AND status='succeeded' AND id <> $3`, o.project, target.Environment, target.ID); err != nil {
				return err
			}
			err := o.s.pool.QueryRow(ctx, `INSERT INTO deployments (organization_id, project_id, source, repository, ref, commit_sha, environment, status, stage, previous_deployment_id, job_id, finished_at)
				VALUES ($1,$2,$3,NULLIF($4,''),$5,NULLIF($6,''),$7,'succeeded','COMPLETE',$8,$9, now()) RETURNING id`,
				o.org, o.project, target.Source, target.Repository, target.Ref, target.CommitSHA, target.Environment, target.ID, r.JobID).Scan(&newID)
			_ = o.s.providers.FS.Remove(ctx, o.workspace()+"/releases/_pre-rollback-"+o.in.RollbackTo.String())
			r.SetResult(map[string]any{"deployment_id": newID, "restored": target.ID})
			return err
		}},
	}
}

func (s *Service) rec(ctx context.Context, ac authctx.AuthContext, action, id string, state any) {
	if err := s.audit.Record(ctx, ac, audit.Entry{Action: action, ResourceType: "deployment", ResourceID: id, Success: true, ResultingState: state}); err != nil {
		logger.FromContext(ctx).Error("failed to write audit entry", "error", err)
	}
}

// Repositories/Branches/Commits expose the Git provider to the UI.
func (s *Service) Repositories(ctx context.Context, ac authctx.AuthContext) ([]providers.Repository, error) {
	if err := rbac.Require(ac, "deployments.read"); err != nil {
		return nil, err
	}
	if s.providers.Git == nil {
		return nil, apierr.New(apierr.CodeValidation, "git provider is not configured on this deployment")
	}
	return s.providers.Git.ListRepositories(ctx)
}
