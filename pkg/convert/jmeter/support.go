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
		{ID: "jmeter-messaging-producers", Area: "Samplers", Construct: "DI-KafkaMeter and PNOP Event Hubs producers", Status: statusPartial, ExecutionStatus: execReviewRequired, ReviewRequired: true, Notes: "Native blocked drafts preserve payloads, Kafka property precedence, empty keys, Event Hubs message-list boundaries, scoped parameter declarations and throughput schedules. Kafka producers carry Java's effective settings: compression.type (none by default) becomes config.kafka.compression, and max.in.flight.requests.per.connection (5 by default) becomes config.kafka.producer.max_in_flight_requests for a non-idempotent producer, while an idempotent one keeps the client's own pipelining. batch.size and an empty client.id are disclosed, not applied: Java's batch size is an allocation target that APEX's batch limit cannot mirror, and APEX connects with its client's default id instead of Java's producer-N. A partitionString, literal or from a variable, selects that numeric partition at run time, and an Event Hubs connection string may end with an EntityPath that names the sampler's hub. Source-plan grouping and protected batch repairs preserve shared runtime ownership and reviewed revisions. Strict replay remains unavailable pending protocol and live conformance, and sampler schedules have no execution path yet; warning acknowledgement cannot enable replay."},
		{ID: "jmeter-throughput-shaping", Area: "Timers", Construct: "Throughput Shaping Timer 2.6 messaging schedules and HTTP pacing", Status: statusPartial, ExecutionStatus: execReviewRequired, ReviewRequired: true, Notes: "Piecewise rates, source identities, thread population and duration are retained in messaging scripts and scenario drafts; sampler scheduling and single-generator execution remain gated. In HTTP, JDBC and network thread groups, constant-rate profiles become group-wide TPS pacing at the most restrictive timer's rate per iteration; rows at 0 requests/s are valid; changing rates, constant 0-rate profiles, timers above the thread group and loop or conditional scopes gate the script until pacing is set explicitly. In messaging groups 0 requests/s rows are kept in the sampler schedule."},
		{ID: "jmeter-test-plan", Area: "Plan", Construct: "Test Plan", Status: statusSupported, ExecutionStatus: execExecutable, Notes: "Test Plan and Thread Groups become one APEX Load script per Thread Group; the scenario draft takes the Test Plan's name."},
		{ID: "jmeter-thread-group", Area: "Plan", Construct: "Thread Group", Status: statusSupported, ExecutionStatus: execExecutable, Fields: []string{"num_threads", "ramp_time", "duration", "loops", "on_sample_error", "same_user_on_next_iteration"}, Notes: "Finite unscheduled groups preserve per-user loops with iteration completion; scheduled groups retain duration and loop limits. Dynamic and mixed completion semantics require review. on_sample_error continue/startnextloop map to continue_on_error true/false; stopthread, stoptest and stoptestnow gate the script. Users, ramp-up, loops and schedule are kept in script metadata (source_population) for script-only imports, and the element's Comments in source_comments. Script names take the group's name, with a numeric suffix only when two groups' names collide. Run Thread Groups consecutively is reported because the scenario draft starts groups together."},
		{ID: "jmeter-csv-export", Area: "Reporting", Construct: "Recognized trace-ID/latency Groovy CSV writer", Status: statusSupported, ExecutionStatus: execExecutable, Notes: "Complete supported writer patterns become per-run archive CSV exports. Partial matches and other executable statements stay blocked."},
		{ID: "jmeter-formatted-time", Area: "Functions", Construct: "Formatted __time timestamps", Status: statusPartial, ExecutionStatus: execReviewRequired, ReviewRequired: true, Notes: "Supported formats use formatted parameters; literal Z needs explicit UTC or source-timezone review. Unsupported formats and timeShift remain gated."},
		{ID: "jmeter-http-defaults", Area: "Config", Construct: "HTTP Request Defaults", Status: statusSupported, ExecutionStatus: execExecutable, Notes: "Default protocol/domain/port/path seed the script base URL."},
		{ID: "jmeter-header-manager", Area: "Config", Construct: "HTTP Header Manager", Status: statusSupported, ExecutionStatus: execExecutable, Notes: "Header occurrence order, duplicate names, original casing, and a last-occurrence compatibility map are preserved."},
		{ID: "jmeter-auth-manager", Area: "Config", Construct: "HTTP Authorization Manager", Status: statusPartial, ExecutionStatus: execReviewRequired, ReviewRequired: true, Notes: "Basic, bearer, and digest are native. Kerberos, NTLM, negotiate, and unknown schemes produce valid review-gated drafts and are never approximated as basic."},
		{ID: "jmeter-cookie-manager", Area: "Config", Construct: "HTTP Cookie Manager", Status: statusSupported, ExecutionStatus: execExecutable, Notes: "Enables cookie handling on the generated HTTP config; clearEachIteration (or the thread group's same-user setting when the manager follows the thread group) maps to clear_cookies_per_iteration (not applied to SOAP-promoted scripts, which keep cookies across iterations; the import warns). Plans without a Cookie Manager import with cookies_enabled=false, as JMeter stores and sends no cookies without one."},
		{ID: "jmeter-cache-manager", Area: "Config", Construct: "HTTP Cache Manager", Status: statusSupported, ExecutionStatus: execExecutable},
		{ID: "jmeter-udv", Area: "Config", Construct: "User Defined Variables", Status: statusSupported, ExecutionStatus: execExecutable, Notes: "Values are expanded into the generated request URLs, bodies, headers and default headers at import; references in HTTP sampler parameters (query and form arguments) are not expanded, so those requests stay gated as using undeclared variables. Values do not become editable script parameters. Source review maps each User Defined Variables element."},
		{ID: "jmeter-counter", Area: "Data", Construct: "Counter / Random Variable", Status: statusPartial, ExecutionStatus: execExecutable, Notes: "Per-user counters map to native counters. Shared counters with an increment of 1 map to unique-number blocks per VUser; a bounded shared counter whose range holds fewer values than a literal thread count becomes a per-VUser counter that keeps start..end and wraps, so VUsers send the same values. With a thread count that is not a literal it keeps unique-number blocks, which can go past the source maximum. Shared counters with other increments stay gated. In messaging groups counters and Random Variables are exact source-engine sequences behind the messaging review gate; other groups do not convert Random Variables. Both count toward stats.variables."},
		{ID: "jmeter-csv", Area: "Data", Construct: "CSV Data Set Config", Status: statusSupported, ExecutionStatus: execExecutable, Notes: "Mapped to a data source; headers inferred from a sidecar .csv when present."},
		{ID: "jmeter-http-sampler", Area: "Samplers", Construct: "HTTP Request samplers", Status: statusSupported, ExecutionStatus: execExecutable, Fields: []string{"method", "domain", "path", "body", "headers"}, Notes: "The core executable mapping; method/URL/body/params/files/auth are converted. JMeter's client behaviour is matched: HTTP/1.1 is pinned (config.http.version 1.1), raw bodies keep their exact bytes, followed redirects cap at 20 (JMeter's httpsampler.max_redirects default), the scheme's default port is dropped from base_url, HTTP Request Defaults timeouts are inherited, and certificate verification is turned off to match JMeter's trust-all with a review warning. Empty timeouts keep APEX's 30 s connect / 60 s request defaults with an informational disclosure."},
		{ID: "jmeter-soap-sampler", Area: "Samplers", Construct: "SOAP-shaped HTTP Request samplers", Status: statusPartial, ExecutionStatus: execExecutable, Notes: "Thread groups that are entirely SOAP-oriented are promoted to the native SOAP protocol."},
		{ID: "jmeter-timers", Area: "Timing", Construct: "Constant / Uniform Random Timer", Status: statusSupported, ExecutionStatus: execExecutable, Notes: "Mapped to think time; throughput timers are approximated as fixed think time."},
		{ID: "jmeter-assertions", Area: "Assertions", Construct: "Response Assertions", Status: statusSupported, ExecutionStatus: execExecutable, Notes: "Mapped to status_code / body_contains / body_not_contains assertions."},
		{ID: "jmeter-extractors", Area: "Correlation", Construct: "Regex / JSON / JMESPath / CSS-JQuery / XPath / XPath2 / Boundary extractors", Status: statusSupported, ExecutionStatus: execExecutable, Notes: "Post-processors become extractors feeding {{var.*}} correlation."},
		{ID: "jmeter-if-controller", Area: "Control flow", Construct: "Simple If Controller", Status: statusSupported, ExecutionStatus: execExecutable, Notes: "Simple ${var}/==/!= conditions map to control_if; complex conditions are omitted and flattened behind a durable review gate."},
		{ID: "jmeter-loop-controller", Area: "Control flow", Construct: "Fixed Loop Controller", Status: statusSupported, ExecutionStatus: execExecutable, Notes: "Numeric loop counts map to control_loop; dynamic/infinite loops are omitted and flattened behind a durable review gate."},
		{ID: "jmeter-while-controller", Area: "Control flow", Construct: "While Controller", Status: statusPartial, ExecutionStatus: execExecutable, Notes: "Static conditions map to a condition-driven control_loop with a safety cap; other conditions are omitted and flattened behind a durable review gate."},
		{ID: "jmeter-once-only", Area: "Control flow", Construct: "Once Only Controller", Status: statusSupported, ExecutionStatus: execExecutable, Notes: "Children move into the script init section."},
		{ID: "jmeter-transaction-controller", Area: "Control flow", Construct: "Transaction Controller / Simple Controller", Status: statusSupported, ExecutionStatus: execExecutable, Notes: "Transaction names propagate as transaction labels on contained samplers; Simple Controllers only group. Config elements inside either stay scoped to its children."},
		{ID: "jmeter-complex-controllers", Area: "Control flow", Construct: "Foreach / Interleave / Random / Throughput controllers", Status: statusReviewOnly, ExecutionStatus: execReviewRequired, ReviewRequired: true, Notes: "Flattened to source order; original conditions/semantics are not reproduced."},
		{ID: "jmeter-jsr223", Area: "Scripting", Construct: "JSR223 / BeanShell / Groovy scripting", Status: statusPartial, ExecutionStatus: execReviewRequired, ReviewRequired: true, Notes: "Fully recognized safe single-statement patterns (vars.put, log.*) are translated; partial or unsupported scripts become review placeholders with original source text preserved."},
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
// distinct conversion outcome. Exports (a JSR223 CSV writer), generated
// parameters and dynamic functions feed a sampler or the run's results rather
// than being workload of their own, so they are contributors too: counting
// them let a plan whose only sampler is review-gated report one "mapped" item.
func categoryForSourceMapping(status, targetKind string) (string, bool) {
	switch targetKind {
	case "contributor", "config", "export", "parameter", "dynamic_function":
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
