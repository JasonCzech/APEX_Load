package api

import "net/http"

// scriptLoadRunsAPIRouteCatalog lists a script's recent load runs for the
// debugger's comparison with load. Readers of the script may list them; a
// service account also needs results:read, since the list is test-run
// metadata (ids, names, statuses, times, sizing), like GET /api/v1/tests.
var scriptLoadRunsAPIRouteCatalog = apiRouteCatalog{Domain: "script-load-runs", Routes: []apiRouteSpec{
	newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/scripts/{id}/load-runs", "handleListScriptLoadRuns", withServiceAccountScope(viewerProjectAccess(resolveScriptProjectID), "results:read"), func(s *Server) http.Handler {
		return http.HandlerFunc(s.handleListScriptLoadRuns)
	}),
}}
