package api

import "net/http"

// domainDeploymentTargetMaxBody bounds the write. The body is four short
// fields, so this is generous.
const domainDeploymentTargetMaxBody = 16384

// Domain deployment targets are system-admin only, reads included. A target
// names the cluster connection a domain's generators run in, which is
// infrastructure topology rather than tenant-visible configuration.
//
// They are registered under /api/v1/system/ rather than /api/v1/domains/ so
// they sit outside the domains prefix catch-all in access_policy.go, whose GET
// branch grants viewer/editor/project_admin — see the note in
// handlers_domain_deployment_target.go.
var domainDeploymentTargetAPIRouteCatalog = apiRouteCatalog{
	Domain: "domain-deployment-targets",
	Routes: []apiRouteSpec{
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/system/deployment-targets", "handleListDomainDeploymentTargets",
			systemAdminAccess(), func(s *Server) http.Handler {
				return http.HandlerFunc(s.handleListDomainDeploymentTargets)
			}),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/system/deployment-targets/{id}", "handleGetDomainDeploymentTarget",
			systemAdminAccess(), func(s *Server) http.Handler {
				return http.HandlerFunc(s.handleGetDomainDeploymentTarget)
			}),
		newAuthenticatedAPIRoute(http.MethodPut, "/api/v1/system/deployment-targets/{id}", "handleUpdateDomainDeploymentTarget",
			accessRequirement{GlobalRoles: []string{RoleSystemAdmin}, MaxBodyBytes: domainDeploymentTargetMaxBody},
			func(s *Server) http.Handler {
				return http.HandlerFunc(s.handleUpdateDomainDeploymentTarget)
			}),
		newAuthenticatedAPIRoute(http.MethodDelete, "/api/v1/system/deployment-targets/{id}", "handleDeleteDomainDeploymentTarget",
			systemAdminAccess(), func(s *Server) http.Handler {
				return http.HandlerFunc(s.handleDeleteDomainDeploymentTarget)
			}),
	},
}
