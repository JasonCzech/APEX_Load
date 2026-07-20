package api

import (
	"net/http"
)

// apiRouteMiddlewareProfile names the common middleware stacks used by the
// controller routing layer. Keeping the profile on the route specification makes
// authentication posture reviewable alongside method, path, handler, and access.
type apiRouteMiddlewareProfile uint8

const (
	apiRouteMiddlewarePublic apiRouteMiddlewareProfile = iota
	apiRouteMiddlewareAuthenticated
)

type apiRouteHandlerBinder func(*Server) http.Handler

// apiRouteSpec is the canonical description for routes migrated to the modular
// registry. The legacy registrations in server.go can be moved one domain at a
// time without changing their HTTP contracts.
type apiRouteSpec struct {
	Method            string
	Path              string
	HandlerIdentity   string
	Handler           apiRouteHandlerBinder
	Access            *accessRequirement
	MiddlewareProfile apiRouteMiddlewareProfile
	// DocumentDerivedServiceAccountScope mirrors whether the pre-migration
	// OpenAPI access text included the route's derived service-account scope. All
	// runtime catalog rules still receive an explicit scope gate.
	DocumentDerivedServiceAccountScope bool
}

type apiRouteCatalog struct {
	Domain string
	Routes []apiRouteSpec
}

type apiRouteMiddlewareStack struct {
	cors func(http.Handler) http.Handler
	log  func(http.Handler) http.Handler
	auth func(http.Handler) http.Handler
}

func newPublicAPIRoute(method, path, handlerIdentity string, handler apiRouteHandlerBinder) apiRouteSpec {
	return apiRouteSpec{
		Method:            method,
		Path:              path,
		HandlerIdentity:   handlerIdentity,
		Handler:           handler,
		MiddlewareProfile: apiRouteMiddlewarePublic,
	}
}

func newAuthenticatedAPIRoute(method, path, handlerIdentity string, requirement accessRequirement, handler apiRouteHandlerBinder) apiRouteSpec {
	return apiRouteSpec{
		Method:                             method,
		Path:                               path,
		HandlerIdentity:                    handlerIdentity,
		Handler:                            handler,
		Access:                             &requirement,
		MiddlewareProfile:                  apiRouteMiddlewareAuthenticated,
		DocumentDerivedServiceAccountScope: true,
	}
}

func preserveRawAccessRequirement(route apiRouteSpec) apiRouteSpec {
	route.DocumentDerivedServiceAccountScope = false
	return route
}

func preserveRawAccessRequirementsForMethod(catalog apiRouteCatalog, method string) apiRouteCatalog {
	for i := range catalog.Routes {
		if catalog.Routes[i].Method == method {
			catalog.Routes[i] = preserveRawAccessRequirement(catalog.Routes[i])
		}
	}
	return catalog
}

func (s *Server) registerAPIRouteCatalog(catalog apiRouteCatalog, stack apiRouteMiddlewareStack) {
	for _, route := range catalog.Routes {
		handler := route.Handler(s)
		switch route.MiddlewareProfile {
		case apiRouteMiddlewarePublic:
			handler = stack.cors(stack.log(handler))
		case apiRouteMiddlewareAuthenticated:
			handler = stack.cors(stack.log(stack.auth(handler)))
		default:
			panic("api: unsupported route middleware profile")
		}
		s.mux.Handle(route.Method+" "+route.Path, handler)
	}
}

func authRulesForAPIRouteCatalogs(catalogs ...apiRouteCatalog) []apiRouteAuthRule {
	var rules []apiRouteAuthRule
	for _, catalog := range catalogs {
		for _, route := range catalog.Routes {
			if route.Access == nil {
				continue
			}
			rules = append(rules, routeAuth(route.Method, route.Path, *route.Access))
		}
	}
	return rules
}

func documentedAccessRequirementFromAPIRouteCatalogs(method, path string) (accessRequirement, bool) {
	for _, catalog := range modularAPIRouteCatalogs {
		for _, route := range catalog.Routes {
			if route.Method != method || route.Path != path || route.Access == nil {
				continue
			}
			requirement := *route.Access
			if route.DocumentDerivedServiceAccountScope {
				requirement = withRouteServiceAccountScope(method, path, requirement)
			}
			return requirement, true
		}
	}
	return accessRequirement{}, false
}
