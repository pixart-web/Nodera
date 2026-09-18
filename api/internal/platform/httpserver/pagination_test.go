package httpserver_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nodera/nodera/internal/platform/httpserver"
)

func TestParsePagination_Defaults(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	p := httpserver.ParsePagination(r)
	if p.Limit != httpserver.DefaultPageLimit || p.Offset != 0 {
		t.Fatalf("unexpected defaults: %+v", p)
	}
}

func TestParsePagination_ValidValues(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/x?limit=10&offset=20", nil)
	p := httpserver.ParsePagination(r)
	if p.Limit != 10 || p.Offset != 20 {
		t.Fatalf("unexpected pagination: %+v", p)
	}
}

func TestParsePagination_LimitCappedAtMax(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/x?limit=99999", nil)
	p := httpserver.ParsePagination(r)
	if p.Limit != httpserver.MaxPageLimit {
		t.Fatalf("expected limit capped at %d, got %d", httpserver.MaxPageLimit, p.Limit)
	}
}

func TestParsePagination_InvalidValuesFallBackToDefaults(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/x?limit=not-a-number&offset=-5", nil)
	p := httpserver.ParsePagination(r)
	if p.Limit != httpserver.DefaultPageLimit {
		t.Errorf("expected default limit for garbage input, got %d", p.Limit)
	}
	if p.Offset != 0 {
		t.Errorf("expected offset 0 for a negative input, got %d", p.Offset)
	}
}

func TestNewPage_DetectsHasMoreAndTrims(t *testing.T) {
	p := httpserver.Pagination{Limit: 2, Offset: 0}
	rows := []int{1, 2, 3} // limit+1 rows fetched, as callers do
	page := httpserver.NewPage(rows, p)

	if !page.HasMore {
		t.Error("expected HasMore=true when more rows than limit were fetched")
	}
	if len(page.Items) != 2 {
		t.Fatalf("expected items trimmed to the limit, got %d", len(page.Items))
	}
}

func TestNewPage_NoMoreWhenExactlyLimitRows(t *testing.T) {
	p := httpserver.Pagination{Limit: 3, Offset: 0}
	rows := []int{1, 2, 3}
	page := httpserver.NewPage(rows, p)

	if page.HasMore {
		t.Error("expected HasMore=false when exactly limit rows were returned")
	}
	if len(page.Items) != 3 {
		t.Fatalf("expected all 3 items, got %d", len(page.Items))
	}
}

func TestNewPage_EmptyResultIsEmptySliceNotNil(t *testing.T) {
	p := httpserver.Pagination{Limit: 10, Offset: 0}
	page := httpserver.NewPage[int](nil, p)

	if page.Items == nil {
		t.Fatal("expected Items to be an empty slice, not nil (so it JSON-encodes as [] not null)")
	}
	if len(page.Items) != 0 {
		t.Fatalf("expected 0 items, got %d", len(page.Items))
	}
}
