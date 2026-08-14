package api

import "net/http"

var generatorGatewayAPIRouteCatalog = apiRouteCatalog{
	Domain: "generator-gateway",
	Routes: []apiRouteSpec{
		newPublicAPIRoute(http.MethodGet, "/api/v1/generator-gateway/connect", "handleGeneratorGatewayConnect", func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleGeneratorGatewayConnect)
		}),
	},
}
