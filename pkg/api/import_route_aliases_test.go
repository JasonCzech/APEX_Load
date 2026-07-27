package api

import (
	"net/http"
	"strings"
	"testing"
)

// importRouteAliases pairs each canonical protected-import route with the
// legacy /bruno-imports/ path it replaced.
//
// The aliases are permanent, not a deprecation window. They are a published API
// surface: released dashboards, the desktop client's download interception, and
// any operator script written against the old paths still use them.
var importRouteAliases = []struct {
	method    string
	canonical string
	legacy    string
}{
	{http.MethodGet, "/api/v1/scripts/sample-id/imports/source", "/api/v1/scripts/sample-id/bruno-imports/source"},
	{http.MethodGet, "/api/v1/scripts/sample-id/imports/revisions", "/api/v1/scripts/sample-id/bruno-imports/revisions"},
	{http.MethodPost, "/api/v1/scripts/sample-id/imports/rollback", "/api/v1/scripts/sample-id/bruno-imports/rollback"},
}

// TestImportRouteAliasesResolveIdenticalAccess pins that renaming these routes
// did not quietly change who can call them.
//
// The failure this prevents is a privilege split, not a 404. If an alias were
// dropped from the route catalog its path would still be served by the mux, but
// accessRequirementForRequest would fall through to a different policy -- so
// /bruno-imports/source could end up readable by a role that cannot read
// /imports/source. Comparing the resolved requirements catches that; comparing
// route lists would not.
func TestImportRouteAliasesResolveIdenticalAccess(t *testing.T) {
	for _, alias := range importRouteAliases {
		t.Run(alias.legacy, func(t *testing.T) {
			canonical := accessRequirementFingerprint(mustRequest(t, alias.method, alias.canonical))
			legacy := accessRequirementFingerprint(mustRequest(t, alias.method, alias.legacy))

			if canonical == "NO_MATCH" {
				t.Fatalf("canonical route %s %s has no access requirement", alias.method, alias.canonical)
			}
			if legacy == "NO_MATCH" {
				t.Fatalf("legacy alias %s %s has no access requirement", alias.method, alias.legacy)
			}
			if legacy != canonical {
				t.Fatalf("alias %s %s resolves to different access than its canonical twin:\n  legacy    %s\n  canonical %s",
					alias.method, alias.legacy, legacy, canonical)
			}
		})
	}
}

// TestImportRouteAliasesAreServed pins that both spellings are registered and
// reach the same handler. The access test above only proves they would be
// authorized identically if served at all.
func TestImportRouteAliasesAreServed(t *testing.T) {
	routes := registeredServerRoutes(t)
	registered := map[string]bool{}
	for _, route := range routes {
		registered[route.Method+" "+route.Path] = true
	}
	for _, alias := range importRouteAliases {
		for _, concrete := range []string{alias.canonical, alias.legacy} {
			pattern := strings.Replace(concrete, "/sample-id/", "/{id}/", 1)
			if !registered[alias.method+" "+pattern] {
				t.Fatalf("%s %s is not registered on the server mux", alias.method, pattern)
			}
		}
	}
}

func mustRequest(t *testing.T, method, path string) *http.Request {
	t.Helper()
	request, err := http.NewRequest(method, path, nil)
	if err != nil {
		t.Fatalf("build request %s %s: %v", method, path, err)
	}
	return request
}
