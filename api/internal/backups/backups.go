// Package backups implements backup, verification, restore and retention as
// operations on the operation framework. Every backup is checksummed (SHA-256)
// and verified; restores take a safety snapshot first and put it back if the
// restore fails; retention and scheduled policies are plain, testable
// functions driven by a sweeper.
package backups

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	OpCreate  = "backup.create"
	OpVerify  = "backup.verify"
	OpRestore = "backup.restore"
	OpDelete  = "backup.delete"
)

var validTypes = map[string]bool{"database": true, "files": true, "media": true, "full": true, "configuration": true}

type AuditRecorder interface {
	Record(ctx context.Context, ac authctx.AuthContext, e audit.Entry) error
}

type Service struct {
	pool      *pgxpool.Pool
	audit     AuditRecorder
	projects  *projects.Service
	providers providers.Set
	engine    *ops.Engine
}

func New(pool *pgxpool.Pool, a AuditRecorder, p *projects.Service, set providers.Set) *Service {
	return &Service{pool: pool, audit: a, projects: p, providers: set}
}

// Register wires the backup operations into the engine and remembers it so the
// scheduler can submit work.
func (s *Service) Register(eng *ops.Engine) {
	s.engine = eng
	eng.Register(ops.Definition{
		Name: OpCreate, Permission: "backups.manage", NotifyKind: "backup.completed", TimeoutSeconds: 1800,
		Factory: func(_ *ops.Env, org, project uuid.UUID, payload json.RawMessage) (ops.Operation, error) {
			var p CreatePayload
			_ = json.Unmarshal(payload, &p)
			return &createOp{s: s, org: org, project: project, in: p}, nil
		},
	})
	eng.Register(ops.Definition{
		Name: OpVerify, Permission: "backups.manage", NotifyKind: "backup.verified", TimeoutSeconds: 900,
		Factory: func(_ *ops.Env, org, project uuid.UUID, payload json.RawMessage) (ops.Operation, error) {
			_, id := ops.PayloadResource(payload)
			var p struct {
				BackupID uuid.UUID `json:"backup_id"`
			}
			_ = json.Unmarshal(payload, &p)
			if p.BackupID != uuid.Nil {
				id = p.BackupID
			}
			return &verifyOp{s: s, org: org, backup: id}, nil
		},
	})
	eng.Register(ops.Definition{
		Name: OpRestore, Permission: "backups.restore", ToolKey: "backup.restore", NotifyKind: "backup.restored", TimeoutSeconds: 3600,
		Factory: func(_ *ops.Env, org, project uuid.UUID, payload json.RawMessage) (ops.Operation, error) {
			_, id := ops.PayloadResource(payload)
			return &restoreOp{s: s, org: org, project: project, backup: id}, nil
		},
	})
	eng.Register(ops.Definition{
		Name: OpDelete, Permission: "backups.delete", ToolKey: "backup.delete", NotifyKind: "backup.deleted",
		Factory: func(_ *ops.Env, org, project uuid.UUID, payload json.RawMessage) (ops.Operation, error) {
			_, id := ops.PayloadResource(payload)
			return &deleteOp{s: s, org: org, backup: id}, nil
		},
	})
}

type CreatePayload struct {
	Type          string     `json:"type"`
	RetentionDays int        `json:"retention_days"`
	PolicyID      *uuid.UUID `json:"policy_id,omitempty"`
	TargetID      *uuid.UUID `json:"target_id,omitempty"`
	// Reason marks internal snapshots, e.g. "pre-restore safety snapshot".
	Reason string `json:"reason,omitempty"`
}

// ---------------- records ----------------

type Backup struct {
	ID             uuid.UUID  `json:"id"`
	OrganizationID uuid.UUID  `json:"organization_id"`
	ProjectID      uuid.UUID  `json:"project_id"`
	Type           string     `json:"type"`
	Status         string     `json:"status"`
	SizeBytes      int64      `json:"size_bytes"`
	Checksum       string     `json:"checksum_sha256,omitempty"`
	RetentionUntil *time.Time `json:"retention_until,omitempty"`
	VerifiedAt     *time.Time `json:"verified_at,omitempty"`
	JobID          *uuid.UUID `json:"job_id,omitempty"`
	Error          string     `json:"error,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	FinishedAt     *time.Time `json:"finished_at,omitempty"`
}

const backupCols = `id, organization_id, project_id, type, status, size_bytes, COALESCE(checksum_sha256,''), retention_until, verified_at, job_id, COALESCE(error,''), created_at, finished_at`

func scanBackup(row pgx.Row) (Backup, error) {
	var b Backup
	err := row.Scan(&b.ID, &b.OrganizationID, &b.ProjectID, &b.Type, &b.Status, &b.SizeBytes, &b.Checksum, &b.RetentionUntil, &b.VerifiedAt, &b.JobID, &b.Error, &b.CreatedAt, &b.FinishedAt)
	return b, err
}

func (s *Service) List(ctx context.Context, ac authctx.AuthContext, projectID *uuid.UUID, limit, offset int) ([]Backup, error) {
	if err := rbac.Require(ac, "backups.read"); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `SELECT `+backupCols+` FROM backups
		WHERE organization_id=$1 AND status <> 'deleted' AND ($2::uuid IS NULL OR project_id=$2)
		ORDER BY created_at DESC LIMIT $3 OFFSET $4`, ac.OrganizationID, projectID, limit, offset)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeInternal, "failed to list backups", err)
	}
	defer rows.Close()
	out := []Backup{}
	for rows.Next() {
		b, err := scanBackup(rows)
		if err != nil {
			return nil, apierr.Wrap(apierr.CodeInternal, "failed to read backup", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *Service) Get(ctx context.Context, ac authctx.AuthContext, id uuid.UUID) (Backup, error) {
	if err := rbac.Require(ac, "backups.read"); err != nil {
		return Backup{}, err
	}
	return s.get(ctx, ac.OrganizationID, id)
}

func (s *Service) get(ctx context.Context, org, id uuid.UUID) (Backup, error) {
	b, err := scanBackup(s.pool.QueryRow(ctx, `SELECT `+backupCols+` FROM backups WHERE id=$1 AND organization_id=$2 AND status <> 'deleted'`, id, org))
	if errors.Is(err, pgx.ErrNoRows) {
		return Backup{}, apierr.NotFound("backup")
	}
	if err != nil {
		return Backup{}, apierr.Wrap(apierr.CodeInternal, "failed to load backup", err)
	}
	return b, nil
}

// ---------------- policies ----------------

type Policy struct {
	ID            uuid.UUID  `json:"id"`
	ProjectID     uuid.UUID  `json:"project_id"`
	Schedule      string     `json:"schedule"`
	Type          string     `json:"type"`
	RetentionDays int        `json:"retention_days"`
	Enabled       bool       `json:"enabled"`
	LastRunAt     *time.Time `json:"last_run_at,omitempty"`
}

type PolicyInput struct {
	Schedule      string `json:"schedule"`
	Type          string `json:"type"`
	RetentionDays int    `json:"retention_days"`
	Enabled       *bool  `json:"enabled"`
}

func (s *Service) SavePolicy(ctx context.Context, ac authctx.AuthContext, projectID uuid.UUID, in PolicyInput) (Policy, error) {
	if err := rbac.Require(ac, "backups.manage"); err != nil {
		return Policy{}, err
	}
	if in.Schedule != "daily" && in.Schedule != "weekly" && in.Schedule != "monthly" {
		return Policy{}, apierr.Validation("schedule must be daily, weekly or monthly")
	}
	if !validTypes[in.Type] {
		return Policy{}, apierr.Validation("invalid backup type")
	}
	if in.RetentionDays == 0 {
		in.RetentionDays = 30
	}
	if in.RetentionDays < 1 || in.RetentionDays > 3650 {
		return Policy{}, apierr.Validation("retention_days must be between 1 and 3650")
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	var p Policy
	err := s.pool.QueryRow(ctx, `
		INSERT INTO backup_policies (organization_id, project_id, schedule, type, retention_days, enabled)
		SELECT $1,$2,$3,$4,$5,$6 WHERE EXISTS (SELECT 1 FROM projects WHERE id=$2 AND organization_id=$1 AND deleted_at IS NULL)
		ON CONFLICT (project_id, schedule, type) DO UPDATE SET retention_days=EXCLUDED.retention_days, enabled=EXCLUDED.enabled
		RETURNING id, project_id, schedule, type, retention_days, enabled, last_run_at`,
		ac.OrganizationID, projectID, in.Schedule, in.Type, in.RetentionDays, enabled).
		Scan(&p.ID, &p.ProjectID, &p.Schedule, &p.Type, &p.RetentionDays, &p.Enabled, &p.LastRunAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Policy{}, apierr.NotFound("project")
	}
	if err != nil {
		return Policy{}, apierr.Wrap(apierr.CodeInternal, "failed to save policy", err)
	}
	s.rec(ctx, ac, "backups.policy.saved", "backup_policy", p.ID.String(), p)
	return p, nil
}

func (s *Service) ListPolicies(ctx context.Context, ac authctx.AuthContext, projectID uuid.UUID) ([]Policy, error) {
	if err := rbac.Require(ac, "backups.read"); err != nil {
		return nil, err
	}
	if _, err := s.projects.Get(ctx, ac, projectID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT id, project_id, schedule, type, retention_days, enabled, last_run_at FROM backup_policies
		WHERE organization_id=$1 AND project_id=$2 ORDER BY schedule, type`, ac.OrganizationID, projectID)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeInternal, "failed to list policies", err)
	}
	defer rows.Close()
	out := []Policy{}
	for rows.Next() {
		var p Policy
		if err := rows.Scan(&p.ID, &p.ProjectID, &p.Schedule, &p.Type, &p.RetentionDays, &p.Enabled, &p.LastRunAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Service) DeletePolicy(ctx context.Context, ac authctx.AuthContext, id uuid.UUID) error {
	if err := rbac.Require(ac, "backups.manage"); err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM backup_policies WHERE id=$1 AND organization_id=$2`, id, ac.OrganizationID)
	if err != nil {
		return apierr.Wrap(apierr.CodeInternal, "failed to delete policy", err)
	}
	if tag.RowsAffected() == 0 {
		return apierr.NotFound("policy")
	}
	s.rec(ctx, ac, "backups.policy.deleted", "backup_policy", id.String(), nil)
	return nil
}

func (s *Service) rec(ctx context.Context, ac authctx.AuthContext, action, typ, id string, state any) {
	if err := s.audit.Record(ctx, ac, audit.Entry{Action: action, ResourceType: typ, ResourceID: id, Success: true, ResultingState: state}); err != nil {
		logger.FromContext(ctx).Error("failed to write audit entry", "error", err)
	}
}

// ---------------- scheduler & retention ----------------

func interval(schedule string) time.Duration {
	switch schedule {
	case "weekly":
		return 7 * 24 * time.Hour
	case "monthly":
		return 30 * 24 * time.Hour
	}
	return 24 * time.Hour
}

// RunDuePolicies submits a backup.create operation for every enabled policy
// that is due. The idempotency key includes the policy and the period, so a
// restart or a second scheduler instance cannot double-run a period.
func (s *Service) RunDuePolicies(ctx context.Context, now time.Time) (int, error) {
	rows, err := s.pool.Query(ctx, `SELECT p.id, p.organization_id, p.project_id, p.schedule, p.type, p.retention_days, p.last_run_at
		FROM backup_policies p JOIN projects pr ON pr.id = p.project_id AND pr.deleted_at IS NULL AND pr.status = 'active'
		WHERE p.enabled`)
	if err != nil {
		return 0, err
	}
	type due struct {
		id, org, project uuid.UUID
		schedule, typ    string
		days             int
	}
	var list []due
	for rows.Next() {
		var d due
		var last *time.Time
		if err := rows.Scan(&d.id, &d.org, &d.project, &d.schedule, &d.typ, &d.days, &last); err != nil {
			rows.Close()
			return 0, err
		}
		if last == nil || now.Sub(*last) >= interval(d.schedule) {
			list = append(list, d)
		}
	}
	rows.Close()
	n := 0
	for _, d := range list {
		period := now.Truncate(interval(d.schedule)).Unix()
		ref, err := s.engine.SubmitTrusted(ctx, authctx.System(d.org), ops.SubmitInput{
			Operation: OpCreate, ProjectID: d.project,
			Payload:        CreatePayload{Type: d.typ, RetentionDays: d.days, PolicyID: &d.id},
			IdempotencyKey: fmt.Sprintf("backup-policy:%s:%d", d.id, period),
		})
		if err != nil {
			logger.FromContext(ctx).Error("scheduled backup submit failed", "policy", d.id, "error", err)
			continue
		}
		if ref.Created {
			n++
		}
		_, _ = s.pool.Exec(ctx, `UPDATE backup_policies SET last_run_at=$2 WHERE id=$1`, d.id, now)
	}
	return n, nil
}

// EnforceRetention deletes backups whose retention has passed (provider
// artifact first, record second so a failed delete is retried next sweep).
func (s *Service) EnforceRetention(ctx context.Context, now time.Time) (int, error) {
	if s.providers.Backups == nil {
		return 0, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT id, organization_id, COALESCE(storage_ref,'') FROM backups
		WHERE status IN ('completed','corrupt','failed') AND retention_until IS NOT NULL AND retention_until < $1
		  AND NOT EXISTS (SELECT 1 FROM restores r WHERE r.safety_backup_id = backups.id AND r.status = 'running')`, now)
	if err != nil {
		return 0, err
	}
	type exp struct {
		id, org uuid.UUID
		ref     string
	}
	var list []exp
	for rows.Next() {
		var e exp
		if err := rows.Scan(&e.id, &e.org, &e.ref); err != nil {
			rows.Close()
			return 0, err
		}
		list = append(list, e)
	}
	rows.Close()
	n := 0
	for _, e := range list {
		if e.ref != "" {
			if err := s.providers.Backups.Delete(ctx, e.ref); err != nil && !errors.Is(err, providers.ErrNotFound) {
				logger.FromContext(ctx).Error("retention delete failed", "backup", e.id, "error", err)
				continue
			}
		}
		if _, err := s.pool.Exec(ctx, `UPDATE backups SET status='deleted' WHERE id=$1`, e.id); err == nil {
			n++
			s.rec(ctx, authctx.System(e.org), "backups.backup.expired", "backup", e.id.String(), nil)
		}
	}
	return n, nil
}

// ---------------- shared snapshot logic ----------------

func (s *Service) requireProviders() error {
	if s.providers.Backups == nil {
		return apierr.New(apierr.CodeValidation, "backup provider is not configured on this deployment")
	}
	if s.providers.FS == nil {
		return apierr.New(apierr.CodeValidation, "filesystem provider is not configured on this deployment")
	}
	return nil
}

type dbRef struct{ name string }

func (s *Service) projectDB(ctx context.Context, org, project uuid.UUID) (*dbRef, error) {
	var name string
	err := s.pool.QueryRow(ctx, `SELECT name FROM project_databases WHERE organization_id=$1 AND project_id=$2 AND status='ready' ORDER BY created_at LIMIT 1`, org, project).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &dbRef{name: name}, nil
}

// snapshot creates, checksums and verifies a backup, recording its row. It is
// the single code path for user backups, scheduled backups and restore safety
// snapshots.
func (s *Service) snapshot(ctx context.Context, r *ops.Run, org, project uuid.UUID, in CreatePayload, jobID *uuid.UUID) (uuid.UUID, error) {
	p, err := s.projects.Get(ctx, authctx.System(org), project)
	if err != nil {
		return uuid.Nil, err
	}
	days := in.RetentionDays
	if days <= 0 {
		days = 30
	}
	var id uuid.UUID
	err = s.pool.QueryRow(ctx, `INSERT INTO backups (organization_id, project_id, policy_id, target_id, type, status, retention_until, job_id)
		VALUES ($1,$2,$3,$4,$5,'running', now() + make_interval(days => $6), $7) RETURNING id`,
		org, project, in.PolicyID, in.TargetID, in.Type, days, jobID).Scan(&id)
	if err != nil {
		return uuid.Nil, err
	}
	fail := func(err error) (uuid.UUID, error) {
		_, _ = s.pool.Exec(ctx, `UPDATE backups SET status='failed', error=$2, finished_at=now() WHERE id=$1`, id, truncate(err.Error(), 500))
		return id, err
	}

	ws := projects.WorkspacePath(p.Slug)
	src := providers.BackupSource{}
	wantFiles := in.Type == "files" || in.Type == "full"
	wantMedia := in.Type == "media"
	wantDB := in.Type == "database" || in.Type == "full"
	if wantFiles {
		if err := s.providers.FS.MkdirAll(ctx, ws+"/data"); err != nil {
			return fail(err)
		}
		src.Paths = append(src.Paths, ws+"/data")
	}
	if wantMedia {
		_ = s.providers.FS.MkdirAll(ctx, ws+"/data/wp-content/uploads")
		src.Paths = append(src.Paths, ws+"/data/wp-content/uploads")
	}
	if in.Type == "configuration" {
		cfg, _ := json.Marshal(map[string]any{"name": p.Name, "slug": p.Slug, "kind": p.Kind, "config": p.Config})
		if err := s.providers.FS.MkdirAll(ctx, ws+"/config"); err != nil {
			return fail(err)
		}
		if err := s.providers.FS.WriteFile(ctx, ws+"/config/project.json", cfg); err != nil {
			return fail(err)
		}
		src.Paths = append(src.Paths, ws+"/config")
	}
	if wantDB {
		db, err := s.projectDB(ctx, org, project)
		if err != nil {
			return fail(err)
		}
		switch {
		case db == nil && in.Type == "database":
			return fail(errors.New("project has no database to back up"))
		case db != nil && s.providers.DB == nil:
			return fail(errors.New("database provider is not configured; cannot dump the project database"))
		case db != nil:
			src.DatabaseDump = func(c context.Context, w io.Writer) error { return s.providers.DB.Dump(c, db.name, w) }
		}
	}
	if len(src.Paths) == 0 && src.DatabaseDump == nil {
		return fail(errors.New("nothing to back up for this project and type"))
	}

	art, err := s.providers.Backups.Create(ctx, id.String(), src)
	if err != nil {
		return fail(err)
	}
	// Verification is part of the backup: an unverified archive is not a backup.
	if err := s.providers.Backups.Verify(ctx, art.Ref, art.SHA256); err != nil {
		_ = s.providers.Backups.Delete(ctx, art.Ref)
		return fail(fmt.Errorf("verification failed: %w", err))
	}
	_, err = s.pool.Exec(ctx, `UPDATE backups SET status='completed', size_bytes=$2, checksum_sha256=$3, storage_ref=$4, verified_at=now(), finished_at=now() WHERE id=$1`,
		id, art.Size, art.SHA256, art.Ref)
	if err != nil {
		return fail(err)
	}
	if r != nil {
		r.Log("info", "backup stored and verified", map[string]any{"backup_id": id, "size_bytes": art.Size, "sha256": art.SHA256})
	}
	return id, nil
}

func jobPtr(r *ops.Run) *uuid.UUID {
	id := r.JobID
	return &id
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// ---------------- operations ----------------

type createOp struct {
	s       *Service
	org     uuid.UUID
	project uuid.UUID
	in      CreatePayload
	jobID   *uuid.UUID
}

func (o *createOp) Name() string                             { return OpCreate }
func (o *createOp) Rollback(context.Context, *ops.Run) error { return nil }
func (o *createOp) Execute(context.Context, *ops.Run) error  { return nil }
func (o *createOp) Validate(ctx context.Context) error {
	if o.project == uuid.Nil {
		return apierr.Validation("project_id is required")
	}
	if o.in.Type == "" {
		o.in.Type = "full"
	}
	if !validTypes[o.in.Type] {
		return apierr.Validation("invalid backup type")
	}
	if o.in.RetentionDays < 0 || o.in.RetentionDays > 3650 {
		return apierr.Validation("retention_days must be between 0 and 3650")
	}
	if err := o.s.requireProviders(); err != nil {
		return err
	}
	_, err := o.s.projects.Get(ctx, authctx.System(o.org), o.project)
	return err
}
func (o *createOp) Steps() []ops.Step {
	return []ops.Step{{
		Name: "create-and-verify",
		Run: func(ctx context.Context, r *ops.Run) error {
			id, err := o.s.snapshot(ctx, r, o.org, o.project, o.in, jobPtr(r))
			if err != nil {
				return err
			}
			r.SetResult(map[string]any{"backup_id": id})
			return nil
		},
	}}
}

type verifyOp struct {
	s      *Service
	org    uuid.UUID
	backup uuid.UUID
}

func (o *verifyOp) Name() string                             { return OpVerify }
func (o *verifyOp) Rollback(context.Context, *ops.Run) error { return nil }
func (o *verifyOp) Execute(context.Context, *ops.Run) error  { return nil }
func (o *verifyOp) Validate(ctx context.Context) error {
	if err := o.s.requireProviders(); err != nil {
		return err
	}
	_, err := o.s.get(ctx, o.org, o.backup)
	return err
}
func (o *verifyOp) Steps() []ops.Step {
	return []ops.Step{{Name: "verify-checksum", Run: func(ctx context.Context, r *ops.Run) error {
		var ref, sum string
		if err := o.s.pool.QueryRow(ctx, `SELECT COALESCE(storage_ref,''), COALESCE(checksum_sha256,'') FROM backups WHERE id=$1 AND organization_id=$2`, o.backup, o.org).Scan(&ref, &sum); err != nil {
			return err
		}
		if ref == "" || sum == "" {
			return errors.New("backup has no stored artifact to verify")
		}
		if err := o.s.providers.Backups.Verify(ctx, ref, sum); err != nil {
			_, _ = o.s.pool.Exec(ctx, `UPDATE backups SET status='corrupt', error=$2 WHERE id=$1`, o.backup, truncate(err.Error(), 500))
			return fmt.Errorf("backup is corrupt: %w", err)
		}
		_, err := o.s.pool.Exec(ctx, `UPDATE backups SET verified_at=now(), status='completed', error=NULL WHERE id=$1`, o.backup)
		return err
	}}}
}

type restoreOp struct {
	s       *Service
	org     uuid.UUID
	project uuid.UUID
	backup  uuid.UUID
	b       Backup
}

func (o *restoreOp) Name() string                             { return OpRestore }
func (o *restoreOp) Rollback(context.Context, *ops.Run) error { return nil }
func (o *restoreOp) Execute(context.Context, *ops.Run) error  { return nil }
func (o *restoreOp) Validate(ctx context.Context) error {
	if err := o.s.requireProviders(); err != nil {
		return err
	}
	b, err := o.s.get(ctx, o.org, o.backup)
	if err != nil {
		return err
	}
	if b.Status != "completed" {
		return apierr.Conflict("only completed backups can be restored (this one is " + b.Status + ")")
	}
	o.b = b
	if o.project == uuid.Nil {
		o.project = b.ProjectID
	}
	if o.project != b.ProjectID {
		return apierr.Validation("backup does not belong to this project")
	}
	return nil
}
func (o *restoreOp) Steps() []ops.Step {
	var restoreID, safetyID uuid.UUID
	return []ops.Step{
		{Name: "verify-source-backup", Run: func(ctx context.Context, r *ops.Run) error {
			var ref, sum string
			if err := o.s.pool.QueryRow(ctx, `SELECT COALESCE(storage_ref,''), COALESCE(checksum_sha256,'') FROM backups WHERE id=$1`, o.backup).Scan(&ref, &sum); err != nil {
				return err
			}
			if err := o.s.providers.Backups.Verify(ctx, ref, sum); err != nil {
				return fmt.Errorf("refusing to restore: source backup failed verification: %w", err)
			}
			return o.s.pool.QueryRow(ctx, `INSERT INTO restores (organization_id, project_id, backup_id, status, job_id) VALUES ($1,$2,$3,'running',$4) RETURNING id`,
				o.org, o.project, o.backup, jobPtr(r)).Scan(&restoreID)
		}, Undo: func(ctx context.Context, r *ops.Run) error {
			if restoreID != uuid.Nil {
				_, _ = o.s.pool.Exec(ctx, `UPDATE restores SET status='rolled_back', finished_at=now() WHERE id=$1 AND status='running'`, restoreID)
			}
			return nil
		}},
		{Name: "safety-snapshot", Run: func(ctx context.Context, r *ops.Run) error {
			id, err := o.s.snapshot(ctx, r, o.org, o.project, CreatePayload{Type: o.b.Type, RetentionDays: 7, Reason: "pre-restore safety snapshot"}, jobPtr(r))
			if err != nil {
				return fmt.Errorf("could not take a safety snapshot, restore aborted: %w", err)
			}
			safetyID = id
			_, err = o.s.pool.Exec(ctx, `UPDATE restores SET safety_backup_id=$2 WHERE id=$1`, restoreID, id)
			return err
		}},
		{Name: "restore",
			Run: func(ctx context.Context, r *ops.Run) error {
				return o.s.restoreArtifact(ctx, o.org, o.project, o.backup)
			},
			Undo: func(ctx context.Context, r *ops.Run) error {
				if safetyID == uuid.Nil {
					return nil
				}
				r.Log("warn", "restore failed: putting the safety snapshot back", nil)
				if err := o.s.restoreArtifact(ctx, o.org, o.project, safetyID); err != nil {
					return fmt.Errorf("ROLLBACK FAILED, manual recovery needed from safety backup %s: %w", safetyID, err)
				}
				return nil
			}},
		{Name: "finalize", Run: func(ctx context.Context, r *ops.Run) error {
			_, err := o.s.pool.Exec(ctx, `UPDATE restores SET status='completed', finished_at=now() WHERE id=$1`, restoreID)
			r.SetResult(map[string]any{"restore_id": restoreID, "safety_backup_id": safetyID})
			return err
		}},
	}
}

func (s *Service) restoreArtifact(ctx context.Context, org, project, backupID uuid.UUID) error {
	var ref, typ string
	if err := s.pool.QueryRow(ctx, `SELECT COALESCE(storage_ref,''), type FROM backups WHERE id=$1 AND organization_id=$2`, backupID, org).Scan(&ref, &typ); err != nil {
		return err
	}
	var dbLoad func(context.Context, io.Reader) error
	if typ == "database" || typ == "full" {
		if db, err := s.projectDB(ctx, org, project); err != nil {
			return err
		} else if db != nil {
			if s.providers.DB == nil {
				return errors.New("database provider is not configured; cannot restore the database")
			}
			dbLoad = func(c context.Context, r io.Reader) error { return s.providers.DB.Load(c, db.name, r) }
		}
	}
	return s.providers.Backups.Restore(ctx, ref, s.providers.FS, dbLoad)
}

type deleteOp struct {
	s      *Service
	org    uuid.UUID
	backup uuid.UUID
}

func (o *deleteOp) Name() string                             { return OpDelete }
func (o *deleteOp) Rollback(context.Context, *ops.Run) error { return nil }
func (o *deleteOp) Execute(context.Context, *ops.Run) error  { return nil }
func (o *deleteOp) Validate(ctx context.Context) error {
	if err := o.s.requireProviders(); err != nil {
		return err
	}
	if _, err := o.s.get(ctx, o.org, o.backup); err != nil {
		return err
	}
	var n int
	_ = o.s.pool.QueryRow(ctx, `SELECT count(*) FROM restores WHERE (backup_id=$1 OR safety_backup_id=$1) AND status='running'`, o.backup).Scan(&n)
	if n > 0 {
		return apierr.Conflict("backup is in use by a running restore")
	}
	return nil
}
func (o *deleteOp) Steps() []ops.Step {
	return []ops.Step{{Name: "delete-backup", Run: func(ctx context.Context, r *ops.Run) error {
		var ref string
		if err := o.s.pool.QueryRow(ctx, `SELECT COALESCE(storage_ref,'') FROM backups WHERE id=$1 AND organization_id=$2`, o.backup, o.org).Scan(&ref); err != nil {
			return err
		}
		if ref != "" {
			if err := o.s.providers.Backups.Delete(ctx, ref); err != nil && !errors.Is(err, providers.ErrNotFound) {
				return err
			}
		}
		_, err := o.s.pool.Exec(ctx, `UPDATE backups SET status='deleted' WHERE id=$1 AND organization_id=$2`, o.backup, o.org)
		return err
	}}}
}

var _ = strings.TrimSpace

// SnapshotNow takes a verified backup synchronously, for use inside another
// operation (e.g. the safety snapshot before a migration cutover). It is not
// reachable over HTTP.
func (s *Service) SnapshotNow(ctx context.Context, r *ops.Run, org, project uuid.UUID, typ string, retentionDays int, reason string) (uuid.UUID, error) {
	if err := s.requireProviders(); err != nil {
		return uuid.Nil, err
	}
	var job *uuid.UUID
	if r != nil {
		job = jobPtr(r)
	}
	return s.snapshot(ctx, r, org, project, CreatePayload{Type: typ, RetentionDays: retentionDays, Reason: reason}, job)
}

// RestoreNow restores a completed backup over the live project (used to roll a
// failed operation back). The caller is responsible for authorisation.
func (s *Service) RestoreNow(ctx context.Context, org, project, backupID uuid.UUID) error {
	if err := s.requireProviders(); err != nil {
		return err
	}
	return s.restoreArtifact(ctx, org, project, backupID)
}
