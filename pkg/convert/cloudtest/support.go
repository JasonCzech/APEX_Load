// Package cloudtest converts Akamai CloudTest repository exports into
// disclosure-safe APEX Load review assets.
package cloudtest

// Disposition is the required outcome assigned to every source element.
type Disposition string

const (
	DispositionExecutableExact   Disposition = "executable_exact"
	DispositionExecutableBounded Disposition = "executable_bounded"
	DispositionBlockedDraft      Disposition = "blocked_draft"
	DispositionRejected          Disposition = "rejected"
)

// SupportEntry describes one proposed CloudTest-to-APEX mapping. FixtureProven
// deliberately remains false until a sanitized, dependency-complete export is
// checked into the golden-fixture suite.
type SupportEntry struct {
	ID                  string      `json:"id"`
	Area                string      `json:"area"`
	CloudTestConstruct  string      `json:"cloudtest_construct"`
	Construct           string      `json:"construct"`
	APEXMapping         string      `json:"apex_mapping"`
	IntendedDisposition Disposition `json:"intended_disposition"`
	FixtureProven       bool        `json:"fixture_proven"`
	Status              string      `json:"status"`
	ExecutionStatus     string      `json:"execution_status"`
	ReviewRequired      bool        `json:"review_required"`
	Notes               string      `json:"notes"`
}

// GetSupportMatrix returns code-owned guidance. It describes intended mapping,
// not executable coverage; the importer itself stays fail-closed while the
// fixture gate is unmet.
func GetSupportMatrix() []SupportEntry {
	entries := []SupportEntry{
		{ID: "cloudtest-clip", Area: "Composition", CloudTestConstruct: "Clip", APEXMapping: "one reusable APEX script referenced by scenario groups", IntendedDisposition: DispositionExecutableBounded, Status: "provisional", Notes: "No runtime call_script is introduced; nested launch semantics remain blocked until fixture-proven."},
		{ID: "cloudtest-composition", Area: "Composition", CloudTestConstruct: "Composition / Track / Distribution", APEXMapping: "scenario draft with ordered groups, active-time offsets, load, ramp, pacing, and load zone", IntendedDisposition: DispositionExecutableBounded, Status: "provisional", Notes: "Attribute names and repeat-VU semantics require real export fixtures."},
		{ID: "cloudtest-hierarchy", Area: "Composition", CloudTestConstruct: "Chain / Group / Transaction", APEXMapping: "ordered actions with typed hierarchy and source-transaction attribution", IntendedDisposition: DispositionExecutableBounded, Status: "provisional", Notes: "Hierarchy is preserved as review evidence until fixture paths are proven."},
		{ID: "cloudtest-condition", Area: "Control flow", CloudTestConstruct: "If / Switch", APEXMapping: "control_if / ordered first-match control_switch", IntendedDisposition: DispositionExecutableBounded, Status: "provisional", Notes: "Expression translation is never guessed."},
		{ID: "cloudtest-burst", Area: "Control flow", CloudTestConstruct: "Burst", APEXMapping: "feature-gated control_parallel when every child is supported and parallel-safe", IntendedDisposition: DispositionBlockedDraft, Status: "planned", Notes: "Native control_parallel is available; CloudTest Burst and source repeat wire semantics remain blocked until sanitized export fixtures prove the mapping."},
		{ID: "cloudtest-page", Area: "Web", CloudTestConstruct: "Page", APEXMapping: "HTTP document action plus existing static/dynamic resource loading and page hierarchy", IntendedDisposition: DispositionExecutableBounded, Status: "provisional", Notes: "No duplicate page transport action is created."},
		{ID: "cloudtest-http", Area: "Protocols", CloudTestConstruct: "HTTP / HTTPS", APEXMapping: "native HTTP action", IntendedDisposition: DispositionExecutableExact, Status: "provisional", Notes: "Target, validation, extraction, body, and authentication wire shapes require fixtures."},
		{ID: "cloudtest-soap", Area: "Protocols", CloudTestConstruct: "SOAP", APEXMapping: "native SOAP action", IntendedDisposition: DispositionExecutableBounded, Status: "provisional", Notes: "Exactness depends on binding/style and attachment evidence."},
		{ID: "cloudtest-messaging", Area: "Protocols", CloudTestConstruct: "JMS / WebSocket", APEXMapping: "native JMS or WebSocket actions when the source adapter contract is exact", IntendedDisposition: DispositionExecutableBounded, Status: "provisional", Notes: "Provider-specific options require fixtures and capability admission."},
		{ID: "cloudtest-database", Area: "Protocols", CloudTestConstruct: "Database", APEXMapping: "native database actions", IntendedDisposition: DispositionExecutableBounded, Status: "provisional", Notes: "Transaction ownership and parameter direction must be proven."},
		{ID: "cloudtest-browser", Area: "Browser", CloudTestConstruct: "Browser action", APEXMapping: "observer/canary browser script with runtime-readiness gates", IntendedDisposition: DispositionBlockedDraft, Status: "provisional", Notes: "Never promoted into bulk VUser traffic automatically."},
		{ID: "cloudtest-touchtest", Area: "Mobile", CloudTestConstruct: "TouchTest AppAction", APEXMapping: "disclosure-safe review artifact", IntendedDisposition: DispositionBlockedDraft, Status: "non_goal", Notes: "Native mobile automation is intentionally outside APEX product fit."},
		{ID: "cloudtest-javascript", Area: "Extensibility", CloudTestConstruct: "arbitrary JavaScript", APEXMapping: "disclosure-safe review artifact", IntendedDisposition: DispositionBlockedDraft, Status: "non_goal", Notes: "Imported JavaScript is never executable by default."},
		{ID: "cloudtest-targets", Area: "Configuration", CloudTestConstruct: "Target / Property", APEXMapping: "environment, parameter, variable, and secret-reference drafts", IntendedDisposition: DispositionBlockedDraft, Status: "provisional", Notes: "Sensitive values are not copied into review evidence."},
		{ID: "cloudtest-seed-data", Area: "Data", CloudTestConstruct: "Seed data", APEXMapping: "project data-pool draft", IntendedDisposition: DispositionBlockedDraft, Status: "provisional", Notes: "Only names and source paths are surfaced until column semantics are fixture-proven."},
		{ID: "cloudtest-monitor", Area: "Monitoring", CloudTestConstruct: "Monitor", APEXMapping: "project monitor-source draft when a supported connector exists", IntendedDisposition: DispositionBlockedDraft, Status: "planned", Notes: "Unsupported definitions remain preserved review artifacts."},
		{ID: "cloudtest-dashboard", Area: "Analytics", CloudTestConstruct: "Dashboard", APEXMapping: "preserved review artifact, later translated to DashboardDefinition", IntendedDisposition: DispositionBlockedDraft, Status: "planned", Notes: "No React render functions or arbitrary queries are imported."},
		{ID: "cloudtest-hds", Area: "Media", CloudTestConstruct: "HDS", APEXMapping: "preserved review evidence", IntendedDisposition: DispositionBlockedDraft, Status: "demand_gated", Notes: "Runtime HDS remains demand-gated as a legacy Adobe workflow."},
	}
	for index := range entries {
		entries[index].Construct = entries[index].CloudTestConstruct
		entries[index].ExecutionStatus = "not_executable"
		entries[index].ReviewRequired = true
	}
	return entries
}

func GetSupportMatrixNotes() []string {
	return []string{
		"All mappings are provisional until sanitized CloudTest XML/ZIP golden fixtures prove element and attribute wire shapes.",
		"Inspection classifies every XML element; recognized and unknown elements are never silently dropped.",
		"Current drafts are execution-blocked and use the generic validate/commit-batch workflow.",
		"The importer does not add call_script or a duplicate page transport action.",
	}
}
