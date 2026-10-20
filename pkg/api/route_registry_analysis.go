package api

import "net/http"

var analysisAPIRouteCatalog = apiRouteCatalog{Domain: "analysis", Routes: []apiRouteSpec{
	newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/analysis/capabilities", "handleAnalysisCapabilities", viewerProjectAccess(resolveAnalysisProject), func(s *Server) http.Handler { return http.HandlerFunc(s.handleAnalysisCapabilities) }),
	newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/analysis/imports", "handleAnalysisImports", viewerProjectAccess(resolveAnalysisProject), func(s *Server) http.Handler { return http.HandlerFunc(s.handleAnalysisImports) }),
	newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/analysis/imports", "handleAnalysisImports", editorProjectAccess(resolveAnalysisProject), func(s *Server) http.Handler { return http.HandlerFunc(s.handleAnalysisImports) }),
	newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/analysis/imports/{id}", "handleAnalysisImport", viewerProjectAccess(resolveAnalysisImportProject), func(s *Server) http.Handler { return http.HandlerFunc(s.handleAnalysisImport) }),
	newAuthenticatedAPIRoute(http.MethodDelete, "/api/v1/analysis/imports/{id}", "handleAnalysisImport", editorProjectAccess(resolveAnalysisImportProject), func(s *Server) http.Handler { return http.HandlerFunc(s.handleAnalysisImport) }),
	newAuthenticatedAPIRoute(http.MethodPut, "/api/v1/analysis/imports/{id}/files/{file_id}", "handleAnalysisUpload", editorImportAccess(resolveAnalysisImportProject, analysisUploadChunk), func(s *Server) http.Handler { return http.HandlerFunc(s.handleAnalysisUpload) }),
	newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/analysis/imports/{id}/files/{file_id}/preview", "handleAnalysisPreview", viewerProjectAccess(resolveAnalysisImportProject), func(s *Server) http.Handler { return http.HandlerFunc(s.handleAnalysisPreview) }),
	newAuthenticatedAPIRoute(http.MethodPut, "/api/v1/analysis/imports/{id}/mapping", "handleAnalysisMapping", editorProjectAccess(resolveAnalysisImportProject), func(s *Server) http.Handler { return http.HandlerFunc(s.handleAnalysisMapping) }),
	newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/analysis/imports/{id}/commit", "handleAnalysisCommit", editorProjectAccess(resolveAnalysisImportProject), func(s *Server) http.Handler { return http.HandlerFunc(s.handleAnalysisCommit) }),
	newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/analysis/datasets/{id}", "handleAnalysisImport", viewerProjectAccess(resolveAnalysisImportProject), func(s *Server) http.Handler { return http.HandlerFunc(s.handleAnalysisImport) }),
	newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/analysis/datasets/{id}/rejections/{index}", "handleAnalysisRejections", viewerProjectAccess(resolveAnalysisImportProject), func(s *Server) http.Handler { return http.HandlerFunc(s.handleAnalysisRejections) }),
	newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/analysis/datasets/{id}/series", "handleAnalysisDatasetSeries", analysisQueryAccess(resolveAnalysisImportProject), func(s *Server) http.Handler { return http.HandlerFunc(s.handleAnalysisDatasetSeries) }),
	newAuthenticatedAPIRoute(http.MethodPut, "/api/v1/analysis/datasets/{id}/association", "handleAnalysisAssociation", editorProjectAccess(resolveAnalysisImportProject), func(s *Server) http.Handler { return http.HandlerFunc(s.handleAnalysisAssociation) }),
	newAuthenticatedAPIRoute(http.MethodPatch, "/api/v1/analysis/datasets/{id}/association", "handleAnalysisMappingAssociation", editorProjectAccess(resolveAnalysisImportProject), func(s *Server) http.Handler { return http.HandlerFunc(s.handleAnalysisMappingAssociation) }),
	newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/analysis/compare", "handleAnalysisComparison", analysisQueryAccess(resolveAnalysisProject), func(s *Server) http.Handler { return http.HandlerFunc(s.handleAnalysisComparison) }),
	newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/analysis/mappings", "handleAnalysisDefinitions", viewerProjectAccess(resolveAnalysisProject), func(s *Server) http.Handler { return http.HandlerFunc(s.handleAnalysisDefinitions) }),
	newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/analysis/mappings", "handleAnalysisDefinitions", editorProjectAccess(resolveAnalysisProject), func(s *Server) http.Handler { return http.HandlerFunc(s.handleAnalysisDefinitions) }),
	newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/analysis/mappings/{id}", "handleAnalysisDefinitions", viewerProjectAccess(resolveAnalysisProject), func(s *Server) http.Handler { return http.HandlerFunc(s.handleAnalysisDefinitions) }),
	newAuthenticatedAPIRoute(http.MethodPut, "/api/v1/analysis/mappings/{id}", "handleAnalysisDefinitions", editorProjectAccess(resolveAnalysisProject), func(s *Server) http.Handler { return http.HandlerFunc(s.handleAnalysisDefinitions) }),
	newAuthenticatedAPIRoute(http.MethodDelete, "/api/v1/analysis/mappings/{id}", "handleAnalysisDefinitions", editorProjectAccess(resolveAnalysisProject), func(s *Server) http.Handler { return http.HandlerFunc(s.handleAnalysisDefinitions) }),
	newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/analysis/comparisons", "handleAnalysisDefinitions", viewerProjectAccess(resolveAnalysisProject), func(s *Server) http.Handler { return http.HandlerFunc(s.handleAnalysisDefinitions) }),
	newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/analysis/comparisons", "handleAnalysisDefinitions", editorProjectAccess(resolveAnalysisProject), func(s *Server) http.Handler { return http.HandlerFunc(s.handleAnalysisDefinitions) }),
	newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/analysis/comparisons/{id}", "handleAnalysisDefinitions", viewerProjectAccess(resolveAnalysisProject), func(s *Server) http.Handler { return http.HandlerFunc(s.handleAnalysisDefinitions) }),
	newAuthenticatedAPIRoute(http.MethodPut, "/api/v1/analysis/comparisons/{id}", "handleAnalysisDefinitions", editorProjectAccess(resolveAnalysisProject), func(s *Server) http.Handler { return http.HandlerFunc(s.handleAnalysisDefinitions) }),
	newAuthenticatedAPIRoute(http.MethodDelete, "/api/v1/analysis/comparisons/{id}", "handleAnalysisDefinitions", editorProjectAccess(resolveAnalysisProject), func(s *Server) http.Handler { return http.HandlerFunc(s.handleAnalysisDefinitions) }),
	// Root-cause graph mapping decisions (rca_mapping definitions).
	newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/analysis/rca-mappings", "handleAnalysisDefinitions", viewerProjectAccess(resolveAnalysisProject), func(s *Server) http.Handler { return http.HandlerFunc(s.handleAnalysisDefinitions) }),
	newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/analysis/rca-mappings", "handleAnalysisDefinitions", editorProjectAccess(resolveAnalysisProject), func(s *Server) http.Handler { return http.HandlerFunc(s.handleAnalysisDefinitions) }),
	newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/analysis/rca-mappings/{id}", "handleAnalysisDefinitions", viewerProjectAccess(resolveAnalysisProject), func(s *Server) http.Handler { return http.HandlerFunc(s.handleAnalysisDefinitions) }),
	newAuthenticatedAPIRoute(http.MethodPut, "/api/v1/analysis/rca-mappings/{id}", "handleAnalysisDefinitions", editorProjectAccess(resolveAnalysisProject), func(s *Server) http.Handler { return http.HandlerFunc(s.handleAnalysisDefinitions) }),
	newAuthenticatedAPIRoute(http.MethodDelete, "/api/v1/analysis/rca-mappings/{id}", "handleAnalysisDefinitions", editorProjectAccess(resolveAnalysisProject), func(s *Server) http.Handler { return http.HandlerFunc(s.handleAnalysisDefinitions) }),
	// Business-key mappings (business_key_mapping definitions): project admins
	// approve them.
	newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/analysis/business-key-mappings", "handleAnalysisDefinitions", viewerProjectAccess(resolveAnalysisProject), func(s *Server) http.Handler { return http.HandlerFunc(s.handleAnalysisDefinitions) }),
	newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/analysis/business-key-mappings", "handleAnalysisDefinitions", projectAdminAccess(resolveAnalysisProject), func(s *Server) http.Handler { return http.HandlerFunc(s.handleAnalysisDefinitions) }),
	newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/analysis/business-key-mappings/{id}", "handleAnalysisDefinitions", viewerProjectAccess(resolveAnalysisProject), func(s *Server) http.Handler { return http.HandlerFunc(s.handleAnalysisDefinitions) }),
	newAuthenticatedAPIRoute(http.MethodPut, "/api/v1/analysis/business-key-mappings/{id}", "handleAnalysisDefinitions", projectAdminAccess(resolveAnalysisProject), func(s *Server) http.Handler { return http.HandlerFunc(s.handleAnalysisDefinitions) }),
	newAuthenticatedAPIRoute(http.MethodDelete, "/api/v1/analysis/business-key-mappings/{id}", "handleAnalysisDefinitions", projectAdminAccess(resolveAnalysisProject), func(s *Server) http.Handler { return http.HandlerFunc(s.handleAnalysisDefinitions) }),
	// Bounded evaluation jobs. Viewers may queue and read evaluations and
	// cancel their own; cancelling or hiding another actor's needs an editor.
	newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/analysis/evaluations", "handleListEvaluationJobs", viewerProjectAccess(resolveAnalysisProject), func(s *Server) http.Handler { return http.HandlerFunc(s.handleListEvaluationJobs) }),
	newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/analysis/evaluations", "handleCreateEvaluationJob", viewerProjectAccess(resolveAnalysisProject), func(s *Server) http.Handler { return http.HandlerFunc(s.handleCreateEvaluationJob) }),
	newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/analysis/evaluations/{id}", "handleGetEvaluationJob", viewerProjectAccess(resolveAnalysisProject), func(s *Server) http.Handler { return http.HandlerFunc(s.handleGetEvaluationJob) }),
	newAuthenticatedAPIRoute(http.MethodDelete, "/api/v1/analysis/evaluations/{id}", "handleDeleteEvaluationJob", viewerProjectAccess(resolveAnalysisProject), func(s *Server) http.Handler { return http.HandlerFunc(s.handleDeleteEvaluationJob) }),
	// Revisioned project policies; a run pins the defaults in force at Start.
	newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/analysis/policies", "handleListAnalysisPolicies", viewerProjectAccess(resolveAnalysisProject), func(s *Server) http.Handler { return http.HandlerFunc(s.handleListAnalysisPolicies) }),
	newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/analysis/policies", "handleCreateAnalysisPolicy", editorProjectAccess(resolveAnalysisProject), func(s *Server) http.Handler { return http.HandlerFunc(s.handleCreateAnalysisPolicy) }),
	newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/analysis/policies/{id}", "handleGetAnalysisPolicy", viewerProjectAccess(resolveAnalysisProject), func(s *Server) http.Handler { return http.HandlerFunc(s.handleGetAnalysisPolicy) }),
	newAuthenticatedAPIRoute(http.MethodPut, "/api/v1/analysis/policies/{id}", "handleUpdateAnalysisPolicy", editorProjectAccess(resolveAnalysisProject), func(s *Server) http.Handler { return http.HandlerFunc(s.handleUpdateAnalysisPolicy) }),
	newAuthenticatedAPIRoute(http.MethodDelete, "/api/v1/analysis/policies/{id}", "handleDeleteAnalysisPolicy", editorProjectAccess(resolveAnalysisProject), func(s *Server) http.Handler { return http.HandlerFunc(s.handleDeleteAnalysisPolicy) }),
}}

func analysisQueryAccess(lookup projectScopedLookup) accessRequirement {
	a := viewerProjectAccess(lookup)
	a.ServiceAccountScope = "results:read"
	a.MaxBodyBytes = 16 << 10
	return a
}
