package api

// Read-only entitlement usage projection. It combines existing aggregators — the
// live test snapshot, per-project quota config, usage accounting, and the static
// protocol-bundle catalog — into per-limit, per-bundle, and per-project
// consumption breakdowns. It surfaces consumption-vs-limits and deliberately does
// NOT enforce anything; there is no licensing engine.

import "strings"

// entitlementLimitView is a single limit projected as current-vs-cap. Limit 0
// means "no cap configured" (unlimited / not enforced).
type entitlementLimitView struct {
	Key         string  `json:"key"` // concurrent_tests | active_vusers
	Label       string  `json:"label"`
	Scope       string  `json:"scope"` // global | project
	Used        int     `json:"used"`
	Limit       int     `json:"limit"`
	Remaining   int     `json:"remaining"`
	Utilization float64 `json:"utilization"` // used/limit in [0,1]; 0 when unlimited
	Unlimited   bool    `json:"unlimited"`
}

// entitlementBundleConsumption is historical run accounting attributed to a
// protocol bundle (summed over the bundle's protocols).
type entitlementBundleConsumption struct {
	ID           string   `json:"id"`
	DisplayName  string   `json:"display_name"`
	Protocols    []string `json:"protocols"`
	RunCount     int      `json:"run_count"`
	TotalVUsers  int64    `json:"total_vusers"`
	VUserSeconds int64    `json:"vuser_seconds"`
}

// projectEntitlementView is one project's per-limit consumption.
type projectEntitlementView struct {
	ProjectID string                 `json:"project_id"`
	Name      string                 `json:"name,omitempty"`
	Limits    []entitlementLimitView `json:"limits"`
}

func makeEntitlementLimitView(key, label, scope string, used, limit int) entitlementLimitView {
	v := entitlementLimitView{Key: key, Label: label, Scope: scope, Used: used, Limit: limit}
	if limit <= 0 {
		v.Limit = 0
		v.Unlimited = true
		return v
	}
	if remaining := limit - used; remaining > 0 {
		v.Remaining = remaining
	}
	v.Utilization = float64(used) / float64(limit)
	return v
}

// activeTestStats returns active (RUNNING/INITIALIZING) test and VUser counts. An
// empty projectID aggregates across all projects.
func (s *Server) activeTestStats(projectID string) (tests int, vusers int) {
	all := strings.TrimSpace(projectID) == ""
	want := normalizeProjectID(projectID)
	for _, test := range s.runner.ListTests() {
		if test == nil {
			continue
		}
		if !all && normalizeProjectID(test.ProjectID) != want {
			continue
		}
		if test.Status == "RUNNING" || test.Status == "INITIALIZING" {
			tests++
			vusers += test.TotalVUsers
		}
	}
	return tests, vusers
}

// globalEntitlementLimits projects the deployment-wide limits. Active VUsers has
// no global cap, so it is shown as unlimited (used-only, for visibility).
func (s *Server) globalEntitlementLimits() []entitlementLimitView {
	settings := s.runner.GetSystemSettings()
	tests, vusers := s.activeTestStats("")
	return []entitlementLimitView{
		makeEntitlementLimitView("concurrent_tests", "Concurrent tests", "global", tests, settings.General.MaxConcurrentTests),
		makeEntitlementLimitView("active_vusers", "Active VUsers", "global", vusers, 0),
	}
}

// projectEntitlementLimits projects one project's limits from its quota config.
func (s *Server) projectEntitlementLimits(projectID string) []entitlementLimitView {
	tests, vusers := s.activeTestStats(projectID)
	return []entitlementLimitView{
		makeEntitlementLimitView("concurrent_tests", "Concurrent tests", "project", tests, s.runner.projectMaxConcurrentTests(projectID)),
		makeEntitlementLimitView("active_vusers", "Active VUsers", "project", vusers, s.runner.projectMaxVUsers(projectID)),
	}
}

// bundleConsumption attributes historical run accounting to each protocol bundle.
// An empty projectID aggregates across all projects.
func (s *Server) bundleConsumption(projectID string) []entitlementBundleConsumption {
	usage := s.usageAccounting(projectID)
	byProtocol := make(map[string]UsageAccountingBucket, len(usage.ByProtocol))
	for _, b := range usage.ByProtocol {
		byProtocol[b.Key] = b
	}
	bundles := protocolBundleEntitlements()
	out := make([]entitlementBundleConsumption, 0, len(bundles))
	for _, bundle := range bundles {
		c := entitlementBundleConsumption{ID: bundle.ID, DisplayName: bundle.DisplayName, Protocols: bundle.Protocols}
		for _, proto := range bundle.Protocols {
			if b, ok := byProtocol[proto]; ok {
				c.RunCount += b.RunCount
				c.TotalVUsers += b.TotalVUsers
				c.VUserSeconds += b.VUserSeconds
			}
		}
		out = append(out, c)
	}
	return out
}

// projectEntitlementView projects one project's per-limit consumption.
func (s *Server) projectEntitlementView(projectID, name string) projectEntitlementView {
	return projectEntitlementView{
		ProjectID: normalizeProjectID(projectID),
		Name:      name,
		Limits:    s.projectEntitlementLimits(projectID),
	}
}

// byProjectEntitlements projects every project's per-limit consumption for the
// system-wide usage view.
func (s *Server) byProjectEntitlements() []projectEntitlementView {
	out := make([]projectEntitlementView, 0)
	if s.store == nil {
		return out
	}
	projects, err := s.store.ListProjects()
	if err != nil {
		return out
	}
	for _, p := range projects {
		if p == nil {
			continue
		}
		out = append(out, s.projectEntitlementView(p.ID, p.Name))
	}
	return out
}
