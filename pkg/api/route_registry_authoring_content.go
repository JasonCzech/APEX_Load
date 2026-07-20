package api

import "net/http"

// objectRepositoryAPIRouteCatalog is the canonical transport and access
// contract for project-scoped object repositories and script seeding/binding.
var objectRepositoryAPIRouteCatalog = apiRouteCatalog{
	Domain: "object-repositories",
	Routes: []apiRouteSpec{
		newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/object-repositories", "handleCreateObjectRepository", editorImportAccess(resolveObjectRepositoryCreateProjectID, 262144), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleCreateObjectRepository)
		}),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/object-repositories", "handleListObjectRepositories", viewerProjectAccess(resolveProjectIDFromQuery), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleListObjectRepositories)
		}),
		newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/object-repositories/from-script", "handleObjectRepositoryFromScript", editorProjectAccess(resolveObjectRepositoryFromScriptProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleObjectRepositoryFromScript)
		}),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/object-repositories/{id}", "handleGetObjectRepository", viewerProjectAccess(resolveObjectRepositoryProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleGetObjectRepository)
		}),
		newAuthenticatedAPIRoute(http.MethodPut, "/api/v1/object-repositories/{id}", "handleUpdateObjectRepository", editorImportAccess(resolveObjectRepositoryProjectID, 262144), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleUpdateObjectRepository)
		}),
		newAuthenticatedAPIRoute(http.MethodDelete, "/api/v1/object-repositories/{id}", "handleDeleteObjectRepository", editorProjectAccess(resolveObjectRepositoryProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleDeleteObjectRepository)
		}),
	},
}

// correlationRulesetAPIRouteCatalog is the canonical transport and access
// contract for learned correlation rules and their script discovery/application
// workflows.
var correlationRulesetAPIRouteCatalog = apiRouteCatalog{
	Domain: "correlation-rulesets",
	Routes: []apiRouteSpec{
		newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/correlation-rulesets", "handleCreateCorrelationRuleset", editorImportAccess(resolveCorrelationRulesetCreateProjectID, 262144), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleCreateCorrelationRuleset)
		}),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/correlation-rulesets", "handleListCorrelationRulesets", viewerProjectAccess(resolveProjectIDFromQuery), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleListCorrelationRulesets)
		}),
		newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/correlation-rulesets/discover-from-script", "handleDiscoverCorrelationRulesetFromScript", editorProjectAccess(resolveCorrelationRulesetFromScriptProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleDiscoverCorrelationRulesetFromScript)
		}),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/correlation-rulesets/{id}", "handleGetCorrelationRuleset", viewerProjectAccess(resolveCorrelationRulesetProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleGetCorrelationRuleset)
		}),
		newAuthenticatedAPIRoute(http.MethodPut, "/api/v1/correlation-rulesets/{id}", "handleUpdateCorrelationRuleset", editorImportAccess(resolveCorrelationRulesetProjectID, 262144), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleUpdateCorrelationRuleset)
		}),
		newAuthenticatedAPIRoute(http.MethodDelete, "/api/v1/correlation-rulesets/{id}", "handleDeleteCorrelationRuleset", editorProjectAccess(resolveCorrelationRulesetProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleDeleteCorrelationRuleset)
		}),
		newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/correlation-rulesets/{id}/apply-to-script", "handleApplyCorrelationRulesetToScript", editorProjectAccess(resolveCorrelationRulesetProjectID), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleApplyCorrelationRulesetToScript)
		}),
	},
}
