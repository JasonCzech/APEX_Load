package api

import "net/http"

var objectActivityAPIRouteCatalog = apiRouteCatalog{Domain: "object-activity", Routes: []apiRouteSpec{
	newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/projects/{id}/activity", "handleProjectActivity", activityProjectAccess(), func(s *Server) http.Handler { return http.HandlerFunc(s.handleProjectActivity) }),
	newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/scripts/{id}/archive", "handleArchiveObject", editorProjectAccess(resolveScriptProjectID), func(s *Server) http.Handler { return http.HandlerFunc(s.handleArchiveObject) }),
	newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/scripts/{id}/restore", "handleArchiveObject", editorProjectAccess(resolveScriptProjectID), func(s *Server) http.Handler { return http.HandlerFunc(s.handleArchiveObject) }),
	newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/scenarios/{id}/archive", "handleArchiveObject", editorProjectAccess(resolveScenarioProjectID), func(s *Server) http.Handler { return http.HandlerFunc(s.handleArchiveObject) }),
	newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/scenarios/{id}/restore", "handleArchiveObject", editorProjectAccess(resolveScenarioProjectID), func(s *Server) http.Handler { return http.HandlerFunc(s.handleArchiveObject) }),
}}

func activityProjectAccess() accessRequirement {
	v := viewerProjectAccess(resolveProjectIDFromPath)
	v.ServiceAccountScope = ""
	v.ServiceAccountScopeLookup = func(r *http.Request) string {
		if r.URL.Query().Get("resource_type") == "script" {
			return "read"
		}
		if r.URL.Query().Get("resource_type") != "" {
			return "results:read"
		}
		return "object-activity:read"
	}
	return v
}
