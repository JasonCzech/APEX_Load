package api

import "net/http"

// documentationAPIRouteCatalog is the first fully migrated route domain. Its
// access requirements feed the runtime authorization catalog as well as route
// registration, removing the former registration/policy duplication.
var documentationAPIRouteCatalog = apiRouteCatalog{
	Domain: "documentation",
	Routes: []apiRouteSpec{
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/docs/swagger.yaml", "handleGetAPIDocsSwagger", viewerGlobalAccess(), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleGetAPIDocsSwagger)
		}),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/docs/endpoint-catalog", "handleGetAPIDocsEndpointCatalog", viewerGlobalAccess(), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleGetAPIDocsEndpointCatalog)
		}),
	},
}

// systemFoundationAPIRouteCatalog covers the anonymous health/readiness probes
// and the authenticated settings contract. It demonstrates both supported
// middleware profiles while keeping system handlers in their existing modules.
var systemFoundationAPIRouteCatalog = apiRouteCatalog{
	Domain: "system-foundation",
	Routes: []apiRouteSpec{
		newPublicAPIRoute(http.MethodGet, "/api/v1/system/health", "handleHealthCheck", func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleHealthCheck)
		}),
		newPublicAPIRoute(http.MethodGet, "/api/v1/system/version", "handleSystemVersion", func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleSystemVersion)
		}),
		newPublicAPIRoute(http.MethodGet, "/api/v1/system/readiness", "handleSystemReadiness", func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleSystemReadiness)
		}),
		newPublicAPIRoute(http.MethodGet, "/api/v1/system/execution-readiness", "handleExecutionReadiness", func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleExecutionReadiness)
		}),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/system/settings", "handleGetSystemSettings", viewerGlobalAccess(), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleGetSystemSettings)
		}),
		newAuthenticatedAPIRoute(http.MethodPut, "/api/v1/system/settings", "handleUpdateSystemSettings", systemAdminAccess(), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleUpdateSystemSettings)
		}),
	},
}

var modularAPIRouteCatalogs = []apiRouteCatalog{
	analysisAPIRouteCatalog,
	runMonitoringAPIRouteCatalog,
	documentationAPIRouteCatalog,
	systemFoundationAPIRouteCatalog,
	systemEntitlementsAPIRouteCatalog,
	systemObservabilityAPIRouteCatalog,
	systemAdministrationAPIRouteCatalog,
	generatorGatewayAPIRouteCatalog,
	capacityAPIRouteCatalog,
	fleetObservabilityAPIRouteCatalog,
	fleetAdministrationAPIRouteCatalog,
	clusterAdministrationAPIRouteCatalog,
	computeAdministrationAPIRouteCatalog,
	domainDeploymentTargetAPIRouteCatalog,
	runArchiveAPIRouteCatalog,
	archiveRetentionAPIRouteCatalog,
	powerBIAPIRouteCatalog,
	confluenceAPIRouteCatalog,
	scenarioSchedulingAPIRouteCatalog,
	baselineComparisonAPIRouteCatalog,
	executionPlanAPIRouteCatalog,
	objectRepositoryAPIRouteCatalog,
	correlationRulesetAPIRouteCatalog,
}
