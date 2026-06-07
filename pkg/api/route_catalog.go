package api

import "net/http"

type apiRouteAuthRule struct {
	Method      string
	Path        string
	Requirement accessRequirement
}

var routeCatalogAuthRules = []apiRouteAuthRule{
	routeAuth(http.MethodGet, "/api/v1/system/settings", accessRequirement{GlobalRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}}),
	routeAuth(http.MethodPut, "/api/v1/system/settings", accessRequirement{GlobalRoles: []string{RoleSystemAdmin}}),
	routeAuth(http.MethodGet, "/api/v1/auth/service-accounts", accessRequirement{GlobalRoles: []string{RoleSystemAdmin}}),
	routeAuth(http.MethodPost, "/api/v1/auth/service-accounts", accessRequirement{GlobalRoles: []string{RoleSystemAdmin}}),
	routeAuth(http.MethodGet, "/api/v1/auth/users", accessRequirement{AuthenticatedOnly: true}),
	routeAuth(http.MethodPost, "/api/v1/auth/users", accessRequirement{AuthenticatedOnly: true}),
	routeAuth(http.MethodGet, "/api/v1/auth/user-activity", accessRequirement{AuthenticatedOnly: true}),
	routeAuth(http.MethodGet, "/api/v1/auth/user-activity/export", accessRequirement{AuthenticatedOnly: true}),
	routeAuth(http.MethodGet, "/api/v1/auth/audit-events", accessRequirement{GlobalRoles: []string{RoleSystemAdmin}}),
	routeAuth(http.MethodGet, "/api/v1/auth/audit-events/export", accessRequirement{GlobalRoles: []string{RoleSystemAdmin}}),
	routeAuth(http.MethodGet, "/api/v1/schedules/health", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/run-calendar", viewerGlobalAccess()),
	routeAuth(http.MethodPost, "/api/v1/tests/preflight", accessRequirement{GlobalRoles: []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin}, ProjectRoles: []string{RoleEditor, RoleProjectAdmin}, ProjectLookup: resolveTestCreateProjectID}),
	routeAuth(http.MethodPost, "/api/v1/scenarios/validate-load-model", accessRequirement{GlobalRoles: []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin}, ProjectRoles: []string{RoleEditor, RoleProjectAdmin}, ProjectLookup: resolveScenarioCreateProjectID}),
	routeAuth(http.MethodPost, "/api/v1/scenarios/size-from-goal", accessRequirement{GlobalRoles: []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin}, ProjectRoles: []string{RoleEditor, RoleProjectAdmin}, ProjectLookup: resolveGoalSizingBaselineProjectID}),
	routeAuth(http.MethodGet, "/api/v1/scenarios/load-models/support-matrix", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/scenarios/load-models/templates", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/system/inventory", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/system/services", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/system/usage", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/system/usage/export", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/system/usage/snapshots", viewerGlobalAccess()),
	routeAuth(http.MethodPost, "/api/v1/system/usage/snapshots", accessRequirement{GlobalRoles: []string{RoleSystemAdmin}}),
	routeAuth(http.MethodGet, "/api/v1/system/parity-status", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/system/browser-benchmarks", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/desktop/status", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/runtime-settings/support-matrix", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/protocol-bundles/support-matrix", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/protocols/native-support-matrix", viewerGlobalAccess()),
	routeAuth(http.MethodPost, "/api/v1/scripts/validate", accessRequirement{GlobalRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}, ProjectRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin}, ProjectLookup: resolveScriptCreateProjectID}),
	routeAuth(http.MethodPost, "/api/v1/scripts/replay/sessions", accessRequirement{GlobalRoles: []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin}, ProjectRoles: []string{RoleEditor, RoleProjectAdmin}, ProjectLookup: resolveDraftScriptProjectID}),
	routeAuth(http.MethodPost, "/api/v1/scripts/browser-to-http/preview", accessRequirement{GlobalRoles: []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin}, ProjectRoles: []string{RoleEditor, RoleProjectAdmin}, ProjectLookup: resolveBrowserToHTTPPreviewProjectID}),
	routeAuth(http.MethodPost, "/api/v1/scripts/authoring/sessions", accessRequirement{GlobalRoles: []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin}, ProjectRoles: []string{RoleEditor, RoleProjectAdmin}, ProjectLookup: resolveAuthoringSessionRequestProjectID}),
	routeAuth(http.MethodGet, "/api/v1/scripts/authoring/sessions", accessRequirement{GlobalRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}, ProjectRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin}, ProjectLookup: resolveProjectIDFromQuery}),
	routeAuth(http.MethodGet, "/api/v1/correlations/rules", viewerGlobalAccess()),
	routeAuth(http.MethodPost, "/api/v1/correlations/diagnose", accessRequirement{AuthenticatedOnly: true}),
	routeAuth(http.MethodPost, "/api/v1/scripts/import/asyncapi", accessRequirement{GlobalRoles: []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin}, ProjectRoles: []string{RoleEditor, RoleProjectAdmin}, ProjectLookup: resolveImportProjectIDFromJSONOrForm}),
	routeAuth(http.MethodGet, "/api/v1/scripts/import/asyncapi/support-matrix", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/fleet/alerts", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/fleet/capacity", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/fleet/load-zones", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/fleet/provisioner", viewerGlobalAccess()),
	routeAuth(http.MethodPatch, "/api/v1/system/generators/drain", accessRequirement{GlobalRoles: []string{RoleSystemAdmin}}),
	routeAuth(http.MethodGet, "/api/v1/integrations/apm/profiles", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/integrations/github/config", accessRequirement{AuthenticatedOnly: true}),
	routeAuth(http.MethodGet, "/api/v1/integrations/github/login", accessRequirement{AuthenticatedOnly: true}),
	routeAuth(http.MethodGet, "/api/v1/integrations/github/callback", accessRequirement{AuthenticatedOnly: true}),
	routeAuth(http.MethodGet, "/api/v1/integrations/github/connection", accessRequirement{AuthenticatedOnly: true}),
	routeAuth(http.MethodDelete, "/api/v1/integrations/github/connection", accessRequirement{AuthenticatedOnly: true}),
	routeAuth(http.MethodGet, "/api/v1/monitoring/packs", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/reports/profiles", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/results/imports", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/library/capabilities", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/ai/authoring/contract", viewerGlobalAccess()),
	routeAuth(http.MethodPost, "/api/v1/archives/retention/plan", accessRequirement{GlobalRoles: []string{RoleSystemAdmin}}),
	routeAuth(http.MethodPost, "/api/v1/archives/retention/apply", accessRequirement{GlobalRoles: []string{RoleSystemAdmin}}),
}

func routeAuth(method string, path string, requirement accessRequirement) apiRouteAuthRule {
	return apiRouteAuthRule{Method: method, Path: path, Requirement: requirement}
}

func viewerGlobalAccess() accessRequirement {
	return accessRequirement{GlobalRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}}
}

func accessRequirementFromRouteCatalog(method string, path string) (accessRequirement, bool) {
	for _, rule := range routeCatalogAuthRules {
		if rule.Method == method && rule.Path == path {
			return rule.Requirement, true
		}
	}
	return accessRequirement{}, false
}
