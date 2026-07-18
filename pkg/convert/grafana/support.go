package grafana

// SupportMatrixEntry describes one class of Grafana dashboard construct and how the
// importer handles it. It backs the .../import/grafana/support-matrix endpoint.
type SupportMatrixEntry struct {
	ID        string   `json:"id"`
	Area      string   `json:"area"`
	Construct string   `json:"construct"`
	Status    string   `json:"status"`
	Fields    []string `json:"fields,omitempty"`
	Notes     string   `json:"notes,omitempty"`
}

func GetSupportMatrix() []SupportMatrixEntry {
	return []SupportMatrixEntry{
		{ID: "grafana-timeseries", Area: "Panels", Construct: "timeseries / graph", Status: "supported", Fields: []string{"targets.expr", "legendFormat", "fieldConfig.defaults.unit"}, Notes: "Rendered as APEX line-chart cards driven by the panel's PromQL targets."},
		{ID: "grafana-stat", Area: "Panels", Construct: "stat / gauge", Status: "supported", Fields: []string{"targets.expr", "fieldConfig.defaults.unit"}, Notes: "Rendered as a single-value tile from the newest sample; exactly one visible target and one returned series are required."},
		{ID: "grafana-rows", Area: "Layout", Construct: "row (collapsed and expanded)", Status: "supported", Notes: "Row children are lifted into the flat panel list; the row title is retained as provenance."},
		{ID: "grafana-vars-time", Area: "Variables", Construct: "$__interval / $__range / $__rate_interval", Status: "supported", Notes: "Resolved from the query window at request time. $__rate_interval is approximated (max(4×step,60s))."},
		{ID: "grafana-vars-input", Area: "Variables", Construct: "query / custom / textbox", Status: "partial", Notes: "Require an explicit binding at commit; option lists are surfaced for review but not queried."},
		{ID: "grafana-vars-multi", Area: "Variables", Construct: "multi / includeAll / adhoc / format modifiers", Status: "unsupported", Notes: "Rewrite the PromQL matcher operator or escaping and cannot be reproduced without a PromQL parser."},
		{ID: "grafana-datasource", Area: "Datasource", Construct: "prometheus datasource reference", Status: "supported", Notes: "All panels query the single APEX-configured Prometheus; non-Prometheus datasources are excluded."},
		{ID: "grafana-query-scope", Area: "Security", Construct: "run/project label scope", Status: "required", Notes: "Every imported query must contain an exact test_id=$test_id or project_id=$project_id label matcher."},
		{ID: "grafana-other-panels", Area: "Panels", Construct: "table / heatmap / piechart / logs / etc.", Status: "unsupported", Notes: "Reported explicitly at import; APEX renders line charts and single-value tiles only."},
	}
}

func GetSupportMatrixNotes() []string {
	return []string{
		"Grafana import maps panels onto APEX graph cards; it does not run Grafana or reproduce its rendering pipeline.",
		"Panel PromQL runs against the Prometheus configured in Settings → Integrations, not a per-dashboard datasource.",
		"APEX rejects unscoped PromQL and verifies a supplied test_id belongs to the authorized project before querying.",
		"Unsupported panel types and variables are surfaced in the import review rather than dropped silently.",
	}
}
