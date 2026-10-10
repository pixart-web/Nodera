package integration_test

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"testing"
	"time"

	"github.com/nodera/nodera/internal/backups"
	"github.com/nodera/nodera/internal/devseed"
	"github.com/nodera/nodera/internal/jobs"
	"github.com/nodera/nodera/internal/monitoring"
	"github.com/nodera/nodera/internal/network"
	"github.com/nodera/nodera/internal/notifications"
	"github.com/nodera/nodera/internal/ops"
	"github.com/nodera/nodera/internal/platform/netpolicy"
	"github.com/nodera/nodera/internal/projects"
	"github.com/nodera/nodera/internal/providers/mock"
	"github.com/nodera/nodera/internal/provisioning"
	"github.com/nodera/nodera/internal/secrets"
	"github.com/nodera/nodera/internal/testhelpers"
)

func TestDevSeed_BuildsLabelledDemoOrganisationAndIsRepeatable(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	h := newHarness(pool)
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	sec, _ := secrets.New(pool, h.audit, h.platform, base64.StdEncoding.EncodeToString(key))
	set, _ := mock.NewSet()
	proj := projects.New(pool, h.audit)
	eng := ops.New(pool, h.audit)
	provisioning.Register(eng, provisioning.Deps{Pool: pool, Projects: proj, Secrets: sec, Providers: set})
	bk := backups.New(pool, h.audit, proj, set)
	bk.Register(eng)
	nw := network.New(pool, h.audit, sec, set)
	nw.Register(eng)
	notif := notifications.New(pool, h.audit, netpolicy.Policy{Level: netpolicy.PublicOnly})
	mon := monitoring.New(pool, h.audit, set, notif)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	w := jobs.NewWorker(pool)
	eng.RegisterWorker(w)
	go w.Run(ctx)

	deps := devseed.Deps{Pool: pool, Identity: h.identity, Tenancy: h.tenancy, Projects: proj, Ops: eng, Network: nw, Monitoring: mon, Notify: notif, Password: "Demo-Seed-Password-2026-x"}
	res, err := devseed.Run(ctx, deps)
	if err != nil {
		t.Fatal(err)
	}
	if res.Email != devseed.Email {
		t.Fatalf("unexpected result %+v", res)
	}
	var name string
	var projectsN, domainsN, certsN, backupsN, monitorsN int
	_ = pool.QueryRow(ctx, `SELECT name FROM organizations WHERE id=$1`, res.OrganizationID).Scan(&name)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM projects WHERE organization_id=$1 AND status='active' AND name LIKE '%(demo)' AND config->>'seed'='development'`, res.OrganizationID).Scan(&projectsN)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM domains WHERE organization_id=$1`, res.OrganizationID).Scan(&domainsN)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM certificates WHERE organization_id=$1 AND status='valid' AND provider='mock'`, res.OrganizationID).Scan(&certsN)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM backups WHERE organization_id=$1 AND status='completed'`, res.OrganizationID).Scan(&backupsN)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM monitors WHERE organization_id=$1`, res.OrganizationID).Scan(&monitorsN)
	if name != devseed.OrgName || projectsN != 2 || domainsN != 2 || certsN != 2 || backupsN != 2 || monitorsN != 2 {
		t.Fatalf("org=%q projects=%d domains=%d certs=%d backups=%d monitors=%d", name, projectsN, domainsN, certsN, backupsN, monitorsN)
	}
	// Mock certificates are labelled as such, never as a real CA.
	var issuer string
	_ = pool.QueryRow(ctx, `SELECT issuer FROM certificates WHERE organization_id=$1 LIMIT 1`, res.OrganizationID).Scan(&issuer)
	if issuer != "Nodera Mock CA" {
		t.Fatalf("issuer %q", issuer)
	}
	// Second run rebuilds rather than duplicating.
	res2, err := devseed.Run(ctx, deps)
	if err != nil {
		t.Fatal(err)
	}
	var orgs int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM organizations WHERE slug=$1`, devseed.OrgSlug).Scan(&orgs)
	if orgs != 1 || res2.OrganizationID == res.OrganizationID {
		t.Fatalf("expected one rebuilt org, got %d (same id=%v)", orgs, res2.OrganizationID == res.OrganizationID)
	}
	// The demo user can sign in with the printed password.
	if _, _, err := h.identity.Login(ctx, devseed.Email, "Demo-Seed-Password-2026-x", "127.0.0.1", "t"); err != nil {
		t.Fatalf("demo login: %v", err)
	}
}
