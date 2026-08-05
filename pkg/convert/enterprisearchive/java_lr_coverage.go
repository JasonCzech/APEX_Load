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
// normalized lr_ names. Shared names resolve through the C lr_* registry; Java
// aliases and Java-only methods resolve through the Java supplement. This slice
// is the authoritative inventory used by coverage tests and docs.
var javaLRPublishedMethods = []string{
	"lr_abort", "lr_advance_param", "lr_aws_get_secret", "lr_cyberark_get_vault",
	"lr_debug_message", "lr_decrypt", "lr_decrypt_ex", "lr_deserialize",
	"lr_enable_redirection", "lr_encrypt_ex", "lr_end_sub_trans", "lr_end_sub_transaction",
	"lr_end_timer", "lr_end_transaction", "lr_error_message", "lr_eval_data", "lr_eval_int",
	"lr_eval_string", "lr_exit", "lr_get_attrib_double", "lr_get_attrib_long",
	"lr_get_attrib_string", "lr_get_debug_message", "lr_get_group_name", "lr_get_host_name",
	"lr_get_master_host_name", "lr_get_object", "lr_get_scenario_id", "lr_get_transaction_duration",
	"lr_get_transaction_status", "lr_get_transaction_think_time", "lr_get_transaction_wasted_time",
	"lr_get_vuser_id", "lr_log", "lr_log_message", "lr_message", "lr_next_row",
	"lr_output_message", "lr_peek_events", "lr_read_file", "lr_read_xml", "lr_redirect",
	"lr_rendezvous", "lr_save_data", "lr_save_int", "lr_save_string", "lr_set_debug_message",
	"lr_set_transaction", "lr_set_transaction_status", "lr_sleep", "lr_start_sub_transaction",
	"lr_start_timer", "lr_start_transaction", "lr_think_time", "lr_unmask", "lr_user_data_point",
	"lr_value_check", "lr_vuser_status_message", "lr_wasted_time",
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
	"lr_abort":                       {"source_runtime action", "Abort the Vuser."},
	"lr_advance_param":               {"import review", "Parameter cursor advancement is preserved for review; the native replay engine does not advance VuGen parameter tables."},
	"lr_aws_get_secret":              {"source_external action", "AWS Secrets Manager fetch; needs an external secret provider."},
	"lr_cyberark_get_vault":          {"source_external action", "CyberArk vault fetch; needs an external secret provider."},
	"lr_debug_message":               {"source_message action", "Conditional debug log message."},
	"lr_decrypt":                     {"import review", "Machine-key-bound ciphertext; not recoverable at import."},
	"lr_decrypt_ex":                  {"import review", "Machine-key-bound ciphertext; not recoverable at import."},
	"lr_deserialize":                 {"import review", "Java object deserialization has no DSL equivalent."},
	"lr_enable_redirection":          {"native no-op", "Java stdout/stderr log redirection; no replay effect."},
	"lr_encrypt_ex":                  {"import review", "Encryption helper; preserved for review."},
	"lr_end_sub_trans":               {"source_transaction action", "Closes a Java sub-transaction alias."},
	"lr_end_sub_transaction":         {"source_transaction action", "Closes a sub-transaction; feeds transaction markers."},
	"lr_end_timer":                   {"source_timer action", "Stops a named timer."},
	"lr_end_transaction":             {"source_transaction action", "Closes a transaction; feeds Stats.Transactions."},
	"lr_error_message":               {"error_message action", "Native error-message action."},
	"lr_eval_data":                   {"import review", "Returns byte[]; no DSL equivalent."},
	"lr_eval_int":                    {"import review", "Java typed parameter evaluation returns a value to user code; native replay does not model that return channel."},
	"lr_eval_string":                 {"source_parameter action", "Evaluates {param} → {{var.*}}."},
	"lr_exit":                        {"source_runtime action", "Exit the Vuser/iteration."},
	"lr_get_attrib_double":           {"import review", "Command-line attribute read; RTS-specific."},
	"lr_get_attrib_long":             {"import review", "Command-line attribute read; RTS-specific."},
	"lr_get_attrib_string":           {"import review", "Command-line attribute read; RTS-specific."},
	"lr_get_debug_message":           {"source_message action", "Reads the debug message level."},
	"lr_get_group_name":              {"import review", "Java informational getter returns a value to user code; native replay does not model that return channel."},
	"lr_get_host_name":               {"source_parameter action", "Informational getter (host)."},
	"lr_get_master_host_name":        {"source_parameter action", "Informational getter (master host)."},
	"lr_get_object":                  {"import review", "Returns a Java runtime object; no DSL equivalent."},
	"lr_get_scenario_id":             {"import review", "Java informational getter returns a value to user code; native replay does not model that return channel."},
	"lr_get_transaction_duration":    {"source_transaction action", "Reads a transaction's duration."},
	"lr_get_transaction_status":      {"source_transaction action", "Reads a transaction's status."},
	"lr_get_transaction_think_time":  {"source_transaction action", "Reads a transaction's think time."},
	"lr_get_transaction_wasted_time": {"source_transaction action", "Reads a transaction's wasted time."},
	"lr_get_vuser_id":                {"import review", "Java informational getter returns a value to user code; native replay does not model that return channel."},
	"lr_log":                         {"source_message action", "General Java log message."},
	"lr_log_message":                 {"source_message action", "Log-only message."},
	"lr_message":                     {"source_message action", "Output-window message."},
	"lr_next_row":                    {"import review", "Data-file row advancement is preserved for review; the native replay engine does not advance VuGen data cursors."},
	"lr_output_message":              {"source_message action", "Output message with location."},
	"lr_peek_events":                 {"native no-op", "Runtime event queue not exposed; no-op."},
	"lr_read_file":                   {"import review", "Local file read; preserved for review."},
	"lr_read_xml":                    {"import review", "XML file read; preserved for review."},
	"lr_redirect":                    {"native no-op", "Output redirection; no replay effect."},
	"lr_rendezvous":                  {"rendezvous action", "Native rendezvous point."},
	"lr_save_data":                   {"import review", "Saves byte[]; no DSL equivalent."},
	"lr_save_int":                    {"source_parameter action", "Saves an int to a parameter."},
	"lr_save_string":                 {"source_parameter action", "Saves a string to a parameter."},
	"lr_set_debug_message":           {"source_message action", "Sets the debug message level."},
	"lr_set_transaction":             {"source_transaction action", "Programmatic transaction with duration/status."},
	"lr_set_transaction_status":      {"source_transaction action", "Sets the current transaction status."},
	"lr_sleep":                       {"source_runtime action", "Sleeps the Java Vuser thread."},
	"lr_start_sub_transaction":       {"source_transaction action", "Opens a sub-transaction."},
	"lr_start_timer":                 {"source_timer action", "Starts a named timer."},
	"lr_start_transaction":           {"source_transaction action", "Opens a transaction; feeds Stats.Transactions + grouping."},
	"lr_think_time":                  {"config.think_time distribution", "Feeds the generated think-time distribution."},
	"lr_unmask":                      {"import review", "Machine-key-bound masked value; not recoverable at import."},
	"lr_user_data_point":             {"source_metric action", "Custom data point."},
	"lr_value_check":                 {"import review", "Value verification has no positional DSL assertion target."},
	"lr_vuser_status_message":        {"source_message action", "Vuser status-area message."},
	"lr_wasted_time":                 {"source_transaction action", "Records wasted time."},
}

// javaLRRunnerOverride captures places where the combined Java sidecar's
// runtime contract differs from the shared C/native-conversion registry. The
// registry remains the source of truth for native DSL conversion; combined
// Java imports and their published coverage must instead describe what the
// governed lrapi.lr shim can faithfully do inside the persistent JVM.
type javaLRRunnerOverride struct {
	mode  SourceFunctionSupportMode
	notes string
}

var javaLRRunnerOverrides = map[string]javaLRRunnerOverride{
	"lr_abort": {
		mode:  SourceFunctionSupportReviewOnly,
		notes: "The shim can fail and unwind the current Java invocation, but the engine does not yet prove LoadRunner's abort-VUser and teardown semantics; reachable calls block promotion.",
	},
	"lr_exit": {
		mode:  SourceFunctionSupportReviewOnly,
		notes: "The shim can report and unwind an lr.exit call, but the runner/engine contract does not yet faithfully implement every LoadRunner action, iteration, and VUser continuation option; reachable calls block promotion.",
	},
	"lr_rendezvous": {
		mode:  SourceFunctionSupportReviewOnly,
		notes: "The shim records bounded rendezvous evidence after the Java invocation; it does not synchronously join the engine's cross-VUser rendezvous barrier.",
	},
	"lr_user_data_point": {
		mode:  SourceFunctionSupportReviewOnly,
		notes: "The shim records a bounded metric event in protocol evidence, but the runner does not yet publish it as an engine or telemetry metric sample.",
	},
	"lr_get_transaction_duration": {
		mode:  SourceFunctionSupportExecutable,
		notes: "The combined Java runner returns the tracked transaction duration through the verified lrapi descriptor.",
	},
	"lr_get_transaction_status": {
		mode:  SourceFunctionSupportExecutable,
		notes: "The combined Java runner returns the tracked transaction status through the verified lrapi descriptor.",
	},
	"lr_get_transaction_think_time": {
		mode:  SourceFunctionSupportExecutable,
		notes: "The combined Java runner returns the tracked transaction think time through the verified lrapi descriptor.",
	},
	"lr_get_transaction_wasted_time": {
		mode:  SourceFunctionSupportExecutable,
		notes: "The combined Java runner returns tracked transaction wasted time through the verified lrapi descriptor.",
	},
	"lr_wasted_time": {
		mode:  SourceFunctionSupportExecutable,
		notes: "The combined Java runner records wasted time on open transactions through the verified lrapi descriptor.",
	},
}

// javaLRRunnerABIDescriptors is the exact published Java bytecode surface that
// the shim implements and that the combined importer is therefore allowed to
// call executable. A source-level Object... convenience overload is not enough:
// operator-provisioned Actions.class files were compiled against OpenText's
// lrapi jar and link by exact JVM descriptor.
var javaLRRunnerABIDescriptors = map[string]string{
	"lr_end_sub_trans":               "(Ljava/lang/String;I)I",
	"lr_end_sub_transaction":         "(Ljava/lang/String;I)I",
	"lr_end_transaction":             "(Ljava/lang/String;I)I",
	"lr_eval_string":                 "(Ljava/lang/String;)Ljava/lang/String;",
	"lr_get_transaction_duration":    "(Ljava/lang/String;)D",
	"lr_get_transaction_status":      "(Ljava/lang/String;)I",
	"lr_get_transaction_think_time":  "(Ljava/lang/String;)D",
	"lr_get_transaction_wasted_time": "(Ljava/lang/String;)D",
	"lr_save_int":                    "(ILjava/lang/String;)I",
	"lr_save_string":                 "(Ljava/lang/String;Ljava/lang/String;)I",
	"lr_set_transaction":             "(Ljava/lang/String;DI)I",
	"lr_set_transaction_status":      "(I)I",
	"lr_start_sub_transaction":       "(Ljava/lang/String;Ljava/lang/String;)I",
	"lr_start_transaction":           "(Ljava/lang/String;)I",
	"lr_think_time":                  "(D)V",
	"lr_wasted_time":                 "(I)I",
}

func javaLRRunnerDisposition(name string) (SourceFunctionDefinition, string, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	def, ok := lookupSourceFunction(name)
	if !ok {
		return SourceFunctionDefinition{}, "", false
	}
	notes := javaLRCoverageDetail[name].notes
	if override, exists := javaLRRunnerOverrides[name]; exists {
		def.SupportMode = override.mode
		if strings.TrimSpace(override.notes) != "" {
			notes = override.notes
		}
	}
	if def.SupportMode != SourceFunctionSupportReviewOnly {
		if _, verified := javaLRRunnerABIDescriptors[name]; !verified {
			def.SupportMode = SourceFunctionSupportReviewOnly
			notes = "The source is preserved, but the combined shim does not advertise an exact verified OpenText lrapi bytecode descriptor for this method; a reachable call blocks promotion to avoid NoSuchMethodError."
		}
	}
	if strings.TrimSpace(notes) == "" {
		notes = "Preserved in the combined Java Vuser source; verify the governed runner behavior before approval."
	}
	return def, notes, true
}

func javaLRRunnerCallDisposition(fn ParsedFunction) (SourceFunctionDefinition, string, bool) {
	def, notes, known := javaLRRunnerDisposition(fn.Name)
	if !known || !strings.EqualFold(strings.TrimSpace(fn.Name), "lr_exit") {
		return def, notes, known
	}
	if javaLRRunnerSupportsExitCall(fn) {
		def.SupportMode = SourceFunctionSupportExecutable
		return def, "The exact two-argument lrapi descriptor is verified. EXIT_ACTION_AND_CONTINUE and EXIT_ITERATION_AND_CONTINUE unwind the current persistent phase, retain the per-VUser Actions instance, emit failed child evidence when requested, and allow the next engine iteration.", true
	}
	return def, notes, true
}

func javaLRRunnerSupportsExitCall(fn ParsedFunction) bool {
	if len(fn.RawArgs) != 2 {
		return false
	}
	option := strings.ReplaceAll(strings.TrimSpace(fn.RawArgs[0]), " ", "")
	option = strings.TrimPrefix(option, "lrapi.")
	switch option {
	case "lr.EXIT_ACTION_AND_CONTINUE",
		"lr.EXIT_ITERATION_AND_CONTINUE",
		"EXIT_ACTION_AND_CONTINUE",
		"EXIT_ITERATION_AND_CONTINUE":
		return true
	default:
		return false
	}
}

func javaLRRunnerTarget(mode SourceFunctionSupportMode) string {
	switch mode {
	case SourceFunctionSupportExecutable:
		return "combined Java runner shim"
	case SourceFunctionSupportNativeNoop:
		return "combined Java runner no-op"
	case SourceFunctionSupportExternalRequired:
		return "combined runner + governed external provider"
	default:
		return "authoring/fidelity review"
	}
}

func javaLRPublishedTarget(name string, mode SourceFunctionSupportMode) string {
	if target := strings.TrimSpace(javaLRCoverageDetail[name].target); target != "" {
		return target
	}
	return javaLRRunnerTarget(mode)
}

// JavaLRFunctionCoverage returns the full published Java lr API coverage matrix,
// sorted by Java method name.
func JavaLRFunctionCoverage() []JavaLRCoverage {
	rows := make([]JavaLRCoverage, 0, len(javaLRPublishedMethods))
	for _, name := range javaLRPublishedMethods {
		def, notes, _ := javaLRRunnerDisposition(name)
		rows = append(rows, JavaLRCoverage{
			Method:      "lr." + strings.TrimPrefix(name, "lr_"),
			Function:    name,
			Family:      def.Family,
			SupportMode: string(def.SupportMode),
			Target:      javaLRPublishedTarget(name, def.SupportMode),
			Notes:       notes,
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
	b.WriteString("**closed `lr` API** only. Java-over-HTTP, JMS, JDBC, SOAP, and no-runtime Java middleware ")
	b.WriteString("families are reported separately by the enterprise support-matrix endpoint. Every family remains ")
	b.WriteString("inside one combined `java_vuser` draft; these rows are review/runtime-shim dispositions, not native lane extraction. ")
	b.WriteString("Launch requires authoring approval and an enabled governed JVM runner.\n\n")

	fmt.Fprintf(&b, "**Total tracked lr methods:** %d (shared C `lr_*` baseline plus Java-only/alias supplement)\n\n", len(rows))
	b.WriteString("| Disposition | Count | Meaning |\n")
	b.WriteString("|---|---|---|\n")
	fmt.Fprintf(&b, "| `executable` | %d | The persistent Java runner/shim has a meaningful runtime implementation for the call. |\n", counts["executable"])
	fmt.Fprintf(&b, "| `native_noop` | %d | The call reaches the shim but is intentionally a no-op in APEX Load; the source mapping states that explicitly. |\n", counts["native_noop"])
	fmt.Fprintf(&b, "| `review_only` | %d | Source and any bounded diagnostic event are preserved, but faithful LoadRunner semantics are not verified; a reachable call blocks promotion. |\n", counts["review_only"])
	fmt.Fprintf(&b, "| `external_required` | %d | Needs an external provider (secret manager) before replay. |\n\n", counts["external_required"])

	b.WriteString("Every published lr method resolves to one of the dispositions above — none falls through to a `custom_call` review stub. ")
	b.WriteString("These are Java-runner dispositions, including explicit overrides where the shared native-conversion registry would overstate sidecar behavior. ")
	b.WriteString("A reachable `review_only` call is a fidelity blocker, while an unused helper remains review evidence only.\n\n")

	b.WriteString("| Java method | Function | Family | Support mode | Target | Notes |\n")
	b.WriteString("|---|---|---|---|---|---|\n")
	for _, row := range rows {
		fmt.Fprintf(&b, "| `%s` | `%s` | %s | `%s` | %s | %s |\n",
			row.Method, row.Function, row.Family, row.SupportMode, row.Target, row.Notes)
	}
	return b.String()
}
