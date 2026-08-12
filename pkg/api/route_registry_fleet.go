package api

import "net/http"

var fleetObservabilityAPIRouteCatalog = apiRouteCatalog{
	Domain: "fleet-observability",
	Routes: []apiRouteSpec{
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/fleet/alerts", "handleGetFleetAlerts", viewerGlobalAccess(), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleGetFleetAlerts)
		}),
		newAuthenticatedAPIRoute(http.MethodPatch, "/api/v1/fleet/alerts/{id}/ack", "handleAckFleetAlert", accessRequirement{GlobalRoles: []string{RoleSystemAdmin}, MaxBodyBytes: 65536}, func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleAckFleetAlert)
		}),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/fleet/capacity", "handleGetFleetCapacity", unrestrictedViewerGlobalAccess(), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleGetFleetCapacity)
		}),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/fleet/load-zones", "handleGetFleetLoadZones", unrestrictedViewerGlobalAccess(), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleGetFleetLoadZones)
		}),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/fleet/provisioner", "handleGetFleetProvisioner", viewerGlobalAccess(), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleGetFleetProvisioner)
		}),
	},
}

var fleetAdministrationAPIRouteCatalog = apiRouteCatalog{
	Domain: "fleet-administration",
	Routes: []apiRouteSpec{
		newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/fleet/pools", "handleCreateFleetPool", accessRequirement{GlobalRoles: []string{RoleSystemAdmin}, MaxBodyBytes: 1048576}, func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleCreateFleetPool)
		}),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/fleet/pools", "handleListFleetPools", systemAdminAccess(), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleListFleetPools)
		}),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/fleet/pools/{id}", "handleGetFleetPool", systemAdminAccess(), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleGetFleetPool)
		}),
		newAuthenticatedAPIRoute(http.MethodPut, "/api/v1/fleet/pools/{id}", "handleUpdateFleetPool", accessRequirement{GlobalRoles: []string{RoleSystemAdmin}, MaxBodyBytes: 1048576}, func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleUpdateFleetPool)
		}),
		newAuthenticatedAPIRoute(http.MethodDelete, "/api/v1/fleet/pools/{id}", "handleDeleteFleetPool", systemAdminAccess(), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleDeleteFleetPool)
		}),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/fleet/generators", "handleListGeneratorMetadata", systemAdminAccess(), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleListGeneratorMetadata)
		}),
		newAuthenticatedAPIRoute(http.MethodPatch, "/api/v1/fleet/generators/{id}", "handlePatchGeneratorMetadata", accessRequirement{GlobalRoles: []string{RoleSystemAdmin}, MaxBodyBytes: 262144}, func(s *Server) http.Handler {
			return http.HandlerFunc(s.handlePatchGeneratorMetadata)
		}),
		newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/fleet/generators/{id}/cordon", "handleCordonGenerator", accessRequirement{GlobalRoles: []string{RoleSystemAdmin}, MaxBodyBytes: 65536}, func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleCordonGenerator)
		}),
		newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/fleet/generators/{id}/uncordon", "handleUncordonGenerator", accessRequirement{GlobalRoles: []string{RoleSystemAdmin}, MaxBodyBytes: 65536}, func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleUncordonGenerator)
		}),
		newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/fleet/generators/{id}/drain", "handleDrainGenerator", accessRequirement{GlobalRoles: []string{RoleSystemAdmin}, MaxBodyBytes: 65536}, func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleDrainGenerator)
		}),
		newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/fleet/generators/{id}/evacuate", "handleEvacuateGenerator", accessRequirement{GlobalRoles: []string{RoleSystemAdmin}, MaxBodyBytes: 65536}, func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleEvacuateGenerator)
		}),
		newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/fleet/provision-requests", "handleCreateProvisioningRequest", accessRequirement{GlobalRoles: []string{RoleSystemAdmin}, MaxBodyBytes: 262144}, func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleCreateProvisioningRequest)
		}),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/fleet/provision-requests", "handleListProvisioningRequests", systemAdminAccess(), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleListProvisioningRequests)
		}),
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/fleet/provision-requests/{id}", "handleGetProvisioningRequest", systemAdminAccess(), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleGetProvisioningRequest)
		}),
		newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/fleet/provision-requests/{id}/cancel", "handleCancelProvisioningRequest", systemAdminAccess(), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleCancelProvisioningRequest)
		}),
		newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/fleet/provision-requests/{id}/retry", "handleRetryProvisioningRequest", systemAdminAccess(), func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleRetryProvisioningRequest)
		}),
	},
}
