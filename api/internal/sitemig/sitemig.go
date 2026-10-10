// Package sitemig implements the website migration engine. A migration moves
// an existing site (WordPress first) into a Nodera project in three separately
// operated, auditable stages:
//
//	plan     discovery + preflight (PASS / WARNING / BLOCKER), no side effects
//	run      transfer into a STAGING area, import into a staging database,
//	         rewrite URLs (serialized-data safe), validate, compute a health score
//	cutover  (approval required) safety backup -> swap files + database ->
//	         restart -> health check; any failure restores the safety backup
//
// The live site is never touched before cutover. Sources that need a network
// connector (FTP/SFTP/SSH/cPanel/UpdraftPlus remote) are pluggable through the
// Connector interface; archive (zip) sources are fully implemented here.
package sitemig

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nodera/nodera/internal/audit"
	"github.com/nodera/nodera/internal/backups"
	"github.com/nodera/nodera/internal/network"
	"github.com/nodera/nodera/internal/ops"
	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/platform/authctx"
	"github.com/nodera/nodera/internal/platform/logger"
	"github.com/nodera/nodera/internal/projects"
	"github.com/nodera/nodera/internal/providers"
	"github.com/nodera/nodera/internal/rbac"
	"github.com/nodera/nodera/internal/secrets"
)

const (
	OpPlan     = "migration.plan"
	OpRun      = "migration.run"
	OpCutover  = "migration.cutover"
	OpRollback = "migration.rollback"

	MaxArchiveBytes = 64 << 20  // upload limit through the API
	maxExtracted    = 512 << 20 // zip-bomb guard
	maxEntries      = 100000
)

var sourceKinds = map[string]bool{"cpanel": true, "ftp": true, "sftp": true, "ssh": true, "updraftplus": true, "zip": true, "wordpress": true, "manual": true}
var archiveKinds = map[string]bool{"zip": true, "updraftplus": true, "manual": true}
var secretLikeKey = regexp.MustCompile(`(?i)pass|secret|token|key|credential`)

type AuditRecorder interface {
	Record(ctx context.Context, ac authctx.AuthContext, e audit.Entry) error
}

// SourceInfo is what discovery learns about the source site.
type SourceInfo struct {
	IsWordPress bool     `json:"is_wordpress"`
	WPVersion   string   `json:"wp_version,omitempty"`
	PHPVersion  string   `json:"php_version,omitempty"`
	Multisite   bool     `json:"multisite"`
	Plugins     []string `json:"plugins"`
	Themes      []string `json:"themes"`
	Files       int      `json:"files"`
	SizeBytes   int64    `json:"size_bytes"`
	HasDatabase bool     `json:"has_database"`
	DBBytes     int64    `json:"db_bytes"`
	SiteURL     string   `json:"site_url,omitempty"`
}

// Connector fetches a site from a remote source. Implementations are
// registered per source kind; none ships enabled by default except in mock
// mode, so unsupported kinds fail preflight with a BLOCKER instead of
// pretending to connect.
type Connector interface {
	Discover(ctx context.Context, cfg map[string]any, creds map[string]string) (SourceInfo, error)
	// Pull writes the site under dest: files in dest/files, the database dump
	// in dest/database.sql.
	Pull(ctx context.Context, cfg map[string]any, creds map[string]string, fs providers.FilesystemProvider, dest string) error
}

type Service struct {
	pool       *pgxpool.Pool
	audit      AuditRecorder
	projects   *projects.Service
	backups    *backups.Service
	secrets    *secrets.Service
	providers  providers.Set
	connectors map[string]Connector
}

func New(pool *pgxpool.Pool, a AuditRecorder, p *projects.Service, b *backups.Service, sec *secrets.Service, set providers.Set) *Service {
	return &Service{pool: pool, audit: a, projects: p, backups: b, secrets: sec, providers: set, connectors: map[string]Connector{}}
}

// RegisterConnector enables a remote source kind.
func (s *Service) RegisterConnector(kind string, c Connector) { s.connectors[kind] = c }

func isUnique(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505"
}

func (s *Service) rec(ctx context.Context, ac authctx.AuthContext, action, id string, state any) {
	if err := s.audit.Record(ctx, ac, audit.Entry{Action: action, ResourceType: "migration", ResourceID: id, Success: true, ResultingState: state}); err != nil {
		logger.FromContext(ctx).Error("failed to write audit entry", "error", err)
	}
}

// ---------------- records ----------------

type Check struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"` // PASS | WARNING | BLOCKER
	Detail string `json:"detail,omitempty"`
}
type Report struct {
	Checks     []Check `json:"checks"`
	Pass       int     `json:"pass"`
	Warnings   int     `json:"warnings"`
	Blockers   int     `json:"blockers"`
	CanProceed bool    `json:"can_proceed"`
}

func (r *Report) add(id, title, status, detail string) {
	r.Checks = append(r.Checks, Check{ID: id, Title: title, Status: status, Detail: detail})
	switch status {
	case "PASS":
		r.Pass++
	case "WARNING":
		r.Warnings++
	case "BLOCKER":
		r.Blockers++
	}
	r.CanProceed = r.Blockers == 0
}

type Migration struct {
	org            uuid.UUID
	ID             uuid.UUID       `json:"id"`
	ProjectID      *uuid.UUID      `json:"project_id"`
	SourceKind     string          `json:"source_kind"`
	SourceConfig   map[string]any  `json:"source_config"`
	TargetDomain   string          `json:"target_domain"`
	SourceURL      string          `json:"source_url,omitempty"`
	Status         string          `json:"status"`
	Phase          string          `json:"phase"`
	HasSource      bool            `json:"has_source_archive"`
	Preflight      json.RawMessage `json:"preflight_report,omitempty"`
	Validation     json.RawMessage `json:"validation_report,omitempty"`
	HealthScore    *int            `json:"health_score"`
	SafetyBackupID *uuid.UUID      `json:"safety_backup_id,omitempty"`
	Error          string          `json:"error,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
	FinishedAt     *time.Time      `json:"finished_at,omitempty"`
}

const migCols = `organization_id, id, project_id, source_kind, source_config, target_domain, COALESCE(source_url,''), status, phase, source_ref IS NOT NULL,
	preflight_report, validation_report, health_score, safety_backup_id, COALESCE(error,''), created_at, updated_at, finished_at`

func scanMig(row pgx.Row) (Migration, error) {
	var m Migration
	err := row.Scan(&m.org, &m.ID, &m.ProjectID, &m.SourceKind, &m.SourceConfig, &m.TargetDomain, &m.SourceURL, &m.Status, &m.Phase, &m.HasSource,
		&m.Preflight, &m.Validation, &m.HealthScore, &m.SafetyBackupID, &m.Error, &m.CreatedAt, &m.UpdatedAt, &m.FinishedAt)
	if m.SourceConfig == nil {
		m.SourceConfig = map[string]any{}
	}
	return m, err
}

type CreateInput struct {
	ProjectID    *uuid.UUID        `json:"project_id"`
	SourceKind   string            `json:"source_kind"`
	SourceConfig map[string]any    `json:"source_config"`
	Credentials  map[string]string `json:"credentials"`
	TargetDomain string            `json:"target_domain"`
	SourceURL    string            `json:"source_url"`
}

func (s *Service) Create(ctx context.Context, ac authctx.AuthContext, in CreateInput) (Migration, error) {
	if err := rbac.Require(ac, "migrations.create"); err != nil {
		return Migration{}, err
	}
	if !sourceKinds[in.SourceKind] {
		return Migration{}, apierr.Validation("unsupported source_kind")
	}
	dom, err := network.NormalizeDomain(in.TargetDomain)
	if err != nil {
		return Migration{}, err
	}
	for k := range in.SourceConfig {
		if secretLikeKey.MatchString(k) {
			return Migration{}, apierr.Validation("source_config must not contain secrets (" + k + "); send them in `credentials`")
		}
	}
	if in.SourceURL != "" {
		if u := strings.TrimRight(in.SourceURL, "/"); !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") || len(u) > 300 {
			return Migration{}, apierr.Validation("source_url must be an http(s) URL")
		}
		in.SourceURL = strings.TrimRight(in.SourceURL, "/")
	}
	if len(in.Credentials) > 0 && s.secrets == nil {
		return Migration{}, apierr.New(apierr.CodeValidation, "secrets service is not configured; credentials cannot be stored")
	}
	if in.SourceConfig == nil {
		in.SourceConfig = map[string]any{}
	}
	var uid any
	if ac.ActorType == authctx.ActorUser {
		uid = ac.ActorID
	}
	id := uuid.New()
	credKey := ""
	if len(in.Credentials) > 0 {
		credKey = "migration/" + id.String() + "/credentials"
	}
	var created uuid.UUID
	err = s.pool.QueryRow(ctx, `
		INSERT INTO site_migrations (id, organization_id, project_id, source_kind, source_config, target_domain, source_url, credential_secret_key, created_by_user_id)
		SELECT $1,$2,$3,$4,$5,$6,NULLIF($7,''),NULLIF($8,''),$9
		WHERE $3::uuid IS NULL OR EXISTS (SELECT 1 FROM projects WHERE id=$3 AND organization_id=$2 AND deleted_at IS NULL)
		RETURNING id`, id, ac.OrganizationID, in.ProjectID, in.SourceKind, in.SourceConfig, dom, in.SourceURL, credKey, uid).Scan(&created)
	if errors.Is(err, pgx.ErrNoRows) {
		return Migration{}, apierr.Validation("project_id does not refer to a project in this organization")
	}
	if err != nil {
		if isUnique(err) {
			return Migration{}, apierr.Conflict("a migration to this domain is already in progress")
		}
		return Migration{}, apierr.Wrap(apierr.CodeInternal, "failed to create migration", err)
	}
	if credKey != "" {
		b, _ := json.Marshal(in.Credentials)
		if _, err := s.secrets.Set(ctx, ac, credKey, string(b), "Migration source credentials"); err != nil {
			_, _ = s.pool.Exec(ctx, `DELETE FROM site_migrations WHERE id=$1`, id)
			return Migration{}, err
		}
	}
	m, err := s.Get(ctx, ac, id)
	if err == nil {
		s.rec(ctx, ac, "migrations.migration.created", id.String(), map[string]any{"source_kind": in.SourceKind, "target_domain": dom}) // never the credentials
	}
	return m, err
}

func (s *Service) Get(ctx context.Context, ac authctx.AuthContext, id uuid.UUID) (Migration, error) {
	if err := rbac.Require(ac, "migrations.read"); err != nil {
		return Migration{}, err
	}
	return s.get(ctx, ac.OrganizationID, id)
}

func (s *Service) get(ctx context.Context, org, id uuid.UUID) (Migration, error) {
	m, err := scanMig(s.pool.QueryRow(ctx, `SELECT `+migCols+` FROM site_migrations WHERE id=$1 AND organization_id=$2`, id, org))
	if errors.Is(err, pgx.ErrNoRows) {
		return Migration{}, apierr.NotFound("migration")
	}
	if err != nil {
		return Migration{}, apierr.Wrap(apierr.CodeInternal, "failed to load migration", err)
	}
	return m, nil
}

func (s *Service) List(ctx context.Context, ac authctx.AuthContext, projectID *uuid.UUID, limit, offset int) ([]Migration, error) {
	if err := rbac.Require(ac, "migrations.read"); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `SELECT `+migCols+` FROM site_migrations WHERE organization_id=$1 AND ($2::uuid IS NULL OR project_id=$2)
		ORDER BY created_at DESC LIMIT $3 OFFSET $4`, ac.OrganizationID, projectID, limit, offset)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeInternal, "failed to list migrations", err)
	}
	defer rows.Close()
	out := []Migration{}
	for rows.Next() {
		m, err := scanMig(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Service) archivePath(org, id uuid.UUID) string {
	return "migrations/" + org.String() + "/" + id.String() + "/source.zip"
}
func (s *Service) stagePath(org, id uuid.UUID) string {
	return "migrations/" + org.String() + "/" + id.String() + "/stage"
}

// UploadSource stores the uploaded archive for archive-type sources.
func (s *Service) UploadSource(ctx context.Context, ac authctx.AuthContext, id uuid.UUID, r io.Reader) (Migration, error) {
	if err := rbac.Require(ac, "migrations.create"); err != nil {
		return Migration{}, err
	}
	if s.providers.FS == nil {
		return Migration{}, apierr.New(apierr.CodeValidation, "filesystem provider is not configured on this deployment")
	}
	m, err := s.get(ctx, ac.OrganizationID, id)
	if err != nil {
		return Migration{}, err
	}
	if !archiveKinds[m.SourceKind] {
		return Migration{}, apierr.Validation("this migration's source is not an archive")
	}
	if m.Status != "discovering" && m.Status != "planned" && m.Status != "preflight_failed" {
		return Migration{}, apierr.Conflict("the source can only be replaced before the migration runs")
	}
	data, err := io.ReadAll(io.LimitReader(r, MaxArchiveBytes+1))
	if err != nil {
		return Migration{}, apierr.Validation("could not read upload")
	}
	if len(data) > MaxArchiveBytes {
		return Migration{}, apierr.Validation(fmt.Sprintf("archive exceeds the %d MiB upload limit", MaxArchiveBytes>>20))
	}
	if len(data) < 4 || !bytes.HasPrefix(data, []byte("PK\x03\x04")) {
		return Migration{}, apierr.Validation("file is not a zip archive")
	}
	ref := s.archivePath(ac.OrganizationID, id)
	if err := s.providers.FS.WriteFile(ctx, ref, data); err != nil {
		return Migration{}, apierr.Wrap(apierr.CodeInternal, "failed to store archive", err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE site_migrations SET source_ref=$2, status='discovering', phase='discovery', preflight_report=NULL, updated_at=now() WHERE id=$1`, id, ref); err != nil {
		return Migration{}, apierr.Wrap(apierr.CodeInternal, "failed to record archive", err)
	}
	s.rec(ctx, ac, "migrations.migration.source_uploaded", id.String(), map[string]any{"bytes": len(data)})
	return s.get(ctx, ac.OrganizationID, id)
}

func (s *Service) setStatus(ctx context.Context, id uuid.UUID, status, phase string, errMsg string) {
	_, _ = s.pool.Exec(ctx, `UPDATE site_migrations SET status=$2, phase=COALESCE(NULLIF($3,''),phase), error=NULLIF($4,''), updated_at=now(),
		finished_at = CASE WHEN $2 IN ('completed','failed','rolled_back','preflight_failed') THEN now() ELSE NULL END WHERE id=$1`, id, status, phase, errMsg)
}

func (s *Service) credentials(ctx context.Context, org uuid.UUID, id uuid.UUID) map[string]string {
	if s.secrets == nil {
		return nil
	}
	v, err := s.secrets.Reveal(ctx, authctx.System(org), "migration/"+id.String()+"/credentials")
	if err != nil {
		return nil
	}
	out := map[string]string{}
	_ = json.Unmarshal([]byte(v), &out)
	return out
}

// ---------------- operations ----------------

func (s *Service) Register(eng *ops.Engine) {
	mk := func(name, perm, tool, notify string, timeout int, f func(o base) ops.Operation) {
		eng.Register(ops.Definition{
			Name: name, Permission: perm, ToolKey: tool, NotifyKind: notify, TimeoutSeconds: timeout, Flag: "migration_engine",
			Factory: func(_ *ops.Env, org, project uuid.UUID, payload json.RawMessage) (ops.Operation, error) {
				var p struct {
					MigrationID uuid.UUID `json:"migration_id"`
				}
				_ = json.Unmarshal(payload, &p)
				if _, id := ops.PayloadResource(payload); id != uuid.Nil {
					p.MigrationID = id
				}
				return f(base{s: s, org: org, id: p.MigrationID, name: name}), nil
			},
		})
	}
	mk(OpPlan, "migrations.create", "", "migration.planned", 600, func(b base) ops.Operation { return &planOp{b} })
	mk(OpRun, "migrations.create", "", "migration.ready", 3600, func(b base) ops.Operation { return &runOp{base: b} })
	mk(OpCutover, "migrations.cutover", "migration.cutover", "migration.completed", 3600, func(b base) ops.Operation { return &cutoverOp{base: b} })
	mk(OpRollback, "migrations.cutover", "", "migration.rolled_back", 3600, func(b base) ops.Operation { return &rollbackOp{base: b} })
}

type base struct {
	s    *Service
	org  uuid.UUID
	id   uuid.UUID
	name string
	m    Migration
}

func (b *base) Name() string                             { return b.name }
func (b *base) Rollback(context.Context, *ops.Run) error { return nil }
func (b *base) Execute(context.Context, *ops.Run) error  { return nil }
func (b *base) load(ctx context.Context) error {
	m, err := b.s.get(ctx, b.org, b.id)
	b.m = m
	return err
}

// ---- plan ----

type planOp struct{ base }

func (o *planOp) Validate(ctx context.Context) error {
	if err := o.load(ctx); err != nil {
		return err
	}
	switch o.m.Status {
	case "discovering", "planned", "preflight_failed":
		return nil
	}
	return apierr.Conflict("migration is " + o.m.Status + "; it can only be planned before it runs")
}

func (o *planOp) Steps() []ops.Step {
	var info SourceInfo
	var discoverErr error
	return []ops.Step{
		{Name: "discovery", Run: func(ctx context.Context, r *ops.Run) error {
			info, discoverErr = o.s.discover(ctx, o.m)
			if discoverErr != nil {
				r.Log("warning", "discovery failed: "+discoverErr.Error(), nil)
			} else {
				r.Log("info", fmt.Sprintf("discovered %d files (%d bytes), wordpress=%v", info.Files, info.SizeBytes, info.IsWordPress), nil)
			}
			return nil // failure is reported as a preflight BLOCKER, not a crashed operation
		}},
		{Name: "preflight", Run: func(ctx context.Context, r *ops.Run) error {
			rep := o.s.preflight(ctx, o.m, info, discoverErr)
			b, _ := json.Marshal(map[string]any{"report": rep, "source": info})
			status := "planned"
			if !rep.CanProceed {
				status = "preflight_failed"
			}
			if _, err := o.s.pool.Exec(ctx, `UPDATE site_migrations SET preflight_report=$2, source_url=COALESCE(source_url, NULLIF($3,'')), status=$4, phase='preflight', error=NULL, updated_at=now() WHERE id=$1`,
				o.id, b, strings.TrimRight(info.SiteURL, "/"), status); err != nil {
				return err
			}
			r.Log("info", fmt.Sprintf("preflight: %d pass, %d warnings, %d blockers", rep.Pass, rep.Warnings, rep.Blockers), nil)
			r.SetResult(rep)
			return nil
		}},
	}
}

func (s *Service) discover(ctx context.Context, m Migration) (SourceInfo, error) {
	if archiveKinds[m.SourceKind] {
		if s.providers.FS == nil {
			return SourceInfo{}, errors.New("filesystem provider is not configured")
		}
		if !m.HasSource {
			return SourceInfo{}, errors.New("no archive has been uploaded yet")
		}
		data, err := s.providers.FS.ReadFile(ctx, s.archivePath(m.orgID(), m.ID))
		if err != nil {
			return SourceInfo{}, err
		}
		return inspectArchive(data)
	}
	c, ok := s.connectors[m.SourceKind]
	if !ok {
		return SourceInfo{}, fmt.Errorf("no connector is available for %q sources in this deployment", m.SourceKind)
	}
	return c.Discover(ctx, m.SourceConfig, s.credentials(ctx, m.orgID(), m.ID))
}

// orgID is resolved via the migration's project or the stored org; Migration
// does not carry the org id on the wire, so the service keeps it internally.
func (m Migration) orgID() uuid.UUID { return m.org }

func (s *Service) preflight(ctx context.Context, m Migration, info SourceInfo, derr error) Report {
	var r Report
	if derr != nil {
		r.add("source", "Source is reachable and readable", "BLOCKER", derr.Error())
	} else {
		r.add("source", "Source is reachable and readable", "PASS", "")
	}
	if derr == nil {
		if info.IsWordPress {
			r.add("wordpress", "WordPress installation detected", "PASS", "version "+orDash(info.WPVersion))
		} else {
			r.add("wordpress", "WordPress installation detected", "BLOCKER", "no wp-config.php / wp-includes found; only WordPress sites are supported")
		}
		if info.IsWordPress {
			r.add("wp_config", "Custom wp-config.php settings", "WARNING", "wp-config.php is not migrated (it holds the old server's database credentials); re-add any custom constants after cutover")
		}
		if info.Multisite {
			r.add("multisite", "Single-site installation", "BLOCKER", "WordPress multisite is not supported")
		} else {
			r.add("multisite", "Single-site installation", "PASS", "")
		}
		if info.HasDatabase {
			r.add("database", "Database dump present", "PASS", fmt.Sprintf("%d bytes", info.DBBytes))
		} else {
			r.add("database", "Database dump present", "BLOCKER", "no .sql dump found in the source")
		}
		if info.SiteURL == "" && m.SourceURL == "" {
			r.add("source_url", "Original site URL known", "WARNING", "could not detect siteurl; set source_url so URLs can be rewritten")
		} else {
			r.add("source_url", "Original site URL known", "PASS", orDash(firstNonEmpty(m.SourceURL, info.SiteURL)))
		}
		if info.SizeBytes > 400<<20 {
			r.add("size", "Site size within limits", "WARNING", fmt.Sprintf("%d MiB; transfer will be slow", info.SizeBytes>>20))
		} else {
			r.add("size", "Site size within limits", "PASS", fmt.Sprintf("%d MiB", info.SizeBytes>>20))
		}
		if v := info.PHPVersion; v != "" && !strings.HasPrefix(v, "7.4") && !strings.HasPrefix(v, "8.") {
			r.add("php", "PHP version compatible", "WARNING", "source runs PHP "+v+"; target uses PHP 8.3 and old plugins may break")
		}
		for _, p := range info.Plugins {
			switch p {
			case "wp-rocket", "w3-total-cache", "wp-super-cache":
				r.add("plugin-"+p, "Cache plugin "+p, "WARNING", "server-level cache configuration is not migrated; purge and reconfigure after cutover")
			case "wordfence":
				r.add("plugin-wordfence", "Security plugin wordfence", "WARNING", "its firewall auto-prepend file references the old path; reinstall after cutover")
			}
		}
	}
	// Target side.
	if _, err := network.NormalizeDomain(m.TargetDomain); err != nil {
		r.add("target_domain", "Target domain is valid", "BLOCKER", err.Error())
	} else {
		r.add("target_domain", "Target domain is valid", "PASS", m.TargetDomain)
	}
	if m.ProjectID == nil {
		r.add("target_project", "Target project selected", "BLOCKER", "create and provision a WordPress project first, then attach it to this migration")
	} else if p, err := s.projects.Get(ctx, authctx.System(m.org), *m.ProjectID); err != nil {
		r.add("target_project", "Target project selected", "BLOCKER", "project not found")
	} else if p.Status != "active" || p.Kind != "wordpress" {
		r.add("target_project", "Target project is an active WordPress project", "BLOCKER", "project is "+p.Kind+"/"+p.Status)
	} else {
		r.add("target_project", "Target project is an active WordPress project", "PASS", p.Slug)
	}
	if s.providers.FS == nil {
		r.add("provider_fs", "Filesystem provider available", "BLOCKER", "not configured")
	}
	if s.providers.DB == nil {
		r.add("provider_db", "Database provider available", "BLOCKER", "not configured on this deployment; databases cannot be imported")
	} else {
		r.add("provider_db", "Database provider available", "PASS", "")
	}
	if s.backups == nil {
		r.add("safety_backup", "Safety backup available", "BLOCKER", "backup service not configured; cutover would be unsafe")
	} else {
		r.add("safety_backup", "Safety backup available", "PASS", "a full backup is taken before cutover")
	}
	return r
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
func firstNonEmpty(a ...string) string {
	for _, s := range a {
		if s != "" {
			return s
		}
	}
	return ""
}

// ---- run ----

type runOp struct{ base }

func (o *runOp) Validate(ctx context.Context) error {
	if err := o.load(ctx); err != nil {
		return err
	}
	if o.m.Status != "planned" {
		return apierr.Conflict("migration is " + o.m.Status + "; run it only after a plan with no blockers")
	}
	if o.s.providers.FS == nil || o.s.providers.DB == nil {
		return apierr.New(apierr.CodeValidation, "filesystem and database providers are required")
	}
	return nil
}

func stagingDB(id uuid.UUID) string { return "mig_" + strings.ReplaceAll(id.String(), "-", "")[:16] }

func (o *runOp) Steps() []ops.Step {
	fs := func() providers.FilesystemProvider { return o.s.providers.FS }
	db := func() providers.DatabaseProvider { return o.s.providers.DB }
	stage := o.s.stagePath(o.org, o.id)
	dbName := stagingDB(o.id)
	var info SourceInfo
	var rewrites int
	var oldURL string
	return []ops.Step{
		{Name: "mark-running", Run: func(ctx context.Context, r *ops.Run) error {
			o.s.setStatus(ctx, o.id, "transferring", "transfer", "")
			return nil
		}, Undo: func(ctx context.Context, r *ops.Run) error {
			o.s.setStatus(ctx, o.id, "failed", "", "migration run failed and was rolled back; the live site was not touched")
			return nil
		}},
		{Name: "transfer-files",
			Run: func(ctx context.Context, r *ops.Run) error {
				if archiveKinds[o.m.SourceKind] {
					data, err := fs().ReadFile(ctx, o.s.archivePath(o.org, o.id))
					if err != nil {
						return err
					}
					i, err := extractArchive(ctx, fs(), data, stage)
					if err != nil {
						return err
					}
					info = i
				} else {
					c, ok := o.s.connectors[o.m.SourceKind]
					if !ok {
						return fmt.Errorf("no connector for %q", o.m.SourceKind)
					}
					if err := c.Pull(ctx, o.m.SourceConfig, o.s.credentials(ctx, o.org, o.id), fs(), stage); err != nil {
						return err
					}
					i, err := c.Discover(ctx, o.m.SourceConfig, o.s.credentials(ctx, o.org, o.id))
					if err != nil {
						return err
					}
					info = i
				}
				o.s.setStatus(ctx, o.id, "importing", "database", "")
				r.Log("info", fmt.Sprintf("transferred %d files (%d bytes) into staging", info.Files, info.SizeBytes), nil)
				return nil
			},
			Undo: func(ctx context.Context, r *ops.Run) error { return fs().Remove(ctx, stage) }},
		{Name: "create-staging-database",
			Run: func(ctx context.Context, r *ops.Run) error {
				pw, _ := randomPassword()
				if _, err := db().EnsureDatabase(ctx, dbName, "u_"+dbName[4:], pw); err != nil {
					return err
				}
				_, err := o.s.pool.Exec(ctx, `UPDATE site_migrations SET staging_db=$2 WHERE id=$1`, o.id, dbName)
				return err
			},
			Undo: func(ctx context.Context, r *ops.Run) error {
				return db().DropDatabase(ctx, dbName, "u_"+dbName[4:])
			}},
		{Name: "import-database", Run: func(ctx context.Context, r *ops.Run) error {
			dump, err := fs().ReadFile(ctx, stage+"/database.sql")
			if err != nil {
				return fmt.Errorf("no database dump in the staged source: %w", err)
			}
			oldURL = strings.TrimRight(firstNonEmpty(o.m.SourceURL, info.SiteURL), "/")
			newURL := "https://" + o.m.TargetDomain
			text := string(dump)
			if oldURL != "" && oldURL != newURL {
				var n int
				text, n = RewriteURLs(text, oldURL, newURL)
				rewrites = n
				r.Log("info", fmt.Sprintf("rewrote %s -> %s in %d values (serialized lengths recomputed)", oldURL, newURL, n), nil)
				// Also cover the scheme-flipped variant (http<->https) of the same host.
				if alt := flipScheme(oldURL); alt != "" {
					text, _ = RewriteURLs(text, alt, newURL)
				}
			}
			o.s.setStatus(ctx, o.id, "importing", "url_rewrite", "")
			return db().Load(ctx, dbName, strings.NewReader(text))
		}},
		{Name: "validate", Run: func(ctx context.Context, r *ops.Run) error {
			o.s.setStatus(ctx, o.id, "validating", "validation", "")
			var dumpBuf bytes.Buffer
			if err := db().Dump(ctx, dbName, &dumpBuf); err != nil {
				return err
			}
			rep, score := scoreMigration(info, dumpBuf.String(), oldURL, "https://"+o.m.TargetDomain, rewrites)
			b, _ := json.Marshal(rep)
			if _, err := o.s.pool.Exec(ctx, `UPDATE site_migrations SET validation_report=$2, health_score=$3, updated_at=now() WHERE id=$1`, o.id, b, score); err != nil {
				return err
			}
			r.Log("info", fmt.Sprintf("health score %d/100", score), nil)
			if rep.Blockers > 0 {
				return errors.New("validation found blocking problems; staging was discarded")
			}
			return nil
		}},
		{Name: "ready", Run: func(ctx context.Context, r *ops.Run) error {
			o.s.setStatus(ctx, o.id, "ready_for_cutover", "validation", "")
			return nil
		}},
	}
}

func flipScheme(u string) string {
	switch {
	case strings.HasPrefix(u, "http://"):
		return "https://" + strings.TrimPrefix(u, "http://")
	case strings.HasPrefix(u, "https://"):
		return "http://" + strings.TrimPrefix(u, "https://")
	}
	return ""
}

func scoreMigration(info SourceInfo, dump, oldURL, newURL string, rewrites int) (Report, int) {
	var r Report
	score := 0
	if info.Files > 0 {
		r.add("files", "Files transferred", "PASS", fmt.Sprintf("%d files", info.Files))
		score += 30
	} else {
		r.add("files", "Files transferred", "BLOCKER", "no files were transferred")
	}
	if len(dump) > 0 {
		r.add("database", "Database imported", "PASS", fmt.Sprintf("%d bytes", len(dump)))
		score += 25
	} else {
		r.add("database", "Database imported", "BLOCKER", "database is empty")
	}
	switch {
	case oldURL == "":
		r.add("urls", "No references to the old URL remain", "WARNING", "original URL unknown; nothing was rewritten")
		score += 5
	case CountOccurrences(dump, oldURL) == 0:
		r.add("urls", "No references to the old URL remain", "PASS", fmt.Sprintf("%d values rewritten", rewrites))
		score += 20
	default:
		r.add("urls", "No references to the old URL remain", "WARNING", fmt.Sprintf("%d references remain (e.g. inside uncommon contexts)", CountOccurrences(dump, oldURL)))
		score += 8
	}
	if info.IsWordPress {
		r.add("wordpress", "WordPress core present", "PASS", "")
		score += 10
	}
	if len(info.Plugins) > 0 || len(info.Themes) > 0 {
		r.add("content", "Plugins/themes present", "PASS", fmt.Sprintf("%d plugins, %d themes", len(info.Plugins), len(info.Themes)))
		score += 10
	} else {
		r.add("content", "Plugins/themes present", "WARNING", "no plugins or themes found")
		score += 5
	}
	if strings.Contains(dump, newURL) {
		r.add("target_url", "Database references the new URL", "PASS", "")
		score += 5
	} else {
		r.add("target_url", "Database references the new URL", "WARNING", "the new URL does not appear in the database")
	}
	if score > 100 {
		score = 100
	}
	if r.Blockers > 0 && score > 40 {
		score = 40
	}
	return r, score
}

// ---- cutover ----

type cutoverOp struct{ base }

func (o *cutoverOp) Validate(ctx context.Context) error {
	if err := o.load(ctx); err != nil {
		return err
	}
	if o.m.Status != "ready_for_cutover" {
		return apierr.Conflict("migration is " + o.m.Status + "; only a migration that is ready_for_cutover can be cut over")
	}
	if o.m.ProjectID == nil {
		return apierr.Validation("no target project")
	}
	if o.m.HealthScore != nil && *o.m.HealthScore < 50 {
		return apierr.Conflict(fmt.Sprintf("health score %d is below the 50 threshold; fix the issues before cutover", *o.m.HealthScore))
	}
	if o.s.backups == nil {
		return apierr.New(apierr.CodeValidation, "backup service is required for a safe cutover")
	}
	return nil
}

func (o *cutoverOp) Steps() []ops.Step {
	fs := func() providers.FilesystemProvider { return o.s.providers.FS }
	db := func() providers.DatabaseProvider { return o.s.providers.DB }
	stage := o.s.stagePath(o.org, o.id)
	var safety uuid.UUID
	var proj projects.Project
	var targetDB string
	restore := func(ctx context.Context, r *ops.Run) error {
		if safety == uuid.Nil {
			return nil
		}
		r.Log("warning", "restoring the safety backup", nil)
		if err := o.s.backups.RestoreNow(ctx, o.org, *o.m.ProjectID, safety); err != nil {
			return fmt.Errorf("ROLLBACK FAILED; recover manually from safety backup %s: %w", safety, err)
		}
		return nil
	}
	return []ops.Step{
		{Name: "begin", Run: func(ctx context.Context, r *ops.Run) error {
			p, err := o.s.projects.Get(ctx, authctx.System(o.org), *o.m.ProjectID)
			if err != nil {
				return err
			}
			proj = p
			err = o.s.pool.QueryRow(ctx, `SELECT name FROM project_databases WHERE organization_id=$1 AND project_id=$2 AND status='ready' ORDER BY created_at LIMIT 1`, o.org, p.ID).Scan(&targetDB)
			if err != nil {
				return errors.New("target project has no ready database")
			}
			o.s.setStatus(ctx, o.id, "cutting_over", "cutover", "")
			return nil
		}, Undo: func(ctx context.Context, r *ops.Run) error {
			o.s.setStatus(ctx, o.id, "ready_for_cutover", "validation", "cutover failed and was rolled back; the previous site is restored")
			return nil
		}},
		{Name: "safety-backup", Run: func(ctx context.Context, r *ops.Run) error {
			id, err := o.s.backups.SnapshotNow(ctx, r, o.org, proj.ID, "full", 30, "pre-cutover safety backup")
			if err != nil {
				return fmt.Errorf("cannot cut over without a verified safety backup: %w", err)
			}
			safety = id
			_, err = o.s.pool.Exec(ctx, `UPDATE site_migrations SET safety_backup_id=$2 WHERE id=$1`, o.id, id)
			return err
		}},
		{Name: "swap-files",
			Run: func(ctx context.Context, r *ops.Run) error {
				live := projects.WorkspacePath(proj.Slug) + "/data"
				if err := fs().Remove(ctx, live); err != nil {
					return err
				}
				return copyDir(ctx, fs(), stage+"/files", live)
			},
			Undo: restore},
		{Name: "swap-database", Run: func(ctx context.Context, r *ops.Run) error {
			var buf bytes.Buffer
			if err := db().Dump(ctx, stagingDB(o.id), &buf); err != nil {
				return err
			}
			return db().Load(ctx, targetDB, &buf)
		}},
		{Name: "restart-and-verify", Run: func(ctx context.Context, r *ops.Run) error {
			c := o.s.providers.Containers
			if c == nil {
				r.Log("warning", "no container provider: runtime restart and health could not be verified", nil)
				return nil
			}
			name := "nodera-" + proj.Slug
			if err := c.Restart(ctx, name); err != nil && !errors.Is(err, providers.ErrNotFound) {
				return err
			}
			info, ok, err := c.Inspect(ctx, name)
			if err != nil {
				return err
			}
			if ok && info.State != "running" {
				return fmt.Errorf("site container is %q after cutover", info.State)
			}
			return nil
		}},
		{Name: "finalize", Run: func(ctx context.Context, r *ops.Run) error {
			cfg := proj.Config
			if cfg == nil {
				cfg = map[string]any{}
			}
			cfg["primary_domain"] = o.m.TargetDomain
			if _, err := o.s.projects.Update(ctx, authctx.System(o.org), proj.ID, projects.UpdateInput{Config: cfg}); err != nil {
				return err
			}
			o.s.setStatus(ctx, o.id, "completed", "complete", "")
			// Staging is no longer needed; the safety backup is kept for its retention period.
			_ = db().DropDatabase(ctx, stagingDB(o.id), "u_"+stagingDB(o.id)[4:])
			_ = fs().Remove(ctx, stage)
			r.SetResult(map[string]any{"safety_backup_id": safety, "domain": o.m.TargetDomain})
			return nil
		}},
	}
}

// ---- rollback after completion ----

type rollbackOp struct{ base }

func (o *rollbackOp) Validate(ctx context.Context) error {
	if err := o.load(ctx); err != nil {
		return err
	}
	if o.m.Status != "completed" {
		return apierr.Conflict("only a completed migration can be rolled back")
	}
	if o.m.SafetyBackupID == nil || o.m.ProjectID == nil {
		return apierr.Conflict("this migration has no safety backup to restore")
	}
	return nil
}
func (o *rollbackOp) Steps() []ops.Step {
	return []ops.Step{{Name: "restore-safety-backup", Run: func(ctx context.Context, r *ops.Run) error {
		if err := o.s.backups.RestoreNow(ctx, o.org, *o.m.ProjectID, *o.m.SafetyBackupID); err != nil {
			return err
		}
		o.s.setStatus(ctx, o.id, "rolled_back", "cutover", "")
		return nil
	}}}
}

func copyDir(ctx context.Context, fs providers.FilesystemProvider, src, dst string) error {
	entries, err := fs.List(ctx, src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		rel := strings.TrimPrefix(strings.TrimPrefix(e.Path, src), "/")
		if e.IsDir {
			if err := copyDir(ctx, fs, e.Path, path.Join(dst, rel)); err != nil {
				return err
			}
			continue
		}
		data, err := fs.ReadFile(ctx, e.Path)
		if err != nil {
			return err
		}
		if err := fs.WriteFile(ctx, path.Join(dst, rel), data); err != nil {
			return err
		}
	}
	return nil
}
