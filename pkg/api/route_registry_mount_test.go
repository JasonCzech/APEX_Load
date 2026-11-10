package api

import (
	"net/http/httptest"
	"regexp"
	"testing"
)

// Catalog membership feeds the generated docs and auth rules, but only
// registerAPIRouteCatalog mounts a route. Check the real server mux so a
// catalog that is documented but never registered (every call 404s) fails here.
func TestModularRouteCatalogRoutesAreMounted(t *testing.T) {
	server, _ := newTimeslotServer(t)
	wildcard := regexp.MustCompile(`\{[^}]+\}`)
	for _, catalog := range modularAPIRouteCatalogs {
		for _, route := range catalog.Routes {
			want := route.Method + " " + route.Path
			request := httptest.NewRequest(route.Method, wildcard.ReplaceAllString(route.Path, "x"), nil)
			if _, pattern := server.mux.Handler(request); pattern != want {
				t.Errorf("%s route %s is not mounted: runtime pattern %q", catalog.Domain, want, pattern)
			}
		}
	}
}
