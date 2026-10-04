package api

import "net/http"

var dynatraceNativeAPIRouteCatalog = apiRouteCatalog{Domain: "dynatrace-native", Routes: []apiRouteSpec{
	newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/projects/{id}/monitoring/dynatrace-connections/{connection_id}/operations", "handleCreateDynatraceOperation", editorProjectAccess(resolveProjectIDFromPath), func(s *Server) http.Handler { return http.HandlerFunc(s.handleCreateDynatraceOperation) }),
	newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/projects/{id}/monitoring/dynatrace-connections/{connection_id}/operations/{operation_id}", "handleGetDynatraceOperation", editorProjectAccess(resolveProjectIDFromPath), func(s *Server) http.Handler { return http.HandlerFunc(s.handleGetDynatraceOperation) }),
	newPublicAPIRoute(http.MethodGet, "/api/v1/monitoring/collectors/{id}/dynatrace-operations", "handlePollDynatraceOperations", func(s *Server) http.Handler { return http.HandlerFunc(s.handlePollDynatraceOperations) }),
	newPublicAPIRoute(http.MethodPost, "/api/v1/monitoring/collectors/{id}/dynatrace-operations/{operation_id}/result", "handleDynatraceOperationResult", func(s *Server) http.Handler { return http.HandlerFunc(s.handleDynatraceOperationResult) }),
	newPublicAPIRoute(http.MethodPost, "/api/v1/monitoring/collectors/{id}/jobs/{job_id}/scan-reservations", "handleDynatraceScanReservation", func(s *Server) http.Handler { return http.HandlerFunc(s.handleDynatraceScanReservation) }),
}}
