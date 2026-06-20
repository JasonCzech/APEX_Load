package enterprisearchive

// java_lr_coverage.go — the published LoadRunner Java Vuser lr API (lrapi.lr)
// coverage matrix. It joins the published method inventory with the registry
// resolution (family/support_mode, shared C baseline + Java supplement) and a
// code-owned target+notes map, backing both the generated evaluation doc
// (docs/LOADRUNNER_JAVA_LR_FUNCTION_COVERAGE.md, via cmd/webcoverage -set java)
// and the GET enterprise support-matrix endpoint.

//go:generate go run ../../../cmd/webcoverage -set java -out ../../../docs/LOADRUNNER_JAVA_LR_FUNCTION_COVERAGE.md

import (
	"fmt"
	"sort"
	"strings"
)

// javaLRPublishedMethods is the complete published Java Vuser lr API surface
// (OpenText VuGen 26.1 "Alphabetical Listing - Java Utility Functions"), as
// normalized lr_ names. 40 resolve through the shared C lr_* registry; the rest
// through the Java supplement. This slice is the authoritative inventory.
var javaLRPublishedMethods = []string{
	"lr_abort", "lr_advance_param", "lr_aws_get_secret", "lr_cyberark_get_vault",
	"lr_debug_message", "lr_decrypt", "lr_decrypt_ex", "lr_deserialize",
	"lr_enable_redirection", "lr_encrypt_ex", "lr_end_sub_transaction", "lr_end_timer",
	"lr_end_transaction", "lr_error_message", "lr_eval_data", "lr_eval_int",
	"lr_eval_string", "lr_exit", "lr_get_attrib_double", "lr_get_attrib_long",
	"lr_get_attrib_string", "lr_get_debug_message", "lr_get_group_name", "lr_get_host_name",
	"lr_get_master_host_name", "lr_get_object", "lr_get_scenario_id", "lr_get_transaction_duration",
	"lr_get_vuser_id", "lr_log", "lr_log_message", "lr_message", "lr_next_row",
	"lr_output_message", "lr_peek_events", "lr_read_file", "lr_read_xml", "lr_redirect",
	"lr_rendezvous", "lr_save_data", "lr_save_int", "lr_save_string", "lr_set_debug_message",
	"lr_set_transaction", "lr_set_transaction_status", "lr_start_sub_transaction", "lr_start_timer",
	"lr_start_transaction", "lr_think_time", "lr_unmask", "lr_user_data_point",
	"lr_value_check", "lr_vuser_status_message",
}

// JavaLRFunctionCoverage is one row of the Java lr API coverage matrix. The Method
// field is the Java call form (e.g. lr.start_transaction).
type JavaLRCoverage struct {
	Method      string `json:"method"`
	Function    string `json:"function"`
	Family      string `json:"family"`
	SupportMode string `json:"support_mode"`
	Target      string `json:"target"`
	Notes       string `json:"notes"`
}

var javaLRCoverageDetail = map[string]webCoverageDetail{
	"lr_abort":                    {"source_runtime action", "Abort the Vuser."},
	"lr_advance_param":            {"source_parameter action", "Advance a parameter to the next value."},
	"lr_aws_get_secret":           {"source_external action", "AWS Secrets Manager fetch; needs an external secret provider."},
	"lr_cyberark_get_vault":       {"source_external action", "CyberArk vault fetch; needs an external secret provider."},
	"lr_debug_message":            {"source_message action", "Conditional debug log message."},
	"lr_decrypt":                  {"import review", "Machine-key-bound ciphertext; not recoverable at import."},
	"lr_decrypt_ex":               {"import review", "Machine-key-bound ciphertext; not recoverable at import."},
	"lr_deserialize":              {"import review", "Java object deserialization has no DSL equivalent."},
	"lr_enable_redirection":       {"native no-op", "Java stdout/stderr log redirection; no replay effect."},
	"lr_encrypt_ex":               {"import review", "Encryption helper; preserved for review."},
	"lr_end_sub_transaction":      {"source_transaction action", "Closes a sub-transaction; feeds transaction markers."},
	"lr_end_timer":                {"source_timer action", "Stops a named timer."},
	"lr_end_transaction":          {"source_transaction action", "Closes a transaction; feeds Stats.Transactions."},
	"lr_error_message":            {"error_message action", "Native error-message action."},
	"lr_eval_data":                {"import review", "Returns byte[]; no DSL equivalent."},
	"lr_eval_int":                 {"source_parameter action", "Java typed eval of a parameter to int."},
	"lr_eval_string":              {"source_parameter action", "Evaluates {param} → {{var.*}}."},
	"lr_exit":                     {"source_runtime action", "Exit the Vuser/iteration."},
	"lr_get_attrib_double":        {"import review", "Command-line attribute read; RTS-specific."},
	"lr_get_attrib_long":          {"import review", "Command-line attribute read; RTS-specific."},
	"lr_get_attrib_string":        {"import review", "Command-line attribute read; RTS-specific."},
	"lr_get_debug_message":        {"source_message action", "Reads the debug message level."},
	"lr_get_group_name":           {"source_parameter action", "Informational getter (Vuser group)."},
	"lr_get_host_name":            {"source_parameter action", "Informational getter (host)."},
	"lr_get_master_host_name":     {"source_parameter action", "Informational getter (master host)."},
	"lr_get_object":               {"import review", "Returns a Java runtime object; no DSL equivalent."},
	"lr_get_scenario_id":          {"source_parameter action", "Informational getter (scenario id)."},
	"lr_get_transaction_duration": {"source_transaction action", "Reads a transaction's duration."},
	"lr_get_vuser_id":             {"source_parameter action", "Informational getter (Vuser id)."},
	"lr_log":                      {"source_message action", "General Java log message."},
	"lr_log_message":              {"source_message action", "Log-only message."},
	"lr_message":                  {"source_message action", "Output-window message."},
	"lr_next_row":                 {"source_parameter action", "Advance a data file to the next row."},
	"lr_output_message":           {"source_message action", "Output message with location."},
	"lr_peek_events":              {"native no-op", "Runtime event queue not exposed; no-op."},
	"lr_read_file":                {"import review", "Local file read; preserved for review."},
	"lr_read_xml":                 {"import review", "XML file read; preserved for review."},
	"lr_redirect":                 {"native no-op", "Output redirection; no replay effect."},
	"lr_rendezvous":               {"rendezvous action", "Native rendezvous point."},
	"lr_save_data":                {"import review", "Saves byte[]; no DSL equivalent."},
	"lr_save_int":                 {"source_parameter action", "Saves an int to a parameter."},
	"lr_save_string":              {"source_parameter action", "Saves a string to a parameter."},
	"lr_set_debug_message":        {"source_message action", "Sets the debug message level."},
	"lr_set_transaction":          {"source_transaction action", "Programmatic transaction with duration/status."},
	"lr_set_transaction_status":   {"source_transaction action", "Sets the current transaction status."},
	"lr_start_sub_transaction":    {"source_transaction action", "Opens a sub-transaction."},
	"lr_start_timer":              {"source_timer action", "Starts a named timer."},
	"lr_start_transaction":        {"source_transaction action", "Opens a transaction; feeds Stats.Transactions + grouping."},
	"lr_think_time":               {"config.think_time distribution", "Feeds the generated think-time distribution."},
	"lr_unmask":                   {"import review", "Machine-key-bound masked value; not recoverable at import."},
	"lr_user_data_point":          {"source_metric action", "Custom data point."},
	"lr_value_check":              {"import review", "Value verification has no positional DSL assertion target."},
	"lr_vuser_status_message":     {"source_message action", "Vuser status-area message."},
}

// JavaLRFunctionCoverage returns the full published Java lr API coverage matrix,
// sorted by Java method name.
func JavaLRFunctionCoverage() []JavaLRCoverage {
	rows := make([]JavaLRCoverage, 0, len(javaLRPublishedMethods))
	for _, name := range javaLRPublishedMethods {
		def, _ := lookupSourceFunction(name)
		detail := javaLRCoverageDetail[name]
		rows = append(rows, JavaLRCoverage{
			Method:      "lr." + strings.TrimPrefix(name, "lr_"),
			Function:    name,
			Family:      def.Family,
			SupportMode: string(def.SupportMode),
			Target:      detail.target,
			Notes:       detail.notes,
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Method < rows[j].Method })
	return rows
}

// RenderJavaLRFunctionCoverageMarkdown renders the committed evaluation document.
func RenderJavaLRFunctionCoverageMarkdown() string {
	rows := JavaLRFunctionCoverage()

	counts := map[string]int{}
	for _, row := range rows {
		counts[row.SupportMode]++
	}

	var b strings.Builder
	b.WriteString("# LoadRunner Java Vuser `lr` API Coverage\n\n")
	b.WriteString("<!-- Generated by `go generate ./pkg/convert/enterprisearchive` (cmd/webcoverage -set java). Do not edit by hand. -->\n\n")
	b.WriteString("Evaluation of the complete published OpenText/Micro Focus VuGen 26.1 Java Vuser ")
	b.WriteString("**lr API** (the `lrapi.lr` class) against the APEX Load conversion engine ")
	b.WriteString("(`pkg/convert/enterprisearchive`). Source: VuGen Function Reference, ")
	b.WriteString("\"Alphabetical Listing - Java Utility Functions\".\n\n")
	b.WriteString("A Java Vuser script is arbitrary Java, not a closed library — so this matrix covers the ")
	b.WriteString("**closed `lr` API** only. Everything else (custom logic, RMI/EJB, HTTP idioms) is routed ")
	b.WriteString("to the governed `java_vuser_run` sidecar that executes the real Java, or preserved for review.\n\n")

	fmt.Fprintf(&b, "**Total published lr methods:** %d (40 shared with the C `lr_*` library, 13 Java-only)\n\n", len(rows))
	b.WriteString("| Disposition | Count | Meaning |\n")
	b.WriteString("|---|---|---|\n")
	fmt.Fprintf(&b, "| `executable` | %d | Translated to an executable DSL action or config mapping. |\n", counts["executable"])
	fmt.Fprintf(&b, "| `native_noop` | %d | Preserved as a no-op (no replay effect in APEX Load). |\n", counts["native_noop"])
	fmt.Fprintf(&b, "| `review_only` | %d | Preserved in the import-review artifact; no APEX equivalent, not executed. |\n", counts["review_only"])
	fmt.Fprintf(&b, "| `external_required` | %d | Needs an external provider (secret manager) before replay. |\n\n", counts["external_required"])

	b.WriteString("Every published lr method resolves to one of the dispositions above — none falls through to a `custom_call` review stub. Enforced by `TestJavaLRResolvesEveryPublishedFunction`.\n\n")

	b.WriteString("| Java method | Function | Family | Support mode | Target | Notes |\n")
	b.WriteString("|---|---|---|---|---|---|\n")
	for _, row := range rows {
		fmt.Fprintf(&b, "| `%s` | `%s` | %s | `%s` | %s | %s |\n",
			row.Method, row.Function, row.Family, row.SupportMode, row.Target, row.Notes)
	}
	return b.String()
}
