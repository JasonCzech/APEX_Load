package api

import "net/http"

var systemEntitlementsAPIRouteCatalog = apiRouteCatalog{
	Domain: "system-entitlements",
	Routes: []apiRouteSpec{
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/system/entitlements", "handleGetEntitlements", viewerGlobalAccess(), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleGetEntitlements)
		}),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/system/entitlements/usage", "handleGetEntitlementsUsage", unrestrictedViewerGlobalAccess(), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleGetEntitlementsUsage)
		}),
	},
}

var systemObservabilityAPIRouteCatalog = apiRouteCatalog{
	Domain: "system-observability",
	Routes: []apiRouteSpec{
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/system/inventory", "handleGetSystemInventory", viewerGlobalAccess(), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleGetSystemInventory)
		}),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/system/services", "handleGetSystemServices", viewerGlobalAccess(), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleGetSystemServices)
		}),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/system/usage", "handleGetUsageAccounting", unrestrictedViewerGlobalAccess(), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleGetUsageAccounting)
		}),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/system/usage/export", "handleExportUsageAccounting", unrestrictedViewerGlobalAccess(), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleExportUsageAccounting)
		}),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/system/usage/snapshots", "handleListUsageAccountingSnapshots", unrestrictedViewerGlobalAccess(), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleListUsageAccountingSnapshots)
		}),
		newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/system/usage/snapshots", "handleCreateUsageAccountingSnapshot", systemAdminAccess(), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleCreateUsageAccountingSnapshot)
		}),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/system/parity-status", "handleGetParityStatus", viewerGlobalAccess(), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleGetParityStatus)
		}),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/system/browser-benchmarks", "handleGetBrowserBenchmarkEvidence", viewerGlobalAccess(), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleGetBrowserBenchmarkEvidence)
		}),
	},
}

var systemAdministrationAPIRouteCatalog = apiRouteCatalog{
	Domain: "system-administration",
	Routes: []apiRouteSpec{
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/system/deployment-lease", "handleGetDeploymentLease", systemAdminAccess(), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleGetDeploymentLease)
		}),
		newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/system/deployment-lease/acquire", "handleAcquireDeploymentLease", accessRequirement{
			GlobalRoles:         []string{RoleSystemAdmin},
			ServiceAccountScope: "deployments:manage",
			MaxBodyBytes:        deploymentLeaseMaxBody,
		}, func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleAcquireDeploymentLease)
		}),
		newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/system/deployment-lease/renew", "handleRenewDeploymentLease", accessRequirement{
			GlobalRoles:         []string{RoleSystemAdmin},
			ServiceAccountScope: "deployments:manage",
			MaxBodyBytes:        deploymentLeaseMaxBody,
		}, func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleRenewDeploymentLease)
		}),
		newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/system/deployment-lease/release", "handleReleaseDeploymentLease", accessRequirement{
			GlobalRoles:         []string{RoleSystemAdmin},
			ServiceAccountScope: "deployments:manage",
			MaxBodyBytes:        deploymentLeaseMaxBody,
		}, func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleReleaseDeploymentLease)
		}),
		newAuthenticatedAPIRoute(http.MethodPatch, "/api/v1/system/generators/drain", "handleSetGeneratorDrainState", systemAdminAccess(), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleSetGeneratorDrainState)
		}),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/system/restarts/capabilities", "handleGetSystemRestartCapabilities", systemAdminAccess(), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleGetSystemRestartCapabilities)
		}),
		newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/system/restarts", "handleSystemRestart", systemAdminAccess(), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleSystemRestart)
		}),
	},
}
