package api

// access_policy.go — split from middleware.go (same package, behavior-preserving).
// accessRequirementForRequest: the per-route access-requirement catalog that is the
// single source of truth for both enforcement and generated swagger role docs.

import (
	"net/http"
	"strings"

	"github.com/jasonczech/apexload/pkg/storage"
)

func accessRequirementForRequest(r *http.Request) (accessRequirement, bool) {
	path := r.URL.Path
	method := r.Method

	// The declarative route catalog (route_catalog.go) is the single source of
	// truth for every route whose access requirement is fixed by method+path. It
	// is consulted first and wins. The branches below are the deliberate residue
	// that the static catalog cannot express:
	//   1. Query-conditional routes — the requirement depends on query params
	//      (e.g. list endpoints that are global when project_id/folder_id is
	//      absent but project-scoped when present: library/catalog, ai/policy,
	//      recorder/status, ws, applications, scripts, data-pools, tests,
	//      scenarios list).
	//   2. Prefix catch-alls — a single rule that uniformly covers a resource's
	//      {id} path and all of its (open-ended) sub-paths by method (domains,
	//      projects, applications/{id}, releases/{id}, scenarios/{id}, tests/{id},
	//      data-pools/{id}, authoring sessions/{id}, scripts/{id}). The catalog's
	//      exact-segment-count template matcher would require enumerating every
	//      sub-path, so the catch-all stays here on purpose.
	if requirement, ok := accessRequirementFromRouteCatalog(method, path); ok {
		return requirement, true
	}

	if path == "/api/v1/library/catalog" && method == http.MethodGet {
		if strings.TrimSpace(r.URL.Query().Get("project_id")) == "" {
			return accessRequirement{GlobalRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}}, true
		}
		return accessRequirement{
			GlobalRoles:   []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
			ProjectRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
			ProjectLookup: resolveProjectIDFromQuery,
		}, true
	}

	if (path == "/api/v1/ai/policy" || path == "/api/v1/ai/review-queue") && method == http.MethodGet {
		if strings.TrimSpace(r.URL.Query().Get("project_id")) == "" {
			return accessRequirement{GlobalRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}}, true
		}
		return accessRequirement{
			GlobalRoles:   []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
			ProjectRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
			ProjectLookup: resolveProjectIDFromQuery,
		}, true
	}

	if path == "/api/v1/recorder/status" && method == http.MethodGet {
		if strings.TrimSpace(r.URL.Query().Get("project_id")) == "" {
			return accessRequirement{GlobalRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}}, true
		}
		return accessRequirement{
			GlobalRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
			ProjectRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin},
			ProjectLookup: func(r *http.Request, _ storage.Store) (string, int, string) {
				return resolveRecorderListProjectID(r)
			},
		}, true
	}

	if path == "/api/v1/recorder/ca.pem" && method == http.MethodGet {
		if strings.TrimSpace(r.URL.Query().Get("project_id")) == "" {
			return accessRequirement{GlobalRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}}, true
		}
		return accessRequirement{
			GlobalRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
			ProjectRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin},
			ProjectLookup: func(r *http.Request, _ storage.Store) (string, int, string) {
				return resolveRecorderListProjectID(r)
			},
		}, true
	}

	if path == "/api/v1/browser-recorder/sessions" && method == http.MethodGet {
		if strings.TrimSpace(r.URL.Query().Get("project_id")) == "" {
			return accessRequirement{GlobalRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}}, true
		}
		return accessRequirement{
			GlobalRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
			ProjectRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin},
			ProjectLookup: func(r *http.Request, _ storage.Store) (string, int, string) {
				return resolveRecorderListProjectID(r)
			},
		}, true
	}

	if path == "/api/v1/ws" && method == http.MethodGet {
		if strings.TrimSpace(r.URL.Query().Get("project_id")) == "" {
			return accessRequirement{GlobalRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}}, true
		}
		return accessRequirement{
			GlobalRoles:   []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
			ProjectRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
			ProjectLookup: resolveProjectIDFromQuery,
		}, true
	}

	if strings.HasPrefix(path, "/api/v1/domains/") {
		if strings.HasSuffix(path, "/application-repository/categories") {
			switch method {
			case http.MethodGet:
				return accessRequirement{
					GlobalRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
					DomainRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
					DomainLookup: resolveDomainIDFromPath,
				}, true
			case http.MethodPost:
				return accessRequirement{
					GlobalRoles:      []string{RoleProjectAdmin, RoleSystemAdmin},
					DomainRoles:      []string{RoleEditor, RoleProjectAdmin},
					DomainLookup:     resolveDomainIDFromPath,
					RepositoryRoles:  []string{RoleEditor, RoleProjectAdmin},
					RepositoryLookup: resolveRepositoryIDFromDomainPath,
				}, true
			}
		}
		if strings.HasSuffix(path, "/application-repository") {
			switch method {
			case http.MethodGet:
				return accessRequirement{
					GlobalRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
					DomainRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
					DomainLookup: resolveDomainIDFromPath,
				}, true
			case http.MethodPut:
				return accessRequirement{
					GlobalRoles:      []string{RoleProjectAdmin, RoleSystemAdmin},
					DomainRoles:      []string{RoleProjectAdmin},
					DomainLookup:     resolveDomainIDFromPath,
					RepositoryRoles:  []string{RoleProjectAdmin},
					RepositoryLookup: resolveRepositoryIDFromDomainPath,
				}, true
			}
		}
		switch method {
		case http.MethodGet:
			return accessRequirement{
				GlobalRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				DomainRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
				DomainLookup: resolveDomainIDFromPath,
			}, true
		case http.MethodPut, http.MethodDelete:
			return accessRequirement{GlobalRoles: []string{RoleSystemAdmin}}, true
		}
	}

	if strings.HasPrefix(path, "/api/v1/projects/") {
		switch method {
		case http.MethodGet:
			if strings.HasSuffix(path, "/releases") {
				return accessRequirement{
					GlobalRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
					ProjectRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin},
					ProjectLookup: func(r *http.Request, _ storage.Store) (string, int, string) {
						return normalizeProjectID(r.PathValue("id")), 0, ""
					},
				}, true
			}
			if strings.HasSuffix(path, "/folders") {
				return accessRequirement{
					GlobalRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
					ProjectRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin},
					ProjectLookup: func(r *http.Request, _ storage.Store) (string, int, string) {
						return normalizeProjectID(r.PathValue("pid")), 0, ""
					},
				}, true
			}
			return accessRequirement{
				GlobalRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin},
				ProjectLookup: func(r *http.Request, _ storage.Store) (string, int, string) {
					return normalizeProjectID(r.PathValue("id")), 0, ""
				},
			}, true
		case http.MethodPut, http.MethodDelete:
			return accessRequirement{
				GlobalRoles:  []string{RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles: []string{RoleProjectAdmin},
				ProjectLookup: func(r *http.Request, _ storage.Store) (string, int, string) {
					return normalizeProjectID(r.PathValue("id")), 0, ""
				},
			}, true
		case http.MethodPost:
			if strings.HasSuffix(path, "/releases") {
				return accessRequirement{
					GlobalRoles:      []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
					ProjectRoles:     []string{RoleEditor, RoleProjectAdmin},
					DomainRoles:      []string{RoleEditor, RoleProjectAdmin},
					RepositoryRoles:  []string{RoleEditor, RoleProjectAdmin},
					DomainLookup:     resolveProjectDomainIDFromPath,
					RepositoryLookup: resolveProjectRepositoryIDFromPath,
					ProjectLookup: func(r *http.Request, _ storage.Store) (string, int, string) {
						return normalizeProjectID(r.PathValue("id")), 0, ""
					},
				}, true
			}
			if strings.HasSuffix(path, "/folders") {
				return accessRequirement{
					GlobalRoles:  []string{RoleProjectAdmin, RoleSystemAdmin},
					ProjectRoles: []string{RoleProjectAdmin},
					ProjectLookup: func(r *http.Request, _ storage.Store) (string, int, string) {
						return normalizeProjectID(r.PathValue("pid")), 0, ""
					},
				}, true
			}
		}
	}

	if path == "/api/v1/applications" {
		switch method {
		case http.MethodGet:
			if strings.TrimSpace(r.URL.Query().Get("project_id")) == "" {
				return accessRequirement{GlobalRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}}, true
			}
			return accessRequirement{
				GlobalRoles:   []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveApplicationListProjectID,
			}, true
		case http.MethodPost:
			return accessRequirement{
				GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveApplicationCreateProjectID,
			}, true
		}
	}

	if r.PathValue("id") != "" && strings.HasPrefix(path, "/api/v1/applications/") {
		if strings.HasSuffix(path, "/releases") {
			switch method {
			case http.MethodGet:
				return accessRequirement{
					GlobalRoles:   []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
					ProjectRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
					ProjectLookup: resolveApplicationProjectID,
				}, true
			case http.MethodPost:
				return accessRequirement{
					GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
					ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
					ProjectLookup: resolveApplicationProjectID,
				}, true
			}
		}

		switch method {
		case http.MethodGet:
			return accessRequirement{
				GlobalRoles:   []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveApplicationProjectID,
			}, true
		case http.MethodPut, http.MethodDelete:
			return accessRequirement{
				GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveApplicationProjectID,
			}, true
		}
	}

	if r.PathValue("id") != "" && strings.HasPrefix(path, "/api/v1/releases/") {
		// Creating or deleting an SLA-breach waiver is a risk-acceptance
		// governance action that flips a failed release gate to "waived"; restrict
		// it to project admins (mirroring schedule approvals above). The generic
		// release-write rule below would otherwise let any project Editor mint one.
		if strings.Contains(path, "/sla-governance/waivers") {
			switch method {
			case http.MethodPost, http.MethodDelete:
				return accessRequirement{
					GlobalRoles:   []string{RoleProjectAdmin, RoleSystemAdmin},
					ProjectRoles:  []string{RoleProjectAdmin},
					ProjectLookup: resolveReleaseProjectID,
				}, true
			}
		}
		switch method {
		case http.MethodGet:
			return accessRequirement{
				GlobalRoles:   []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveReleaseProjectID,
			}, true
		case http.MethodPut, http.MethodPost, http.MethodDelete:
			return accessRequirement{
				GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveReleaseProjectID,
			}, true
		}
	}

	if path == "/api/v1/scripts" {
		switch method {
		case http.MethodGet:
			if strings.TrimSpace(r.URL.Query().Get("project_id")) == "" && !r.URL.Query().Has("folder_id") {
				return accessRequirement{GlobalRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}}, true
			}
			return accessRequirement{
				GlobalRoles:   []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveScriptListProjectID,
			}, true
		case http.MethodPost:
			return accessRequirement{
				GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveScriptCreateProjectID,
			}, true
		}
	}

	if path == "/api/v1/data-pools" {
		switch method {
		case http.MethodGet:
			if strings.TrimSpace(r.URL.Query().Get("project_id")) == "" {
				return accessRequirement{GlobalRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}}, true
			}
			return accessRequirement{
				GlobalRoles:   []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveDataPoolListProjectID,
			}, true
		case http.MethodPost:
			return accessRequirement{
				GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveDataPoolCreateProjectID,
			}, true
		}
	}

	if r.PathValue("id") != "" && strings.HasPrefix(path, "/api/v1/data-pools/") {
		switch method {
		case http.MethodGet:
			return accessRequirement{
				GlobalRoles:   []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveDataPoolProjectID,
			}, true
		case http.MethodPost, http.MethodPut, http.MethodDelete:
			return accessRequirement{
				GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveDataPoolProjectID,
			}, true
		}
	}

	if path == "/api/v1/tests" {
		switch method {
		case http.MethodGet:
			if strings.TrimSpace(r.URL.Query().Get("application_id")) != "" || strings.TrimSpace(r.URL.Query().Get("release_id")) != "" || strings.TrimSpace(r.URL.Query().Get("scenario_id")) != "" {
				return accessRequirement{
					GlobalRoles:   []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
					ProjectRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
					ProjectLookup: resolveTestContextProjectID,
				}, true
			}
			if strings.TrimSpace(r.URL.Query().Get("project_id")) == "" && !r.URL.Query().Has("folder_id") {
				return accessRequirement{GlobalRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}}, true
			}
			return accessRequirement{
				GlobalRoles:   []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveTestListProjectID,
			}, true
		case http.MethodPost:
			return accessRequirement{
				GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveTestCreateProjectID,
			}, true
		}
	}

	if path == "/api/v1/scenarios" {
		switch method {
		case http.MethodGet:
			if strings.TrimSpace(r.URL.Query().Get("project_id")) == "" {
				return accessRequirement{GlobalRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}}, true
			}
			return accessRequirement{
				GlobalRoles:   []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveScenarioListProjectID,
			}, true
		case http.MethodPost:
			return accessRequirement{
				GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveScenarioCreateProjectID,
			}, true
		}
	}

	if r.PathValue("id") != "" && strings.Contains(path, "/schedules/") && strings.Contains(path, "/approvals/") && method == http.MethodPost {
		return accessRequirement{
			GlobalRoles:   []string{RoleProjectAdmin, RoleSystemAdmin},
			ProjectRoles:  []string{RoleProjectAdmin},
			ProjectLookup: resolveScenarioProjectID,
		}, true
	}

	if r.PathValue("id") != "" && strings.HasPrefix(path, "/api/v1/scenarios/") {
		switch method {
		case http.MethodGet:
			return accessRequirement{
				GlobalRoles:   []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveScenarioProjectID,
			}, true
		case http.MethodPost:
			return accessRequirement{
				GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveScenarioProjectID,
			}, true
		case http.MethodPut, http.MethodDelete:
			return accessRequirement{
				GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveScenarioProjectID,
			}, true
		}
	}

	if r.PathValue("id") != "" && strings.HasPrefix(path, "/api/v1/tests/") {
		switch method {
		case http.MethodGet:
			return accessRequirement{
				GlobalRoles:   []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveTestProjectID,
			}, true
		case http.MethodPut:
			return accessRequirement{
				GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveTestProjectID,
			}, true
		case http.MethodDelete:
			return accessRequirement{
				GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveTestProjectID,
			}, true
		case http.MethodPatch:
			lookup := resolveTestProjectID
			if strings.HasSuffix(path, "/move") {
				lookup = resolveTestMoveProjectID
			}
			return accessRequirement{
				GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
				ProjectLookup: lookup,
			}, true
		case http.MethodPost:
			return accessRequirement{
				GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveTestProjectID,
			}, true
		}
	}

	if r.PathValue("id") != "" && strings.HasPrefix(path, "/api/v1/scripts/authoring/sessions/") {
		switch method {
		case http.MethodGet:
			return accessRequirement{
				GlobalRoles:   []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveAuthoringSessionProjectID,
			}, true
		case http.MethodPut, http.MethodDelete, http.MethodPost:
			return accessRequirement{
				GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveAuthoringSessionProjectID,
			}, true
		}
	}

	if r.PathValue("id") != "" && strings.HasPrefix(path, "/api/v1/scripts/") {
		switch method {
		case http.MethodGet:
			return accessRequirement{
				GlobalRoles:   []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveScriptProjectID,
			}, true
		case http.MethodPut, http.MethodDelete, http.MethodPatch:
			return accessRequirement{
				GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveScriptProjectID,
			}, true
		case http.MethodPost:
			if strings.HasSuffix(path, "/validate") || strings.HasSuffix(path, "/export") {
				return accessRequirement{
					GlobalRoles:   []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
					ProjectRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
					ProjectLookup: resolveScriptProjectID,
				}, true
			}
			return accessRequirement{
				GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveScriptProjectID,
			}, true
		}
	}

	return accessRequirement{}, false
}
