package api

import "net/http"

var capacityAPIRouteCatalog = apiRouteCatalog{
	Domain: "capacity",
	Routes: []apiRouteSpec{
		newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/capacity/timeslots", "handleCreateCapacityReservation", editorImportAccess(resolveTimeslotCreateProjectID, 65536), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleCreateCapacityReservation)
		}),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/capacity/timeslots", "handleListCapacityReservations", viewerProjectAccess(resolveProjectIDFromQuery), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleListCapacityReservations)
		}),
		newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/capacity/timeslots/availability", "handleCheckCapacityAvailability", accessRequirement{
			GlobalRoles:   []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
			ProjectRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
			ProjectLookup: resolveTimeslotCreateProjectID,
			MaxBodyBytes:  65536,
		}, func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleCheckCapacityAvailability)
		}),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/capacity/timeslots/{id}", "handleGetCapacityReservation", viewerProjectAccess(resolveTimeslotProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleGetCapacityReservation)
		}),
		newAuthenticatedAPIRoute(http.MethodPut, "/api/v1/capacity/timeslots/{id}", "handleUpdateCapacityReservation", editorImportAccess(resolveTimeslotProjectID, 65536), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleUpdateCapacityReservation)
		}),
		newAuthenticatedAPIRoute(http.MethodDelete, "/api/v1/capacity/timeslots/{id}", "handleDeleteCapacityReservation", projectAdminAccess(resolveTimeslotProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleDeleteCapacityReservation)
		}),
		newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/capacity/timeslots/{id}/approve", "handleApproveCapacityReservation", projectAdminAccess(resolveTimeslotProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleApproveCapacityReservation)
		}),
		newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/capacity/timeslots/{id}/reject", "handleRejectCapacityReservation", projectAdminAccess(resolveTimeslotProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleRejectCapacityReservation)
		}),
		newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/capacity/timeslots/{id}/bind", "handleBindCapacityTimeslot", editorImportAccess(resolveTimeslotProjectID, 65536), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleBindCapacityTimeslot)
		}),
	},
}
