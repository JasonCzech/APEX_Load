package api

import "net/http"

var elasticAPIRouteCatalog = apiRouteCatalog{Domain: "elastic", Routes: []apiRouteSpec{
	newPublicAPIRoute(http.MethodGet, "/api/v1/monitoring/collectors/{id}/elastic-cleanups", "handlePollElasticCleanups", func(s *Server) http.Handler { return http.HandlerFunc(s.handlePollElasticCleanups) }),
	newPublicAPIRoute(http.MethodPost, "/api/v1/monitoring/collectors/{id}/jobs/{job_id}/cleanup", "handleElasticCleanupReceipt", func(s *Server) http.Handler { return http.HandlerFunc(s.handleElasticCleanupReceipt) }),
	newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/projects/{id}/monitoring/elk-connections/{connection_id}/inspections", "handleCreateElasticInspection", projectAdminAccess(resolveProjectIDFromPath), func(s *Server) http.Handler { return http.HandlerFunc(s.handleCreateElasticInspection) }),
	newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/projects/{id}/monitoring/elk-connections/{connection_id}/inspections/{operation_id}", "handleGetElasticInspection", projectAdminAccess(resolveProjectIDFromPath), func(s *Server) http.Handler { return http.HandlerFunc(s.handleGetElasticInspection) }),
	newAuthenticatedAPIRoute(http.MethodDelete, "/api/v1/projects/{id}/monitoring/elk-connections/{connection_id}/inspections/{operation_id}", "handleCancelElasticInspection", projectAdminAccess(resolveProjectIDFromPath), func(s *Server) http.Handler { return http.HandlerFunc(s.handleCancelElasticInspection) }),
	newPublicAPIRoute(http.MethodGet, "/api/v1/monitoring/collectors/{id}/elastic-inspections", "handlePollElasticInspections", func(s *Server) http.Handler { return http.HandlerFunc(s.handlePollElasticInspections) }),
	newPublicAPIRoute(http.MethodPost, "/api/v1/monitoring/collectors/{id}/elastic-inspections/{operation_id}/result", "handleElasticInspectionResult", func(s *Server) http.Handler { return http.HandlerFunc(s.handleElasticInspectionResult) }),
}}
