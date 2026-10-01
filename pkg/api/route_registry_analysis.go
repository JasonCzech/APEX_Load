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
}}

func analysisQueryAccess(lookup projectScopedLookup) accessRequirement {
	a := viewerProjectAccess(lookup)
	a.ServiceAccountScope = "results:read"
	a.MaxBodyBytes = 16 << 10
	return a
}
