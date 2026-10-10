// Package devseed populates a DEVELOPMENT / DEMO organisation so the UI has
// something real-looking to show. It is only ever run in mock provider mode
// (NODERA_PROVIDER_MODE=mock, never in production), and everything it creates
// is clearly labelled: the organisation is named "DEVELOPMENT (demo data)",
// clients/projects carry "(demo)" in their names and config.seed="development".
//
// The seed drives the real services and operations (so history, steps and
// logs are genuine) against the in-memory mock providers that live in the same
// process. Because that state is in memory, the demo organisation is rebuilt
// on every start.
package devseed

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nodera/nodera/internal/backups"
	"github.com/nodera/nodera/internal/identity"
	"github.com/nodera/nodera/internal/monitoring"
	"github.com/nodera/nodera/internal/network"
	"github.com/nodera/nodera/internal/ops"
	"github.com/nodera/nodera/internal/platform/authctx"
	"github.com/nodera/nodera/internal/projects"
	"github.com/nodera/nodera/internal/provisioning"
	"github.com/nodera/nodera/internal/tenancy"
)

const (
	OrgSlug = "development-demo"
	OrgName = "DEVELOPMENT (demo data)"
	Email   = "demo@nodera.local"
)

type Deps struct {
	Pool       *pgxpool.Pool
	Identity   *identity.Service
	Tenancy    *tenancy.Service
	Projects   *projects.Service
	Ops        *ops.Engine
	Network    *network.Service
	Monitoring *monitoring.Service
	Notify     interface {
		Notify(ctx context.Context, orgID uuid.UUID, kind, title, body, resourceType, resourceID string)
	}
	// Password for the demo user. Empty generates a random one (returned).
	Password string
}

// Result tells the operator how to sign in.
type Result struct {
	Email, Password string
	OrganizationID  uuid.UUID
}

func (d Deps) wait(ctx context.Context, id uuid.UUID) error {
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		var st string
		if err := d.Pool.QueryRow(ctx, `SELECT status FROM jobs WHERE id=$1`, id).Scan(&st); err != nil {
			return err
		}
		switch st {
		case "succeeded":
			return nil
		case "failed", "cancelled":
			return fmt.Errorf("seed operation %s ended as %s", id, st)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
	}
	return errors.New("seed operation timed out")
}

func randomPassword() string {
	b := make([]byte, 18)
	_, _ = rand.Read(b)
	return "Demo-" + base64.RawURLEncoding.EncodeToString(b)
}

// Run (re)creates the demo organisation. Safe to call on every startup.
func Run(ctx context.Context, d Deps) (Result, error) {
	// Remove a previous demo org (cascade) but keep the demo user.
	if _, err := d.Pool.Exec(ctx, `DELETE FROM organizations WHERE slug=$1`, OrgSlug); err != nil {
		return Result{}, err
	}
	pw := d.Password
	if pw == "" {
		pw = randomPassword()
	}
	var userID uuid.UUID
	if err := d.Pool.QueryRow(ctx, `SELECT id FROM users WHERE lower(email)=lower($1)`, Email).Scan(&userID); err != nil {
		u, err := d.Identity.SignUp(ctx, Email, pw, "Demo User")
		if err != nil {
			return Result{}, fmt.Errorf("seed user: %w", err)
		}
		userID = u.ID
	} else {
		pw = "(existing demo user; password unchanged)"
	}
	org, err := d.Tenancy.CreateOrganization(ctx, userID, OrgName, OrgSlug)
	if err != nil {
		return Result{}, fmt.Errorf("seed org: %w", err)
	}
	// A system context with full rights, labelled so audit shows who did this.
	ac := authctx.System(org.ID)
	ac.ActorLabel = "devseed"

	acme, err := d.Projects.CreateClient(ctx, ac, projects.ClientInput{Name: "Acme Studio (demo)", ContactEmail: "ops@acme.example", Notes: "DEVELOPMENT seed data"})
	if err != nil {
		return Result{}, err
	}
	globex, err := d.Projects.CreateClient(ctx, ac, projects.ClientInput{Name: "Globex Retail (demo)", ContactEmail: "it@globex.example", Notes: "DEVELOPMENT seed data"})
	if err != nil {
		return Result{}, err
	}

	var nodeID uuid.UUID
	if err := d.Pool.QueryRow(ctx, `INSERT INTO nodes (organization_id, hostname, provider, role, environment, status, labels)
		VALUES ($1,'dev-node-01 (demo)','local','application','development','online','{"seed":"development"}') RETURNING id`, org.ID).Scan(&nodeID); err != nil {
		return Result{}, err
	}

	type site struct {
		name   string
		client uuid.UUID
		domain string
	}
	var made []projects.Project
	for _, s := range []site{{"Acme Blog (demo)", acme.ID, "blog.acme.example"}, {"Globex Shop (demo)", globex.ID, "shop.globex.example"}} {
		cid := s.client
		p, err := d.Projects.Create(ctx, ac, projects.ProjectInput{Name: s.name, Kind: "wordpress", ClientID: &cid, NodeID: &nodeID,
			Description: "DEVELOPMENT seed data: runs on mock providers", Config: map[string]any{"seed": "development", "primary_domain": s.domain}})
		if err != nil {
			return Result{}, err
		}
		ref, err := d.Ops.SubmitTrusted(ctx, ac, ops.SubmitInput{Operation: provisioning.OpProvision, ProjectID: p.ID})
		if err != nil {
			return Result{}, err
		}
		if err := d.wait(ctx, ref.JobID); err != nil {
			return Result{}, err
		}
		made = append(made, p)

		dom, err := d.Network.AddDomain(ctx, ac, s.domain, &p.ID)
		if err != nil {
			return Result{}, err
		}
		if _, err := d.Network.UpsertRecord(ctx, ac, dom.ID, network.RecordInput{Type: "A", Name: "@", Value: "203.0.113.10"}); err != nil {
			return Result{}, err
		}
		if _, err := d.Network.CheckPropagation(ctx, ac, dom.ID); err != nil {
			return Result{}, err
		}
		if ref, err := d.Ops.SubmitTrusted(ctx, ac, ops.SubmitInput{Operation: network.OpIssue, Payload: map[string]any{"domain_id": dom.ID}}); err == nil {
			_ = d.wait(ctx, ref.JobID)
		}
		if ref, err := d.Ops.SubmitTrusted(ctx, ac, ops.SubmitInput{Operation: backups.OpCreate, ProjectID: p.ID, Payload: backups.CreatePayload{Type: "configuration"}}); err == nil {
			_ = d.wait(ctx, ref.JobID)
		}
		pid := p.ID
		if _, err := d.Monitoring.CreateMonitor(ctx, ac, monitoring.MonitorInput{ProjectID: &pid, Kind: "http", Name: s.name + " homepage", Target: "https://" + s.domain, IntervalSeconds: 60}); err != nil {
			return Result{}, err
		}
	}
	if _, err := d.Monitoring.CreateRule(ctx, ac, monitoring.RuleInput{Name: "Site down (demo)", Condition: "http_failure", Severity: "critical"}); err != nil {
		return Result{}, err
	}
	d.Notify.Notify(ctx, org.ID, "system.demo", "Demo data loaded", "This organisation contains DEVELOPMENT seed data running on mock providers. Nothing here touches real infrastructure.", "", "")
	return Result{Email: Email, Password: pw, OrganizationID: org.ID}, nil
}
