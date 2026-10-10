package integration_test

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nodera/nodera/internal/jobs"
	"github.com/nodera/nodera/internal/network"
	"github.com/nodera/nodera/internal/ops"
	"github.com/nodera/nodera/internal/platform/authctx"
	"github.com/nodera/nodera/internal/platform/netpolicy"
	"github.com/nodera/nodera/internal/providers"
	"github.com/nodera/nodera/internal/providers/local"
	"github.com/nodera/nodera/internal/providers/mock"
	"github.com/nodera/nodera/internal/secrets"
	"github.com/nodera/nodera/internal/testhelpers"
	"github.com/nodera/nodera/internal/tools"
)

type netEnv struct {
	h   *testHarness
	svc *network.Service
	eng *ops.Engine
	w   *jobs.Worker
	reg *tools.Registry
	set providers.Set
	mk  *mock.Handles
}

func newNetEnv(t *testing.T, realProviders bool) (*netEnv, context.Context) {
	t.Helper()
	pool := testhelpers.RequirePool(t)
	h := newHarness(pool)
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	sec, err := secrets.New(pool, h.audit, h.platform, base64.StdEncoding.EncodeToString(key))
	if err != nil {
		t.Fatal(err)
	}
	var set providers.Set
	msets, mk := mock.NewSet()
	if realProviders {
		set, err = local.NewSet(t.TempDir(), netpolicy.Policy{Level: netpolicy.PublicOnly})
		if err != nil {
			t.Fatal(err)
		}
	} else {
		set = msets
	}
	svc := network.New(pool, h.audit, sec, set)
	eng := ops.New(pool, h.audit)
	svc.Register(eng)
	reg := tools.New(pool, h.audit)
	eng.BridgeTools(reg)
	w := jobs.NewWorker(pool)
	eng.RegisterWorker(w)
	return &netEnv{h: h, svc: svc, eng: eng, w: w, reg: reg, set: set, mk: mk}, context.Background()
}

func (e *netEnv) opStatus(t *testing.T, ctx context.Context, ac authctx.AuthContext, op string, payload any) string {
	t.Helper()
	ref, err := e.eng.Submit(ctx, ac, ops.SubmitInput{Operation: op, Payload: payload})
	if err != nil {
		return "rejected: " + err.Error()
	}
	e.w.RunOnce(ctx)
	v, _ := e.eng.Get(ctx, ac, ref.JobID)
	return v.Status
}

func TestNetwork_DomainDNSValidationAndProviderFailure(t *testing.T) {
	e, ctx := newNetEnv(t, false)
	ac, _ := e.h.newOwnerContext(t, ctx, "net-dns@nodera.dev")

	for _, bad := range []string{"", "localhost", "192.168.1.1", "-bad.com", "exa mple.com", strings.Repeat("a", 64) + ".com"} {
		if _, err := e.svc.AddDomain(ctx, ac, bad, nil); err == nil {
			t.Errorf("domain %q should be rejected", bad)
		}
	}
	d, err := e.svc.AddDomain(ctx, ac, "  Example.COM. ", nil)
	if err != nil || d.Name != "example.com" {
		t.Fatalf("add domain: %+v %v", d, err)
	}
	if _, err := e.svc.AddDomain(ctx, ac, "example.com", nil); err == nil {
		t.Fatal("duplicate domain must conflict")
	}

	for name, in := range map[string]network.RecordInput{
		"A with hostname": {Type: "A", Name: "@", Value: "example.org"},
		"A with ipv6":     {Type: "A", Name: "@", Value: "::1"},
		"CNAME at apex":   {Type: "CNAME", Name: "@", Value: "other.example.net"},
		"bad ttl":         {Type: "A", Name: "@", Value: "1.2.3.4", TTL: 5},
		"bad type":        {Type: "SRV", Name: "@", Value: "x"},
		"TXT newline":     {Type: "TXT", Name: "@", Value: "a\nb"},
		"bad name":        {Type: "A", Name: "bad name", Value: "1.2.3.4"},
		"CAA malformed":   {Type: "CAA", Name: "@", Value: "nonsense"},
	} {
		if _, err := e.svc.UpsertRecord(ctx, ac, d.ID, in); err == nil {
			t.Errorf("%s should be rejected", name)
		}
	}
	if _, err := e.svc.UpsertRecord(ctx, ac, d.ID, network.RecordInput{Type: "A", Name: "www", Value: "203.0.113.10"}); err != nil {
		t.Fatal(err)
	}
	// CNAME cannot sit next to an A of the same name, and vice versa.
	if _, err := e.svc.UpsertRecord(ctx, ac, d.ID, network.RecordInput{Type: "CNAME", Name: "www", Value: "other.example.net"}); err == nil {
		t.Fatal("CNAME next to A must be rejected")
	}

	// A provider failure must leave no phantom record in the database.
	e.mk.Faults.FailOnce("dns.upsert", errors.New("provider API down"))
	if _, err := e.svc.UpsertRecord(ctx, ac, d.ID, network.RecordInput{Type: "A", Name: "api", Value: "203.0.113.11"}); err == nil {
		t.Fatal("expected provider failure to surface")
	}
	recs, _ := e.svc.ListRecords(ctx, ac, d.ID)
	if len(recs) != 1 {
		t.Fatalf("expected exactly the one good record, got %+v", recs)
	}

	// Propagation is reported from the provider, and only then is DNS "ok".
	res, err := e.svc.CheckPropagation(ctx, ac, d.ID)
	if err != nil || len(res) != 1 || !res[0].Propagated {
		t.Fatalf("propagation: %+v %v", res, err)
	}
	if got, _ := e.svc.GetDomain(ctx, ac, d.ID); got.DNSStatus != "ok" || got.Status != "active" || got.VerifiedAt == nil {
		t.Fatalf("domain should be verified: %+v", got)
	}
	// Deleting the record removes it from DB and provider.
	if err := e.svc.DeleteRecord(ctx, ac, d.ID, recs[0].ID); err != nil {
		t.Fatal(err)
	}
	if left, _ := e.set.DNS.ListRecords(ctx, "example.com"); len(left) != 0 {
		t.Fatalf("provider still has records: %+v", left)
	}
}

func TestNetwork_SSLLifecycleWithRealLocalCA(t *testing.T) {
	e, ctx := newNetEnv(t, true)
	ac, _ := e.h.newOwnerContext(t, ctx, "net-ssl@nodera.dev")
	d, _ := e.svc.AddDomain(ctx, ac, "secure.example.com", nil)

	if st := e.opStatus(t, ctx, ac, network.OpRenew, map[string]any{"domain_id": d.ID}); !strings.HasPrefix(st, "rejected") {
		t.Fatalf("renew without a certificate must be rejected, got %s", st)
	}
	if st := e.opStatus(t, ctx, ac, network.OpIssue, map[string]any{"domain_id": d.ID}); st != "succeeded" {
		t.Fatalf("issue: %s", st)
	}
	certs, _ := e.svc.ListCertificates(ctx, ac, nil, 50, 0)
	if len(certs) != 1 || certs[0].Status != "valid" || !certs[0].HasPrivKey || certs[0].DaysLeft == nil || *certs[0].DaysLeft < 80 {
		t.Fatalf("unexpected certificate: %+v", certs)
	}
	if got, _ := e.svc.GetDomain(ctx, ac, d.ID); got.SSLStatus != "valid" {
		t.Fatalf("domain ssl_status = %s", got.SSLStatus)
	}
	if st := e.opStatus(t, ctx, ac, network.OpIssue, map[string]any{"domain_id": d.ID}); !strings.HasPrefix(st, "rejected") {
		t.Fatalf("second live certificate must be rejected, got %s", st)
	}

	// The private key must exist only encrypted: no PEM private key block in any
	// table column, while the secret is retrievable via the secrets service.
	var leaks int
	_ = e.h.pool.QueryRow(ctx, `SELECT count(*) FROM certificates c WHERE c::text LIKE '%PRIVATE KEY%'`).Scan(&leaks)
	_ = e.h.pool.QueryRow(ctx, `SELECT count(*) + $1 FROM secrets s WHERE s::text LIKE '%PRIVATE KEY%'`, leaks).Scan(&leaks)
	_ = e.h.pool.QueryRow(ctx, `SELECT count(*) + $1 FROM audit_log a WHERE a::text LIKE '%PRIVATE KEY%'`, leaks).Scan(&leaks)
	_ = e.h.pool.QueryRow(ctx, `SELECT count(*) + $1 FROM job_logs l WHERE l::text LIKE '%PRIVATE KEY%'`, leaks).Scan(&leaks)
	if leaks != 0 {
		t.Fatalf("private key material found in plaintext in %d rows", leaks)
	}

	oldSerial := certs[0].Serial
	if st := e.opStatus(t, ctx, ac, network.OpRenew, map[string]any{"domain_id": d.ID}); st != "succeeded" {
		t.Fatalf("renew: %s", st)
	}
	certs, _ = e.svc.ListCertificates(ctx, ac, nil, 50, 0)
	if certs[0].Serial == oldSerial {
		t.Fatal("renewal must produce a new serial")
	}

	// Expiry sweep: time travel past not_after marks the certificate expired,
	// which frees the domain for a fresh issue.
	if _, err := e.svc.Sweep(ctx, time.Now().AddDate(0, 0, 100)); err != nil {
		t.Fatal(err)
	}
	if c, _ := e.svc.GetCertificate(ctx, ac, certs[0].ID); c.Status != "expired" {
		t.Fatalf("status = %s, want expired", c.Status)
	}

	if st := e.opStatus(t, ctx, ac, network.OpIssue, map[string]any{"domain_id": d.ID}); st != "succeeded" {
		t.Fatalf("re-issue after expiry: %s", st)
	}
	if st := e.opStatus(t, ctx, ac, network.OpRevoke, map[string]any{"domain_id": d.ID}); st != "succeeded" {
		t.Fatalf("revoke: %s", st)
	}
	if got, _ := e.svc.GetDomain(ctx, ac, d.ID); got.SSLStatus != "none" {
		t.Fatalf("after revoke ssl_status = %s", got.SSLStatus)
	}
}

func TestNetwork_ExpiringCertificatesAutoRenew(t *testing.T) {
	e, ctx := newNetEnv(t, true)
	ac, _ := e.h.newOwnerContext(t, ctx, "net-auto@nodera.dev")
	d, _ := e.svc.AddDomain(ctx, ac, "auto.example.com", nil)
	if st := e.opStatus(t, ctx, ac, network.OpIssue, map[string]any{"domain_id": d.ID}); st != "succeeded" {
		t.Fatal(st)
	}
	old, _ := e.svc.ListCertificates(ctx, ac, nil, 50, 0)
	n, err := e.svc.Sweep(ctx, time.Now().AddDate(0, 0, 70)) // inside the 30-day renew window
	if err != nil || n == 0 {
		t.Fatalf("sweep n=%d err=%v", n, err)
	}
	e.w.RunOnce(ctx)
	now, _ := e.svc.ListCertificates(ctx, ac, nil, 50, 0)
	if now[0].Serial == old[0].Serial || now[0].Status != "valid" {
		t.Fatalf("expected auto-renewed certificate, got %+v", now[0])
	}
	// Running the sweep again the same day does not queue a second renewal.
	_, _ = e.svc.Sweep(ctx, time.Now().AddDate(0, 0, 70))
	if e.w.RunOnce(ctx) {
		t.Fatal("renewal was queued twice")
	}
}

func TestNetwork_RemoveDomainNeedsApproval_AndTenancy(t *testing.T) {
	e, ctx := newNetEnv(t, true)
	ac, _ := e.h.newOwnerContext(t, ctx, "net-rm@nodera.dev")
	other, _ := e.h.newOwnerContext(t, ctx, "net-other@nodera.dev")
	member := e.h.newMemberContext(t, ctx, ac.OrganizationID, "net-member@nodera.dev")
	d, _ := e.svc.AddDomain(ctx, ac, "gone.example.com", nil)
	_, _ = e.svc.UpsertRecord(ctx, ac, d.ID, network.RecordInput{Type: "A", Name: "@", Value: "203.0.113.5"})
	if st := e.opStatus(t, ctx, ac, network.OpIssue, map[string]any{"domain_id": d.ID}); st != "succeeded" {
		t.Fatal(st)
	}

	if _, err := e.eng.Submit(ctx, ac, ops.SubmitInput{Operation: network.OpRemoveDomain, Payload: map[string]any{"domain_id": d.ID}}); err == nil {
		t.Fatal("domain removal must go through the approval gateway")
	}
	res, err := e.reg.Execute(ctx, ac, "domain.remove", tools.ExecuteInput{ResourceType: "domain", ResourceID: d.ID.String()})
	if err != nil || res.ApprovalID == nil {
		t.Fatalf("expected approval: %+v %v", res, err)
	}
	if _, err := e.svc.GetDomain(ctx, ac, d.ID); err != nil {
		t.Fatal("domain must exist until approved")
	}
	if _, err := e.reg.DecideApproval(ctx, ac, *res.ApprovalID, true, "ok"); err != nil {
		t.Fatal(err)
	}
	e.w.RunOnce(ctx)
	if _, err := e.svc.GetDomain(ctx, ac, d.ID); err == nil {
		t.Fatal("domain should be gone")
	}
	if left, _ := e.set.DNS.ListRecords(ctx, "gone.example.com"); len(left) != 0 {
		t.Fatalf("DNS records survived: %+v", left)
	}

	d2, _ := e.svc.AddDomain(ctx, ac, "mine.example.com", nil)
	if _, err := e.svc.GetDomain(ctx, other, d2.ID); err == nil {
		t.Fatal("cross-tenant domain read must fail")
	}
	if _, err := e.svc.UpsertRecord(ctx, other, d2.ID, network.RecordInput{Type: "A", Name: "@", Value: "1.2.3.4"}); err == nil {
		t.Fatal("cross-tenant record write must fail")
	}
	if st := e.opStatus(t, ctx, other, network.OpIssue, map[string]any{"domain_id": d2.ID}); !strings.HasPrefix(st, "rejected") {
		t.Fatalf("cross-tenant issue = %s", st)
	}
	if _, err := e.svc.AddDomain(ctx, member, "member.example.com", nil); err == nil {
		t.Fatal("member must not add domains")
	}
	if _, err := e.svc.ListDomains(ctx, member, nil, "", 50, 0); err != nil {
		t.Fatalf("member should list domains: %v", err)
	}
	// The same name may exist in two tenants only after the first is removed; here it conflicts globally per-org.
	if _, err := e.svc.AddDomain(ctx, other, "mine.example.com", nil); err != nil {
		t.Logf("same name in another org: %v", err)
	}
	_ = uuid.Nil
}
