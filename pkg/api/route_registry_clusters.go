package api

import "net/http"

// Cluster administration is system-admin only, without exception. Even the
// redacted read shape carries the API server endpoint and Azure directory
// identifiers, so there is no viewer/editor tier here — unlike the fleet
// observability catalog, which does grant viewer reads.
var clusterAdministrationAPIRouteCatalog = apiRouteCatalog{
	Domain: "cluster-administration",
	Routes: []apiRouteSpec{
		newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/system/clusters", "handleCreateClusterConnection",
			accessRequirement{GlobalRoles: []string{RoleSystemAdmin}, MaxBodyBytes: 262144}, func(s *Server) http.Handler {
				return http.HandlerFunc(s.handleCreateClusterConnection)
			}),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/system/clusters", "handleListClusterConnections",
			systemAdminAccess(), func(s *Server) http.Handler {
				return http.HandlerFunc(s.handleListClusterConnections)
			}),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/system/clusters/{id}", "handleGetClusterConnection",
			systemAdminAccess(), func(s *Server) http.Handler {
				return http.HandlerFunc(s.handleGetClusterConnection)
			}),
		newAuthenticatedAPIRoute(http.MethodPut, "/api/v1/system/clusters/{id}", "handleUpdateClusterConnection",
			accessRequirement{GlobalRoles: []string{RoleSystemAdmin}, MaxBodyBytes: 262144}, func(s *Server) http.Handler {
				return http.HandlerFunc(s.handleUpdateClusterConnection)
			}),
		newAuthenticatedAPIRoute(http.MethodDelete, "/api/v1/system/clusters/{id}", "handleDeleteClusterConnection",
			systemAdminAccess(), func(s *Server) http.Handler {
				return http.HandlerFunc(s.handleDeleteClusterConnection)
			}),
		newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/system/clusters/{id}/test", "handleTestClusterConnection",
			accessRequirement{GlobalRoles: []string{RoleSystemAdmin}, MaxBodyBytes: 65536}, func(s *Server) http.Handler {
				return http.HandlerFunc(s.handleTestClusterConnection)
			}),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/system/clusters/{id}/capacity", "handleGetClusterCapacity",
			systemAdminAccess(), func(s *Server) http.Handler {
				return http.HandlerFunc(s.handleGetClusterCapacity)
			}),
		newAuthenticatedAPIRoute(http.MethodPut, "/api/v1/fleet/pools/{id}/allocation", "handleUpdateFleetPoolAllocation",
			accessRequirement{GlobalRoles: []string{RoleSystemAdmin}, MaxBodyBytes: 262144}, func(s *Server) http.Handler {
				return http.HandlerFunc(s.handleUpdateFleetPoolAllocation)
			}),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/fleet/pools/{id}/allocation/preflight", "handleFleetPoolAllocationPreflight",
			systemAdminAccess(), func(s *Server) http.Handler {
				return http.HandlerFunc(s.handleFleetPoolAllocationPreflight)
			}),
	},
}
