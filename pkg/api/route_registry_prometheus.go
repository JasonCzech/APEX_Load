package api

import "net/http"

var prometheusAPIRouteCatalog = apiRouteCatalog{Domain: "prometheus-monitoring", Routes: []apiRouteSpec{
	newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/projects/{id}/monitoring/prometheus-connections/{connection_id}/adopt-dashboard", "handlePrometheusAdoptDashboard", projectAdminAccess(resolveProjectIDFromPath), func(s *Server) http.Handler { return http.HandlerFunc(s.handlePrometheusAdoptDashboard) }),
	newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/projects/{id}/monitoring/prometheus-connections", "handleListPrometheusConnections", viewerProjectAccess(resolveProjectIDFromPath), func(s *Server) http.Handler { return http.HandlerFunc(s.handleListPrometheusConnections) }),
	newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/projects/{id}/monitoring/prometheus-connections", "handleCreatePrometheusConnection", projectAdminAccess(resolveProjectIDFromPath), func(s *Server) http.Handler { return http.HandlerFunc(s.handleCreatePrometheusConnection) }),
	newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/projects/{id}/monitoring/prometheus-connections/{connection_id}", "handleGetPrometheusConnection", projectAdminAccess(resolveProjectIDFromPath), func(s *Server) http.Handler { return http.HandlerFunc(s.handleGetPrometheusConnection) }),
	newAuthenticatedAPIRoute(http.MethodPut, "/api/v1/projects/{id}/monitoring/prometheus-connections/{connection_id}", "handleUpdatePrometheusConnection", projectAdminAccess(resolveProjectIDFromPath), func(s *Server) http.Handler { return http.HandlerFunc(s.handleUpdatePrometheusConnection) }),
	newAuthenticatedAPIRoute(http.MethodDelete, "/api/v1/projects/{id}/monitoring/prometheus-connections/{connection_id}", "handleDeletePrometheusConnection", projectAdminAccess(resolveProjectIDFromPath), func(s *Server) http.Handler { return http.HandlerFunc(s.handleDeletePrometheusConnection) }),
	newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/projects/{id}/monitoring/prometheus-connections/{connection_id}/test", "handleTestPrometheusConnection", projectAdminAccess(resolveProjectIDFromPath), func(s *Server) http.Handler { return http.HandlerFunc(s.handleTestPrometheusConnection) }),
	newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/projects/{id}/monitoring/prometheus-connections/{connection_id}/discover", "handleDiscoverPrometheusConnection", projectAdminAccess(resolveProjectIDFromPath), func(s *Server) http.Handler { return http.HandlerFunc(s.handleDiscoverPrometheusConnection) }),
	newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/projects/{id}/monitoring/prometheus-connections/{connection_id}/preview", "handlePreviewPrometheusConnection", projectAdminAccess(resolveProjectIDFromPath), func(s *Server) http.Handler { return http.HandlerFunc(s.handlePreviewPrometheusConnection) }),
	newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/projects/{id}/monitoring/prometheus-connections/{connection_id}/operations/{operation_id}", "handleGetPrometheusOperation", projectAdminAccess(resolveProjectIDFromPath), func(s *Server) http.Handler { return http.HandlerFunc(s.handleGetPrometheusOperation) }),
}}
