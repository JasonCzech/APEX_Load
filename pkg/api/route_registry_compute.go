package api

import "net/http"

// Normalized compute administration is deliberately separate from the legacy
// Kubernetes cluster registry. Cloud connections own credentials; execution
// targets own placement and runtime capabilities.
var computeAdministrationAPIRouteCatalog = apiRouteCatalog{
	Domain: "compute-administration",
	Routes: []apiRouteSpec{
		newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/system/cloud-connections", "handleCreateCloudConnection",
			accessRequirement{GlobalRoles: []string{RoleSystemAdmin}, MaxBodyBytes: 262144}, func(s *Server) http.Handler {
				return http.HandlerFunc(s.handleCreateCloudConnection)
			}),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/system/cloud-connections", "handleListCloudConnections",
			systemAdminAccess(), func(s *Server) http.Handler { return http.HandlerFunc(s.handleListCloudConnections) }),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/system/cloud-connections/{id}", "handleGetCloudConnection",
			systemAdminAccess(), func(s *Server) http.Handler { return http.HandlerFunc(s.handleGetCloudConnection) }),
		newAuthenticatedAPIRoute(http.MethodPut, "/api/v1/system/cloud-connections/{id}", "handleUpdateCloudConnection",
			accessRequirement{GlobalRoles: []string{RoleSystemAdmin}, MaxBodyBytes: 262144}, func(s *Server) http.Handler {
				return http.HandlerFunc(s.handleUpdateCloudConnection)
			}),
		newAuthenticatedAPIRoute(http.MethodDelete, "/api/v1/system/cloud-connections/{id}", "handleDeleteCloudConnection",
			systemAdminAccess(), func(s *Server) http.Handler { return http.HandlerFunc(s.handleDeleteCloudConnection) }),
		newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/system/cloud-connections/{id}/test", "handleTestCloudConnection",
			accessRequirement{GlobalRoles: []string{RoleSystemAdmin}, MaxBodyBytes: 65536}, func(s *Server) http.Handler {
				return http.HandlerFunc(s.handleTestCloudConnection)
			}),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/system/cloud-connections/{id}/resources", "handleListCloudConnectionResources",
			systemAdminAccess(), func(s *Server) http.Handler { return http.HandlerFunc(s.handleListCloudConnectionResources) }),

		newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/system/execution-targets", "handleCreateExecutionTarget",
			accessRequirement{GlobalRoles: []string{RoleSystemAdmin}, MaxBodyBytes: 262144}, func(s *Server) http.Handler {
				return http.HandlerFunc(s.handleCreateExecutionTarget)
			}),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/system/execution-targets", "handleListExecutionTargets",
			systemAdminAccess(), func(s *Server) http.Handler { return http.HandlerFunc(s.handleListExecutionTargets) }),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/system/execution-targets/{id}", "handleGetExecutionTarget",
			systemAdminAccess(), func(s *Server) http.Handler { return http.HandlerFunc(s.handleGetExecutionTarget) }),
		newAuthenticatedAPIRoute(http.MethodPut, "/api/v1/system/execution-targets/{id}", "handleUpdateExecutionTarget",
			accessRequirement{GlobalRoles: []string{RoleSystemAdmin}, MaxBodyBytes: 262144}, func(s *Server) http.Handler {
				return http.HandlerFunc(s.handleUpdateExecutionTarget)
			}),
		newAuthenticatedAPIRoute(http.MethodDelete, "/api/v1/system/execution-targets/{id}", "handleDeleteExecutionTarget",
			systemAdminAccess(), func(s *Server) http.Handler { return http.HandlerFunc(s.handleDeleteExecutionTarget) }),
		newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/system/execution-targets/{id}/test", "handleTestExecutionTarget",
			accessRequirement{GlobalRoles: []string{RoleSystemAdmin}, MaxBodyBytes: 65536}, func(s *Server) http.Handler {
				return http.HandlerFunc(s.handleTestExecutionTarget)
			}),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/system/execution-targets/{id}/capabilities", "handleGetExecutionTargetCapabilities",
			systemAdminAccess(), func(s *Server) http.Handler { return http.HandlerFunc(s.handleGetExecutionTargetCapabilities) }),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/system/execution-targets/{id}/capacity", "handleGetExecutionTargetCapacity",
			systemAdminAccess(), func(s *Server) http.Handler { return http.HandlerFunc(s.handleGetExecutionTargetCapacity) }),
	},
}
