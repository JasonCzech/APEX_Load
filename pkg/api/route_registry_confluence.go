package api

import "net/http"

var confluenceAPIRouteCatalog = apiRouteCatalog{Domain: "confluence", Routes: []apiRouteSpec{
	newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/projects/{id}/integrations/confluence-connections", "handleListConfluenceConnections", viewerProjectAccess(resolveProjectIDFromPath), func(s *Server) http.Handler { return http.HandlerFunc(s.handleListConfluenceConnections) }),
	newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/projects/{id}/integrations/confluence-connections", "handleCreateConfluenceConnection", projectAdminAccess(resolveProjectIDFromPath), func(s *Server) http.Handler { return http.HandlerFunc(s.handleCreateConfluenceConnection) }),
	newAuthenticatedAPIRoute(http.MethodPut, "/api/v1/projects/{id}/integrations/confluence-connections/{connection_id}", "handleUpdateConfluenceConnection", projectAdminAccess(resolveProjectIDFromPath), func(s *Server) http.Handler { return http.HandlerFunc(s.handleUpdateConfluenceConnection) }),
	newAuthenticatedAPIRoute(http.MethodDelete, "/api/v1/projects/{id}/integrations/confluence-connections/{connection_id}", "handleDeleteConfluenceConnection", projectAdminAccess(resolveProjectIDFromPath), func(s *Server) http.Handler { return http.HandlerFunc(s.handleDeleteConfluenceConnection) }),
	newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/projects/{id}/integrations/confluence-connections/{connection_id}/test", "handleTestConfluenceConnection", projectAdminAccess(resolveProjectIDFromPath), func(s *Server) http.Handler { return http.HandlerFunc(s.handleTestConfluenceConnection) }),
	newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/tests/{id}/publish/confluence", "handleGetConfluencePublicationStatus", viewerProjectAccess(resolveTestProjectID), func(s *Server) http.Handler { return http.HandlerFunc(s.handleGetConfluencePublicationStatus) }),
	newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/tests/{id}/publish/confluence", "handlePublishRunToConfluence", editorProjectAccess(resolveTestProjectID), func(s *Server) http.Handler { return http.HandlerFunc(s.handlePublishRunToConfluence) }),
}}
