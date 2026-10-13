package api

import "net/http"

// scenarioDraftsAPIRouteCatalog is the transport and access contract for
// scenario-builder drafts saved to a project. Reads follow scenario reads and
// writes follow scenario writes, for people and service accounts alike.
var scenarioDraftsAPIRouteCatalog = apiRouteCatalog{Domain: "scenario-drafts", Routes: []apiRouteSpec{
	newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/projects/{id}/scenario-drafts", "handleListScenarioDrafts", withServiceAccountScope(viewerProjectAccess(resolveProjectIDFromPath), "results:read"), func(s *Server) http.Handler {
		return http.HandlerFunc(s.handleListScenarioDrafts)
	}),
	newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/projects/{id}/scenario-drafts", "handleCreateScenarioDraft", withServiceAccountScope(editorProjectAccessWithBodyLimit(resolveProjectIDFromPath, scenarioDraftMaxBodyBytes), "tests:run"), func(s *Server) http.Handler {
		return http.HandlerFunc(s.handleCreateScenarioDraft)
	}),
	newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/projects/{id}/scenario-drafts/{draftID}", "handleGetScenarioDraft", withServiceAccountScope(viewerProjectAccess(resolveProjectIDFromPath), "results:read"), func(s *Server) http.Handler {
		return http.HandlerFunc(s.handleGetScenarioDraft)
	}),
	newAuthenticatedAPIRoute(http.MethodPut, "/api/v1/projects/{id}/scenario-drafts/{draftID}", "handleUpdateScenarioDraft", withServiceAccountScope(editorProjectAccessWithBodyLimit(resolveProjectIDFromPath, scenarioDraftMaxBodyBytes), "tests:run"), func(s *Server) http.Handler {
		return http.HandlerFunc(s.handleUpdateScenarioDraft)
	}),
	newAuthenticatedAPIRoute(http.MethodDelete, "/api/v1/projects/{id}/scenario-drafts/{draftID}", "handleDeleteScenarioDraft", withServiceAccountScope(editorProjectAccess(resolveProjectIDFromPath), "tests:run"), func(s *Server) http.Handler {
		return http.HandlerFunc(s.handleDeleteScenarioDraft)
	}),
}}
