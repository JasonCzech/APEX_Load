package api

import (
	"net/http"
	"strings"

	"github.com/jasonczech/apexload/pkg/storage"
)

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
	routeAuth(http.MethodGet, "/api/v1/auth/users/{id}", accessRequirement{AuthenticatedOnly: true}),
	routeAuth(http.MethodPatch, "/api/v1/auth/users/{id}", accessRequirement{AuthenticatedOnly: true}),
	routeAuth(http.MethodPost, "/api/v1/auth/users/{id}/bindings", accessRequirement{AuthenticatedOnly: true}),
	routeAuth(http.MethodDelete, "/api/v1/auth/users/{id}/bindings/{binding_id}", accessRequirement{AuthenticatedOnly: true}),
	routeAuth(http.MethodPost, "/api/v1/auth/users/{id}/disable", accessRequirement{AuthenticatedOnly: true}),
	routeAuth(http.MethodPost, "/api/v1/auth/users/{id}/enable", accessRequirement{AuthenticatedOnly: true}),
	routeAuth(http.MethodGet, "/api/v1/auth/user-activity", accessRequirement{AuthenticatedOnly: true}),
	routeAuth(http.MethodGet, "/api/v1/auth/user-activity/export", accessRequirement{AuthenticatedOnly: true}),
	routeAuth(http.MethodGet, "/api/v1/auth/audit-events", accessRequirement{GlobalRoles: []string{RoleSystemAdmin}}),
	routeAuth(http.MethodGet, "/api/v1/auth/audit-events/export", accessRequirement{GlobalRoles: []string{RoleSystemAdmin}}),
	routeAuth(http.MethodGet, "/api/v1/schedules/health", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/run-calendar", viewerGlobalAccess()),

	// Execution plans (ordered cross-run orchestration).
	routeAuth(http.MethodPost, "/api/v1/execution-plans", editorImportAccess(resolveExecutionPlanCreateProjectID, 262144)),
	routeAuth(http.MethodGet, "/api/v1/execution-plans", viewerProjectAccess(resolveProjectIDFromQuery)),
	routeAuth(http.MethodGet, "/api/v1/execution-plans/{id}", viewerProjectAccess(resolveExecutionPlanProjectID)),
	routeAuth(http.MethodPut, "/api/v1/execution-plans/{id}", editorImportAccess(resolveExecutionPlanProjectID, 262144)),
	routeAuth(http.MethodDelete, "/api/v1/execution-plans/{id}", editorProjectAccess(resolveExecutionPlanProjectID)),
	routeAuth(http.MethodPost, "/api/v1/execution-plans/{id}/runs", editorProjectAccess(resolveExecutionPlanProjectID)),
	routeAuth(http.MethodGet, "/api/v1/execution-plans/{id}/runs", viewerProjectAccess(resolveExecutionPlanProjectID)),
	routeAuth(http.MethodGet, "/api/v1/execution-plans/{id}/runs/{runId}", viewerProjectAccess(resolveExecutionPlanProjectID)),
	routeAuth(http.MethodPost, "/api/v1/object-repositories", editorImportAccess(resolveObjectRepositoryCreateProjectID, 262144)),
	routeAuth(http.MethodGet, "/api/v1/object-repositories", viewerProjectAccess(resolveProjectIDFromQuery)),
	routeAuth(http.MethodPost, "/api/v1/object-repositories/from-script", editorProjectAccess(resolveObjectRepositoryFromScriptProjectID)),
	routeAuth(http.MethodGet, "/api/v1/object-repositories/{id}", viewerProjectAccess(resolveObjectRepositoryProjectID)),
	routeAuth(http.MethodPut, "/api/v1/object-repositories/{id}", editorImportAccess(resolveObjectRepositoryProjectID, 262144)),
	routeAuth(http.MethodDelete, "/api/v1/object-repositories/{id}", editorProjectAccess(resolveObjectRepositoryProjectID)),

	routeAuth(http.MethodPost, "/api/v1/correlation-rulesets", editorImportAccess(resolveCorrelationRulesetCreateProjectID, 262144)),
	routeAuth(http.MethodGet, "/api/v1/correlation-rulesets", viewerProjectAccess(resolveProjectIDFromQuery)),
	routeAuth(http.MethodPost, "/api/v1/correlation-rulesets/discover-from-script", editorProjectAccess(resolveCorrelationRulesetFromScriptProjectID)),
	routeAuth(http.MethodGet, "/api/v1/correlation-rulesets/{id}", viewerProjectAccess(resolveCorrelationRulesetProjectID)),
	routeAuth(http.MethodPut, "/api/v1/correlation-rulesets/{id}", editorImportAccess(resolveCorrelationRulesetProjectID, 262144)),
	routeAuth(http.MethodDelete, "/api/v1/correlation-rulesets/{id}", editorProjectAccess(resolveCorrelationRulesetProjectID)),
	routeAuth(http.MethodPost, "/api/v1/correlation-rulesets/{id}/apply-to-script", editorProjectAccess(resolveCorrelationRulesetProjectID)),

	// Monitoring collectors (restricted-network agent control plane) — project-admin infra.
	routeAuth(http.MethodPost, "/api/v1/monitoring/collectors", accessRequirement{GlobalRoles: []string{RoleProjectAdmin, RoleSystemAdmin}, ProjectRoles: []string{RoleProjectAdmin}, ProjectLookup: resolveMonitoringCollectorCreateProjectID, MaxBodyBytes: 65536}),
	routeAuth(http.MethodGet, "/api/v1/monitoring/collectors", viewerProjectAccess(resolveProjectIDFromQuery)),
	routeAuth(http.MethodGet, "/api/v1/monitoring/collectors/{id}", viewerProjectAccess(resolveMonitoringCollectorProjectID)),
	routeAuth(http.MethodPatch, "/api/v1/monitoring/collectors/{id}", accessRequirement{GlobalRoles: []string{RoleProjectAdmin, RoleSystemAdmin}, ProjectRoles: []string{RoleProjectAdmin}, ProjectLookup: resolveMonitoringCollectorProjectID, MaxBodyBytes: 65536}),
	routeAuth(http.MethodDelete, "/api/v1/monitoring/collectors/{id}", projectAdminAccess(resolveMonitoringCollectorProjectID)),
	routeAuth(http.MethodPost, "/api/v1/monitoring/collectors/{id}/rotate-token", projectAdminAccess(resolveMonitoringCollectorProjectID)),
	routeAuth(http.MethodGet, "/api/v1/monitoring/collectors/{id}/metrics", viewerProjectAccess(resolveMonitoringCollectorProjectID)),

	// Enterprise entitlements (read-only consumption vs limits).
	routeAuth(http.MethodGet, "/api/v1/system/entitlements", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/system/entitlements/usage", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/projects/{id}/entitlements/usage", viewerProjectAccess(resolveProjectIDFromPath)),

	// Capacity reservations (timeslots).
	routeAuth(http.MethodPost, "/api/v1/capacity/timeslots", editorImportAccess(resolveTimeslotCreateProjectID, 65536)),
	routeAuth(http.MethodGet, "/api/v1/capacity/timeslots", viewerProjectAccess(resolveProjectIDFromQuery)),
	routeAuth(http.MethodPost, "/api/v1/capacity/timeslots/availability", accessRequirement{
		GlobalRoles:   []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
		ProjectRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
		ProjectLookup: resolveTimeslotCreateProjectID,
		MaxBodyBytes:  65536,
	}),
	routeAuth(http.MethodGet, "/api/v1/capacity/timeslots/{id}", viewerProjectAccess(resolveTimeslotProjectID)),
	routeAuth(http.MethodPut, "/api/v1/capacity/timeslots/{id}", editorImportAccess(resolveTimeslotProjectID, 65536)),
	routeAuth(http.MethodDelete, "/api/v1/capacity/timeslots/{id}", projectAdminAccess(resolveTimeslotProjectID)),
	routeAuth(http.MethodPost, "/api/v1/capacity/timeslots/{id}/approve", projectAdminAccess(resolveTimeslotProjectID)),
	routeAuth(http.MethodPost, "/api/v1/capacity/timeslots/{id}/reject", projectAdminAccess(resolveTimeslotProjectID)),
	routeAuth(http.MethodPost, "/api/v1/capacity/timeslots/{id}/bind", editorImportAccess(resolveTimeslotProjectID, 65536)),
	routeAuth(http.MethodPost, "/api/v1/tests/preflight", accessRequirement{GlobalRoles: []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin}, ProjectRoles: []string{RoleEditor, RoleProjectAdmin}, ProjectLookup: resolveTestCreateProjectID}),
	routeAuth(http.MethodPost, "/api/v1/scenarios/validate-load-model", accessRequirement{GlobalRoles: []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin}, ProjectRoles: []string{RoleEditor, RoleProjectAdmin}, ProjectLookup: resolveScenarioCreateProjectID}),
	routeAuth(http.MethodPost, "/api/v1/scenarios/size-from-goal", accessRequirement{GlobalRoles: []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin}, ProjectRoles: []string{RoleEditor, RoleProjectAdmin}, ProjectLookup: resolveGoalSizingBaselineProjectID}),
	routeAuth(http.MethodGet, "/api/v1/scenarios/load-models/support-matrix", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/scenarios/load-models/templates", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/tests/{id}/report/annotations", accessRequirement{GlobalRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}, ProjectRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin}, ProjectLookup: resolveTestProjectID}),
	routeAuth(http.MethodPut, "/api/v1/tests/{id}/report/annotations", accessRequirement{GlobalRoles: []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin}, ProjectRoles: []string{RoleEditor, RoleProjectAdmin}, ProjectLookup: resolveTestProjectID}),
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
	routeAuth(http.MethodGet, "/api/v1/dsl/actions", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/dsl/config-defaults", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/browser-recorder/devices", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/browser-recorder/install", viewerGlobalAccess()),
	routeAuth(http.MethodPost, "/api/v1/browser-recorder/install", accessRequirement{GlobalRoles: []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin}}),
	routeAuth(http.MethodGet, "/api/v1/downloads/recording-agent/latest", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/downloads/recording-agent/latest/checksum", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/downloads/desktop/latest", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/downloads/desktop/latest/checksum", viewerGlobalAccess()),
	routeAuth(http.MethodPost, "/api/v1/correlations/diagnose", accessRequirement{AuthenticatedOnly: true}),
	routeAuth(http.MethodPost, "/api/v1/correlations/diff", accessRequirement{AuthenticatedOnly: true}),
	routeAuth(http.MethodPost, "/api/v1/selectors/diagnose", accessRequirement{AuthenticatedOnly: true}),
	routeAuth(http.MethodPost, "/api/v1/scripts/import/asyncapi", accessRequirement{GlobalRoles: []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin}, ProjectRoles: []string{RoleEditor, RoleProjectAdmin}, ProjectLookup: resolveImportProjectIDFromJSONOrForm, MaxBodyBytes: webAPIImportMaxBodyBytes}),
	routeAuth(http.MethodGet, "/api/v1/scripts/import/asyncapi/support-matrix", viewerGlobalAccess()),
	routeAuth(http.MethodPost, "/api/v1/scripts/import/eventhubs-capture", editorImportAccess(resolveImportProjectIDFromForm, webAPIImportMaxBodyBytes)),
	routeAuth(http.MethodGet, "/api/v1/scripts/import/eventhubs-capture/support-matrix", viewerGlobalAccess()),
	routeAuth(http.MethodPost, "/api/v1/scripts/import/broker-config", editorImportAccess(resolveImportProjectIDFromForm, webAPIImportMaxBodyBytes)),
	routeAuth(http.MethodGet, "/api/v1/scripts/import/broker-config/support-matrix", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/scripts/import/raml/support-matrix", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/fleet/alerts", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/fleet/capacity", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/fleet/load-zones", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/fleet/provisioner", viewerGlobalAccess()),

	// Fleet management (pools, generator metadata, provisioning) — system-admin infra.
	routeAuth(http.MethodPost, "/api/v1/fleet/pools", accessRequirement{GlobalRoles: []string{RoleSystemAdmin}, MaxBodyBytes: 1048576}),
	routeAuth(http.MethodGet, "/api/v1/fleet/pools", systemAdminAccess()),
	routeAuth(http.MethodGet, "/api/v1/fleet/pools/{id}", systemAdminAccess()),
	routeAuth(http.MethodPut, "/api/v1/fleet/pools/{id}", accessRequirement{GlobalRoles: []string{RoleSystemAdmin}, MaxBodyBytes: 1048576}),
	routeAuth(http.MethodDelete, "/api/v1/fleet/pools/{id}", systemAdminAccess()),
	routeAuth(http.MethodGet, "/api/v1/fleet/generators", systemAdminAccess()),
	routeAuth(http.MethodPatch, "/api/v1/fleet/generators/{id}", accessRequirement{GlobalRoles: []string{RoleSystemAdmin}, MaxBodyBytes: 262144}),
	routeAuth(http.MethodPost, "/api/v1/fleet/provision-requests", accessRequirement{GlobalRoles: []string{RoleSystemAdmin}, MaxBodyBytes: 262144}),
	routeAuth(http.MethodGet, "/api/v1/fleet/provision-requests/{id}", systemAdminAccess()),
	routeAuth(http.MethodPatch, "/api/v1/system/generators/drain", accessRequirement{GlobalRoles: []string{RoleSystemAdmin}}),
	routeAuth(http.MethodGet, "/api/v1/system/restarts/capabilities", systemAdminAccess()),
	routeAuth(http.MethodPost, "/api/v1/system/restarts", systemAdminAccess()),
	routeAuth(http.MethodGet, "/api/v1/integrations/apm/profiles", viewerGlobalAccess()),
	routeAuth(http.MethodPost, "/api/v1/integrations/dynatrace/dry-run", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/integrations/github/config", accessRequirement{AuthenticatedOnly: true}),
	routeAuth(http.MethodGet, "/api/v1/integrations/github/login", accessRequirement{AuthenticatedOnly: true}),
	routeAuth(http.MethodGet, "/api/v1/integrations/github/callback", accessRequirement{AuthenticatedOnly: true}),
	routeAuth(http.MethodGet, "/api/v1/integrations/github/connection", accessRequirement{AuthenticatedOnly: true}),
	routeAuth(http.MethodDelete, "/api/v1/integrations/github/connection", accessRequirement{AuthenticatedOnly: true}),
	routeAuth(http.MethodGet, "/api/v1/monitoring/packs", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/monitoring/prometheus/graphs", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/reports/profiles", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/results/imports", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/library/capabilities", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/ai/authoring/contract", viewerGlobalAccess()),
	routeAuth(http.MethodPost, "/api/v1/archives/retention/plan", accessRequirement{GlobalRoles: []string{RoleSystemAdmin}}),
	routeAuth(http.MethodPost, "/api/v1/archives/retention/apply", accessRequirement{GlobalRoles: []string{RoleSystemAdmin}}),

	// Service-account lifecycle (system-admin only).
	routeAuth(http.MethodPatch, "/api/v1/auth/service-accounts/{id}", systemAdminAccess()),
	routeAuth(http.MethodPost, "/api/v1/auth/service-accounts/{id}/rotate", systemAdminAccess()),
	routeAuth(http.MethodDelete, "/api/v1/auth/service-accounts/{id}", systemAdminAccess()),

	// GitHub integration read endpoints (any authenticated user).
	routeAuth(http.MethodGet, "/api/v1/integrations/github/repos", accessRequirement{AuthenticatedOnly: true}),
	routeAuth(http.MethodGet, "/api/v1/integrations/github/branches", accessRequirement{AuthenticatedOnly: true}),
	routeAuth(http.MethodGet, "/api/v1/integrations/github/contents", accessRequirement{AuthenticatedOnly: true}),
	routeAuth(http.MethodGet, "/api/v1/integrations/github/diagnostics", accessRequirement{AuthenticatedOnly: true}),
	routeAuth(http.MethodGet, "/api/v1/integrations/github/installations", accessRequirement{AuthenticatedOnly: true}),
	routeAuth(http.MethodGet, "/api/v1/integrations/github/sync-jobs", accessRequirement{AuthenticatedOnly: true}),
	routeAuth(http.MethodGet, "/api/v1/integrations/github/sync-jobs/{id}", accessRequirement{AuthenticatedOnly: true}),
	routeAuth(http.MethodGet, "/api/v1/integrations/github/webhooks", accessRequirement{AuthenticatedOnly: true}),
	routeAuth(http.MethodGet, "/api/v1/integrations/github/check-links", accessRequirement{AuthenticatedOnly: true}),
	routeAuth(http.MethodPost, "/api/v1/integrations/github/sync-jobs", editorProjectAccess(resolveImportProjectIDFromJSON)),

	// Library transfer + remotes (flat endpoints).
	routeAuth(http.MethodPost, "/api/v1/library/export", viewerProjectAccess(resolveLibraryTransferProjectID)),
	routeAuth(http.MethodPost, "/api/v1/library/import", editorProjectAccess(resolveLibraryImportProjectID)),
	routeAuth(http.MethodPost, "/api/v1/library/import/archive", editorProjectAccess(resolveProjectIDFromQuery)),
	routeAuth(http.MethodGet, "/api/v1/library/results/{id}/archive", viewerProjectAccess(resolveLibraryResultArchiveProjectID)),
	routeAuth(http.MethodGet, "/api/v1/library/remotes", viewerGlobalAccess()),
	routeAuth(http.MethodPost, "/api/v1/library/remotes", systemAdminAccess()),
	routeAuth(http.MethodPut, "/api/v1/library/remotes/{id}", systemAdminAccess()),
	routeAuth(http.MethodDelete, "/api/v1/library/remotes/{id}", systemAdminAccess()),
	routeAuth(http.MethodPost, "/api/v1/library/remotes/{id}/probe", systemAdminAccess()),
	routeAuth(http.MethodGet, "/api/v1/library/remotes/{id}/catalog", viewerGlobalAccess()),
	routeAuth(http.MethodPost, "/api/v1/library/remotes/{id}/diff", viewerProjectAccess(resolveLibraryTransferProjectID)),
	routeAuth(http.MethodPost, "/api/v1/library/remotes/{id}/pull", editorProjectAccess(resolveLibraryTransferProjectID)),
	routeAuth(http.MethodPost, "/api/v1/library/remotes/{id}/push", editorProjectAccess(resolveLibraryTransferProjectID)),
	routeAuth(http.MethodPost, "/api/v1/remotes/{id}/runs", accessRequirement{GlobalRoles: []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin}}),
	routeAuth(http.MethodGet, "/api/v1/remotes/{id}/runs/{remote_test_id}", viewerGlobalAccess()),
	routeAuth(http.MethodPost, "/api/v1/remotes/{id}/runs/{remote_test_id}/start", accessRequirement{GlobalRoles: []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin}}),
	routeAuth(http.MethodPost, "/api/v1/remotes/{id}/runs/{remote_test_id}/stop", accessRequirement{GlobalRoles: []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin}}),
	routeAuth(http.MethodPost, "/api/v1/remotes/{id}/runs/{remote_test_id}/abort", accessRequirement{GlobalRoles: []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin}}),
	routeAuth(http.MethodPost, "/api/v1/remotes/{id}/runs/{remote_test_id}/pause", accessRequirement{GlobalRoles: []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin}}),
	routeAuth(http.MethodPost, "/api/v1/remotes/{id}/runs/{remote_test_id}/resume", accessRequirement{GlobalRoles: []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin}}),

	// Assisted-authoring drafts/repairs/explain + external result import.
	routeAuth(http.MethodPost, "/api/v1/ai/drafts", editorProjectAccess(resolveAIAssistanceProjectID)),
	routeAuth(http.MethodPost, "/api/v1/ai/repairs", editorProjectAccess(resolveAIAssistanceProjectID)),
	routeAuth(http.MethodPost, "/api/v1/ai/explain", editorProjectAccess(resolveAIAssistanceProjectID)),
	routeAuth(http.MethodPost, "/api/v1/results/import", editorProjectAccess(resolveExternalResultImportProjectID)),

	// Domains, application-repository categories, projects, folders (flat).
	routeAuth(http.MethodGet, "/api/v1/domains", viewerGlobalAccess()),
	routeAuth(http.MethodPost, "/api/v1/domains", systemAdminAccess()),
	routeAuth(http.MethodPut, "/api/v1/application-repository/categories/{id}", accessRequirement{
		GlobalRoles:      []string{RoleProjectAdmin, RoleSystemAdmin},
		DomainRoles:      []string{RoleProjectAdmin},
		DomainLookup:     resolveCategoryDomainID,
		RepositoryRoles:  []string{RoleProjectAdmin},
		RepositoryLookup: resolveCategoryRepositoryID,
	}),
	routeAuth(http.MethodDelete, "/api/v1/application-repository/categories/{id}", accessRequirement{
		GlobalRoles:      []string{RoleProjectAdmin, RoleSystemAdmin},
		DomainRoles:      []string{RoleProjectAdmin},
		DomainLookup:     resolveCategoryDomainID,
		RepositoryRoles:  []string{RoleProjectAdmin},
		RepositoryLookup: resolveCategoryRepositoryID,
	}),
	routeAuth(http.MethodGet, "/api/v1/projects", viewerGlobalAccess()),
	routeAuth(http.MethodPost, "/api/v1/projects", accessRequirement{
		GlobalRoles:      []string{RoleProjectAdmin, RoleSystemAdmin},
		DomainRoles:      []string{RoleEditor, RoleProjectAdmin},
		DomainLookup:     resolveProjectCreateDomainID,
		RepositoryRoles:  []string{RoleEditor, RoleProjectAdmin},
		RepositoryLookup: resolveProjectCreateRepositoryID,
	}),
	routeAuth(http.MethodPut, "/api/v1/folders/{id}", projectAdminAccess(resolveFolderProjectID)),
	routeAuth(http.MethodDelete, "/api/v1/folders/{id}", projectAdminAccess(resolveFolderProjectID)),

	// Script draft/replay/debug sessions.
	routeAuth(http.MethodPost, "/api/v1/scripts/replay", editorProjectAccess(resolveDraftScriptProjectID)),
	routeAuth(http.MethodPost, "/api/v1/scripts/debug/sessions", editorProjectAccess(resolveDraftScriptProjectID)),
	routeAuth(http.MethodPost, "/api/v1/scripts/browser-replay/sessions", editorProjectAccess(resolveDraftScriptProjectID)),

	// Importer support matrices + authoring catalogs (read-level).
	routeAuth(http.MethodGet, "/api/v1/scripts/import/jmeter/support-matrix", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/scripts/import/enterprise-script-archive/support-matrix", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/scripts/web-api/support-matrix", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/scripts/import/devweb/support-matrix", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/scripts/import/k6/support-matrix", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/scripts/import/oracle-ebusiness/support-matrix", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/scripts/authoring/capabilities", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/scripts/authoring/templates", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/scripts/authoring/components", viewerGlobalAccess()),
	routeAuth(http.MethodPost, "/api/v1/scripts/authoring/components", editorProjectAccess(resolveReusableBlockCreateProjectID)),
	routeAuth(http.MethodDelete, "/api/v1/scripts/authoring/components/{id}", editorProjectAccess(resolveReusableBlockProjectID)),

	// Script importers (write-level, with per-format upload limits).
	routeAuth(http.MethodPost, "/api/v1/scripts/import/jmeter/save", editorProjectAccess(resolveJMeterSaveProjectID)),
	routeAuth(http.MethodPost, "/api/v1/scripts/import/enterprise-script-archive", editorImportAccess(resolveImportProjectIDFromForm, enterpriseArchiveImportMaxBodyBytes)),
	routeAuth(http.MethodPost, "/api/v1/scripts/import/enterprise-script-archive/validate", editorImportAccess(resolveImportProjectIDFromForm, enterpriseArchiveImportMaxBodyBytes)),
	routeAuth(http.MethodPost, "/api/v1/scripts/import/jmeter", editorImportAccess(resolveImportProjectIDFromForm, jmeterImportMaxBodyBytes)),
	routeAuth(http.MethodPost, "/api/v1/scripts/import/jmeter/validate", editorImportAccess(resolveImportProjectIDFromForm, jmeterImportMaxBodyBytes)),
	routeAuth(http.MethodPost, "/api/v1/scripts/import/github", editorProjectAccess(resolveImportProjectIDFromJSON)),
	routeAuth(http.MethodPost, "/api/v1/scripts/import/sample", editorProjectAccess(resolveImportProjectIDFromJSON)),
	routeAuth(http.MethodPost, "/api/v1/scripts/import/oracle-ebusiness/review", editorImportAccess(resolveImportProjectIDFromJSONOrForm, oracleReviewImportMaxBodyBytes)),
	routeAuth(http.MethodPost, "/api/v1/scripts/import/k6", editorImportAccess(resolveImportProjectIDFromJSONOrForm, k6ImportMaxBodyBytes)),
	routeAuth(http.MethodPost, "/api/v1/scripts/import/har", editorImportAccess(resolveImportProjectIDFromJSONOrForm, harImportMaxBodyBytes)),
	routeAuth(http.MethodPost, "/api/v1/scripts/import/salesforce", editorImportAccess(resolveImportProjectIDFromJSONOrForm, harImportMaxBodyBytes)),
	routeAuth(http.MethodPost, "/api/v1/scripts/import/recorder", editorImportAccess(resolveImportProjectIDFromJSONOrForm, recorderImportMaxBodyBytes)),
	routeAuth(http.MethodPost, "/api/v1/scripts/import/parameterize/apply", editorProjectAccess(resolveParameterizeApplyProjectID)),
	routeAuth(http.MethodPost, "/api/v1/scripts/{id}/parameterize/suggest", editorProjectAccess(resolveScriptProjectID)),
	routeAuth(http.MethodPost, "/api/v1/scripts/import/devweb", editorImportAccess(resolveImportProjectIDFromJSONOrForm, webAPIImportMaxBodyBytes)),
	routeAuth(http.MethodPost, "/api/v1/scripts/import/openapi", editorImportAccess(resolveImportProjectIDFromJSONOrForm, webAPIImportMaxBodyBytes)),
	routeAuth(http.MethodPost, "/api/v1/scripts/import/raml", editorImportAccess(resolveImportProjectIDFromJSONOrForm, webAPIImportMaxBodyBytes)),
	routeAuth(http.MethodPost, "/api/v1/scripts/import/postman", editorImportAccess(resolveImportProjectIDFromJSONOrForm, webAPIImportMaxBodyBytes)),
	routeAuth(http.MethodPost, "/api/v1/scripts/import/wsdl", editorImportAccess(resolveImportProjectIDFromJSONOrForm, webAPIImportMaxBodyBytes)),
	routeAuth(http.MethodPost, "/api/v1/scripts/export/k6", editorProjectAccess(resolveK6ExportProjectID)),

	// Per-resource GitHub links (project policy, script push/status, test check-run).
	routeAuth(http.MethodGet, "/api/v1/projects/{id}/github/policy", viewerProjectAccess(resolveProjectIDFromPath)),
	routeAuth(http.MethodPut, "/api/v1/projects/{id}/github/policy", projectAdminAccess(resolveProjectIDFromPath)),
	routeAuth(http.MethodPost, "/api/v1/scripts/{id}/github/push", editorProjectAccess(resolveScriptProjectID)),
	routeAuth(http.MethodGet, "/api/v1/scripts/{id}/github/status", viewerProjectAccess(resolveScriptProjectID)),
	routeAuth(http.MethodGet, "/api/v1/scripts/{id}/recording/status", viewerProjectAccess(resolveScriptProjectID)),
	routeAuth(http.MethodPost, "/api/v1/scripts/{id}/diagnose-replay", viewerProjectAccess(resolveScriptProjectID)),
	routeAuth(http.MethodPost, "/api/v1/tests/{id}/github/check-run", editorProjectAccess(resolveTestProjectID)),

	// Capacity estimate (write-level, project-scoped).
	routeAuth(http.MethodPost, "/api/v1/tests/estimate", editorProjectAccess(resolveTestCreateProjectID)),
}

func routeAuth(method string, path string, requirement accessRequirement) apiRouteAuthRule {
	return apiRouteAuthRule{Method: method, Path: path, Requirement: requirement}
}

func viewerGlobalAccess() accessRequirement {
	return accessRequirement{GlobalRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}}
}

func systemAdminAccess() accessRequirement {
	return accessRequirement{GlobalRoles: []string{RoleSystemAdmin}}
}

// projectScopedLookup is the per-request resolver shape used to bind an access
// requirement to a specific project/domain/repository for fine-grained checks.
type projectScopedLookup = func(*http.Request, storage.Store) (string, int, string)

// viewerProjectAccess grants read-level access globally and within a project.
func viewerProjectAccess(lookup projectScopedLookup) accessRequirement {
	return accessRequirement{
		GlobalRoles:   []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
		ProjectRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
		ProjectLookup: lookup,
	}
}

// editorProjectAccess grants write-level access globally and within a project.
func editorProjectAccess(lookup projectScopedLookup) accessRequirement {
	return accessRequirement{
		GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
		ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
		ProjectLookup: lookup,
	}
}

// editorImportAccess is editorProjectAccess with an upload body-size limit.
func editorImportAccess(lookup projectScopedLookup, maxBody int64) accessRequirement {
	req := editorProjectAccess(lookup)
	req.MaxBodyBytes = maxBody
	return req
}

// projectAdminAccess grants project-admin-level write access within a project.
func projectAdminAccess(lookup projectScopedLookup) accessRequirement {
	return accessRequirement{
		GlobalRoles:   []string{RoleProjectAdmin, RoleSystemAdmin},
		ProjectRoles:  []string{RoleProjectAdmin},
		ProjectLookup: lookup,
	}
}

func accessRequirementFromRouteCatalog(method string, path string) (accessRequirement, bool) {
	// Exact-literal rules take precedence so a concrete path (e.g.
	// "/api/v1/scripts/replay") always wins over a template rule that could
	// also match it (e.g. "/api/v1/scripts/{id}").
	for _, rule := range routeCatalogAuthRules {
		if rule.Method == method && rule.Path == path {
			return rule.Requirement, true
		}
	}
	// Then segment-wise template rules: a "{name}" segment matches exactly one
	// non-empty path segment. This lets parameterized routes be declared
	// declaratively instead of via hand-coded prefix/suffix checks.
	for _, rule := range routeCatalogAuthRules {
		if rule.Method == method && routePathTemplateMatches(rule.Path, path) {
			return rule.Requirement, true
		}
	}
	return accessRequirement{}, false
}

// routePathTemplateMatches reports whether path matches a "/a/{id}/b"-style
// template. Each "{...}" segment matches exactly one non-empty path segment;
// every other segment must match literally. Templates without a placeholder are
// ignored here (they are handled by the exact-match pass).
func routePathTemplateMatches(template string, path string) bool {
	if !strings.Contains(template, "{") {
		return false
	}
	tSeg := strings.Split(strings.Trim(template, "/"), "/")
	pSeg := strings.Split(strings.Trim(path, "/"), "/")
	if len(tSeg) != len(pSeg) {
		return false
	}
	for i := range tSeg {
		if strings.HasPrefix(tSeg[i], "{") && strings.HasSuffix(tSeg[i], "}") {
			if pSeg[i] == "" {
				return false
			}
			continue
		}
		if tSeg[i] != pSeg[i] {
			return false
		}
	}
	return true
}
