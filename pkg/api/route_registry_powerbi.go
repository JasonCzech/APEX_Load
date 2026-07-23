package api

import "net/http"

// powerBIAPIRouteCatalog exposes the manual "publish run results to Power BI"
// flow. Publishing mutates external state from project data, so it requires
// editor rights (precedent: the GitHub check-run publish); the connection test
// and destructive dataset recreate touch tenant-wide credentials and are
// system-admin only.
var powerBIAPIRouteCatalog = apiRouteCatalog{
	Domain: "powerbi",
	Routes: []apiRouteSpec{
		newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/tests/{id}/publish/powerbi", "handlePublishRunToPowerBI", editorProjectAccess(resolveTestProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handlePublishRunToPowerBI)
		}),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/tests/{id}/publish/powerbi", "handleGetPowerBIPublishStatus", viewerProjectAccess(resolveTestProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleGetPowerBIPublishStatus)
		}),
		newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/integrations/powerbi/test-connection", "handleTestPowerBIConnection", systemAdminAccess(), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleTestPowerBIConnection)
		}),
		newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/integrations/powerbi/dataset/recreate", "handleRecreatePowerBIDataset", systemAdminAccess(), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleRecreatePowerBIDataset)
		}),
	},
}
