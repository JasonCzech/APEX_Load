package api

import "net/http"

// executionPlanAPIRouteCatalog is the canonical transport and access contract
// for execution-plan CRUD, materialized runs, recurring schedules, occurrence
// recovery, and governance approvals.
var executionPlanAPIRouteCatalog = apiRouteCatalog{
	Domain: "execution-plans",
	Routes: []apiRouteSpec{
		newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/execution-plans", "handleCreateExecutionPlan", editorImportAccess(resolveExecutionPlanCreateProjectID, 262144), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleCreateExecutionPlan)
		}),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/execution-plans", "handleListExecutionPlans", viewerProjectAccess(resolveProjectIDFromQuery), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleListExecutionPlans)
		}),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/execution-plans/{id}", "handleGetExecutionPlan", viewerProjectAccess(resolveExecutionPlanProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleGetExecutionPlan)
		}),
		newAuthenticatedAPIRoute(http.MethodPut, "/api/v1/execution-plans/{id}", "handleUpdateExecutionPlan", editorImportAccess(resolveExecutionPlanProjectID, 262144), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleUpdateExecutionPlan)
		}),
		newAuthenticatedAPIRoute(http.MethodDelete, "/api/v1/execution-plans/{id}", "handleDeleteExecutionPlan", editorProjectAccess(resolveExecutionPlanProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleDeleteExecutionPlan)
		}),
		newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/execution-plans/{id}/runs", "handleMaterializeAndRunExecutionPlan", editorProjectAccess(resolveExecutionPlanProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleMaterializeAndRunExecutionPlan)
		}),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/execution-plans/{id}/runs", "handleListExecutionPlanRuns", viewerProjectAccess(resolveExecutionPlanProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleListExecutionPlanRuns)
		}),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/execution-plans/{id}/runs/{runId}", "handleGetExecutionPlanRun", viewerProjectAccess(resolveExecutionPlanProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleGetExecutionPlanRun)
		}),
		newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/execution-plans/{id}/runs/{runId}/stop", "handleStopExecutionPlanRun", editorProjectAccess(resolveExecutionPlanProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleStopExecutionPlanRun)
		}),
		newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/execution-plans/{id}/schedules/preview", "handlePreviewExecutionPlanSchedule", editorProjectAccess(resolveExecutionPlanProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handlePreviewExecutionPlanSchedule)
		}),
		newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/execution-plans/{id}/schedules", "handleCreateExecutionPlanSchedule", editorProjectAccess(resolveExecutionPlanProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleCreateExecutionPlanSchedule)
		}),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/execution-plans/{id}/schedules", "handleListExecutionPlanSchedules", viewerProjectAccess(resolveExecutionPlanProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleListExecutionPlanSchedules)
		}),
		newAuthenticatedAPIRoute(http.MethodPut, "/api/v1/execution-plans/{id}/schedules/{schedule_id}", "handleUpdateExecutionPlanSchedule", editorProjectAccess(resolveExecutionPlanProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleUpdateExecutionPlanSchedule)
		}),
		newAuthenticatedAPIRoute(http.MethodDelete, "/api/v1/execution-plans/{id}/schedules/{schedule_id}", "handleDeleteExecutionPlanSchedule", editorProjectAccess(resolveExecutionPlanProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleDeleteExecutionPlanSchedule)
		}),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/execution-plans/{id}/schedules/{schedule_id}/occurrences", "handleListExecutionPlanScheduleOccurrences", viewerProjectAccess(resolveExecutionPlanProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleListExecutionPlanScheduleOccurrences)
		}),
		newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/execution-plans/{id}/schedules/{schedule_id}/occurrences/{occurrence_id}/resolve", "handleResolveExecutionPlanScheduleOccurrence", projectAdminAccess(resolveExecutionPlanProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleResolveExecutionPlanScheduleOccurrence)
		}),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/execution-plans/{id}/approvals", "handleListExecutionPlanApprovals", viewerProjectAccess(resolveExecutionPlanProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleListExecutionPlanApprovals)
		}),
		newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/execution-plans/{id}/approvals/{approval_id}/approve", "handleApproveExecutionPlanApproval", projectAdminAccess(resolveExecutionPlanProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleApproveExecutionPlanApproval)
		}),
		newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/execution-plans/{id}/approvals/{approval_id}/reject", "handleRejectExecutionPlanApproval", projectAdminAccess(resolveExecutionPlanProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleRejectExecutionPlanApproval)
		}),
	},
}
