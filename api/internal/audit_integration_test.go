package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/nodera/nodera/internal/audit"
	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/testhelpers"
)

// Query's From/To narrow by created_at — this backdates rows directly
// (a real Record call always uses now(), so this is the only way to get
// deterministic, well-separated timestamps to filter against) and
// confirms the filter genuinely excludes rows outside the window, not
// just that the query runs without error.
func TestAudit_QueryFiltersByTimeRange(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "audit-timerange-owner@nodera.dev")

	insert := func(action string, createdAt time.Time) {
		if _, err := pool.Exec(ctx, `
			INSERT INTO audit_log (organization_id, actor_label, action, resource_type, success, created_at)
			VALUES ($1, 'test-actor', $2, 'test-resource', true, $3)
		`, ac.OrganizationID, action, createdAt); err != nil {
			t.Fatalf("failed to insert backdated audit row: %v", err)
		}
	}

	base := time.Now().UTC().Truncate(time.Second)
	insert("test.timerange.old", base.Add(-48*time.Hour))
	insert("test.timerange.middle", base.Add(-24*time.Hour))
	insert("test.timerange.recent", base.Add(-1*time.Hour))

	// A window covering only the middle row.
	records, err := h.audit.Query(ctx, ac, audit.QueryFilter{
		OrganizationID: ac.OrganizationID,
		Action:         "", // no action filter — isolate the time-range behavior
		ResourceType:   "test-resource",
		From:           base.Add(-30 * time.Hour),
		To:             base.Add(-12 * time.Hour),
	})
	if err != nil {
		t.Fatalf("Query (windowed): %v", err)
	}
	if len(records) != 1 || records[0].Action != "test.timerange.middle" {
		t.Fatalf("expected exactly the middle row, got %+v", records)
	}

	// From alone (open-ended upper bound) includes middle and recent, not old.
	records, err = h.audit.Query(ctx, ac, audit.QueryFilter{
		OrganizationID: ac.OrganizationID,
		ResourceType:   "test-resource",
		From:           base.Add(-30 * time.Hour),
	})
	if err != nil {
		t.Fatalf("Query (from only): %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 rows (middle, recent), got %d: %+v", len(records), records)
	}

	// No time filter at all returns everything, same as before this phase.
	records, err = h.audit.Query(ctx, ac, audit.QueryFilter{
		OrganizationID: ac.OrganizationID,
		ResourceType:   "test-resource",
	})
	if err != nil {
		t.Fatalf("Query (no time filter): %v", err)
	}
	if len(records) != 3 {
		t.Fatalf("expected all 3 rows with no time filter, got %d", len(records))
	}
}

func TestAudit_QueryRejectsFromAfterTo(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "audit-timerange-invalid-owner@nodera.dev")

	now := time.Now()
	if _, err := h.audit.Query(ctx, ac, audit.QueryFilter{
		OrganizationID: ac.OrganizationID,
		From:           now,
		To:             now.Add(-time.Hour),
	}); err == nil {
		t.Fatal("expected from-after-to to be rejected")
	} else if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeValidation {
		t.Fatalf("expected VALIDATION_ERROR, got %v", err)
	}
}
