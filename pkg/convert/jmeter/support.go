package jmeter

import "github.com/jasonczech/apexload/pkg/convert/sourceview"

// support.go — structured JMeter import support matrix.
//
// JMeter historically exposed a flat SupportMatrix{Supported,Partial,Unsupported}.
// This file makes the structured []SupportMatrixEntry form (mirroring the
// AsyncAPI/k6/DevWeb importers, see pkg/convert/asyncapi/support.go) the single
// source of truth and derives the flat arrays from it for dashboard back-compat.

// Status values describe how completely a construct is converted.
const (
	statusSupported  = "supported"
	statusPartial    = "partial"
	statusReviewOnly = "review_only"
)

// ExecutionStatus values describe whether the converted output runs as-is.
// adapter_required is a forward-looking label: the construct has a native APEX
// Load equivalent, but it needs an operator-supplied adapter/provider (e.g. a
// concrete JMS provider) wired before the generated script can execute.
const (
	execExecutable      = "executable"
	execReviewRequired  = "review_required"
	execAdapterRequired = "adapter_required"
	execNotExecutable   = "not_executable"
)

// Conversion categories used by the converter's source mappings and stats to
// classify each element. These are the four buckets reported back to the user.
const (
	categoryMapped          = "mapped"
	categoryReviewRequired  = "review_required"
	categoryAdapterRequired = "adapter_required"
	categoryUnsupported     = "unsupported"
)

// SupportMatrixEntry describes coverage of one JMeter construct. Field shape is
// intentionally identical to asyncapi.SupportMatrixEntry so the dashboard and
// sibling importers share one rendering model.
type SupportMatrixEntry struct {
	ID              string   `json:"id"`
	Area            string   `json:"area"`
	Construct       string   `json:"construct"`
	Status          string   `json:"status"`
	ExecutionStatus string   `json:"execution_status"`
	ReviewRequired  bool     `json:"review_required,omitempty"`
	Fields          []string `json:"fields,omitempty"`
	Notes           string   `json:"notes,omitempty"`
}

// GetSupportMatrixEntries returns the structured JMeter import support matrix.
func GetSupportMatrixEntries() []SupportMatrixEntry {
	return []SupportMatrixEntry{
		{ID: "jmeter-test-plan", Area: "Plan", Construct: "Test Plan", Status: statusSupported, ExecutionStatus: execExecutable, Notes: "Test Plan and Thread Groups become one APEX Load script per Thread Group."},
		{ID: "jmeter-thread-group", Area: "Plan", Construct: "Thread Group", Status: statusSupported, ExecutionStatus: execExecutable, Fields: []string{"num_threads", "ramp_time", "duration"}, Notes: "Thread counts and ramp feed the optional scenario draft."},
		{ID: "jmeter-http-defaults", Area: "Config", Construct: "HTTP Request Defaults", Status: statusSupported, ExecutionStatus: execExecutable, Notes: "Default protocol/domain/port/path seed the script base URL."},
		{ID: "jmeter-header-manager", Area: "Config", Construct: "HTTP Header Manager", Status: statusSupported, ExecutionStatus: execExecutable, Notes: "Header occurrence order, duplicate names, original casing, and a last-occurrence compatibility map are preserved."},
		{ID: "jmeter-auth-manager", Area: "Config", Construct: "HTTP Authorization Manager", Status: statusPartial, ExecutionStatus: execReviewRequired, ReviewRequired: true, Notes: "Basic, bearer, and digest are native. Kerberos, NTLM, negotiate, and unknown schemes produce valid review-gated drafts and are never approximated as basic."},
		{ID: "jmeter-cookie-manager", Area: "Config", Construct: "HTTP Cookie Manager", Status: statusSupported, ExecutionStatus: execExecutable, Notes: "Enables cookie handling on the generated HTTP config."},
		{ID: "jmeter-cache-manager", Area: "Config", Construct: "HTTP Cache Manager", Status: statusSupported, ExecutionStatus: execExecutable},
		{ID: "jmeter-udv", Area: "Config", Construct: "User Defined Variables", Status: statusSupported, ExecutionStatus: execExecutable, Notes: "Become {{var.*}} template values."},
		{ID: "jmeter-csv", Area: "Data", Construct: "CSV Data Set Config", Status: statusSupported, ExecutionStatus: execExecutable, Notes: "Mapped to a data source; headers inferred from a sidecar .csv when present."},
		{ID: "jmeter-http-sampler", Area: "Samplers", Construct: "HTTP Request samplers", Status: statusSupported, ExecutionStatus: execExecutable, Fields: []string{"method", "domain", "path", "body", "headers"}, Notes: "The core executable mapping; method/URL/body/params/files/auth are converted."},
		{ID: "jmeter-soap-sampler", Area: "Samplers", Construct: "SOAP-shaped HTTP Request samplers", Status: statusPartial, ExecutionStatus: execExecutable, Notes: "Thread groups that are entirely SOAP-oriented are promoted to the native SOAP protocol."},
		{ID: "jmeter-timers", Area: "Timing", Construct: "Constant / Uniform Random Timer", Status: statusSupported, ExecutionStatus: execExecutable, Notes: "Mapped to think time; throughput timers are approximated as fixed think time."},
		{ID: "jmeter-assertions", Area: "Assertions", Construct: "Response Assertions", Status: statusSupported, ExecutionStatus: execExecutable, Notes: "Mapped to status_code / body_contains / body_not_contains assertions."},
		{ID: "jmeter-extractors", Area: "Correlation", Construct: "Regex / JSON / JMESPath / CSS-JQuery / XPath / XPath2 / Boundary extractors", Status: statusSupported, ExecutionStatus: execExecutable, Notes: "Post-processors become extractors feeding {{var.*}} correlation."},
		{ID: "jmeter-if-controller", Area: "Control flow", Construct: "Simple If Controller", Status: statusSupported, ExecutionStatus: execExecutable, Notes: "Simple ${var}/==/!= conditions map to control_if; complex conditions are omitted and flattened behind a durable review gate."},
		{ID: "jmeter-loop-controller", Area: "Control flow", Construct: "Fixed Loop Controller", Status: statusSupported, ExecutionStatus: execExecutable, Notes: "Numeric loop counts map to control_loop; dynamic/infinite loops are omitted and flattened behind a durable review gate."},
		{ID: "jmeter-while-controller", Area: "Control flow", Construct: "While Controller", Status: statusPartial, ExecutionStatus: execExecutable, Notes: "Static conditions map to a condition-driven control_loop with a safety cap; other conditions are omitted and flattened behind a durable review gate."},
		{ID: "jmeter-once-only", Area: "Control flow", Construct: "Once Only Controller", Status: statusSupported, ExecutionStatus: execExecutable, Notes: "Children move into the script init section."},
		{ID: "jmeter-transaction-controller", Area: "Control flow", Construct: "Transaction Controller", Status: statusSupported, ExecutionStatus: execExecutable, Notes: "Names propagate as transaction labels on contained samplers."},
		{ID: "jmeter-complex-controllers", Area: "Control flow", Construct: "Foreach / Interleave / Random / Throughput controllers", Status: statusReviewOnly, ExecutionStatus: execReviewRequired, ReviewRequired: true, Notes: "Flattened to source order; original conditions/semantics are not reproduced."},
		{ID: "jmeter-jsr223", Area: "Scripting", Construct: "JSR223 / BeanShell / Groovy scripting", Status: statusPartial, ExecutionStatus: execReviewRequired, ReviewRequired: true, Notes: "Fully recognized safe single-statement patterns (vars.put, log.*) are translated; partial or unsupported scripts become disclosure-safe review placeholders and source text is omitted."},
		{ID: "jmeter-jdbc", Area: "Samplers", Construct: "JDBC Request samplers", Status: statusPartial, ExecutionStatus: execAdapterRequired, ReviewRequired: true, Notes: "Database-homogeneous thread groups convert to db_query/db_exec; the connection (driver/DSN/credentials) is adapter_required and must be verified before running."},
		{ID: "jmeter-jms", Area: "Samplers", Construct: "JMS Publisher / Subscriber / point-to-point samplers", Status: statusReviewOnly, ExecutionStatus: execAdapterRequired, ReviewRequired: true, Notes: "Map to jms_publish/jms_consume but require selecting and configuring a JMS provider (jms-solace/jms-ibmmq/jms-servicebus) before running."},
		{ID: "jmeter-ftp-tcp", Area: "Samplers", Construct: "FTP / TCP samplers", Status: statusPartial, ExecutionStatus: execExecutable, Notes: "Network-homogeneous thread groups convert to ftp_command/tcp_request under the network protocol."},
		{ID: "jmeter-plugins", Area: "Plugins", Construct: "Third-party plugin samplers and controllers", Status: statusReviewOnly, ExecutionStatus: execNotExecutable, ReviewRequired: true, Notes: "Unknown plugin classes are preserved for review; there is no native equivalent wired."},
		{ID: "jmeter-listeners", Area: "Reporting", Construct: "Listeners (View Results Tree, Aggregate Report, Backend Listener, …)", Status: statusReviewOnly, ExecutionStatus: execNotExecutable, Notes: "Reporting-only elements do not affect replay and are preserved as metadata."},
	}
}

// GetSupportMatrixNotes returns top-level caveats for the JMeter importer.
func GetSupportMatrixNotes() []string {
	return []string{
		"JMeter import targets deterministic, natively-mappable constructs; HTTP plans convert most completely.",
		"Unsupported and review-gated constructs are reported in the import review instead of being silently dropped.",
		"SOAP-shaped HTTP samplers can be promoted into native APEX Load SOAP scripts.",
		"adapter_required marks constructs with a native equivalent that still needs an operator-supplied adapter or provider before execution.",
	}
}

// GetSupportMatrix returns the legacy flat support matrix, derived from the
// structured entries so the two never drift. Retained for the existing
// support-matrix endpoint and dashboard consumers.
func GetSupportMatrix() SupportMatrix {
	matrix := SupportMatrix{Notes: GetSupportMatrixNotes()}
	for _, entry := range GetSupportMatrixEntries() {
		switch entry.Status {
		case statusSupported:
			matrix.Supported = append(matrix.Supported, entry.Construct)
		case statusPartial:
			matrix.Partial = append(matrix.Partial, entry.Construct)
		default:
			matrix.Unsupported = append(matrix.Unsupported, entry.Construct)
		}
	}
	return matrix
}

// categoryForSourceMapping maps a source-mapping (status, targetKind) pair to one
// of the four reporting categories. The bool is false for informational mappings
// (contributors and ignored/diagnostic elements) that should not be counted as a
// distinct conversion outcome.
func categoryForSourceMapping(status, targetKind string) (string, bool) {
	switch targetKind {
	case "contributor", "config":
		return "", false
	}
	switch status {
	case "converted":
		return categoryMapped, true
	case "review_only":
		return categoryReviewRequired, true
	case categoryAdapterRequired:
		return categoryAdapterRequired, true
	case "unsupported":
		return categoryUnsupported, true
	default:
		// "ignored" / "diagnostic" and anything else are informational.
		return "", false
	}
}

// tallyJMeterCategories counts the four-way classification across source
// mappings, skipping informational entries (see categoryForSourceMapping).
func tallyJMeterCategories(mappings []sourceview.Mapping) (mapped, reviewRequired, adapterRequired int) {
	for _, mapping := range mappings {
		category, ok := categoryForSourceMapping(mapping.Status, mapping.TargetKind)
		if !ok {
			continue
		}
		switch category {
		case categoryMapped:
			mapped++
		case categoryReviewRequired:
			reviewRequired++
		case categoryAdapterRequired:
			adapterRequired++
		}
	}
	return mapped, reviewRequired, adapterRequired
}
