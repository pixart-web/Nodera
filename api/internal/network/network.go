// Package network implements domains, DNS records and SSL certificates. The
// database is the source of truth; DNS and certificate providers are
// synchronisation targets behind interfaces (local simulation, mock, and
// later Cloudflare/Hetzner/Let's Encrypt). Private keys are never stored in
// plain text: they go straight into internal/secrets (AES-GCM).
package network

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nodera/nodera/internal/audit"
	"github.com/nodera/nodera/internal/ops"
	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/platform/authctx"
	"github.com/nodera/nodera/internal/platform/logger"
	"github.com/nodera/nodera/internal/providers"
	"github.com/nodera/nodera/internal/rbac"
	"github.com/nodera/nodera/internal/secrets"
)

const (
	OpIssue        = "ssl.issue"
	OpRenew        = "ssl.renew"
	OpRevoke       = "ssl.revoke"
	OpRemoveDomain = "domain.remove"

	renewWindow = 30 * 24 * time.Hour
	maxRecords  = 200
)

type AuditRecorder interface {
	Record(ctx context.Context, ac authctx.AuthContext, e audit.Entry) error
}

type Service struct {
	pool      *pgxpool.Pool
	audit     AuditRecorder
	secrets   *secrets.Service
	providers providers.Set
	engine    *ops.Engine
}

func New(pool *pgxpool.Pool, a AuditRecorder, sec *secrets.Service, set providers.Set) *Service {
	return &Service{pool: pool, audit: a, secrets: sec, providers: set}
}

func isUnique(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505"
}

func (s *Service) rec(ctx context.Context, ac authctx.AuthContext, action, typ, id string, state any) {
	if err := s.audit.Record(ctx, ac, audit.Entry{Action: action, ResourceType: typ, ResourceID: id, Success: true, ResultingState: state}); err != nil {
		logger.FromContext(ctx).Error("failed to write audit entry", "error", err)
	}
}

// ---------------- domains ----------------

type Domain struct {
	ID             uuid.UUID  `json:"id"`
	OrganizationID uuid.UUID  `json:"organization_id"`
	ProjectID      *uuid.UUID `json:"project_id"`
	Name           string     `json:"name"`
	Status         string     `json:"status"`
	DNSStatus      string     `json:"dns_status"`
	SSLStatus      string     `json:"ssl_status"`
	DNSProvider    string     `json:"dns_provider"`
	VerifiedAt     *time.Time `json:"verified_at"`
	CreatedAt      time.Time  `json:"created_at"`
}

const domainCols = `id, organization_id, project_id, name, status, dns_status, ssl_status, dns_provider, verified_at, created_at`

func scanDomain(row pgx.Row) (Domain, error) {
	var d Domain
	err := row.Scan(&d.ID, &d.OrganizationID, &d.ProjectID, &d.Name, &d.Status, &d.DNSStatus, &d.SSLStatus, &d.DNSProvider, &d.VerifiedAt, &d.CreatedAt)
	return d, err
}

func (s *Service) dnsName() string {
	if s.providers.DNS == nil {
		return "none"
	}
	return "configured"
}

// AddDomain registers a domain (optionally to a project) and creates its zone.
func (s *Service) AddDomain(ctx context.Context, ac authctx.AuthContext, name string, projectID *uuid.UUID) (Domain, error) {
	if err := rbac.Require(ac, "domains.manage"); err != nil {
		return Domain{}, err
	}
	if s.providers.DNS == nil {
		return Domain{}, apierr.New(apierr.CodeValidation, "DNS provider is not configured on this deployment")
	}
	n, err := NormalizeDomain(name)
	if err != nil {
		return Domain{}, err
	}
	d, err := scanDomain(s.pool.QueryRow(ctx, `
		INSERT INTO domains (organization_id, project_id, name, dns_provider)
		SELECT $1, $2, $3, $4 WHERE $2::uuid IS NULL OR EXISTS (SELECT 1 FROM projects WHERE id=$2 AND organization_id=$1 AND deleted_at IS NULL)
		RETURNING `+domainCols, ac.OrganizationID, projectID, n, s.dnsName()))
	if errors.Is(err, pgx.ErrNoRows) {
		return Domain{}, apierr.Validation("project_id does not refer to a project in this organization")
	}
	if err != nil {
		if isUnique(err) {
			return Domain{}, apierr.Conflict("this domain is already registered")
		}
		return Domain{}, apierr.Wrap(apierr.CodeInternal, "failed to add domain", err)
	}
	if err := s.providers.DNS.EnsureZone(ctx, n); err != nil {
		_, _ = s.pool.Exec(ctx, `DELETE FROM domains WHERE id=$1`, d.ID)
		return Domain{}, apierr.Wrap(apierr.CodeInternal, "failed to create DNS zone", err)
	}
	s.rec(ctx, ac, "domains.domain.added", "domain", d.ID.String(), d)
	return d, nil
}

func (s *Service) ListDomains(ctx context.Context, ac authctx.AuthContext, projectID *uuid.UUID, search string, limit, offset int) ([]Domain, error) {
	if err := rbac.Require(ac, "domains.read"); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `SELECT `+domainCols+` FROM domains
		WHERE organization_id=$1 AND deleted_at IS NULL AND status <> 'removed' AND ($2::uuid IS NULL OR project_id=$2)
		  AND ($3 = '' OR name ILIKE '%' || $3 || '%') ORDER BY name LIMIT $4 OFFSET $5`,
		ac.OrganizationID, projectID, strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(search), limit, offset)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeInternal, "failed to list domains", err)
	}
	defer rows.Close()
	out := []Domain{}
	for rows.Next() {
		d, err := scanDomain(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Service) GetDomain(ctx context.Context, ac authctx.AuthContext, id uuid.UUID) (Domain, error) {
	if err := rbac.Require(ac, "domains.read"); err != nil {
		return Domain{}, err
	}
	return s.getDomain(ctx, ac.OrganizationID, id)
}

func (s *Service) getDomain(ctx context.Context, org, id uuid.UUID) (Domain, error) {
	d, err := scanDomain(s.pool.QueryRow(ctx, `SELECT `+domainCols+` FROM domains WHERE id=$1 AND organization_id=$2 AND deleted_at IS NULL AND status <> 'removed'`, id, org))
	if errors.Is(err, pgx.ErrNoRows) {
		return Domain{}, apierr.NotFound("domain")
	}
	if err != nil {
		return Domain{}, apierr.Wrap(apierr.CodeInternal, "failed to load domain", err)
	}
	return d, nil
}

// ---------------- DNS records ----------------

type Record struct {
	ID       uuid.UUID `json:"id"`
	DomainID uuid.UUID `json:"domain_id"`
	Type     string    `json:"type"`
	Name     string    `json:"name"`
	Value    string    `json:"value"`
	TTL      int       `json:"ttl"`
	Priority int       `json:"priority,omitempty"`
}

func (s *Service) ListRecords(ctx context.Context, ac authctx.AuthContext, domainID uuid.UUID) ([]Record, error) {
	if err := rbac.Require(ac, "domains.read"); err != nil {
		return nil, err
	}
	if _, err := s.getDomain(ctx, ac.OrganizationID, domainID); err != nil {
		return nil, err
	}
	return s.records(ctx, ac.OrganizationID, domainID)
}

func (s *Service) records(ctx context.Context, org, domainID uuid.UUID) ([]Record, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, domain_id, type, name, value, ttl, COALESCE(priority,0) FROM dns_records
		WHERE organization_id=$1 AND domain_id=$2 ORDER BY type, name, value`, org, domainID)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeInternal, "failed to list records", err)
	}
	defer rows.Close()
	out := []Record{}
	for rows.Next() {
		var r Record
		if err := rows.Scan(&r.ID, &r.DomainID, &r.Type, &r.Name, &r.Value, &r.TTL, &r.Priority); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func toProvider(in RecordInput) providers.DNSRecord {
	return providers.DNSRecord{Type: in.Type, Name: in.Name, Value: in.Value, TTL: in.TTL, Priority: in.Priority}
}

// UpsertRecord validates, stores and pushes a record to the DNS provider. A
// provider failure leaves the database unchanged.
func (s *Service) UpsertRecord(ctx context.Context, ac authctx.AuthContext, domainID uuid.UUID, in RecordInput) (Record, error) {
	if err := rbac.Require(ac, "domains.manage"); err != nil {
		return Record{}, err
	}
	if s.providers.DNS == nil {
		return Record{}, apierr.New(apierr.CodeValidation, "DNS provider is not configured on this deployment")
	}
	if err := in.Validate(); err != nil {
		return Record{}, err
	}
	d, err := s.getDomain(ctx, ac.OrganizationID, domainID)
	if err != nil {
		return Record{}, err
	}
	existing, err := s.records(ctx, ac.OrganizationID, domainID)
	if err != nil {
		return Record{}, err
	}
	if len(existing) >= maxRecords {
		return Record{}, apierr.Validation(fmt.Sprintf("a domain can hold at most %d records", maxRecords))
	}
	for _, r := range existing {
		if r.Name != in.Name {
			continue
		}
		if in.Type == "CNAME" && r.Type != "CNAME" || r.Type == "CNAME" && in.Type != "CNAME" {
			return Record{}, apierr.Conflict("a CNAME cannot coexist with other records of the same name")
		}
		if in.Type == "CNAME" && r.Type == "CNAME" && r.Value != in.Value {
			return Record{}, apierr.Conflict("a name can only have one CNAME")
		}
	}
	pr, err := s.providers.DNS.UpsertRecord(ctx, d.Name, toProvider(in))
	if err != nil {
		return Record{}, apierr.Wrap(apierr.CodeInternal, "DNS provider rejected the record", err)
	}
	var out Record
	err = s.pool.QueryRow(ctx, `
		INSERT INTO dns_records (organization_id, domain_id, type, name, value, ttl, priority, provider_record_id)
		VALUES ($1,$2,$3,$4,$5,$6,NULLIF($7,0),$8)
		ON CONFLICT (domain_id, type, name, value) DO UPDATE SET ttl=EXCLUDED.ttl, priority=EXCLUDED.priority, provider_record_id=EXCLUDED.provider_record_id, updated_at=now()
		RETURNING id, domain_id, type, name, value, ttl, COALESCE(priority,0)`,
		ac.OrganizationID, domainID, in.Type, in.Name, in.Value, in.TTL, in.Priority, pr.ID).
		Scan(&out.ID, &out.DomainID, &out.Type, &out.Name, &out.Value, &out.TTL, &out.Priority)
	if err != nil {
		return Record{}, apierr.Wrap(apierr.CodeInternal, "failed to store record", err)
	}
	_, _ = s.pool.Exec(ctx, `UPDATE domains SET dns_status='pending', updated_at=now() WHERE id=$1`, domainID)
	s.rec(ctx, ac, "domains.record.upserted", "dns_record", out.ID.String(), out)
	return out, nil
}

func (s *Service) DeleteRecord(ctx context.Context, ac authctx.AuthContext, domainID, recordID uuid.UUID) error {
	if err := rbac.Require(ac, "domains.manage"); err != nil {
		return err
	}
	if s.providers.DNS == nil {
		return apierr.New(apierr.CodeValidation, "DNS provider is not configured on this deployment")
	}
	d, err := s.getDomain(ctx, ac.OrganizationID, domainID)
	if err != nil {
		return err
	}
	var pid *string
	if err := s.pool.QueryRow(ctx, `SELECT provider_record_id FROM dns_records WHERE id=$1 AND domain_id=$2 AND organization_id=$3`, recordID, domainID, ac.OrganizationID).Scan(&pid); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return apierr.NotFound("record")
		}
		return apierr.Wrap(apierr.CodeInternal, "failed to load record", err)
	}
	if pid != nil {
		if err := s.providers.DNS.DeleteRecord(ctx, d.Name, *pid); err != nil && !errors.Is(err, providers.ErrNotFound) {
			return apierr.Wrap(apierr.CodeInternal, "DNS provider failed to delete the record", err)
		}
	}
	if _, err := s.pool.Exec(ctx, `DELETE FROM dns_records WHERE id=$1`, recordID); err != nil {
		return apierr.Wrap(apierr.CodeInternal, "failed to delete record", err)
	}
	s.rec(ctx, ac, "domains.record.deleted", "dns_record", recordID.String(), nil)
	return nil
}

// Sync re-pushes every stored record to the provider. Needed after a restart
// of an in-process (local) DNS provider, and as a drift repair tool.
func (s *Service) Sync(ctx context.Context, ac authctx.AuthContext, domainID uuid.UUID) (int, error) {
	if err := rbac.Require(ac, "domains.manage"); err != nil {
		return 0, err
	}
	if s.providers.DNS == nil {
		return 0, apierr.New(apierr.CodeValidation, "DNS provider is not configured on this deployment")
	}
	d, err := s.getDomain(ctx, ac.OrganizationID, domainID)
	if err != nil {
		return 0, err
	}
	if err := s.providers.DNS.EnsureZone(ctx, d.Name); err != nil {
		return 0, apierr.Wrap(apierr.CodeInternal, "failed to ensure zone", err)
	}
	recs, err := s.records(ctx, ac.OrganizationID, domainID)
	if err != nil {
		return 0, err
	}
	for _, r := range recs {
		pr, err := s.providers.DNS.UpsertRecord(ctx, d.Name, providers.DNSRecord{Type: r.Type, Name: r.Name, Value: r.Value, TTL: r.TTL, Priority: r.Priority})
		if err != nil {
			return 0, apierr.Wrap(apierr.CodeInternal, "sync failed on "+r.Type+" "+r.Name, err)
		}
		_, _ = s.pool.Exec(ctx, `UPDATE dns_records SET provider_record_id=$2 WHERE id=$1`, r.ID, pr.ID)
	}
	return len(recs), nil
}

type Propagation struct {
	Record     Record `json:"record"`
	Propagated bool   `json:"propagated"`
	Error      string `json:"error,omitempty"`
}

// CheckPropagation asks the provider whether each stored record is observable
// and updates the domain's dns_status truthfully (ok only if all are).
func (s *Service) CheckPropagation(ctx context.Context, ac authctx.AuthContext, domainID uuid.UUID) ([]Propagation, error) {
	if err := rbac.Require(ac, "domains.read"); err != nil {
		return nil, err
	}
	if s.providers.DNS == nil {
		return nil, apierr.New(apierr.CodeValidation, "DNS provider is not configured on this deployment")
	}
	d, err := s.getDomain(ctx, ac.OrganizationID, domainID)
	if err != nil {
		return nil, err
	}
	recs, err := s.records(ctx, ac.OrganizationID, domainID)
	if err != nil {
		return nil, err
	}
	out := make([]Propagation, 0, len(recs))
	all := len(recs) > 0
	for _, r := range recs {
		ok, err := s.providers.DNS.CheckPropagation(ctx, d.Name, providers.DNSRecord{Type: r.Type, Name: r.Name, Value: r.Value, TTL: r.TTL})
		p := Propagation{Record: r, Propagated: ok}
		if err != nil {
			p.Error = err.Error()
		}
		if !ok {
			all = false
		}
		out = append(out, p)
	}
	status := "pending"
	if all {
		status = "ok"
	}
	_, _ = s.pool.Exec(ctx, `UPDATE domains SET dns_status=$2,
		status = CASE WHEN $2='ok' THEN 'active' ELSE status END,
		verified_at = CASE WHEN $2='ok' THEN COALESCE(verified_at, now()) ELSE verified_at END, updated_at=now() WHERE id=$1`, domainID, status)
	return out, nil
}

// ---------------- certificates ----------------

type Certificate struct {
	ID         uuid.UUID  `json:"id"`
	DomainID   uuid.UUID  `json:"domain_id"`
	Domain     string     `json:"domain"`
	Status     string     `json:"status"`
	Provider   string     `json:"provider"`
	Issuer     string     `json:"issuer"`
	Serial     string     `json:"serial"`
	NotBefore  *time.Time `json:"not_before"`
	NotAfter   *time.Time `json:"not_after"`
	DaysLeft   *int       `json:"days_left"`
	AutoRenew  bool       `json:"auto_renew"`
	LastError  string     `json:"last_error,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	HasPrivKey bool       `json:"has_private_key"`
}

const certCols = `c.id, c.domain_id, d.name, c.status, c.provider, c.issuer, c.serial, c.not_before, c.not_after, c.auto_renew, COALESCE(c.last_error,''), c.created_at, c.key_secret_key IS NOT NULL`

func scanCert(row pgx.Row) (Certificate, error) {
	var c Certificate
	err := row.Scan(&c.ID, &c.DomainID, &c.Domain, &c.Status, &c.Provider, &c.Issuer, &c.Serial, &c.NotBefore, &c.NotAfter, &c.AutoRenew, &c.LastError, &c.CreatedAt, &c.HasPrivKey)
	if c.NotAfter != nil {
		days := int(time.Until(*c.NotAfter).Hours() / 24)
		c.DaysLeft = &days
	}
	return c, err
}

func (s *Service) ListCertificates(ctx context.Context, ac authctx.AuthContext, projectID *uuid.UUID, limit, offset int) ([]Certificate, error) {
	if err := rbac.Require(ac, "ssl.read"); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `SELECT `+certCols+` FROM certificates c JOIN domains d ON d.id=c.domain_id
		WHERE c.organization_id=$1 AND ($2::uuid IS NULL OR d.project_id=$2) ORDER BY d.name, c.created_at DESC LIMIT $3 OFFSET $4`,
		ac.OrganizationID, projectID, limit, offset)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeInternal, "failed to list certificates", err)
	}
	defer rows.Close()
	out := []Certificate{}
	for rows.Next() {
		c, err := scanCert(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Service) GetCertificate(ctx context.Context, ac authctx.AuthContext, id uuid.UUID) (Certificate, error) {
	if err := rbac.Require(ac, "ssl.read"); err != nil {
		return Certificate{}, err
	}
	c, err := scanCert(s.pool.QueryRow(ctx, `SELECT `+certCols+` FROM certificates c JOIN domains d ON d.id=c.domain_id WHERE c.id=$1 AND c.organization_id=$2`, id, ac.OrganizationID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Certificate{}, apierr.NotFound("certificate")
	}
	return c, err
}

func (s *Service) requireSSL() error {
	if s.providers.Certs == nil {
		return apierr.New(apierr.CodeValidation, "certificate provider is not configured on this deployment")
	}
	if s.secrets == nil {
		return apierr.New(apierr.CodeValidation, "secrets service is not configured; private keys cannot be stored safely")
	}
	return nil
}

// Register adds the SSL and domain-removal operations to the engine.
func (s *Service) Register(eng *ops.Engine) {
	s.engine = eng
	mk := func(name, perm, tool, notify string, kind string) {
		eng.Register(ops.Definition{
			Name: name, Permission: perm, ToolKey: tool, NotifyKind: notify, TimeoutSeconds: 300,
			Factory: func(_ *ops.Env, org, project uuid.UUID, payload json.RawMessage) (ops.Operation, error) {
				var p struct {
					DomainID uuid.UUID `json:"domain_id"`
				}
				_ = json.Unmarshal(payload, &p)
				if _, id := ops.PayloadResource(payload); id != uuid.Nil {
					p.DomainID = id
				}
				return &certOp{s: s, org: org, domain: p.DomainID, kind: kind, name: name}, nil
			},
		})
	}
	mk(OpIssue, "ssl.issue", "", "ssl.issued", "issue")
	mk(OpRenew, "ssl.renew", "", "ssl.renewed", "renew")
	mk(OpRevoke, "ssl.revoke", "", "ssl.revoked", "revoke")
	eng.Register(ops.Definition{
		Name: OpRemoveDomain, Permission: "domains.manage", ToolKey: "domain.remove", NotifyKind: "domain.removed",
		Factory: func(_ *ops.Env, org, project uuid.UUID, payload json.RawMessage) (ops.Operation, error) {
			_, id := ops.PayloadResource(payload)
			return &removeDomainOp{s: s, org: org, domain: id}, nil
		},
	})
}

type certOp struct {
	s      *Service
	org    uuid.UUID
	domain uuid.UUID
	kind   string
	name   string
	d      Domain
}

func (o *certOp) Name() string                             { return o.name }
func (o *certOp) Rollback(context.Context, *ops.Run) error { return nil }
func (o *certOp) Execute(context.Context, *ops.Run) error  { return nil }

func (o *certOp) Validate(ctx context.Context) error {
	if err := o.s.requireSSL(); err != nil {
		return err
	}
	d, err := o.s.getDomain(ctx, o.org, o.domain)
	if err != nil {
		return err
	}
	o.d = d
	var status string
	err = o.s.pool.QueryRow(ctx, `SELECT status FROM certificates WHERE domain_id=$1 AND status NOT IN ('revoked','error','expired')`, d.ID).Scan(&status)
	live := err == nil
	switch o.kind {
	case "issue":
		if live {
			return apierr.Conflict("this domain already has a live certificate; renew it instead")
		}
	case "renew", "revoke":
		if !live {
			return apierr.Conflict("this domain has no live certificate to " + o.kind)
		}
	}
	return nil
}

func (o *certOp) Steps() []ops.Step {
	var certID uuid.UUID
	var orderID uuid.UUID
	var bundle providers.CertBundle
	var keyName string
	return []ops.Step{
		{Name: "open-order", Run: func(ctx context.Context, r *ops.Run) error {
			if o.kind == "issue" {
				// DNS must be at least configured; we do not block on propagation
				// for the local CA, but we say so in the log.
				if o.d.DNSStatus != "ok" {
					r.Log("warn", "DNS for this domain is not verified yet; a public CA would require it", nil)
				}
				err := o.s.pool.QueryRow(ctx, `INSERT INTO certificates (organization_id, domain_id, status, provider, auto_renew)
					VALUES ($1,$2,'pending',$3,true) RETURNING id`, o.org, o.d.ID, o.s.providers.Certs.Name()).Scan(&certID)
				if err != nil {
					return err
				}
			} else {
				if err := o.s.pool.QueryRow(ctx, `SELECT id FROM certificates WHERE domain_id=$1 AND status NOT IN ('revoked','error','expired')`, o.d.ID).Scan(&certID); err != nil {
					return err
				}
			}
			return o.s.pool.QueryRow(ctx, `INSERT INTO certificate_orders (organization_id, certificate_id, kind, status, job_id) VALUES ($1,$2,$3,'running',$4) RETURNING id`,
				o.org, certID, o.kind, r.JobID).Scan(&orderID)
		}, Undo: func(ctx context.Context, r *ops.Run) error {
			if o.kind == "issue" && certID != uuid.Nil {
				// A certificate that never got issued must not block a retry.
				_, _ = o.s.pool.Exec(ctx, `UPDATE certificates SET status='error', last_error='issuance failed', updated_at=now() WHERE id=$1 AND status='pending'`, certID)
			}
			if orderID != uuid.Nil {
				_, _ = o.s.pool.Exec(ctx, `UPDATE certificate_orders SET status='failed', finished_at=now() WHERE id=$1 AND status='running'`, orderID)
			}
			return nil
		}},
		{Name: "provider-" + o.kind, Run: func(ctx context.Context, r *ops.Run) error {
			req := providers.IssueRequest{Domains: []string{o.d.Name}}
			var err error
			switch o.kind {
			case "issue":
				bundle, err = o.s.providers.Certs.Issue(ctx, req)
			case "renew":
				bundle, err = o.s.providers.Certs.Renew(ctx, req)
			case "revoke":
				var serial string
				if err = o.s.pool.QueryRow(ctx, `SELECT serial FROM certificates WHERE id=$1`, certID).Scan(&serial); err != nil {
					return err
				}
				return o.s.providers.Certs.Revoke(ctx, serial)
			}
			if err != nil {
				_, _ = o.s.pool.Exec(ctx, `UPDATE certificates SET last_error=$2 WHERE id=$1`, certID, truncate(err.Error(), 400))
			}
			return err
		}},
		{Name: "store-certificate", Run: func(ctx context.Context, r *ops.Run) error {
			if o.kind == "revoke" {
				if _, err := o.s.pool.Exec(ctx, `UPDATE certificates SET status='revoked', updated_at=now() WHERE id=$1`, certID); err != nil {
					return err
				}
				_, err := o.s.pool.Exec(ctx, `UPDATE domains SET ssl_status='none', updated_at=now() WHERE id=$1`, o.d.ID)
				return err
			}
			// Private key: encrypted in internal/secrets, never in this table.
			keyName = fmt.Sprintf("ssl/%s/%s", o.d.Name, bundle.Serial)
			if _, err := o.s.secrets.Set(ctx, authctx.System(o.org), keyName, string(bundle.KeyPEM), "TLS private key for "+o.d.Name); err != nil {
				return fmt.Errorf("could not store the private key securely: %w", err)
			}
			info, err := o.s.providers.Certs.Inspect(ctx, bundle.CertPEM)
			if err != nil {
				return err
			}
			_, err = o.s.pool.Exec(ctx, `UPDATE certificates SET status='valid', issuer=$2, serial=$3, not_before=$4, not_after=$5,
				key_secret_key=$6, cert_pem=$7, subject_names=$8, last_error=NULL, updated_at=now() WHERE id=$1`,
				certID, info.Issuer, info.Serial, info.NotBefore, info.NotAfter, keyName, string(bundle.CertPEM), info.DNSNames)
			if err != nil {
				return err
			}
			_, err = o.s.pool.Exec(ctx, `UPDATE domains SET ssl_status='valid', updated_at=now() WHERE id=$1`, o.d.ID)
			r.SetResult(map[string]any{"certificate_id": certID, "serial": info.Serial, "not_after": info.NotAfter})
			return err
		}, Undo: func(ctx context.Context, r *ops.Run) error {
			if keyName != "" {
				_ = o.s.secrets.Delete(ctx, authctx.System(o.org), keyName)
			}
			return nil
		}},
		{Name: "finish-order", Run: func(ctx context.Context, r *ops.Run) error {
			_, err := o.s.pool.Exec(ctx, `UPDATE certificate_orders SET status='completed', finished_at=now() WHERE id=$1`, orderID)
			return err
		}},
	}
}

type removeDomainOp struct {
	s      *Service
	org    uuid.UUID
	domain uuid.UUID
	d      Domain
}

func (o *removeDomainOp) Name() string                             { return OpRemoveDomain }
func (o *removeDomainOp) Rollback(context.Context, *ops.Run) error { return nil }
func (o *removeDomainOp) Execute(context.Context, *ops.Run) error  { return nil }
func (o *removeDomainOp) Validate(ctx context.Context) error {
	d, err := o.s.getDomain(ctx, o.org, o.domain)
	o.d = d
	return err
}
func (o *removeDomainOp) Steps() []ops.Step {
	return []ops.Step{
		{Name: "revoke-certificate", Run: func(ctx context.Context, r *ops.Run) error {
			var serial string
			err := o.s.pool.QueryRow(ctx, `SELECT serial FROM certificates WHERE domain_id=$1 AND status NOT IN ('revoked','error','expired')`, o.d.ID).Scan(&serial)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			if err != nil {
				return err
			}
			if o.s.providers.Certs != nil {
				if err := o.s.providers.Certs.Revoke(ctx, serial); err != nil {
					return err
				}
			}
			_, err = o.s.pool.Exec(ctx, `UPDATE certificates SET status='revoked', updated_at=now() WHERE domain_id=$1 AND status NOT IN ('revoked','error','expired')`, o.d.ID)
			return err
		}},
		{Name: "remove-dns-records", Run: func(ctx context.Context, r *ops.Run) error {
			if o.s.providers.DNS != nil {
				recs, _ := o.s.records(ctx, o.org, o.d.ID)
				for _, rec := range recs {
					var pid *string
					_ = o.s.pool.QueryRow(ctx, `SELECT provider_record_id FROM dns_records WHERE id=$1`, rec.ID).Scan(&pid)
					if pid != nil {
						if err := o.s.providers.DNS.DeleteRecord(ctx, o.d.Name, *pid); err != nil && !errors.Is(err, providers.ErrNotFound) {
							return err
						}
					}
				}
			}
			_, err := o.s.pool.Exec(ctx, `DELETE FROM dns_records WHERE domain_id=$1`, o.d.ID)
			return err
		}},
		{Name: "mark-removed", Run: func(ctx context.Context, r *ops.Run) error {
			_, err := o.s.pool.Exec(ctx, `UPDATE domains SET status='removed', ssl_status='none', deleted_at=now(), updated_at=now() WHERE id=$1`, o.d.ID)
			return err
		}},
	}
}

// Sweep keeps certificate status truthful and renews what is due. It returns
// how many certificates changed state or were queued for renewal.
func (s *Service) Sweep(ctx context.Context, now time.Time) (int, error) {
	n := 0
	tag, err := s.pool.Exec(ctx, `UPDATE certificates SET status='expired', updated_at=now() WHERE status IN ('valid','expiring') AND not_after < $1`, now)
	if err != nil {
		return 0, err
	}
	n += int(tag.RowsAffected())
	tag, err = s.pool.Exec(ctx, `UPDATE certificates SET status='expiring', updated_at=now() WHERE status='valid' AND not_after < $1`, now.Add(renewWindow))
	if err != nil {
		return n, err
	}
	n += int(tag.RowsAffected())
	_, _ = s.pool.Exec(ctx, `UPDATE domains d SET ssl_status = c.status FROM certificates c
		WHERE c.domain_id = d.id AND c.status IN ('expiring','expired') AND d.ssl_status <> c.status`)
	if s.engine == nil {
		return n, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT c.organization_id, c.domain_id, c.id FROM certificates c WHERE c.status='expiring' AND c.auto_renew`)
	if err != nil {
		return n, err
	}
	type due struct{ org, dom, cert uuid.UUID }
	var list []due
	for rows.Next() {
		var d due
		if err := rows.Scan(&d.org, &d.dom, &d.cert); err != nil {
			rows.Close()
			return n, err
		}
		list = append(list, d)
	}
	rows.Close()
	for _, d := range list {
		ref, err := s.engine.SubmitTrusted(ctx, authctx.System(d.org), ops.SubmitInput{
			Operation: OpRenew, Payload: map[string]any{"domain_id": d.dom},
			IdempotencyKey: fmt.Sprintf("ssl-renew:%s:%s", d.cert, now.Format("2006-01-02")),
		})
		if err != nil {
			logger.FromContext(ctx).Error("auto-renew submit failed", "certificate", d.cert, "error", err)
			continue
		}
		if ref.Created {
			n++
		}
	}
	return n, nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
