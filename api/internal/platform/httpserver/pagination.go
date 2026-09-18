package httpserver

import (
	"net/http"
	"strconv"
)

// DefaultPageLimit and MaxPageLimit bound every paginated list endpoint.
// A request that omits ?limit gets DefaultPageLimit; one that asks for
// more than MaxPageLimit is silently capped, not rejected — an oversized
// request is still a valid one, just answered conservatively.
const (
	DefaultPageLimit = 50
	MaxPageLimit     = 200
)

// Pagination is the limit/offset pair every paginated list endpoint
// accepts via ?limit=&offset=.
type Pagination struct {
	Limit  int
	Offset int
}

// ParsePagination reads ?limit and ?offset from the request, applying
// DefaultPageLimit/MaxPageLimit bounds and treating a negative or
// unparsable value as unset rather than erroring — pagination is meant to
// degrade gracefully, not add a new way for a client to get a 400.
func ParsePagination(r *http.Request) Pagination {
	p := Pagination{Limit: DefaultPageLimit, Offset: 0}

	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			p.Limit = n
		}
	}
	if p.Limit > MaxPageLimit {
		p.Limit = MaxPageLimit
	}

	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			p.Offset = n
		}
	}

	return p
}

// Page is the standard envelope every paginated list endpoint returns, so
// a client can tell whether more results exist without a separate count
// query. Items is always a concrete slice (never nil) so the JSON output
// is `[]`, not `null`, when empty.
type Page[T any] struct {
	Items   []T  `json:"items"`
	Limit   int  `json:"limit"`
	Offset  int  `json:"offset"`
	HasMore bool `json:"has_more"`
}

// NewPage builds a Page from a result slice fetched with limit+1 rows
// (the standard over-fetch-by-one trick to compute HasMore without a
// second COUNT query) — see how callers use it in cmd/server/router.go.
func NewPage[T any](rows []T, p Pagination) Page[T] {
	hasMore := len(rows) > p.Limit
	if hasMore {
		rows = rows[:p.Limit]
	}
	if rows == nil {
		rows = []T{}
	}
	return Page[T]{Items: rows, Limit: p.Limit, Offset: p.Offset, HasMore: hasMore}
}
