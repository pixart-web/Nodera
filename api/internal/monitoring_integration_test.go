package integration_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nodera/nodera/internal/audit"
	"github.com/nodera/nodera/internal/logs"
	"github.com/nodera/nodera/internal/monitoring"
	"github.com/nodera/nodera/internal/notifications"
	"github.com/nodera/nodera/internal/platform/netpolicy"
	"github.com/nodera/nodera/internal/providers/mock"
	"github.com/nodera/nodera/internal/testhelpers"
)

func TestMonitoring_ChecksIncidentsDedupeAutoResolveAndNotifications(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	h := newHarness(pool)
	ctx := context.Background()
	set, mk := mock.NewSet()
	notif := notifications.New(pool, h.audit, netpolicy.Policy{Level: netpolicy.PublicOnly})
	mon := monitoring.New(pool, h.audit, set, notif)

	ac, _ := h.newOwnerContext(t, ctx, "mon-ok@nodera.dev")
	other, _ := h.newOwnerContext(t, ctx, "mon-other@nodera.dev")
	member := h.newMemberContext(t, ctx, ac.OrganizationID, "mon-member@nodera.dev")

	for _, bad := range []monitoring.MonitorInput{
		{Kind: "http", Name: "x", Target: "ftp://example.com"},
		{Kind: "http", Name: "x", Target: "http://user:pw@example.com"},
		{Kind: "tcp", Name: "x", Target: "no-port"},
		{Kind: "container", Name: "x", Target: "a b; rm"},
		{Kind: "bogus", Name: "x", Target: "a"},
		{Kind: "http", Name: "x", Target: "https://example.com", IntervalSeconds: 1},
	} {
		if _, err := mon.CreateMonitor(ctx, ac, bad); err == nil {
			t.Errorf("monitor %+v should be rejected", bad)
		}
	}
	m, err := mon.CreateMonitor(ctx, ac, monitoring.MonitorInput{Kind: "http", Name: "Homepage", Target: "https://site.example.com", IntervalSeconds: 10})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mon.CreateMonitor(ctx, member, monitoring.MonitorInput{Kind: "http", Name: "nope", Target: "https://a.example.com"}); err == nil {
		t.Fatal("member must not create monitors")
	}
	if _, err := mon.RunNow(ctx, other, m.ID); err == nil {
		t.Fatal("cross-tenant run must fail")
	}

	rule, err := mon.CreateRule(ctx, ac, monitoring.RuleInput{Name: "Site down", Condition: "http_failure", Severity: "critical"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mon.CreateRule(ctx, ac, monitoring.RuleInput{Name: "bad", Condition: "cpu_above", Threshold: 500}); err == nil {
		t.Fatal("cpu threshold > 100 must be rejected")
	}

	// healthy -> no incident
	got, _ := mon.RunNow(ctx, ac, m.ID)
	if got.LastStatus != "ok" || got.LastLatencyMs == nil {
		t.Fatalf("healthy check: %+v", got)
	}
	if o, r, _ := mon.Evaluate(ctx); o != 0 || r != 0 {
		t.Fatalf("no incident expected, opened=%d resolved=%d", o, r)
	}

	// failing -> exactly one incident + one notification, even if evaluated repeatedly
	mk.Monitoring.SetDown("https://site.example.com", true)
	got, _ = mon.RunNow(ctx, ac, m.ID)
	if got.LastStatus != "failing" {
		t.Fatalf("failing check: %+v", got)
	}
	for i := 0; i < 3; i++ {
		_, _, _ = mon.Evaluate(ctx)
	}
	incs, _ := mon.ListIncidents(ctx, ac, "open", nil, 50, 0)
	if len(incs) != 1 || incs[0].Severity != "critical" {
		t.Fatalf("expected exactly one open incident, got %+v", incs)
	}
	ns, _ := notif.List(ctx, ac, true, 50, 0)
	if len(ns) != 1 || ns[0].Kind != "incident.opened" {
		t.Fatalf("expected exactly one notification, got %+v", ns)
	}
	if n, _ := notif.UnreadCount(ctx, ac); n != 1 {
		t.Fatalf("unread = %d", n)
	}
	if err := notif.MarkRead(ctx, ac, []uuid.UUID{ns[0].ID}); err != nil {
		t.Fatal(err)
	}
	if n, _ := notif.UnreadCount(ctx, ac); n != 0 {
		t.Fatalf("unread after read = %d", n)
	}
	// read state is per user; other tenant sees nothing.
	if list, _ := notif.List(ctx, other, false, 50, 0); len(list) != 0 {
		t.Fatal("cross-tenant notification leak")
	}

	// lifecycle
	inc := incs[0]
	if _, err := mon.Transition(ctx, member, inc.ID, "acknowledge", ""); err == nil {
		t.Fatal("member must not manage incidents")
	}
	if _, err := mon.Transition(ctx, other, inc.ID, "acknowledge", ""); err == nil {
		t.Fatal("cross-tenant incident transition must fail")
	}
	if _, err := mon.Transition(ctx, ac, inc.ID, "close", ""); err == nil {
		t.Fatal("cannot close an open incident")
	}
	if i, err := mon.Transition(ctx, ac, inc.ID, "acknowledge", "looking"); err != nil || i.Status != "acknowledged" || i.AcknowledgedAt == nil {
		t.Fatalf("acknowledge: %+v %v", i, err)
	}
	if _, err := mon.Transition(ctx, ac, inc.ID, "acknowledge", ""); err == nil {
		t.Fatal("double acknowledge must be a conflict")
	}
	if _, err := mon.Transition(ctx, ac, inc.ID, "investigate", ""); err != nil {
		t.Fatal(err)
	}

	// recovery -> auto resolve
	mk.Monitoring.SetDown("https://site.example.com", false)
	_, _ = mon.RunNow(ctx, ac, m.ID)
	if _, r, _ := mon.Evaluate(ctx); r != 1 {
		t.Fatalf("expected 1 auto-resolved incident, got %d", r)
	}
	full, _ := mon.GetIncident(ctx, ac, inc.ID)
	if full.Status != "resolved" || len(full.Events) < 4 {
		t.Fatalf("incident timeline: %+v", full)
	}
	if _, err := mon.Transition(ctx, ac, inc.ID, "close", "done"); err != nil {
		t.Fatal(err)
	}
	_ = rule

	// A new failure after resolution opens a NEW incident.
	mk.Monitoring.SetDown("https://site.example.com", true)
	_, _ = mon.RunNow(ctx, ac, m.ID)
	if o, _, _ := mon.Evaluate(ctx); o != 1 {
		t.Fatalf("recurrence should open a new incident, opened=%d", o)
	}

	// RunDue claims each monitor once per interval.
	n, err := mon.RunDue(ctx, time.Now().Add(time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("RunDue n=%d err=%v", n, err)
	}
	if n, _ := mon.RunDue(ctx, time.Now().Add(time.Hour)); n != 0 {
		t.Fatalf("monitor must not be re-run within its interval, n=%d", n)
	}
}

func TestNotifications_WebhookSSRFProtection(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	h := newHarness(pool)
	ctx := context.Background()
	notif := notifications.New(pool, h.audit, netpolicy.Policy{Level: netpolicy.PublicOnly})
	ac, _ := h.newOwnerContext(t, ctx, "notif-ssrf@nodera.dev")
	for _, target := range []string{"http://127.0.0.1:8080/hook", "http://169.254.169.254/latest/meta-data", "http://localhost/x", "http://10.0.0.5/x", "file:///etc/passwd", "http://user:pw@example.com/x", "gopher://x"} {
		if _, err := notif.CreateChannel(ctx, ac, notifications.ChannelInput{Kind: "webhook", Name: "w-" + target, Target: target}); err == nil {
			t.Errorf("webhook target %q must be rejected", target)
		}
	}
	if _, err := notif.CreateChannel(ctx, ac, notifications.ChannelInput{Kind: "email", Name: "bad", Target: "not-an-email"}); err == nil {
		t.Error("invalid email must be rejected")
	}
	// An email channel can be created, but delivery honestly reports that SMTP is missing.
	if _, err := notif.CreateChannel(ctx, ac, notifications.ChannelInput{Kind: "email", Name: "ops", Target: "ops@example.com"}); err != nil {
		t.Fatal(err)
	}
	fails := notif.Dispatch(ctx, ac.OrganizationID, []string{"email"}, "test", "t", "b", "", "")
	if len(fails) != 1 || !strings.Contains(fails[0], "SMTP is not configured") {
		t.Fatalf("email delivery must report missing SMTP, got %v", fails)
	}
}

func TestLogs_RedactionSearchAndRetention(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	h := newHarness(pool)
	ctx := context.Background()
	set, _ := mock.NewSet()
	lg := logs.New(pool, set)
	ac, _ := h.newOwnerContext(t, ctx, "logs-ok@nodera.dev")
	other, _ := h.newOwnerContext(t, ctx, "logs-other@nodera.dev")
	member := h.newMemberContext(t, ctx, ac.OrganizationID, "logs-member@nodera.dev")

	_ = lg.Ingest(ctx, ac.OrganizationID, nil, "application", "web", "error", "login failed for user, password=SuperSecret123 token=abc")
	_ = lg.Ingest(ctx, ac.OrganizationID, nil, "bogus", "web", "bogus", "plain line about 100% done_ok")
	res, _ := lg.Query(ctx, ac, logs.Filter{})
	if len(res) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(res))
	}
	for _, e := range res {
		if strings.Contains(e.Message, "SuperSecret123") {
			t.Fatal("password stored in a log line")
		}
	}
	if r, _ := lg.Query(ctx, ac, logs.Filter{Level: "error"}); len(r) != 1 {
		t.Fatalf("level filter: %d", len(r))
	}
	// LIKE wildcards in the query are literal, not patterns.
	if r, _ := lg.Query(ctx, ac, logs.Filter{Query: "100%"}); len(r) != 1 {
		t.Fatalf("literal %% search: %d", len(r))
	}
	if r, _ := lg.Query(ctx, ac, logs.Filter{Query: "%"}); len(r) != 1 {
		t.Fatalf("a lone %% must only match literal percent signs, got %d", len(r))
	}
	if r, _ := lg.Query(ctx, other, logs.Filter{}); len(r) != 0 {
		t.Fatal("cross-tenant log leak")
	}
	if _, err := lg.Query(ctx, member, logs.Filter{}); err != nil {
		t.Fatalf("member can read logs: %v", err)
	}

	// Retention: entries older than the window are removed, audit is protected by default.
	_, _ = pool.Exec(ctx, `UPDATE log_entries SET created_at = now() - interval '40 days' WHERE message LIKE 'login failed%'`)
	removed, err := lg.Sweep(ctx, time.Now())
	if err != nil || removed["log_entries"] != 1 {
		t.Fatalf("sweep: %v %v", removed, err)
	}
	if err := lg.SetRetention(ctx, ac, "audit_log", 7); err == nil {
		t.Fatal("audit retention below 90 days must be refused")
	}
	_ = h.audit.Record(ctx, ac, audit.Entry{Action: "test.event", ResourceType: "test", ResourceID: "1", Success: true})
	var auditBefore, auditAfter int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE organization_id=$1`, ac.OrganizationID).Scan(&auditBefore)
	_, _ = lg.Sweep(ctx, time.Now().AddDate(5, 0, 0))
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE organization_id=$1`, ac.OrganizationID).Scan(&auditAfter)
	if auditBefore == 0 || auditAfter != auditBefore {
		t.Fatalf("audit log must not be pruned without an explicit policy (%d -> %d)", auditBefore, auditAfter)
	}
	if err := lg.SetRetention(ctx, member, "log_entries", 10); err == nil {
		t.Fatal("member must not change retention")
	}
}
