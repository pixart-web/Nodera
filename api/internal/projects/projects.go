// Package projects implements the client and project domain: the
// organisational units everything else (applications, domains, backups,
// deployments, migrations, monitoring) hangs off. Records are tenant-scoped,
// soft-deleted, paginated and audited. Provisioning of the underlying
// infrastructure is not done here — it is the "project.provision" operation
// (internal/provisioning) driven by the operation engine.
package projects

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nodera/nodera/internal/audit"
	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/platform/authctx"
	"github.com/nodera/nodera/internal/platform/logger"
	"github.com/nodera/nodera/internal/rbac"
)

type AuditRecorder interface {
	Record(ctx context.Context, ac authctx.AuthContext, e audit.Entry) error
}

type Service struct {
	pool  *pgxpool.Pool
	audit AuditRecorder
}

func New(pool *pgxpool.Pool, a AuditRecorder) *Service { return &Service{pool: pool, audit: a} }

type Client struct {
	ID             uuid.UUID `json:"id"`
	OrganizationID uuid.UUID `json:"organization_id"`
	Name           string    `json:"name"`
	ContactEmail   string    `json:"contact_email"`
	Notes          string    `json:"notes"`
	ProjectCount   int       `json:"project_count"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type Project struct {
	ID             uuid.UUID      `json:"id"`
	OrganizationID uuid.UUID      `json:"organization_id"`
	ClientID       *uuid.UUID     `json:"client_id"`
	ClientName     string         `json:"client_name,omitempty"`
	NodeID         *uuid.UUID     `json:"node_id"`
	Name           string         `json:"name"`
	Slug           string         `json:"slug"`
	Kind           string         `json:"kind"`
	Status         string         `json:"status"`
	Description    string         `json:"description"`
	Config         map[string]any `json:"config"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
}

// WorkspacePath is the project's directory under the filesystem provider root.
func WorkspacePath(slug string) string { return "projects/" + slug }

var slugRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,48}[a-z0-9])?$`)

// Slugify derives a URL/container-safe slug from a name.
func Slugify(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		default:
			if b.Len() > 0 && !dash {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 50 {
		out = strings.Trim(out[:50], "-")
	}
	return out
}

func (s *Service) rec(ctx context.Context, ac authctx.AuthContext, action, typ string, id uuid.UUID, state any) {
	if err := s.audit.Record(ctx, ac, audit.Entry{Action: action, ResourceType: typ, ResourceID: id.String(), Success: true, ResultingState: state}); err != nil {
		logger.FromContext(ctx).Error("failed to write audit entry", "error", err)
	}
}

func isUnique(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505"
}
func isFK(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23503"
}
func clampLimit(n int) int {
	if n <= 0 {
		return 50
	}
	if n > 200 {
		return 200
	}
	return n
}

// ---- Clients ----

type ClientInput struct {
	Name         string `json:"name"`
	ContactEmail string `json:"contact_email"`
	Notes        string `json:"notes"`
}

func (in *ClientInput) validate() error {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 120 {
		return apierr.FieldValidation("name", "name is required (max 120 characters)")
	}
	if len(in.Notes) > 4000 {
		return apierr.Validation("notes too long")
	}
	if in.ContactEmail != "" && (!strings.Contains(in.ContactEmail, "@") || len(in.ContactEmail) > 200) {
		return apierr.Validation("contact_email is not a valid email address")
	}
	return nil
}

const clientCols = `c.id, c.organization_id, c.name, c.contact_email, c.notes,
	(SELECT count(*) FROM projects p WHERE p.client_id = c.id AND p.deleted_at IS NULL), c.created_at, c.updated_at`

func scanClient(row pgx.Row) (Client, error) {
	var c Client
	err := row.Scan(&c.ID, &c.OrganizationID, &c.Name, &c.ContactEmail, &c.Notes, &c.ProjectCount, &c.CreatedAt, &c.UpdatedAt)
	return c, err
}

func (s *Service) CreateClient(ctx context.Context, ac authctx.AuthContext, in ClientInput) (Client, error) {
	if err := rbac.Require(ac, "clients.manage"); err != nil {
		return Client{}, err
	}
	if err := in.validate(); err != nil {
		return Client{}, err
	}
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `INSERT INTO clients (organization_id, name, contact_email, notes) VALUES ($1,$2,$3,$4) RETURNING id`,
		ac.OrganizationID, in.Name, in.ContactEmail, in.Notes).Scan(&id)
	if err != nil {
		if isUnique(err) {
			return Client{}, apierr.Conflict("a client with this name already exists")
		}
		return Client{}, apierr.Wrap(apierr.CodeInternal, "failed to create client", err)
	}
	c, err := s.GetClient(ctx, ac, id)
	if err == nil {
		s.rec(ctx, ac, "clients.client.created", "client", id, c)
	}
	return c, err
}

func (s *Service) GetClient(ctx context.Context, ac authctx.AuthContext, id uuid.UUID) (Client, error) {
	if err := rbac.Require(ac, "clients.read"); err != nil {
		return Client{}, err
	}
	c, err := scanClient(s.pool.QueryRow(ctx, `SELECT `+clientCols+` FROM clients c WHERE c.id=$1 AND c.organization_id=$2 AND c.deleted_at IS NULL`, id, ac.OrganizationID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Client{}, apierr.NotFound("client")
	}
	if err != nil {
		return Client{}, apierr.Wrap(apierr.CodeInternal, "failed to load client", err)
	}
	return c, nil
}

func (s *Service) ListClients(ctx context.Context, ac authctx.AuthContext, search string, limit, offset int) ([]Client, error) {
	if err := rbac.Require(ac, "clients.read"); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT `+clientCols+` FROM clients c
		WHERE c.organization_id=$1 AND c.deleted_at IS NULL AND ($2 = '' OR c.name ILIKE '%' || $2 || '%')
		ORDER BY lower(c.name) LIMIT $3 OFFSET $4`, ac.OrganizationID, escapeLike(search), clampLimit(limit), offset)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeInternal, "failed to list clients", err)
	}
	defer rows.Close()
	out := []Client{}
	for rows.Next() {
		c, err := scanClient(rows)
		if err != nil {
			return nil, apierr.Wrap(apierr.CodeInternal, "failed to read client", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Service) UpdateClient(ctx context.Context, ac authctx.AuthContext, id uuid.UUID, in ClientInput) (Client, error) {
	if err := rbac.Require(ac, "clients.manage"); err != nil {
		return Client{}, err
	}
	if err := in.validate(); err != nil {
		return Client{}, err
	}
	tag, err := s.pool.Exec(ctx, `UPDATE clients SET name=$3, contact_email=$4, notes=$5, updated_at=now()
		WHERE id=$1 AND organization_id=$2 AND deleted_at IS NULL`, id, ac.OrganizationID, in.Name, in.ContactEmail, in.Notes)
	if err != nil {
		if isUnique(err) {
			return Client{}, apierr.Conflict("a client with this name already exists")
		}
		return Client{}, apierr.Wrap(apierr.CodeInternal, "failed to update client", err)
	}
	if tag.RowsAffected() == 0 {
		return Client{}, apierr.NotFound("client")
	}
	c, err := s.GetClient(ctx, ac, id)
	if err == nil {
		s.rec(ctx, ac, "clients.client.updated", "client", id, c)
	}
	return c, err
}

// DeleteClient soft-deletes a client. Projects keep existing but lose the
// association (they are not deleted with their client).
func (s *Service) DeleteClient(ctx context.Context, ac authctx.AuthContext, id uuid.UUID) error {
	if err := rbac.Require(ac, "clients.manage"); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return apierr.Wrap(apierr.CodeInternal, "failed to delete client", err)
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE clients SET deleted_at=now() WHERE id=$1 AND organization_id=$2 AND deleted_at IS NULL`, id, ac.OrganizationID)
	if err != nil {
		return apierr.Wrap(apierr.CodeInternal, "failed to delete client", err)
	}
	if tag.RowsAffected() == 0 {
		return apierr.NotFound("client")
	}
	if _, err := tx.Exec(ctx, `UPDATE projects SET client_id=NULL, updated_at=now() WHERE client_id=$1 AND organization_id=$2`, id, ac.OrganizationID); err != nil {
		return apierr.Wrap(apierr.CodeInternal, "failed to detach projects", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return apierr.Wrap(apierr.CodeInternal, "failed to delete client", err)
	}
	s.rec(ctx, ac, "clients.client.deleted", "client", id, nil)
	return nil
}

// ---- Projects ----

type ProjectInput struct {
	Name        string         `json:"name"`
	Slug        string         `json:"slug"`
	Kind        string         `json:"kind"`
	ClientID    *uuid.UUID     `json:"client_id"`
	NodeID      *uuid.UUID     `json:"node_id"`
	Description string         `json:"description"`
	Config      map[string]any `json:"config"`
}

func (in *ProjectInput) validate() error {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 120 {
		return apierr.FieldValidation("name", "name is required (max 120 characters)")
	}
	if in.Slug == "" {
		in.Slug = Slugify(in.Name)
	}
	if !slugRe.MatchString(in.Slug) {
		return apierr.FieldValidation("slug", "slug must be 1-50 lowercase letters, digits or hyphens")
	}
	if in.Kind != "wordpress" && in.Kind != "application" {
		return apierr.FieldValidation("kind", "kind must be 'wordpress' or 'application'")
	}
	if len(in.Description) > 2000 {
		return apierr.Validation("description too long")
	}
	if in.Config == nil {
		in.Config = map[string]any{}
	}
	return nil
}

const projectCols = `p.id, p.organization_id, p.client_id, COALESCE(c.name,''), p.node_id, p.name, p.slug, p.kind, p.status,
	p.description, p.config, p.created_at, p.updated_at`
const projectFrom = ` FROM projects p LEFT JOIN clients c ON c.id = p.client_id `

func scanProject(row pgx.Row) (Project, error) {
	var p Project
	err := row.Scan(&p.ID, &p.OrganizationID, &p.ClientID, &p.ClientName, &p.NodeID, &p.Name, &p.Slug, &p.Kind, &p.Status,
		&p.Description, &p.Config, &p.CreatedAt, &p.UpdatedAt)
	if p.Config == nil {
		p.Config = map[string]any{}
	}
	return p, err
}

// Create records a project in status 'provisioning'. It does not touch
// infrastructure; submit the project.provision operation for that.
func (s *Service) Create(ctx context.Context, ac authctx.AuthContext, in ProjectInput) (Project, error) {
	if err := rbac.Require(ac, "projects.create"); err != nil {
		return Project{}, err
	}
	if err := in.validate(); err != nil {
		return Project{}, err
	}
	var uid any
	if ac.ActorType == authctx.ActorUser {
		uid = ac.ActorID
	}
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `
		INSERT INTO projects (organization_id, client_id, node_id, name, slug, kind, description, config, created_by_user_id)
		SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9
		WHERE ($2::uuid IS NULL OR EXISTS (SELECT 1 FROM clients WHERE id=$2 AND organization_id=$1 AND deleted_at IS NULL))
		  AND ($3::uuid IS NULL OR EXISTS (SELECT 1 FROM nodes WHERE id=$3 AND organization_id=$1))
		RETURNING id`,
		ac.OrganizationID, in.ClientID, in.NodeID, in.Name, in.Slug, in.Kind, in.Description, in.Config, uid).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Project{}, apierr.Validation("client_id or node_id does not refer to a resource in this organization")
	}
	if err != nil {
		if isUnique(err) {
			return Project{}, apierr.Conflict("a project with this slug already exists")
		}
		if isFK(err) {
			return Project{}, apierr.Validation("referenced resource does not exist")
		}
		return Project{}, apierr.Wrap(apierr.CodeInternal, "failed to create project", err)
	}
	p, err := s.Get(ctx, ac, id)
	if err == nil {
		s.rec(ctx, ac, "projects.project.created", "project", id, p)
	}
	return p, err
}

func (s *Service) Get(ctx context.Context, ac authctx.AuthContext, id uuid.UUID) (Project, error) {
	if err := rbac.Require(ac, "projects.read"); err != nil {
		return Project{}, err
	}
	return s.get(ctx, ac.OrganizationID, id)
}

func (s *Service) get(ctx context.Context, orgID, id uuid.UUID) (Project, error) {
	p, err := scanProject(s.pool.QueryRow(ctx, `SELECT `+projectCols+projectFrom+`WHERE p.id=$1 AND p.organization_id=$2 AND p.deleted_at IS NULL`, id, orgID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Project{}, apierr.NotFound("project")
	}
	if err != nil {
		return Project{}, apierr.Wrap(apierr.CodeInternal, "failed to load project", err)
	}
	return p, nil
}

type ListFilter struct {
	Search   string
	Status   string
	Kind     string
	ClientID *uuid.UUID
	Limit    int
	Offset   int
}

func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

func (s *Service) List(ctx context.Context, ac authctx.AuthContext, f ListFilter) ([]Project, error) {
	if err := rbac.Require(ac, "projects.read"); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT `+projectCols+projectFrom+`
		WHERE p.organization_id=$1 AND p.deleted_at IS NULL
		  AND ($2 = '' OR p.name ILIKE '%' || $2 || '%' OR p.slug ILIKE '%' || $2 || '%')
		  AND ($3 = '' OR p.status = $3) AND ($4 = '' OR p.kind = $4)
		  AND ($5::uuid IS NULL OR p.client_id = $5)
		ORDER BY p.created_at DESC, p.id LIMIT $6 OFFSET $7`,
		ac.OrganizationID, escapeLike(f.Search), f.Status, f.Kind, f.ClientID, clampLimit(f.Limit), f.Offset)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeInternal, "failed to list projects", err)
	}
	defer rows.Close()
	out := []Project{}
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, apierr.Wrap(apierr.CodeInternal, "failed to read project", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

type UpdateInput struct {
	Name        *string        `json:"name"`
	Description *string        `json:"description"`
	ClientID    *uuid.UUID     `json:"client_id"`
	Config      map[string]any `json:"config"`
}

func (s *Service) Update(ctx context.Context, ac authctx.AuthContext, id uuid.UUID, in UpdateInput) (Project, error) {
	if err := rbac.Require(ac, "projects.update"); err != nil {
		return Project{}, err
	}
	if in.Name != nil {
		n := strings.TrimSpace(*in.Name)
		if n == "" || len(n) > 120 {
			return Project{}, apierr.Validation("name must be 1-120 characters")
		}
		in.Name = &n
	}
	if in.Description != nil && len(*in.Description) > 2000 {
		return Project{}, apierr.Validation("description too long")
	}
	tag, err := s.pool.Exec(ctx, `UPDATE projects SET
			name = COALESCE($3, name), description = COALESCE($4, description),
			client_id = CASE WHEN $5::uuid IS NULL THEN client_id ELSE $5 END,
			config = CASE WHEN $6::jsonb IS NULL THEN config ELSE $6 END, updated_at = now()
		WHERE id=$1 AND organization_id=$2 AND deleted_at IS NULL
		  AND ($5::uuid IS NULL OR EXISTS (SELECT 1 FROM clients WHERE id=$5 AND organization_id=$2 AND deleted_at IS NULL))`,
		id, ac.OrganizationID, in.Name, in.Description, in.ClientID, in.Config)
	if err != nil {
		return Project{}, apierr.Wrap(apierr.CodeInternal, "failed to update project", err)
	}
	if tag.RowsAffected() == 0 {
		return Project{}, apierr.NotFound("project")
	}
	p, err := s.get(ctx, ac.OrganizationID, id)
	if err == nil {
		s.rec(ctx, ac, "projects.project.updated", "project", id, p)
	}
	return p, err
}

// SetStatus is used by operations (system context) to move a project through
// its lifecycle. It is not exposed over HTTP.
func (s *Service) SetStatus(ctx context.Context, orgID, id uuid.UUID, status string) error {
	_, err := s.pool.Exec(ctx, `UPDATE projects SET status=$3, updated_at=now() WHERE id=$1 AND organization_id=$2 AND deleted_at IS NULL`, id, orgID, status)
	return err
}

// Overview is the data behind the project's Overview tab: real counts of
// related resources, nothing fabricated.
type Overview struct {
	Project      Project        `json:"project"`
	Counts       map[string]int `json:"counts"`
	LastActivity *time.Time     `json:"last_activity"`
}

func (s *Service) Overview(ctx context.Context, ac authctx.AuthContext, id uuid.UUID) (Overview, error) {
	p, err := s.Get(ctx, ac, id)
	if err != nil {
		return Overview{}, err
	}
	o := Overview{Project: p, Counts: map[string]int{}}
	for key, q := range map[string]string{
		"applications": `SELECT count(*) FROM applications WHERE project_id=$1 AND organization_id=$2`,
		"domains":      `SELECT count(*) FROM domains WHERE project_id=$1 AND organization_id=$2`,
		"certificates": `SELECT count(*) FROM certificates c JOIN domains d ON d.id=c.domain_id WHERE d.project_id=$1 AND c.organization_id=$2`,
		"backups":      `SELECT count(*) FROM backups WHERE project_id=$1 AND organization_id=$2`,
		"deployments":  `SELECT count(*) FROM deployments WHERE project_id=$1 AND organization_id=$2`,
		"migrations":   `SELECT count(*) FROM site_migrations WHERE project_id=$1 AND organization_id=$2`,
		"monitors":     `SELECT count(*) FROM monitors WHERE project_id=$1 AND organization_id=$2`,
		"incidents":    `SELECT count(*) FROM incidents WHERE project_id=$1 AND organization_id=$2 AND status NOT IN ('resolved','closed')`,
	} {
		var n int
		if err := s.pool.QueryRow(ctx, q, id, ac.OrganizationID).Scan(&n); err != nil {
			return Overview{}, apierr.Wrap(apierr.CodeInternal, "failed to count "+key, err)
		}
		o.Counts[key] = n
	}
	_ = s.pool.QueryRow(ctx, `SELECT max(created_at) FROM jobs WHERE project_id=$1 AND organization_id=$2`, id, ac.OrganizationID).Scan(&o.LastActivity)
	return o, nil
}

// MarkDeleted soft-deletes a project (called by the delete operation).
func (s *Service) MarkDeleted(ctx context.Context, orgID, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `UPDATE projects SET status='deleted', deleted_at=now(), updated_at=now() WHERE id=$1 AND organization_id=$2`, id, orgID)
	return err
}

// ---- related resources shown on the project detail tabs ----

type Database struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Engine    string    `json:"engine"`
	Username  string    `json:"username"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// Databases lists a project's databases. Credentials are never included (the
// password lives encrypted in internal/secrets and is referenced, not shown).
func (s *Service) Databases(ctx context.Context, ac authctx.AuthContext, id uuid.UUID) ([]Database, error) {
	if _, err := s.Get(ctx, ac, id); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT id, name, engine, username, status, created_at FROM project_databases
		WHERE organization_id=$1 AND project_id=$2 AND status <> 'deleted' ORDER BY created_at`, ac.OrganizationID, id)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeInternal, "failed to list databases", err)
	}
	defer rows.Close()
	out := []Database{}
	for rows.Next() {
		var d Database
		if err := rows.Scan(&d.ID, &d.Name, &d.Engine, &d.Username, &d.Status, &d.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

type ProjectApplication struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Kind        string    `json:"kind"`
	Environment string    `json:"environment"`
	Status      string    `json:"status"`
}

func (s *Service) Applications(ctx context.Context, ac authctx.AuthContext, id uuid.UUID) ([]ProjectApplication, error) {
	if _, err := s.Get(ctx, ac, id); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT id, name, kind, environment, status FROM applications WHERE organization_id=$1 AND project_id=$2 ORDER BY name`, ac.OrganizationID, id)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeInternal, "failed to list applications", err)
	}
	defer rows.Close()
	out := []ProjectApplication{}
	for rows.Next() {
		var a ProjectApplication
		if err := rows.Scan(&a.ID, &a.Name, &a.Kind, &a.Environment, &a.Status); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
