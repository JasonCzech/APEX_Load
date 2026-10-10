package api

import "net/http"

var runArchiveAPIRouteCatalog = preserveRawAccessRequirementsForMethod(apiRouteCatalog{
	Domain: "run-archives",
	Routes: []apiRouteSpec{
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/tests/{id}/analysis/annotations", "handleGetRunAnnotations", viewerProjectAccess(resolveTestProjectID), func(s *Server) http.Handler { return http.HandlerFunc(s.handleGetRunAnnotations) }),
		newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/tests/{id}/analysis/annotations", "handleCreateRunAnnotation", editorProjectAccessWithBodyLimit(resolveTestProjectID, 8<<10), func(s *Server) http.Handler { return http.HandlerFunc(s.handleCreateRunAnnotation) }),
		newAuthenticatedAPIRoute(http.MethodDelete, "/api/v1/tests/{id}/analysis/annotations/{annotation_id}", "handleDeleteRunAnnotation", editorProjectAccess(resolveTestProjectID), func(s *Server) http.Handler { return http.HandlerFunc(s.handleDeleteRunAnnotation) }),

		newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/tests/{id}/telemetry/client-render", "handleReportClientRender", viewerProjectAccessWithBodyLimit(resolveTestProjectID, clientRenderMaxBodyBytes), func(s *Server) http.Handler { return http.HandlerFunc(s.handleReportClientRender) }),
		newAuthenticatedAPIRoute(http.MethodPut, "/api/v1/projects/{id}/telemetry-policy", "handleUpdateProjectTelemetryPolicy", editorProjectAccessWithBodyLimit(resolveProjectIDFromPath, 4<<10), func(s *Server) http.Handler { return http.HandlerFunc(s.handleUpdateProjectTelemetryPolicy) }),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/tests/{id}/messaging-progress", "handleMessagingProgress", analysisQueryAccess(resolveTestProjectID), func(s *Server) http.Handler { return http.HandlerFunc(s.handleMessagingProgress) }),
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
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/tests/{id}/archive/browser/har", "handleListRunArchiveBrowserHARs", viewerProjectAccess(resolveTestProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleListRunArchiveBrowserHARs)
		}),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/tests/{id}/archive/browser/har/{artifact}", "handleDownloadRunArchiveBrowserHAR", editorProjectAccess(resolveTestProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleDownloadRunArchiveBrowserHAR)
		}),
		browserResourceDeadlineRoute(newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/tests/{id}/archive/browser/har-entry", "handleGetRunArchiveBrowserHAREntry", editorProjectAccess(resolveTestProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleGetRunArchiveBrowserHAREntry)
		})),
		browserResourceDeadlineRoute(newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/tests/{id}/archive/browser-resources", "handleGetRunArchiveBrowserResources", viewerProjectAccess(resolveBrowserResourceArchiveProjectID), func(s *Server) http.Handler { return http.HandlerFunc(s.handleGetRunArchiveBrowserResources) })),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/tests/{id}/archive/transactions", "handleGetRunArchiveTransactions", viewerProjectAccess(resolveTestProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleGetRunArchiveTransactions)
		}),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/tests/{id}/archive/browser-findings", "handleGetRunArchiveBrowserFindings", viewerProjectAccess(resolveTestProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleGetRunArchiveBrowserFindings)
		}),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/tests/{id}/archive/browser-interactions", "handleGetRunArchiveBrowserInteractions", viewerProjectAccess(resolveTestProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleGetRunArchiveBrowserInteractions)
		}),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/tests/{id}/archive/journeys", "handleGetRunArchiveJourneys", viewerProjectAccess(resolveTestProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleGetRunArchiveJourneys)
		}),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/tests/{id}/archive/resources", "handleGetRunArchiveResources", viewerProjectAccess(resolveTestProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleGetRunArchiveResources)
		}),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/tests/{id}/archive/exemplars", "handleGetRunArchiveExemplars", viewerProjectAccess(resolveTestProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleGetRunArchiveExemplars)
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
		newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/tests/{id}/archive/root-cause/analyses", "handleCreateRCAAnalysis", editorProjectAccessWithBodyLimit(resolveTestProjectID, 16<<10), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleCreateRCAAnalysis)
		}),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/tests/{id}/archive/root-cause/analyses/{analysis_id}", "handleGetRCAAnalysis", viewerProjectAccess(resolveTestProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleGetRCAAnalysis)
		}),
		newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/tests/{id}/archive/root-cause/analyses/{analysis_id}/cancel", "handleCancelRCAAnalysis", editorProjectAccess(resolveTestProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleCancelRCAAnalysis)
		}),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/tests/{id}/archive/root-cause/analyses/{analysis_id}/bundle", "handleGetRCABundle", viewerProjectAccess(resolveTestProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleGetRCABundle)
		}),
		// Narration checks are reads: they change nothing.
		newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/tests/{id}/archive/root-cause/analyses/{analysis_id}/narration-check", "handleCheckRCANarration", rcaNarrationAccess(viewerProjectAccess(resolveTestProjectID)), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleCheckRCANarration)
		}),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/tests/{id}/archive/root-cause/analyses/{analysis_id}/critiques", "handleListRCACritiques", viewerProjectAccess(resolveTestProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleListRCACritiques)
		}),
		newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/tests/{id}/archive/root-cause/analyses/{analysis_id}/critiques", "handleCreateRCACritique", rcaNarrationAccess(editorProjectAccess(resolveTestProjectID)), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleCreateRCACritique)
		}),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/tests/{id}/archive/root-cause/v2", "handleGetRCAAssessmentV2", viewerProjectAccess(resolveTestProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleGetRCAAssessmentV2)
		}),
		newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/tests/{id}/archive/business-keys/lookup", "handleBusinessKeyLookup", editorProjectAccessWithBodyLimit(resolveTestProjectID, 16<<10), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleBusinessKeyLookup)
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
