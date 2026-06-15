package api

// Enterprise entitlements: a READ-ONLY projection of what the deployment is
// entitled to (edition, enabled protocol bundles, operational limits) and its
// current consumption. It surfaces existing data — system settings, the protocol
// bundle catalog, usage, project and service-account counts — and deliberately
// does NOT enforce anything; there is no licensing engine.

import "net/http"

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
		"concurrent_tests": map[string]any{
			"used":  activeTests,
			"limit": settings.General.MaxConcurrentTests,
		},
		"service_accounts": serviceAccounts,
		"projects":         projects,
	})
}
