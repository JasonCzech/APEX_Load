package api

import "net/http"

// sharedAssetsAPIRouteCatalog is the transport and access contract for what
// Shared assets reads about a project's assets as a whole.
var sharedAssetsAPIRouteCatalog = apiRouteCatalog{Domain: "shared-assets", Routes: []apiRouteSpec{
	newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/projects/{id}/shared-asset-usage", "handleSharedAssetUsage", withServiceAccountScope(viewerProjectAccess(resolveProjectIDFromPath), "read"), func(s *Server) http.Handler {
		return http.HandlerFunc(s.handleSharedAssetUsage)
	}),
}}
