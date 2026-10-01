package api

import "net/http"

var runArchiveAPIRouteCatalog = preserveRawAccessRequirementsForMethod(apiRouteCatalog{
	Domain: "run-archives",
	Routes: []apiRouteSpec{
		newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/tests/{id}/native-series", "handleNativeEvidenceSeries", analysisQueryAccess(resolveTestProjectID), func(s *Server) http.Handler { return http.HandlerFunc(s.handleNativeEvidenceSeries) }),
		newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/tests/{id}/archive/native-series", "handleNativeArchiveSeries", analysisQueryAccess(resolveTestProjectID), func(s *Server) http.Handler { return http.HandlerFunc(s.handleNativeArchiveSeries) }),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/tests/{id}/archive", "handleDownloadRunArchive", viewerProjectAccess(resolveTestProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleDownloadRunArchive)
		}),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/tests/{id}/archive/manifest", "handleGetRunArchiveManifest", viewerProjectAccess(resolveTestProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleGetRunArchiveManifest)
		}),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/tests/{id}/archive/verify", "handleVerifyRunArchive", viewerProjectAccess(resolveTestProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleVerifyRunArchive)
		}),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/tests/{id}/archive/files/{name}", "handleDownloadRunArchiveArtifact", viewerProjectAccess(resolveTestProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleDownloadRunArchiveArtifact)
		}),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/tests/{id}/archive/browser-diagnostics", "handleDownloadRunArchiveBrowserDiagnostics", viewerProjectAccess(resolveTestProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleDownloadRunArchiveBrowserDiagnostics)
		}),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/tests/{id}/archive/transactions", "handleGetRunArchiveTransactions", viewerProjectAccess(resolveTestProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleGetRunArchiveTransactions)
		}),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/tests/{id}/archive/resources", "handleGetRunArchiveResources", viewerProjectAccess(resolveTestProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleGetRunArchiveResources)
		}),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/tests/{id}/archive/summaries", "handleGetRunArchiveSummaries", viewerProjectAccess(resolveTestProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleGetRunArchiveSummaries)
		}),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/tests/{id}/archive/report", "handleGetRunArchiveReport", viewerProjectAccess(resolveTestProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleGetRunArchiveReport)
		}),
		newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/tests/archive/reports", "handleGetRunArchiveReportsBatch", accessRequirement{
			GlobalRoles:   []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
			ProjectRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
			ProjectLookup: resolveProjectIDFromQuery,
			MaxBodyBytes:  65536,
		}, func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleGetRunArchiveReportsBatch)
		}),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/tests/{id}/archive/analysis", "handleGetRunArchiveAnalysis", viewerProjectAccess(resolveTestProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleGetRunArchiveAnalysis)
		}),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/tests/{id}/archive/root-cause", "handleGetRunArchiveRootCause", viewerProjectAccess(resolveTestProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleGetRunArchiveRootCause)
		}),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/tests/{id}/archive/ai-root-cause", "handleGetRunArchiveAIRootCause", viewerProjectAccess(resolveTestProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleGetRunArchiveAIRootCause)
		}),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/tests/{id}/archive/comparison", "handleGetRunArchiveComparison", viewerProjectAccess(resolveTestProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleGetRunArchiveComparison)
		}),
	},
}, http.MethodGet)

var archiveRetentionAPIRouteCatalog = apiRouteCatalog{
	Domain: "archive-retention",
	Routes: []apiRouteSpec{
		newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/archives/retention/plan", "handlePlanArchiveRetention", systemAdminAccess(), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handlePlanArchiveRetention)
		}),
		newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/archives/retention/apply", "handleApplyArchiveRetention", systemAdminAccess(), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleApplyArchiveRetention)
		}),
	},
}
