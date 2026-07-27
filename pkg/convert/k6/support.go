package k6

type SupportMatrixEntry struct {
	ID              string   `json:"id"`
	Area            string   `json:"area"`
	Construct       string   `json:"construct"`
	Status          string   `json:"status"`
	ExecutionStatus string   `json:"execution_status"`
	ReviewRequired  bool     `json:"review_required"`
	Fields          []string `json:"fields,omitempty"`
	Notes           string   `json:"notes,omitempty"`
}

func GetSupportMatrix() []SupportMatrixEntry {
	return []SupportMatrixEntry{
		{ID: "k6-http", Area: "HTTP", Construct: "http.get/post/put/patch/delete/del/head/options", Status: "supported", ExecutionStatus: "executable", Fields: []string{"url", "method", "headers", "body"}, Notes: "Converted into APEX Load HTTP request actions only when wire-affecting arguments are proven string literals."},
		{ID: "k6-options", Area: "Runtime", Construct: "options.vus/duration/stages", Status: "partial", ExecutionStatus: "review_required", ReviewRequired: true, Fields: []string{"vus", "duration", "stages"}, Notes: "Preserved as metadata for scenario review; APEX scenario sizing remains the execution source of truth."},
		{ID: "k6-checks", Area: "Assertions", Construct: "check()", Status: "partial", ExecutionStatus: "not_executable", ReviewRequired: true, Notes: "Detected and durably gated; recreate important checks as APEX Load assertions before execution."},
		{ID: "k6-thresholds", Area: "Release Gates", Construct: "options.thresholds", Status: "partial", ExecutionStatus: "not_executable", ReviewRequired: true, Notes: "Detected, preserved as evidence, and durably gated; map to APEX goal_config/SLA governance before execution."},
		{ID: "k6-browser", Area: "Browser", Construct: "k6/browser", Status: "review_only", ExecutionStatus: "not_executable", ReviewRequired: true, Notes: "Use APEX browser canary authoring instead; browser scripts are not converted into protocol-load scripts."},
		{ID: "k6-javascript", Area: "JavaScript Runtime", Construct: "modules, loops, branches, dynamic expressions", Status: "review_only", ExecutionStatus: "not_executable", ReviewRequired: true, Notes: "APEX Load does not run k6 JavaScript; structural review evidence is retained, requests are durably gated, and residual source text is omitted from public results."},
	}
}

func GetSupportMatrixNotes() []string {
	return []string{
		"k6 compatibility is import/export and result ingestion only; APEX Load does not execute k6 JavaScript.",
		"The supported import subset is intentionally constrained to statically parseable HTTP calls.",
		"Browser scripts, custom modules, and dynamic JavaScript flow should remain review-only.",
	}
}
