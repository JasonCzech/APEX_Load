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
		// Append-only qualification evidence; only system administrators record it.
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/system/acceptance-records", "handleListFeatureAcceptanceRecords", viewerGlobalAccess(), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleListFeatureAcceptanceRecords)
		}),
		newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/system/acceptance-records", "handleCreateFeatureAcceptanceRecord", systemAdminAccess(), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleCreateFeatureAcceptanceRecord)
		}),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/system/acceptance-status", "handleFeatureAcceptanceStatus", viewerGlobalAccess(), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleFeatureAcceptanceStatus)
		}),
	},
}

var systemAdministrationAPIRouteCatalog = apiRouteCatalog{
	Domain: "system-administration",
	Routes: []apiRouteSpec{
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/system/library/remotes/{id}/workspaces", "handleLibraryRemoteWorkspaces", accessRequirement{GlobalRoles: []string{RoleSystemAdmin}, ServiceAccountScope: "settings:manage"}, func(s *Server) http.Handler { return http.HandlerFunc(s.handleLibraryRemoteWorkspaces) }),
		newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/system/settings/connections/test", "handleConnectionTest", accessRequirement{GlobalRoles: []string{RoleSystemAdmin}, ServiceAccountScope: "settings:manage", MaxBodyBytes: 4096}, func(s *Server) http.Handler { return http.HandlerFunc(s.handleConnectionTest) }),
		newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/system/redaction/preview", "handleRedactionPreview", accessRequirement{AuthenticatedOnly: true, ServiceAccountScope: "read", MaxBodyBytes: 65536}, func(s *Server) http.Handler { return http.HandlerFunc(s.handleRedactionPreview) }),
		newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/system/settings/notifications/preview", "handleNotificationPreview", accessRequirement{GlobalRoles: []string{RoleSystemAdmin}, ServiceAccountScope: "settings:manage", MaxBodyBytes: 4096}, func(s *Server) http.Handler { return http.HandlerFunc(s.handleNotificationPreview) }),
		newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/system/settings/notifications/test", "handleNotificationTest", accessRequirement{GlobalRoles: []string{RoleSystemAdmin}, ServiceAccountScope: "settings:manage", MaxBodyBytes: 4096}, func(s *Server) http.Handler { return http.HandlerFunc(s.handleNotificationTest) }),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/system/settings/notifications/deliveries", "handleNotificationDeliveries", accessRequirement{GlobalRoles: []string{RoleSystemAdmin}, ServiceAccountScope: "settings:manage", MaxBodyBytes: 4096}, func(s *Server) http.Handler { return http.HandlerFunc(s.handleNotificationDeliveries) }),
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
