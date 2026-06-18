package api

// Enterprise entitlements: a READ-ONLY projection of what the deployment is
// entitled to (edition, enabled protocol bundles, operational limits) and its
// current consumption. It surfaces existing data — system settings, the protocol
// bundle catalog, usage, project and service-account counts — and deliberately
// does NOT enforce anything; there is no licensing engine.

import (
	"net/http"
	"strings"
)

func (s *Server) handleGetEntitlements(w http.ResponseWriter, r *http.Request) {
	settings := s.runner.GetSystemSettings()
	bundles := protocolBundleEntitlements()
	bundleViews := make([]map[string]any, 0, len(bundles))
	for _, b := range bundles {
		bundleViews = append(bundleViews, map[string]any{
			"id":           b.ID,
			"display_name": b.DisplayName,
			"protocols":    b.Protocols,
			"default":      b.Default,
			"governance":   b.Governance,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"edition": "self-hosted",
		"limits": map[string]any{
			"max_concurrent_tests": settings.General.MaxConcurrentTests,
		},
		"protocol_bundles": map[string]any{
			"count":   len(bundleViews),
			"bundles": bundleViews,
		},
	})
}

func (s *Server) handleGetEntitlementsUsage(w http.ResponseWriter, r *http.Request) {
	settings := s.runner.GetSystemSettings()
	activeTests := 0
	for _, test := range s.runner.ListTests() {
		if test == nil {
			continue
		}
		if test.Status == "RUNNING" || test.Status == "INITIALIZING" {
			activeTests++
		}
	}
	serviceAccounts, projects := 0, 0
	if s.store != nil {
		if accounts, err := s.store.ListServiceAccounts(); err == nil {
			serviceAccounts = len(accounts)
		}
		if ps, err := s.store.ListProjects(); err == nil {
			projects = len(ps)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		// Back-compat top-level fields (EntitlementHeadroom relies on these).
		"concurrent_tests": map[string]any{
			"used":  activeTests,
			"limit": settings.General.MaxConcurrentTests,
		},
		"service_accounts": serviceAccounts,
		"projects":         projects,
		// Richer read-only breakdowns: per-limit, per-bundle consumption, per project.
		"limits":           s.globalEntitlementLimits(),
		"protocol_bundles": s.bundleConsumption(""),
		"by_project":       s.byProjectEntitlements(),
	})
}

// handleGetProjectEntitlementsUsage projects one project's entitlement limits and
// per-bundle consumption — the per-project scoping of the read-only entitlements
// surface (a project-scoped role can read its own project without global view).
func (s *Server) handleGetProjectEntitlementsUsage(w http.ResponseWriter, r *http.Request) {
	projectID := strings.TrimSpace(r.PathValue("id"))
	if projectID == "" {
		writeError(w, http.StatusBadRequest, "project id is required")
		return
	}
	name := ""
	if s.store != nil {
		if project, err := s.store.GetProject(normalizeProjectID(projectID)); err == nil && project != nil {
			name = project.Name
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"project_id":       normalizeProjectID(projectID),
		"name":             name,
		"limits":           s.projectEntitlementLimits(projectID),
		"protocol_bundles": s.bundleConsumption(projectID),
	})
}
