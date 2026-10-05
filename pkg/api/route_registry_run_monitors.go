package api

import "net/http"

var runMonitoringAPIRouteCatalog = apiRouteCatalog{Domain: "run-monitoring", Routes: []apiRouteSpec{
	newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/tests/{id}/dependency-evidence", "handleDependencyEvidence", viewerProjectAccess(resolveTestProjectID), func(s *Server) http.Handler { return http.HandlerFunc(s.handleDependencyEvidence) }),
	newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/tests/{id}/preparation", "handlePreparation", editorProjectAccess(resolveTestProjectID), func(s *Server) http.Handler { return http.HandlerFunc(s.handlePreparation) }),
	newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/tests/{id}/preparation", "handlePreparation", viewerProjectAccess(resolveTestProjectID), func(s *Server) http.Handler { return http.HandlerFunc(s.handlePreparation) }),
	newAuthenticatedAPIRoute(http.MethodDelete, "/api/v1/tests/{id}/preparation", "handlePreparation", editorProjectAccess(resolveTestProjectID), func(s *Server) http.Handler { return http.HandlerFunc(s.handlePreparation) }),
	newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/projects/{id}/monitor-sources", "handleMonitorSources", viewerProjectAccess(resolveProjectIDFromPath), func(s *Server) http.Handler { return http.HandlerFunc(s.handleMonitorSources) }),
	newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/projects/{id}/monitor-sources", "handleMonitorSources", projectAdminAccess(resolveProjectIDFromPath), func(s *Server) http.Handler { return http.HandlerFunc(s.handleMonitorSources) }),
	newAuthenticatedAPIRoute(http.MethodPut, "/api/v1/projects/{id}/monitor-sources/{source_id}", "handleMonitorSources", projectAdminAccess(resolveProjectIDFromPath), func(s *Server) http.Handler { return http.HandlerFunc(s.handleMonitorSources) }),
	newAuthenticatedAPIRoute(http.MethodDelete, "/api/v1/projects/{id}/monitor-sources/{source_id}", "handleMonitorSources", projectAdminAccess(resolveProjectIDFromPath), func(s *Server) http.Handler { return http.HandlerFunc(s.handleMonitorSources) }),
	newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/projects/{id}/monitor-sources/{source_id}/management-state", "handleManagementCollectionState", viewerProjectAccess(resolveProjectIDFromPath), func(s *Server) http.Handler { return http.HandlerFunc(s.handleManagementCollectionState) }),
	newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/projects/{id}/monitor-sources/{source_id}/management-state", "handleManagementCollectionState", projectAdminAccess(resolveProjectIDFromPath), func(s *Server) http.Handler { return http.HandlerFunc(s.handleManagementCollectionState) }),
	newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/scenarios/{id}/monitor-bindings", "handleMonitorBindings", viewerProjectAccess(resolveScenarioProjectID), func(s *Server) http.Handler { return http.HandlerFunc(s.handleMonitorBindings) }),
	newAuthenticatedAPIRoute(http.MethodPut, "/api/v1/scenarios/{id}/monitor-bindings", "handleMonitorBindings", editorProjectAccess(resolveScenarioProjectID), func(s *Server) http.Handler { return http.HandlerFunc(s.handleMonitorBindings) }),
	newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/tests/{id}/monitors", "handleRunMonitors", viewerProjectAccess(resolveTestProjectID), func(s *Server) http.Handler { return http.HandlerFunc(s.handleRunMonitors) }),
	newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/tests/{id}/monitor-series", "handleRunMonitorSeries", viewerProjectAccess(resolveTestProjectID), func(s *Server) http.Handler { return http.HandlerFunc(s.handleRunMonitorSeries) }),
	newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/tests/{id}/monitor-management", "handleMonitorManagement", viewerProjectAccess(resolveTestProjectID), func(s *Server) http.Handler { return http.HandlerFunc(s.handleMonitorManagement) }),
	newPublicAPIRoute(http.MethodPost, "/api/v1/monitoring/collectors/{id}/jobs/{job_id}/management-polls", "handleManagementPoll", func(s *Server) http.Handler { return http.HandlerFunc(s.handleManagementPoll) }),
	newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/tests/{id}/monitor-evidence", "handleRunMonitorEvidence", viewerProjectAccess(resolveTestProjectID), func(s *Server) http.Handler { return http.HandlerFunc(s.handleRunMonitorEvidence) }),
	newPublicAPIRoute(http.MethodGet, "/api/v1/monitoring/collectors/{id}/jobs", "handlePollRunMonitorJobs", func(s *Server) http.Handler { return http.HandlerFunc(s.handlePollRunMonitorJobs) }),
	newPublicAPIRoute(http.MethodPost, "/api/v1/monitoring/collectors/{id}/jobs/{job_id}/samples", "handleRunMonitorSamples", func(s *Server) http.Handler { return http.HandlerFunc(s.handleRunMonitorSamples) }),
}}
