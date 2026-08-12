package api

import "net/http"

// scenarioSchedulingAPIRouteCatalog is the canonical transport and access
// contract for scenario schedules, their occurrence/approval workflows, and
// the operator-wide scheduling views. Most scenario routes historically
// inherited authorization from the scenario prefix policy; those entries keep
// their pre-migration OpenAPI access text while the runtime catalog receives
// the equivalent explicit service-account scope.
var scenarioSchedulingAPIRouteCatalog = apiRouteCatalog{
	Domain: "scenario-scheduling",
	Routes: []apiRouteSpec{
		preserveRawAccessRequirement(newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/scenarios/{id}/schedules/preview", "handleScenarioSchedulePreview", editorProjectAccess(resolveScenarioProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleScenarioSchedulePreview)
		})),
		preserveRawAccessRequirement(newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/scenarios/{id}/schedules", "handleCreateScenarioSchedule", editorProjectAccess(resolveScenarioProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleCreateScenarioSchedule)
		})),
		preserveRawAccessRequirement(newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/scenarios/{id}/schedules", "handleListScenarioSchedules", viewerProjectAccess(resolveScenarioProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleListScenarioSchedules)
		})),
		preserveRawAccessRequirement(newAuthenticatedAPIRoute(http.MethodPut, "/api/v1/scenarios/{id}/schedules/{schedule_id}", "handleUpdateScenarioSchedule", editorProjectAccess(resolveScenarioProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleUpdateScenarioSchedule)
		})),
		preserveRawAccessRequirement(newAuthenticatedAPIRoute(http.MethodDelete, "/api/v1/scenarios/{id}/schedules/{schedule_id}", "handleDeleteScenarioSchedule", editorProjectAccess(resolveScenarioProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleDeleteScenarioSchedule)
		})),
		preserveRawAccessRequirement(newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/scenarios/{id}/schedules/{schedule_id}/occurrences", "handleListScenarioScheduleOccurrences", viewerProjectAccess(resolveScenarioProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleListScenarioScheduleOccurrences)
		})),
		newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/scenarios/{id}/schedules/{schedule_id}/occurrences/{occurrence_id}/resolve", "handleResolveScenarioScheduleOccurrence", projectAdminAccess(resolveScenarioProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleResolveScenarioScheduleOccurrence)
		}),
		preserveRawAccessRequirement(newAuthenticatedAPIRoute(http.MethodDelete, "/api/v1/scenarios/{id}/schedules/{schedule_id}/occurrences/{occurrence_id}", "handleDeleteScenarioScheduleOccurrence", editorProjectAccess(resolveScenarioProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleDeleteScenarioScheduleOccurrence)
		})),
		preserveRawAccessRequirement(newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/scenarios/{id}/schedules/{schedule_id}/approvals", "handleListScenarioScheduleApprovals", viewerProjectAccess(resolveScenarioProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleListScenarioScheduleApprovals)
		})),
		preserveRawAccessRequirement(newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/scenarios/{id}/schedules/{schedule_id}/approvals/{approval_id}/approve", "handleApproveScenarioScheduleApproval", projectAdminAccess(resolveScenarioProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleApproveScenarioScheduleApproval)
		})),
		preserveRawAccessRequirement(newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/scenarios/{id}/schedules/{schedule_id}/approvals/{approval_id}/reject", "handleRejectScenarioScheduleApproval", projectAdminAccess(resolveScenarioProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleRejectScenarioScheduleApproval)
		})),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/schedules/health", "handleScenarioScheduleHealth", unrestrictedViewerGlobalAccess(), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleScenarioScheduleHealth)
		}),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/run-calendar", "handleGetRunCalendar", unrestrictedViewerGlobalAccess(), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleGetRunCalendar)
		}),
	},
}
