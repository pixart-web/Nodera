package main

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/nodera/nodera/openapi"
)

// TestOpenAPI_CoversEveryRoute fails when a route is registered without being
// documented in api/openapi/openapi.json (and when the spec documents a route
// that no longer exists), so the spec cannot silently drift from the code.
func TestOpenAPI_CoversEveryRoute(t *testing.T) {
	var spec struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(openapi.Spec, &spec); err != nil {
		t.Fatal(err)
	}
	documented := map[string]bool{}
	for p, ops := range spec.Paths {
		for m := range ops {
			documented[strings.ToUpper(m)+" "+p] = true
		}
	}
	routes := map[string]bool{}
	r := newRouter(apiDeps{}).(chi.Router)
	err := chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		route = strings.TrimSuffix(route, "/")
		if method == http.MethodOptions || method == http.MethodHead || route == "/docs" || route == "/openapi.json" {
			return nil
		}
		routes[method+" "+route] = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var missing, stale []string
	for k := range routes {
		if !documented[k] {
			missing = append(missing, k)
		}
	}
	for k := range documented {
		if !routes[k] {
			stale = append(stale, k)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	if len(missing) > 0 {
		t.Errorf("%d routes are not documented in openapi.json:\n  %s", len(missing), strings.Join(missing, "\n  "))
	}
	if len(stale) > 0 {
		t.Errorf("%d documented routes do not exist:\n  %s", len(stale), strings.Join(stale, "\n  "))
	}
}
