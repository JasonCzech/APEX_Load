package api

import "net/http"

var baselineComparisonAPIRouteCatalog = apiRouteCatalog{Domain: "baseline-comparison", Routes: []apiRouteSpec{
	newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/tests/{id}/baseline-candidates", "handleBaselineComparisonCandidates", viewerProjectAccess(resolveTestProjectID), func(s *Server) http.Handler { return http.HandlerFunc(s.handleBaselineComparisonCandidates) }),
	newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/tests/{id}/baseline-comparisons", "handleCreateBaselineComparison", baselineComparisonEvaluationAccess(), func(s *Server) http.Handler { return http.HandlerFunc(s.handleCreateBaselineComparison) }),
	newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/tests/{id}/baseline-comparisons", "handleListBaselineComparisons", viewerProjectAccess(resolveTestProjectID), func(s *Server) http.Handler { return http.HandlerFunc(s.handleListBaselineComparisons) }),
	newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/tests/{id}/baseline-comparisons/{comparison_id}", "handleGetBaselineComparison", viewerProjectAccess(resolveTestProjectID), func(s *Server) http.Handler { return http.HandlerFunc(s.handleGetBaselineComparison) }),
	newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/scenarios/{id}/comparison-policy", "handleGetBaselineComparisonPolicy", viewerProjectAccess(resolveScenarioProjectID), func(s *Server) http.Handler { return http.HandlerFunc(s.handleGetBaselineComparisonPolicy) }),
	newAuthenticatedAPIRoute(http.MethodPut, "/api/v1/scenarios/{id}/comparison-policy", "handlePutBaselineComparisonPolicy", baselineComparisonPolicyAccess(), func(s *Server) http.Handler { return http.HandlerFunc(s.handlePutBaselineComparisonPolicy) }),
}}

func baselineComparisonEvaluationAccess() accessRequirement {
	a := viewerProjectAccess(resolveTestProjectID)
	a.ServiceAccountScope = "results:read"
	a.MaxBodyBytes = 128 << 10
	return a
}
func baselineComparisonPolicyAccess() accessRequirement {
	a := editorProjectAccess(resolveScenarioProjectID)
	a.MaxBodyBytes = 128 << 10
	return a
}
