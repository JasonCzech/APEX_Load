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

// legacyRouteCatalogAuthRules contains routes that have not yet moved to a
// domain apiRouteCatalog. Migrated domains contribute their access requirements
// directly from the canonical route specifications below.
var legacyRouteCatalogAuthRules = []apiRouteAuthRule{
	// Live tables disclose and persist shared data, so use the same explicit
	// scopes as data pools despite being addressed beneath a run URL.
	routeAuth(http.MethodGet, "/api/v1/tests/{id}/live-tables", withServiceAccountScope(viewerProjectAccess(resolveTestProjectID), "read")),
	routeAuth(http.MethodGet, "/api/v1/projects/{id}/live-tables", withServiceAccountScope(viewerProjectAccess(resolveProjectIDFromPath), "read")),
	routeAuth(http.MethodPost, "/api/v1/projects/{id}/live-tables", withServiceAccountScope(editorProjectAccessWithBodyLimit(resolveProjectIDFromPath, 1<<20), "scripts:write")),
	routeAuth(http.MethodGet, "/api/v1/projects/{id}/live-tables/{tableID}", withServiceAccountScope(viewerProjectAccess(resolveProjectIDFromPath), "read")),
	routeAuth(http.MethodGet, "/api/v1/projects/{id}/live-tables/{tableID}/rows", withServiceAccountScope(viewerProjectAccess(resolveProjectIDFromPath), "read")),
	routeAuth(http.MethodGet, "/api/v1/projects/{id}/live-tables/{tableID}/export", withServiceAccountScope(viewerProjectAccess(resolveProjectIDFromPath), "read")),
	routeAuth(http.MethodPost, "/api/v1/projects/{id}/live-tables/{tableID}/mutations", withServiceAccountScope(editorProjectAccessWithBodyLimit(resolveProjectIDFromPath, 1<<20), "scripts:write")),
	routeAuth(http.MethodPost, "/api/v1/projects/{id}/live-tables/{tableID}/lifecycle", withServiceAccountScope(projectAdminAccessWithBodyLimit(resolveProjectIDFromPath, 1<<20), "scripts:write")),
	routeAuth(http.MethodGet, "/api/v1/tests/{id}/live-tables/{tableID}/rows", withServiceAccountScope(viewerProjectAccess(resolveTestProjectID), "read")),
	routeAuth(http.MethodGet, "/api/v1/tests/{id}/live-tables/{tableID}/export", withServiceAccountScope(viewerProjectAccess(resolveTestProjectID), "read")),
	routeAuth(http.MethodPost, "/api/v1/tests/{id}/live-tables/{tableID}/finalize", withServiceAccountScope(editorProjectAccess(resolveTestProjectID), liveTablePersistenceServiceAccountScope)),
	routeAuth(http.MethodGet, "/api/v1/auth/diagnostics", systemAdminAccess()),
	routeAuth(http.MethodGet, "/api/v1/auth/service-accounts", accessRequirement{GlobalRoles: []string{RoleSystemAdmin}}),
	routeAuth(http.MethodPost, "/api/v1/auth/service-accounts", accessRequirement{GlobalRoles: []string{RoleSystemAdmin}}),
	routeAuth(http.MethodGet, "/api/v1/auth/users", accessRequirement{AuthenticatedOnly: true}),
	routeAuth(http.MethodPost, "/api/v1/auth/users", accessRequirement{AuthenticatedOnly: true}),
	routeAuth(http.MethodPost, "/api/v1/auth/users/onboard", accessRequirement{AuthenticatedOnly: true}),
	routeAuth(http.MethodGet, "/api/v1/auth/users/{id}", accessRequirement{AuthenticatedOnly: true}),
	routeAuth(http.MethodPatch, "/api/v1/auth/users/{id}", accessRequirement{AuthenticatedOnly: true}),
	routeAuth(http.MethodDelete, "/api/v1/auth/users/{id}", accessRequirement{AuthenticatedOnly: true}),
	routeAuth(http.MethodPost, "/api/v1/auth/users/{id}/bindings", accessRequirement{AuthenticatedOnly: true}),
	routeAuth(http.MethodDelete, "/api/v1/auth/users/{id}/bindings/{binding_id}", accessRequirement{AuthenticatedOnly: true}),
	routeAuth(http.MethodPost, "/api/v1/auth/users/{id}/disable", accessRequirement{AuthenticatedOnly: true}),
	routeAuth(http.MethodPost, "/api/v1/auth/users/{id}/enable", accessRequirement{AuthenticatedOnly: true}),
	routeAuth(http.MethodPost, "/api/v1/auth/users/{id}/revoke-sessions", accessRequirement{AuthenticatedOnly: true}),
	routeAuth(http.MethodGet, "/api/v1/auth/user-activity", accessRequirement{AuthenticatedOnly: true}),
	routeAuth(http.MethodGet, "/api/v1/auth/user-activity/export", accessRequirement{AuthenticatedOnly: true}),
	routeAuth(http.MethodGet, "/api/v1/auth/access-review", accessRequirement{AuthenticatedOnly: true}),
	routeAuth(http.MethodGet, "/api/v1/auth/effective-access/principal", accessRequirement{GlobalRoles: []string{RoleSystemAdmin}}),
	routeAuth(http.MethodGet, "/api/v1/auth/audit-events", accessRequirement{GlobalRoles: []string{RoleSystemAdmin}}),
	routeAuth(http.MethodGet, "/api/v1/auth/audit-events/export", accessRequirement{GlobalRoles: []string{RoleSystemAdmin}}),
	// Controller-native CI execution. ci_runner exists only on this bounded
	// surface; API-key fallback must also carry the explicit run/read scope.
	routeAuth(http.MethodPost, "/api/v1/ci/runs", accessRequirement{GlobalRoles: []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin}, ProjectRoles: []string{RoleCIRunner, RoleEditor, RoleProjectAdmin}, ProjectLookup: resolveCIRunCreateProjectID, ServiceAccountScope: "tests:run", MaxBodyBytes: 4 << 20}),
	routeAuth(http.MethodGet, "/api/v1/ci/runs/{id}", accessRequirement{GlobalRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}, ProjectRoles: []string{RoleCIRunner, RoleViewer, RoleEditor, RoleProjectAdmin}, ProjectLookup: resolveTestProjectID, ServiceAccountScope: "results:read"}),
	routeAuth(http.MethodPost, "/api/v1/ci/runs/{id}/abort", accessRequirement{GlobalRoles: []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin}, ProjectRoles: []string{RoleCIRunner, RoleEditor, RoleProjectAdmin}, ProjectLookup: resolveTestProjectID, ServiceAccountScope: "tests:run"}),

	// Monitoring collectors (restricted-network agent control plane) — project-admin infra.
	routeAuth(http.MethodPost, "/api/v1/monitoring/collectors", accessRequirement{GlobalRoles: []string{RoleProjectAdmin, RoleSystemAdmin}, ProjectRoles: []string{RoleProjectAdmin}, ProjectLookup: resolveMonitoringCollectorCreateProjectID, MaxBodyBytes: 65536}),
	routeAuth(http.MethodGet, "/api/v1/monitoring/threshold-incidents", viewerProjectAccess(resolveProjectIDFromQuery)),
	routeAuth(http.MethodPatch, "/api/v1/monitoring/threshold-incidents/{id}/ack", accessRequirement{GlobalRoles: []string{RoleProjectAdmin, RoleSystemAdmin}, ProjectRoles: []string{RoleProjectAdmin}, ProjectLookup: resolveThresholdIncidentProjectID, MaxBodyBytes: 65536}),
	routeAuth(http.MethodGet, "/api/v1/monitoring/collectors", viewerProjectAccess(resolveProjectIDFromQuery)),
	routeAuth(http.MethodGet, "/api/v1/monitoring/collectors/{id}", viewerProjectAccess(resolveMonitoringCollectorProjectID)),
	routeAuth(http.MethodPatch, "/api/v1/monitoring/collectors/{id}", accessRequirement{GlobalRoles: []string{RoleProjectAdmin, RoleSystemAdmin}, ProjectRoles: []string{RoleProjectAdmin}, ProjectLookup: resolveMonitoringCollectorProjectID, MaxBodyBytes: 65536}),
	routeAuth(http.MethodDelete, "/api/v1/monitoring/collectors/{id}", projectAdminAccess(resolveMonitoringCollectorProjectID)),
	routeAuth(http.MethodPost, "/api/v1/monitoring/collectors/{id}/rotate-token", projectAdminAccess(resolveMonitoringCollectorProjectID)),
	routeAuth(http.MethodGet, "/api/v1/monitoring/collectors/{id}/metrics", viewerProjectAccess(resolveMonitoringCollectorProjectID)),

	// Enterprise entitlements (read-only consumption vs limits).
	routeAuth(http.MethodGet, "/api/v1/projects/{id}/entitlements/usage", viewerProjectAccess(resolveProjectIDFromPath)),
	routeAuth(http.MethodGet, "/api/v1/projects/{id}/monitoring/elk-connections", editorProjectAccess(resolveProjectIDFromPath)),
	routeAuth(http.MethodGet, "/api/v1/projects/{id}/monitoring/elk-connections/{connection_id}", projectAdminAccess(resolveProjectIDFromPath)),
	routeAuth(http.MethodPost, "/api/v1/projects/{id}/monitoring/elk-connections", accessRequirement{GlobalRoles: []string{RoleProjectAdmin, RoleSystemAdmin}, ProjectRoles: []string{RoleProjectAdmin}, ProjectLookup: resolveProjectIDFromPath, MaxBodyBytes: 65536}),
	routeAuth(http.MethodPut, "/api/v1/projects/{id}/monitoring/elk-connections/{connection_id}", accessRequirement{GlobalRoles: []string{RoleProjectAdmin, RoleSystemAdmin}, ProjectRoles: []string{RoleProjectAdmin}, ProjectLookup: resolveProjectIDFromPath, MaxBodyBytes: 65536}),
	routeAuth(http.MethodDelete, "/api/v1/projects/{id}/monitoring/elk-connections/{connection_id}", projectAdminAccess(resolveProjectIDFromPath)),
	routeAuth(http.MethodPost, "/api/v1/projects/{id}/monitoring/elk-connections/{connection_id}/test", projectAdminAccess(resolveProjectIDFromPath)),
	routeAuth(http.MethodGet, "/api/v1/projects/{id}/monitoring/dynatrace-connections", viewerProjectAccess(resolveProjectIDFromPath)),
	routeAuth(http.MethodGet, "/api/v1/projects/{id}/monitoring/dynatrace-connections/{connection_id}", projectAdminAccess(resolveProjectIDFromPath)),
	routeAuth(http.MethodPost, "/api/v1/projects/{id}/monitoring/dynatrace-connections", accessRequirement{GlobalRoles: []string{RoleProjectAdmin, RoleSystemAdmin}, ProjectRoles: []string{RoleProjectAdmin}, ProjectLookup: resolveProjectIDFromPath, MaxBodyBytes: 65536}),
	routeAuth(http.MethodPut, "/api/v1/projects/{id}/monitoring/dynatrace-connections/{connection_id}", accessRequirement{GlobalRoles: []string{RoleProjectAdmin, RoleSystemAdmin}, ProjectRoles: []string{RoleProjectAdmin}, ProjectLookup: resolveProjectIDFromPath, MaxBodyBytes: 65536}),
	routeAuth(http.MethodDelete, "/api/v1/projects/{id}/monitoring/dynatrace-connections/{connection_id}", projectAdminAccess(resolveProjectIDFromPath)),
	routeAuth(http.MethodPost, "/api/v1/projects/{id}/monitoring/dynatrace-connections/{connection_id}/test", projectAdminAccess(resolveProjectIDFromPath)),

	routeAuth(http.MethodPost, "/api/v1/tests/preflight", accessRequirement{GlobalRoles: []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin}, ProjectRoles: []string{RoleEditor, RoleProjectAdmin}, ProjectLookup: resolveTestCreateProjectID}),
	routeAuth(http.MethodPost, "/api/v1/scenarios/validate-load-model", accessRequirement{GlobalRoles: []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin}, ProjectRoles: []string{RoleEditor, RoleProjectAdmin}, ProjectLookup: resolveScenarioCreateProjectID}),
	routeAuth(http.MethodPost, "/api/v1/scenarios/size-from-goal", accessRequirement{GlobalRoles: []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin}, ProjectRoles: []string{RoleEditor, RoleProjectAdmin}, ProjectLookup: resolveGoalSizingBaselineProjectID}),
	routeAuth(http.MethodGet, "/api/v1/scenarios/traffic-profile/sources", viewerGlobalAccess()),
	routeAuth(http.MethodPost, "/api/v1/scenarios/traffic-profile/analyze", withDomainFeature(editorImportAccess(resolveImportProjectIDFromJSONOrForm, trafficProfileUploadMaxBytes), workloadShapeFeatureID(productionReplayShapeID))),
	routeAuth(http.MethodPost, "/api/v1/scenarios/traffic-profile/derive", withDomainFeature(editorProjectAccessWithBodyLimit(resolveImportProjectIDFromJSONOrForm, trafficProfileDeriveMaxBytes), workloadShapeFeatureID(productionReplayShapeID))),
	routeAuth(http.MethodPost, "/api/v1/scenarios/csv-driven/analyze", withDomainFeature(editorImportAccess(resolveImportProjectIDFromJSONOrForm, trafficProfileUploadMaxBytes), workloadShapeFeatureID(csvDrivenShapeID))),
	routeAuth(http.MethodPost, "/api/v1/scenarios/csv-driven/derive", withDomainFeature(editorProjectAccessWithBodyLimit(resolveImportProjectIDFromJSONOrForm, trafficProfileDeriveMaxBytes), workloadShapeFeatureID(csvDrivenShapeID))),
	routeAuth(http.MethodGet, "/api/v1/scenarios/load-models/support-matrix", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/scenarios/load-models/templates", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/tests/{id}/report/annotations", accessRequirement{GlobalRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}, ProjectRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin}, ProjectLookup: resolveTestProjectID}),
	routeAuth(http.MethodPut, "/api/v1/tests/{id}/report/annotations", accessRequirement{GlobalRoles: []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin}, ProjectRoles: []string{RoleEditor, RoleProjectAdmin}, ProjectLookup: resolveTestProjectID}),
	routeAuth(http.MethodGet, "/api/v1/tests/{id}/groups/{group}/runtime-settings", viewerProjectAccess(resolveTestProjectID)),
	routeAuth(http.MethodPatch, "/api/v1/tests/{id}/groups/{group}/runtime-settings", editorProjectAccessWithBodyLimit(resolveTestProjectID, 1<<20)),
	routeAuth(http.MethodGet, "/api/v1/tests/{id}/groups/{group}/parameters", viewerProjectAccess(resolveTestProjectID)),
	routeAuth(http.MethodPut, "/api/v1/tests/{id}/groups/{group}/parameters", editorProjectAccessWithBodyLimit(resolveTestProjectID, 2<<20)),
	routeAuth(http.MethodGet, "/api/v1/tests/{id}/notes", viewerProjectAccess(resolveTestProjectID)),
	routeAuth(http.MethodPost, "/api/v1/tests/{id}/notes", editorImportAccess(resolveTestProjectID, 262144)),
	routeAuth(http.MethodPatch, "/api/v1/tests/{id}/notes/{note_id}", editorImportAccess(resolveTestProjectID, 262144)),
	routeAuth(http.MethodDelete, "/api/v1/tests/{id}/notes/{note_id}", editorProjectAccess(resolveTestProjectID)),
	routeAuth(http.MethodGet, "/api/v1/tests/{id}/transactions/live/{event_id}", viewerProjectAccess(resolveTestProjectID)),
	routeAuth(http.MethodGet, "/api/v1/desktop/status", viewerGlobalAccess()),
	routeAuth(http.MethodPost, "/api/v1/desktop/browser-downloads", accessRequirement{AuthenticatedOnly: true, MaxBodyBytes: 4096}),
	routeAuth(http.MethodGet, "/api/v1/runtime-settings/support-matrix", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/protocol-bundles/support-matrix", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/protocols/native-support-matrix", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/projects/{id}/secrets", viewerProjectAccess(resolveProjectIDFromPath)),
	routeAuth(http.MethodGet, "/api/v1/projects/{id}/secrets/{name}/usage", viewerProjectAccess(resolveProjectIDFromPath)),
	routeAuth(http.MethodPost, "/api/v1/projects/{id}/secrets/status", accessRequirement{
		GlobalRoles:   []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
		ProjectRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
		ProjectLookup: resolveProjectIDFromPath,
		MaxBodyBytes:  65536,
	}),
	routeAuth(http.MethodPost, "/api/v1/projects/{id}/secrets", editorImportAccess(resolveProjectIDFromPath, 2<<20)),
	routeAuth(http.MethodPut, "/api/v1/projects/{id}/secrets/{name}", accessRequirement{
		GlobalRoles:   []string{RoleProjectAdmin, RoleSystemAdmin},
		ProjectRoles:  []string{RoleProjectAdmin},
		ProjectLookup: resolveProjectIDFromPath,
		MaxBodyBytes:  131072,
	}),
	routeAuth(http.MethodGet, "/api/v1/applications/{id}/secrets", viewerProjectAccess(resolveApplicationProjectID)),
	routeAuth(http.MethodGet, "/api/v1/applications/{id}/secrets/{name}/usage", viewerProjectAccess(resolveApplicationProjectID)),
	routeAuth(http.MethodPost, "/api/v1/applications/{id}/secrets/status", accessRequirement{
		GlobalRoles:   []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
		ProjectRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
		ProjectLookup: resolveApplicationProjectID,
		MaxBodyBytes:  65536,
	}),
	routeAuth(http.MethodPost, "/api/v1/applications/{id}/secrets", editorImportAccess(resolveApplicationProjectID, 2<<20)),
	routeAuth(http.MethodPut, "/api/v1/applications/{id}/secrets/{name}", accessRequirement{
		GlobalRoles:   []string{RoleProjectAdmin, RoleSystemAdmin},
		ProjectRoles:  []string{RoleProjectAdmin},
		ProjectLookup: resolveApplicationProjectID,
		MaxBodyBytes:  131072,
	}),
	routeAuth(http.MethodPost, "/api/v1/scripts/validate", accessRequirement{GlobalRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}, ProjectRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin}, ProjectLookup: resolveScriptCreateProjectID}),
	routeAuth(http.MethodPost, "/api/v1/scripts/secret-bindings/apply", editorProjectAccess(resolveScriptCreateProjectID)),
	routeAuth(http.MethodPost, "/api/v1/scripts/import/repairs/preview", editorProjectAccess(resolveScriptCreateProjectID)),
	routeAuth(http.MethodPost, "/api/v1/scripts/import/repairs/apply", editorProjectAccess(resolveScriptCreateProjectID)),
	routeAuth(http.MethodPost, "/api/v1/scripts/secret-references/rebind", editorImportAccess(resolveScriptCreateProjectID, 4<<20)),
	routeAuth(http.MethodPost, "/api/v1/scripts/import/secrets/migrate", accessRequirement{
		GlobalRoles:   []string{RoleProjectAdmin, RoleSystemAdmin},
		ProjectRoles:  []string{RoleProjectAdmin},
		ProjectLookup: bodyProjectIDResolver("project_id is required for source secret migration"),
		MaxBodyBytes:  2 << 20,
	}),
	routeAuth(http.MethodPost, "/api/v1/scripts/import/secrets/discard", accessRequirement{
		GlobalRoles:   []string{RoleProjectAdmin, RoleSystemAdmin},
		ProjectRoles:  []string{RoleProjectAdmin},
		ProjectLookup: bodyProjectIDResolver("project_id is required for source secret session discard"),
		MaxBodyBytes:  65536,
	}),
	routeAuth(http.MethodGet, "/api/v1/scripts/{id}/secret-status", viewerProjectAccess(resolveScriptProjectID)),
	routeAuth(http.MethodPost, "/api/v1/scripts/{id}/clone-draft", accessRequirement{
		GlobalRoles:     []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
		ProjectRoles:    []string{RoleEditor, RoleProjectAdmin},
		ProjectLookup:   resolveScriptProjectID,
		MaxBodyBytes:    65536,
		DomainFeatureID: "script_builder.existing_scripts",
	}),
	routeAuth(http.MethodPost, "/api/v1/scripts/replay/sessions", accessRequirement{GlobalRoles: []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin}, ProjectRoles: []string{RoleEditor, RoleProjectAdmin}, ProjectLookup: resolveDraftScriptProjectID}),
	routeAuth(http.MethodPost, "/api/v1/scripts/browser-to-http/preview", accessRequirement{GlobalRoles: []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin}, ProjectRoles: []string{RoleEditor, RoleProjectAdmin}, ProjectLookup: resolveBrowserToHTTPPreviewProjectID}),
	routeAuth(http.MethodPost, "/api/v1/scripts/authoring/sessions", accessRequirement{GlobalRoles: []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin}, ProjectRoles: []string{RoleEditor, RoleProjectAdmin}, ProjectLookup: resolveAuthoringSessionRequestProjectID}),
	routeAuth(http.MethodGet, "/api/v1/scripts/authoring/sessions", accessRequirement{GlobalRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}, ProjectRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin}, ProjectLookup: resolveProjectIDFromQuery}),
	routeAuth(http.MethodGet, "/api/v1/correlations/rules", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/dsl/actions", viewerGlobalAccess()),
	routeAuth(http.MethodPost, "/api/v1/dsl/parameter-preview", accessRequirement{GlobalRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}, MaxBodyBytes: 8 << 20}),
	routeAuth(http.MethodGet, "/api/v1/dsl/config-defaults", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/dsl/browser-emulation-catalog", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/browser-recorder/devices", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/browser-recorder/install", viewerGlobalAccess()),
	routeAuth(http.MethodPost, "/api/v1/browser-recorder/install", accessRequirement{GlobalRoles: []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin}}),
	routeAuth(http.MethodDelete, "/api/v1/browser-recorder/sessions/{id}", accessRequirement{GlobalRoles: []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin}}),
	routeAuth(http.MethodGet, "/api/v1/downloads/recording-agent/latest", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/downloads/recording-agent/latest/checksum", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/downloads/desktop/latest", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/downloads/desktop/latest/checksum", viewerGlobalAccess()),
	routeAuth(http.MethodPost, "/api/v1/correlations/diagnose", accessRequirement{AuthenticatedOnly: true, MaxBodyBytes: maxCorrelationRequestBodyBytes}),
	routeAuth(http.MethodPost, "/api/v1/correlations/diff", accessRequirement{AuthenticatedOnly: true, MaxBodyBytes: maxCorrelationRequestBodyBytes}),
	routeAuth(http.MethodPost, "/api/v1/selectors/diagnose", accessRequirement{AuthenticatedOnly: true}),
	// Data pool CSV import is the only data-pool route with a raised body
	// limit; the other data-pool routes deliberately ride the prefix catch-all
	// in access_policy.go at the CRUD default.
	routeAuth(http.MethodPost, "/api/v1/data-pools/{id}/rows/import", editorImportAccess(resolveDataPoolProjectID, dataPoolCSVImportMaxBodyBytes)),
	routeAuth(http.MethodPost, "/api/v1/scripts/import/asyncapi", editorImportAccess(resolveImportProjectIDFromJSONOrForm, webAPIImportMaxBodyBytes)),
	routeAuth(http.MethodGet, "/api/v1/scripts/import/asyncapi/support-matrix", viewerGlobalAccess()),
	routeAuth(http.MethodPost, "/api/v1/scripts/import/eventhubs-capture", editorImportAccess(resolveImportProjectIDFromForm, webAPIImportMaxBodyBytes)),
	routeAuth(http.MethodGet, "/api/v1/scripts/import/eventhubs-capture/support-matrix", viewerGlobalAccess()),
	routeAuth(http.MethodPost, "/api/v1/scripts/import/broker-config", editorImportAccess(resolveImportProjectIDFromForm, webAPIImportMaxBodyBytes)),
	routeAuth(http.MethodGet, "/api/v1/scripts/import/broker-config/support-matrix", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/scripts/import/raml/support-matrix", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/integrations/apm/profiles", viewerGlobalAccess()),
	// Saved-run enrichment performs an additional project check in the handler;
	// previews containing only caller-supplied data retain global UI access.
	routeAuth(http.MethodPost, "/api/v1/integrations/dynatrace/dry-run", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/integrations/github/config", accessRequirement{AuthenticatedOnly: true}),
	routeAuth(http.MethodGet, "/api/v1/integrations/github/login", accessRequirement{AuthenticatedOnly: true}),
	routeAuth(http.MethodGet, "/api/v1/integrations/github/callback", accessRequirement{AuthenticatedOnly: true}),
	routeAuth(http.MethodPost, "/api/v1/integrations/github/callback", accessRequirement{AuthenticatedOnly: true}),
	routeAuth(http.MethodGet, "/api/v1/integrations/github/connection", accessRequirement{AuthenticatedOnly: true}),
	routeAuth(http.MethodDelete, "/api/v1/integrations/github/connection", accessRequirement{AuthenticatedOnly: true}),
	routeAuth(http.MethodGet, "/api/v1/monitoring/packs", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/monitoring/prometheus/graphs", unrestrictedViewerGlobalAccess()),
	// Imported Grafana dashboards.
	routeAuth(http.MethodGet, "/api/v1/monitoring/dashboards/import/grafana/support-matrix", viewerGlobalAccess()),
	routeAuth(http.MethodPost, "/api/v1/monitoring/dashboards/import/grafana", editorImportAccess(resolveImportProjectIDFromJSONOrForm, grafanaDashboardImportMaxBodyBytes)),
	routeAuth(http.MethodPost, "/api/v1/monitoring/dashboards/import/grafana/live", editorImportAccess(resolveImportProjectIDFromJSONOrForm, grafanaDashboardImportMaxBodyBytes)),
	routeAuth(http.MethodPost, "/api/v1/monitoring/dashboards/import/grafana/commit", editorImportAccess(resolveGrafanaDashboardCommitProjectID, 65536)),
	routeAuth(http.MethodPost, "/api/v1/monitoring/dashboards/import/grafana/preview", editorImportAccess(resolveGrafanaDashboardCommitProjectID, 65536)),
	routeAuth(http.MethodPost, "/api/v1/monitoring/dashboards/import/grafana/label-values", editorImportAccess(resolveGrafanaDashboardCommitProjectID, 65536)),
	routeAuth(http.MethodGet, "/api/v1/monitoring/dashboards", viewerProjectAccess(resolveProjectIDFromQuery)),
	routeAuth(http.MethodGet, "/api/v1/monitoring/dashboards/graphs", viewerProjectAccess(resolveProjectIDFromQuery)),
	routeAuth(http.MethodGet, "/api/v1/monitoring/dashboards/{id}", viewerProjectAccess(resolveGrafanaDashboardProjectID)),
	routeAuth(http.MethodPatch, "/api/v1/monitoring/dashboards/{id}", editorProjectAccess(resolveGrafanaDashboardProjectID)),
	routeAuth(http.MethodDelete, "/api/v1/monitoring/dashboards/{id}", editorProjectAccess(resolveGrafanaDashboardProjectID)),
	routeAuth(http.MethodGet, "/api/v1/monitoring/dashboards/{id}/panels", viewerProjectAccess(resolveGrafanaDashboardProjectID)),
	routeAuth(http.MethodPost, "/api/v1/monitoring/dashboard-definitions", editorImportAccess(resolveDashboardDefinitionCreateProjectID, 1<<20)),
	routeAuth(http.MethodGet, "/api/v1/monitoring/dashboard-definitions", viewerProjectAccess(resolveProjectIDFromQuery)),
	routeAuth(http.MethodGet, "/api/v1/monitoring/dashboard-definitions/{id}", viewerProjectAccess(resolveDashboardDefinitionProjectID)),
	routeAuth(http.MethodPatch, "/api/v1/monitoring/dashboard-definitions/{id}", editorImportAccess(resolveDashboardDefinitionProjectID, 1<<20)),
	routeAuth(http.MethodDelete, "/api/v1/monitoring/dashboard-definitions/{id}", editorProjectAccess(resolveDashboardDefinitionProjectID)),
	routeAuth(http.MethodGet, "/api/v1/reports/profiles", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/results/imports", unrestrictedViewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/library/capabilities", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/ai/authoring/contract", viewerGlobalAccess()),

	// Service-account lifecycle (system-admin only).
	routeAuth(http.MethodPatch, "/api/v1/auth/service-accounts/{id}", systemAdminAccess()),
	routeAuth(http.MethodPost, "/api/v1/auth/service-accounts/{id}/rotate", systemAdminAccess()),
	routeAuth(http.MethodDelete, "/api/v1/auth/service-accounts/{id}", systemAdminAccess()),

	// GitHub integration user endpoints. Operational GitHub App/webhook state is
	// system-admin-only; sync/check histories are project-scoped below.
	routeAuth(http.MethodGet, "/api/v1/integrations/github/repos", accessRequirement{AuthenticatedOnly: true}),
	routeAuth(http.MethodGet, "/api/v1/integrations/github/branches", accessRequirement{AuthenticatedOnly: true}),
	routeAuth(http.MethodGet, "/api/v1/integrations/github/contents", accessRequirement{AuthenticatedOnly: true}),
	routeAuth(http.MethodGet, "/api/v1/integrations/github/diagnostics", systemAdminAccess()),
	routeAuth(http.MethodGet, "/api/v1/integrations/github/installations", systemAdminAccess()),
	routeAuth(http.MethodGet, "/api/v1/integrations/github/sync-jobs", viewerProjectAccess(resolveProjectIDFromQuery)),
	routeAuth(http.MethodGet, "/api/v1/integrations/github/sync-jobs/{id}", viewerProjectAccess(resolveGitHubSyncJobProjectID)),
	routeAuth(http.MethodGet, "/api/v1/integrations/github/webhooks", systemAdminAccess()),
	routeAuth(http.MethodGet, "/api/v1/integrations/github/check-links", viewerProjectAccess(resolveGitHubCheckLinksProjectID)),
	routeAuth(http.MethodPost, "/api/v1/integrations/github/sync-jobs", editorProjectAccess(resolveImportProjectIDFromJSON)),

	// Library transfer + remotes (flat endpoints).
	routeAuth(http.MethodPost, "/api/v1/library/export", viewerProjectAccess(resolveLibraryTransferProjectID)),
	routeAuth(http.MethodPost, "/api/v1/library/import", editorImportAccess(resolveLibraryImportProjectID, libraryBundleImportMaxBodyBytes)),
	routeAuth(http.MethodPost, "/api/v1/library/import/archive", editorImportAccess(resolveProjectIDFromQuery, libraryArchiveImportMaxBodyBytes)),
	routeAuth(http.MethodGet, "/api/v1/library/results/{id}/archive", viewerProjectAccess(resolveLibraryResultArchiveProjectID)),
	routeAuth(http.MethodGet, "/api/v1/library/remotes", viewerGlobalAccess()),
	routeAuth(http.MethodPost, "/api/v1/library/remotes", systemAdminAccess()),
	routeAuth(http.MethodPut, "/api/v1/library/remotes/{id}", systemAdminAccess()),
	routeAuth(http.MethodDelete, "/api/v1/library/remotes/{id}", systemAdminAccess()),
	routeAuth(http.MethodPost, "/api/v1/library/remotes/{id}/probe", systemAdminAccess()),
	routeAuth(http.MethodGet, "/api/v1/library/remotes/{id}/catalog", viewerProjectAccess(resolveProjectIDFromQuery)),
	routeAuth(http.MethodPost, "/api/v1/library/remotes/{id}/diff", viewerProjectAccess(resolveLibraryTransferProjectID)),
	routeAuth(http.MethodPost, "/api/v1/library/remotes/{id}/pull", editorProjectAccess(resolveLibraryTransferProjectID)),
	routeAuth(http.MethodPost, "/api/v1/library/remotes/{id}/push", editorProjectAccess(resolveLibraryTransferProjectID)),
	routeAuth(http.MethodPost, "/api/v1/remotes/{id}/runs", editorProjectAccess(resolveRemoteRunCreateProjectID)),
	routeAuth(http.MethodGet, "/api/v1/remotes/{id}/runs/{remote_test_id}", viewerProjectAccess(resolveRemoteRunLinkProjectID)),
	routeAuth(http.MethodPost, "/api/v1/remotes/{id}/runs/{remote_test_id}/start", editorProjectAccess(resolveRemoteRunLinkProjectID)),
	routeAuth(http.MethodPost, "/api/v1/remotes/{id}/runs/{remote_test_id}/stop", editorProjectAccess(resolveRemoteRunLinkProjectID)),
	routeAuth(http.MethodPost, "/api/v1/remotes/{id}/runs/{remote_test_id}/abort", editorProjectAccess(resolveRemoteRunLinkProjectID)),
	routeAuth(http.MethodPost, "/api/v1/remotes/{id}/runs/{remote_test_id}/pause", editorProjectAccess(resolveRemoteRunLinkProjectID)),
	routeAuth(http.MethodPost, "/api/v1/remotes/{id}/runs/{remote_test_id}/resume", editorProjectAccess(resolveRemoteRunLinkProjectID)),

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
	routeAuth(http.MethodGet, "/api/v1/scripts/import/openapi/support-matrix", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/scripts/import/bruno/support-matrix", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/scripts/import/cloudtest/support-matrix", viewerGlobalAccess()),
	routeAuth(http.MethodGet, "/api/v1/scripts/authoring/templates", withDomainFeature(viewerGlobalAccess(), "script_builder.starter_templates")),
	routeAuth(http.MethodGet, "/api/v1/scripts/authoring/components", withDomainFeature(viewerGlobalAccess(), "script_builder.reusable_blocks")),
	routeAuth(http.MethodPost, "/api/v1/scripts/authoring/components", withDomainFeature(editorProjectAccess(resolveReusableBlockCreateProjectID), "script_builder.reusable_blocks")),
	routeAuth(http.MethodDelete, "/api/v1/scripts/authoring/components/{id}", withDomainFeature(editorProjectAccess(resolveReusableBlockProjectID), "script_builder.reusable_blocks")),

	// Script importers (write-level, with per-format upload limits).
	routeAuth(http.MethodPost, "/api/v1/scripts/import/jmeter/save", editorProjectAccessWithBodyLimit(resolveJMeterSaveProjectID, jmeterImportSaveMaxBodyBytes)),
	routeAuth(http.MethodPost, "/api/v1/scripts/import/enterprise-script-archive/save", editorProjectAccessWithBodyLimit(resolveJMeterSaveProjectID, enterpriseArchiveImportSaveMaxBodyBytes)),
	routeAuth(http.MethodPost, "/api/v1/scripts/import/commit-batch", editorProjectAccessWithBodyLimit(resolveJMeterSaveProjectID, webAPIImportCommitMaxBodyBytes)),
	routeAuth(http.MethodPost, "/api/v1/scripts/import/enterprise-script-archive", editorImportAccess(resolveImportProjectIDFromForm, enterpriseArchiveImportMaxBodyBytes)),
	routeAuth(http.MethodPost, "/api/v1/scripts/import/enterprise-script-archive/validate", editorImportAccess(resolveImportProjectIDFromForm, enterpriseArchiveImportMaxBodyBytes)),
	routeAuth(http.MethodPost, "/api/v1/scripts/import/jmeter", editorImportAccess(resolveImportProjectIDFromForm, jmeterImportMaxBodyBytes)),
	routeAuth(http.MethodPost, "/api/v1/scripts/import/jmeter/validate", editorImportAccess(resolveImportProjectIDFromForm, jmeterImportMaxBodyBytes)),
	routeAuth(http.MethodPost, "/api/v1/scripts/import/github", editorProjectAccess(resolveImportProjectIDFromJSON)),
	routeAuth(http.MethodPost, "/api/v1/scripts/import/sample", editorProjectAccess(resolveImportProjectIDFromJSON)),
	routeAuth(http.MethodPost, "/api/v1/scripts/import/{id}/assets/upload", withDomainFeature(editorImportAccess(resolveImportProjectIDFromForm, importAssetMaxBodyBytes), "script_builder.imports")),
	routeAuth(http.MethodPost, "/api/v1/scripts/import/{id}/java-classpath/upload", withDomainFeature(editorImportAccess(resolveImportProjectIDFromForm, javaClasspathUploadMaxBodyBytes), "script_builder.imports")),
	routeAuth(http.MethodPost, "/api/v1/scripts/import/oracle-ebusiness/review", editorImportAccess(resolveImportProjectIDFromJSONOrForm, oracleReviewImportMaxBodyBytes)),
	routeAuth(http.MethodPost, "/api/v1/scripts/import/k6", editorImportAccess(resolveImportProjectIDFromJSONOrForm, k6ImportMaxBodyBytes)),
	routeAuth(http.MethodPost, "/api/v1/scripts/import/har", editorImportAccess(resolveImportProjectIDFromJSONOrForm, harImportMaxBodyBytes)),
	routeAuth(http.MethodPost, "/api/v1/scripts/import/salesforce", editorImportAccess(resolveImportProjectIDFromJSONOrForm, harImportMaxBodyBytes)),
	routeAuth(http.MethodPost, "/api/v1/scripts/import/recorder", editorImportAccess(resolveImportProjectIDFromJSONOrForm, recorderImportMaxBodyBytes)),
	routeAuth(http.MethodPost, "/api/v1/scripts/import/recorder/validate", editorImportAccess(resolveImportProjectIDFromJSONOrForm, recorderImportMaxBodyBytes)),
	routeAuth(http.MethodPost, "/api/v1/scripts/import/recorder/replay", editorImportAccess(resolveImportProjectIDFromJSONOrForm, recorderImportMaxBodyBytes)),
	routeAuth(http.MethodPost, "/api/v1/scripts/import/browser-recorder/attest", editorImportAccess(resolveBridgedBrowserImportProjectID, webAPIImportMaxBodyBytes)),
	routeAuth(http.MethodPost, "/api/v1/scripts/import/settings", editorProjectAccess(resolveParameterizeApplyProjectID)),
	routeAuth(http.MethodPost, "/api/v1/scripts/import/parameterize/apply", editorProjectAccess(resolveParameterizeApplyProjectID)),
	routeAuth(http.MethodPost, "/api/v1/scripts/import/transport/apply", editorProjectAccess(resolveParameterizeApplyProjectID)),
	routeAuth(http.MethodPost, "/api/v1/scripts/import/shared-values/apply", editorProjectAccess(resolveSharedValuesApplyProjectID)),
	routeAuth(http.MethodPost, "/api/v1/scripts/{id}/parameterize/suggest", editorProjectAccess(resolveScriptProjectID)),
	routeAuth(http.MethodPost, "/api/v1/scripts/import/asyncapi/commit", editorProjectAccessWithBodyLimit(resolveAsyncAPICommitProjectID, webAPIImportCommitMaxBodyBytes)),
	routeAuth(http.MethodPost, "/api/v1/scripts/import/asyncapi/commit-batch", editorProjectAccessWithBodyLimit(resolveJMeterSaveProjectID, webAPIImportCommitMaxBodyBytes)),
	routeAuth(http.MethodPost, "/api/v1/scripts/import/devweb", editorImportAccess(resolveImportProjectIDFromJSONOrForm, webAPIImportMaxBodyBytes)),
	routeAuth(http.MethodPost, "/api/v1/scripts/import/openapi", editorImportAccess(resolveImportProjectIDFromJSONOrForm, webAPIImportMaxBodyBytes)),
	routeAuth(http.MethodPost, "/api/v1/scripts/import/openapi/commit", editorProjectAccessWithBodyLimit(resolveOpenAPICommitProjectID, webAPIImportCommitMaxBodyBytes)),
	routeAuth(http.MethodPost, "/api/v1/scripts/import/bruno/inspect", editorImportAccess(resolveImportProjectIDFromJSONOrForm, brunoImportMaxBodyBytes)),
	routeAuth(http.MethodPost, "/api/v1/scripts/import/bruno/commit", editorProjectAccess(bodyProjectIDResolver("project_id is required for Bruno import commit"))),
	routeAuth(http.MethodPost, "/api/v1/scripts/import/cloudtest", editorImportAccess(resolveImportProjectIDFromJSONOrForm, webAPIImportMaxBodyBytes)),
	routeAuth(http.MethodPost, "/api/v1/scripts/import/raml", editorImportAccess(resolveImportProjectIDFromJSONOrForm, webAPIImportMaxBodyBytes)),
	routeAuth(http.MethodPost, "/api/v1/scripts/import/postman", editorImportAccess(resolveImportProjectIDFromJSONOrForm, webAPIImportMaxBodyBytes)),
	routeAuth(http.MethodPost, "/api/v1/scripts/import/postman/validate", editorImportAccess(resolveImportProjectIDFromJSONOrForm, webAPIImportMaxBodyBytes)),
	routeAuth(http.MethodPost, "/api/v1/scripts/import/postman/save", editorProjectAccessWithBodyLimit(resolvePostmanSaveProjectID, webAPIImportCommitMaxBodyBytes)),
	routeAuth(http.MethodGet, "/api/v1/scripts/import/postman/support-matrix", viewerGlobalAccess()),
	routeAuth(http.MethodPost, "/api/v1/scripts/import/wsdl", editorImportAccess(resolveImportProjectIDFromJSONOrForm, webAPIImportMaxBodyBytes)),
	routeAuth(http.MethodPost, "/api/v1/scripts/export/k6", editorProjectAccess(resolveK6ExportProjectID)),

	// Per-resource GitHub links (project policy, script push/status, test check-run).
	routeAuth(http.MethodGet, "/api/v1/projects/{id}/github/policy", viewerProjectAccess(resolveProjectIDFromPath)),
	routeAuth(http.MethodPut, "/api/v1/projects/{id}/github/policy", systemAdminAccess()),
	routeAuth(http.MethodPost, "/api/v1/scripts/{id}/github/push", editorProjectAccess(resolveScriptProjectID)),
	routeAuth(http.MethodGet, "/api/v1/scripts/{id}/github/status", viewerProjectAccess(resolveScriptProjectID)),
	routeAuth(http.MethodGet, "/api/v1/scripts/{id}/recording/status", viewerProjectAccess(resolveScriptProjectID)),
	routeAuth(http.MethodGet, "/api/v1/scripts/{id}/imports/source", editorProjectAccess(resolveScriptProjectID)),
	routeAuth(http.MethodGet, "/api/v1/scripts/{id}/imports/revisions", viewerProjectAccess(resolveScriptProjectID)),
	// Full text of a truncated source file a review names by hash; readable by
	// whoever can read the project's scripts, before or after the import is saved.
	routeAuth(http.MethodGet, "/api/v1/projects/{id}/source-review/texts/{sha256}", viewerProjectAccess(resolveProjectIDFromPath)),
	routeAuth(http.MethodPost, "/api/v1/scripts/{id}/imports/rollback", editorProjectAccess(resolveScriptProjectID)),
	// Legacy aliases. An entry missing here does not merely lose a name: the
	// route would resolve to the default access policy instead of the intended
	// one, so the alias must carry the same access as its canonical twin.
	routeAuth(http.MethodGet, "/api/v1/scripts/{id}/bruno-imports/source", editorProjectAccess(resolveScriptProjectID)),
	routeAuth(http.MethodGet, "/api/v1/scripts/{id}/bruno-imports/revisions", viewerProjectAccess(resolveScriptProjectID)),
	routeAuth(http.MethodPost, "/api/v1/scripts/{id}/bruno-imports/rollback", editorProjectAccess(resolveScriptProjectID)),
	routeAuth(http.MethodPost, "/api/v1/scripts/{id}/diagnose-replay", editorProjectAccessWithBodyLimit(resolveScriptProjectID, maxCorrelationRequestBodyBytes)),
	routeAuth(http.MethodPost, "/api/v1/tests/{id}/github/check-run", editorProjectAccess(resolveTestProjectID)),

	// Capacity estimate (write-level, project-scoped).
	routeAuth(http.MethodPost, "/api/v1/tests/estimate", editorProjectAccess(resolveTestCreateProjectID)),
}

var routeCatalogAuthRules = func() []apiRouteAuthRule {
	rules := authRulesForAPIRouteCatalogs(modularAPIRouteCatalogs...)
	return append(rules, legacyRouteCatalogAuthRules...)
}()

type routeTemplateBucketKey struct {
	method       string
	segmentCount int
}

type compiledRouteAuthTemplate struct {
	segments    []string
	requirement accessRequirement
}

type compiledRouteAuthCatalog struct {
	exact     map[string]accessRequirement
	templates map[routeTemplateBucketKey][]compiledRouteAuthTemplate
}

var indexedRouteCatalogAuthRules = compileRouteAuthCatalog(routeCatalogAuthRules)

func compileRouteAuthCatalog(rules []apiRouteAuthRule) compiledRouteAuthCatalog {
	compiled := compiledRouteAuthCatalog{
		exact:     make(map[string]accessRequirement, len(rules)),
		templates: make(map[routeTemplateBucketKey][]compiledRouteAuthTemplate),
	}
	for _, rule := range rules {
		// Preserve the catalog's historical first-rule-wins behavior. Duplicate
		// rules are rejected by TestRouteCatalogHasNoDuplicateRules, but keeping
		// lookup deterministic makes this helper safe in isolation as well.
		exactKey := routeAuthLookupKey(rule.Method, rule.Path)
		if _, exists := compiled.exact[exactKey]; !exists {
			compiled.exact[exactKey] = rule.Requirement
		}
		if !strings.Contains(rule.Path, "{") {
			continue
		}
		segments := splitRoutePath(rule.Path)
		key := routeTemplateBucketKey{method: rule.Method, segmentCount: len(segments)}
		compiled.templates[key] = append(compiled.templates[key], compiledRouteAuthTemplate{
			segments:    segments,
			requirement: rule.Requirement,
		})
	}
	return compiled
}

func routeAuthLookupKey(method, path string) string {
	return method + "\x00" + path
}

func routeAuth(method string, path string, requirement accessRequirement) apiRouteAuthRule {
	requirement = withRouteServiceAccountScope(method, path, requirement)
	return apiRouteAuthRule{Method: method, Path: path, Requirement: requirement}
}

func withRouteServiceAccountScope(method string, path string, requirement accessRequirement) accessRequirement {
	if requirement.ServiceAccountScope != "" || requirement.ServiceAccountScopeLookup != nil {
		return requirement
	}
	if strings.HasPrefix(path, "/api/v1/library") {
		requirement.ServiceAccountScopeLookup = requiredLibraryServiceAccountScope
		return requirement
	}
	requirement.ServiceAccountScope = staticServiceAccountScopeForRoute(method, path)
	return requirement
}

func viewerGlobalAccess() accessRequirement {
	return accessRequirement{GlobalRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}}
}

func withDomainFeature(requirement accessRequirement, featureID string) accessRequirement {
	requirement.DomainFeatureID = featureID
	return requirement
}

func withServiceAccountScope(requirement accessRequirement, scope string) accessRequirement {
	requirement.ServiceAccountScope = scope
	return requirement
}

// unrestrictedViewerGlobalAccess marks a read surface that returns data across
// projects when no concrete scope is selected. Local Test Login personas whose
// global viewer role is only a UI baseline must select one of their bound
// projects; established auth providers retain their global-viewer behavior.
func unrestrictedViewerGlobalAccess() accessRequirement {
	requirement := viewerGlobalAccess()
	requirement.RequireUnrestrictedViewer = true
	return requirement
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

// viewerProjectAccessWithBodyLimit is viewerProjectAccess with a request
// body-size limit, for read-side evidence a viewer submits.
func viewerProjectAccessWithBodyLimit(lookup projectScopedLookup, maxBody int64) accessRequirement {
	req := viewerProjectAccess(lookup)
	req.MaxBodyBytes = maxBody
	return req
}

// editorProjectAccessWithBodyLimit grants normal editor access while applying
// a route-specific JSON body limit to non-upload mutation endpoints.
func editorProjectAccessWithBodyLimit(lookup projectScopedLookup, maxBody int64) accessRequirement {
	req := editorProjectAccess(lookup)
	req.MaxBodyBytes = maxBody
	return req
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

// projectAdminAccessWithBodyLimit is projectAdminAccess with a request
// body-size limit.
func projectAdminAccessWithBodyLimit(lookup projectScopedLookup, maxBody int64) accessRequirement {
	req := projectAdminAccess(lookup)
	req.MaxBodyBytes = maxBody
	return req
}

func accessRequirementFromRouteCatalog(method string, path string) (accessRequirement, bool) {
	return accessRequirementFromCompiledRouteCatalog(indexedRouteCatalogAuthRules, method, path)
}

func accessRequirementFromCompiledRouteCatalog(catalog compiledRouteAuthCatalog, method string, path string) (accessRequirement, bool) {
	// Exact rules take precedence so a concrete path (e.g. "/scripts/replay")
	// wins over a template that also matches it (e.g. "/scripts/{id}").
	if requirement, ok := catalog.exact[routeAuthLookupKey(method, path)]; ok {
		return requirement, true
	}
	pathSegments := splitRoutePath(path)
	key := routeTemplateBucketKey{method: method, segmentCount: len(pathSegments)}
	for _, candidate := range catalog.templates[key] {
		if compiledRoutePathTemplateMatches(candidate.segments, pathSegments) {
			return candidate.requirement, true
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
	return compiledRoutePathTemplateMatches(splitRoutePath(template), splitRoutePath(path))
}

func splitRoutePath(path string) []string {
	return strings.Split(strings.Trim(path, "/"), "/")
}

func compiledRoutePathTemplateMatches(templateSegments, pathSegments []string) bool {
	if len(templateSegments) != len(pathSegments) {
		return false
	}
	for index, segment := range templateSegments {
		if strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}") {
			if pathSegments[index] == "" {
				return false
			}
			continue
		}
		if segment != pathSegments[index] {
			return false
		}
	}
	return true
}
