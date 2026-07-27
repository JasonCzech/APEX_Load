package enterprisearchive

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

// publishedJavaLRFunctionExpectations pins the shared registry baseline of every
// method in the published OpenText/Micro Focus VuGen 26.1 Java Vuser lr API (the
// lrapi.lr class, "Alphabetical Listing - Java Utility Functions"). Shared names
// resolve through the native C lr_* registry; Java aliases and Java-only methods
// live in the supplement. Combined-runner overrides are tested separately so the
// native C runtime can stay honest without downgrading faithfully implemented
// Java sidecar calls.
var publishedJavaLRFunctionExpectations = map[string]struct {
	family string
	mode   SourceFunctionSupportMode
}{
	// --- shared with the C lr_* baseline (40) ---
	"lr_abort":                      {"runtime", SourceFunctionSupportExecutable},
	"lr_advance_param":              {"parameter", SourceFunctionSupportReviewOnly},
	"lr_aws_get_secret":             {"secret", SourceFunctionSupportExternalRequired},
	"lr_cyberark_get_vault":         {"secret", SourceFunctionSupportExternalRequired},
	"lr_debug_message":              {"message", SourceFunctionSupportExecutable},
	"lr_decrypt":                    {"parameter", SourceFunctionSupportReviewOnly},
	"lr_decrypt_ex":                 {"parameter", SourceFunctionSupportReviewOnly},
	"lr_encrypt_ex":                 {"parameter", SourceFunctionSupportReviewOnly},
	"lr_end_sub_transaction":        {"transaction", SourceFunctionSupportExecutable},
	"lr_end_timer":                  {"timer", SourceFunctionSupportExecutable},
	"lr_end_transaction":            {"transaction", SourceFunctionSupportExecutable},
	"lr_error_message":              {"message", SourceFunctionSupportExecutable},
	"lr_eval_string":                {"parameter", SourceFunctionSupportExecutable},
	"lr_exit":                       {"runtime", SourceFunctionSupportExecutable},
	"lr_get_attrib_double":          {"attribute", SourceFunctionSupportReviewOnly},
	"lr_get_attrib_long":            {"attribute", SourceFunctionSupportReviewOnly},
	"lr_get_attrib_string":          {"attribute", SourceFunctionSupportReviewOnly},
	"lr_get_debug_message":          {"message", SourceFunctionSupportExecutable},
	"lr_get_host_name":              {"info", SourceFunctionSupportExecutable},
	"lr_get_master_host_name":       {"info", SourceFunctionSupportExecutable},
	"lr_get_transaction_status":     {"transaction", SourceFunctionSupportReviewOnly},
	"lr_get_transaction_duration":   {"transaction", SourceFunctionSupportReviewOnly},
	"lr_get_transaction_think_time": {"transaction", SourceFunctionSupportReviewOnly},
	"lr_log_message":                {"message", SourceFunctionSupportExecutable},
	"lr_message":                    {"message", SourceFunctionSupportExecutable},
	"lr_next_row":                   {"parameter", SourceFunctionSupportReviewOnly},
	"lr_output_message":             {"message", SourceFunctionSupportExecutable},
	"lr_peek_events":                {"runtime", SourceFunctionSupportNativeNoop},
	"lr_read_file":                  {"parameter", SourceFunctionSupportReviewOnly},
	"lr_rendezvous":                 {"runtime", SourceFunctionSupportExecutable},
	"lr_save_int":                   {"parameter", SourceFunctionSupportExecutable},
	"lr_save_string":                {"parameter", SourceFunctionSupportExecutable},
	"lr_set_debug_message":          {"message", SourceFunctionSupportExecutable},
	"lr_set_transaction":            {"transaction", SourceFunctionSupportExecutable},
	"lr_set_transaction_status":     {"transaction", SourceFunctionSupportExecutable},
	"lr_start_sub_transaction":      {"transaction", SourceFunctionSupportExecutable},
	"lr_start_timer":                {"timer", SourceFunctionSupportExecutable},
	"lr_start_transaction":          {"transaction", SourceFunctionSupportExecutable},
	"lr_think_time":                 {"runtime", SourceFunctionSupportExecutable},
	"lr_unmask":                     {"parameter", SourceFunctionSupportReviewOnly},
	"lr_user_data_point":            {"metric", SourceFunctionSupportExecutable},
	"lr_vuser_status_message":       {"message", SourceFunctionSupportExecutable},
	"lr_wasted_time":                {"transaction", SourceFunctionSupportReviewOnly},
	// --- Java-only/alias supplement ---
	"lr_deserialize":                 {"parameter", SourceFunctionSupportReviewOnly},
	"lr_enable_redirection":          {"runtime", SourceFunctionSupportNativeNoop},
	"lr_end_sub_trans":               {"transaction", SourceFunctionSupportExecutable},
	"lr_eval_data":                   {"parameter", SourceFunctionSupportReviewOnly},
	"lr_eval_int":                    {"parameter", SourceFunctionSupportReviewOnly},
	"lr_get_group_name":              {"info", SourceFunctionSupportReviewOnly},
	"lr_get_object":                  {"parameter", SourceFunctionSupportReviewOnly},
	"lr_get_scenario_id":             {"info", SourceFunctionSupportReviewOnly},
	"lr_get_transaction_wasted_time": {"transaction", SourceFunctionSupportExecutable},
	"lr_get_vuser_id":                {"info", SourceFunctionSupportReviewOnly},
	"lr_log":                         {"message", SourceFunctionSupportExecutable},
	"lr_read_xml":                    {"xml", SourceFunctionSupportReviewOnly},
	"lr_redirect":                    {"runtime", SourceFunctionSupportNativeNoop},
	"lr_save_data":                   {"parameter", SourceFunctionSupportReviewOnly},
	"lr_sleep":                       {"runtime", SourceFunctionSupportExecutable},
	"lr_value_check":                 {"parameter", SourceFunctionSupportReviewOnly},
}

// javaOnlyLRSupplement is the subset expected to live in the Java supplement file
// (everything not in the C baseline).
var javaOnlyLRSupplement = map[string]struct {
	family string
	mode   SourceFunctionSupportMode
}{
	"lr_deserialize":                 {"parameter", SourceFunctionSupportReviewOnly},
	"lr_enable_redirection":          {"runtime", SourceFunctionSupportNativeNoop},
	"lr_end_sub_trans":               {"transaction", SourceFunctionSupportExecutable},
	"lr_eval_data":                   {"parameter", SourceFunctionSupportReviewOnly},
	"lr_eval_int":                    {"parameter", SourceFunctionSupportReviewOnly},
	"lr_get_group_name":              {"info", SourceFunctionSupportReviewOnly},
	"lr_get_object":                  {"parameter", SourceFunctionSupportReviewOnly},
	"lr_get_scenario_id":             {"info", SourceFunctionSupportReviewOnly},
	"lr_get_transaction_wasted_time": {"transaction", SourceFunctionSupportExecutable},
	"lr_get_vuser_id":                {"info", SourceFunctionSupportReviewOnly},
	"lr_log":                         {"message", SourceFunctionSupportExecutable},
	"lr_read_xml":                    {"xml", SourceFunctionSupportReviewOnly},
	"lr_redirect":                    {"runtime", SourceFunctionSupportNativeNoop},
	"lr_save_data":                   {"parameter", SourceFunctionSupportReviewOnly},
	"lr_sleep":                       {"runtime", SourceFunctionSupportExecutable},
	"lr_value_check":                 {"parameter", SourceFunctionSupportReviewOnly},
}

// TestJavaLRRegistryMatchesPublishedLibrary pins the resolution of every published
// Java lr method to the expected family/mode, and pins the supplement set exactly.
func TestJavaLRRegistryMatchesPublishedLibrary(t *testing.T) {
	for name, want := range publishedJavaLRFunctionExpectations {
		def, ok := lookupSourceFunction(name)
		if !ok {
			t.Errorf("Java lr method %q does not resolve in any registry", name)
			continue
		}
		if def.Family != want.family || def.SupportMode != want.mode {
			t.Errorf("%q resolves as (%s,%s), want (%s,%s)", name, def.Family, def.SupportMode, want.family, want.mode)
		}
	}

	supplement := allJavaLRSupplementDefinitions()
	if len(supplement) != len(javaOnlyLRSupplement) {
		t.Fatalf("Java supplement has %d functions, want %d", len(supplement), len(javaOnlyLRSupplement))
	}
	for _, def := range supplement {
		want, ok := javaOnlyLRSupplement[def.Name]
		if !ok {
			t.Errorf("supplement function %q is not in the expected Java-only set", def.Name)
			continue
		}
		if def.Family != want.family || def.SupportMode != want.mode {
			t.Errorf("supplement %q = (%s,%s), want (%s,%s)", def.Name, def.Family, def.SupportMode, want.family, want.mode)
		}
	}
}

// TestJavaLRPublishedInventoriesAgree keeps the two hand-maintained published-lr
// inventories in lockstep: the coverage slice javaLRPublishedMethods (which backs
// the coverage doc + support-matrix endpoint) must be exactly the set pinned by
// publishedJavaLRFunctionExpectations (the disposition pin table). Drift would let
// a method be documented-but-unpinned, or pinned-but-undocumented, without notice.
func TestJavaLRPublishedInventoriesAgree(t *testing.T) {
	sliceSet := make(map[string]bool, len(javaLRPublishedMethods))
	for _, name := range javaLRPublishedMethods {
		if sliceSet[name] {
			t.Errorf("javaLRPublishedMethods contains duplicate %q", name)
		}
		sliceSet[name] = true
	}
	for name := range publishedJavaLRFunctionExpectations {
		if !sliceSet[name] {
			t.Errorf("%q is pinned in publishedJavaLRFunctionExpectations but missing from javaLRPublishedMethods (coverage doc would omit it)", name)
		}
	}
	for name := range sliceSet {
		if _, ok := publishedJavaLRFunctionExpectations[name]; !ok {
			t.Errorf("%q is in javaLRPublishedMethods but not pinned in publishedJavaLRFunctionExpectations (disposition unpinned)", name)
		}
	}
}

// TestJavaLRSupplementDisjointFromCRegistry guarantees the supplement only holds
// genuine Java-only methods — a name in both registries with a different mode
// would make the pin table silently lie (C wins in lookupSourceFunction).
func TestJavaLRSupplementDisjointFromCRegistry(t *testing.T) {
	cNames := make(map[string]bool)
	for _, def := range allSourceFunctionDefinitions() {
		cNames[def.Name] = true
	}
	for _, def := range allJavaLRSupplementDefinitions() {
		if cNames[def.Name] {
			t.Errorf("Java supplement function %q duplicates a C baseline entry; remove it from the supplement", def.Name)
		}
	}
}

// convertJavaLRBody exercises the retained native-analysis pass directly. That
// pass supplies review evidence and transaction detection to the combined
// importer, but is no longer an executable import path.
func convertJavaLRBody(t *testing.T, body string) *ConvertResult {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	addJavaZipFile(t, zw, "JavaLR/JavaLR.usr", "[General]\nType=Java Vuser\nScriptLanguage=JAVA\n[Actions]\nAction=Actions.java\n")
	addJavaZipFile(t, zw, "JavaLR/Actions.java", "import lrapi.lr;\npublic class Actions {\n  public int action() throws Throwable {\n    "+body+"\n    return 0;\n  }\n}\n")
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	result, err := convertZipJavaNativeAnalysisForTest(buf.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip: %v", err)
	}
	return result
}

// convertCombinedJavaLRBody runs the production singleton Java import path.
// Coverage dispositions must describe the governed shim, not the retained
// native-analysis implementation used only to derive review evidence.
func convertCombinedJavaLRBody(t *testing.T, body string) *ConvertResult {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	addJavaZipFile(t, zw, "JavaLR/JavaLR.usr", "[General]\nType=Java Vuser\nScriptLanguage=JAVA\n[Actions]\nAction=Actions.java\n")
	addJavaZipFile(t, zw, "JavaLR/Actions.java", "import lrapi.lr;\npublic class Actions {\n  public int action() throws Throwable {\n    "+body+"\n    return 0;\n  }\n}\n")
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	result, err := ConvertZip(buf.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip: %v", err)
	}
	return result
}

// TestJavaLRResolvesEveryPublishedFunction is the closure backbone: every published
// Java lr method, driven through the real Java import path, must classify to a
// concrete disposition (never a custom_call stub) with the combined runner's
// descriptor- and semantics-aware support mode.
func TestJavaLRResolvesEveryPublishedFunction(t *testing.T) {
	for name := range publishedJavaLRFunctionExpectations {
		name := name
		t.Run(name, func(t *testing.T) {
			call := "lr." + strings.TrimPrefix(name, "lr_") + "(\"arg\");"
			result := convertCombinedJavaLRBody(t, call)
			if result.Stats.CustomCallFunctions != 0 {
				t.Fatalf("%s produced %d custom_call stubs, want 0", name, result.Stats.CustomCallFunctions)
			}
			if result.Stats.Skipped != 0 || len(result.Stats.UnsupportedFuncs) != 0 {
				t.Fatalf("%s skipped=%d unsupported=%v, want none", name, result.Stats.Skipped, result.Stats.UnsupportedFuncs)
			}
			if result.Script != nil {
				for _, action := range result.Script.Actions {
					if action.Type == "custom_call" {
						t.Fatalf("%s emitted a custom_call action: %#v", name, action)
					}
				}
			}
			if result.SourceReview == nil {
				t.Fatalf("%s: missing source review", name)
			}
			want, _, known := javaLRRunnerCallDisposition(ParsedFunction{
				Name:    name,
				RawArgs: []string{`"arg"`},
			})
			if !known {
				t.Fatalf("%s: missing runner disposition", name)
			}
			assertSourceReviewSupportMode(t, result.SourceReview.Mappings, name, want.SupportMode)
		})
	}
}

func TestJavaTransactionValueRunnerOverridesRemainExecutable(t *testing.T) {
	tests := []struct {
		function string
		call     string
		baseMode SourceFunctionSupportMode
	}{
		{"lr_get_transaction_duration", `lr.get_transaction_duration("Checkout");`, SourceFunctionSupportReviewOnly},
		{"lr_get_transaction_status", `lr.get_transaction_status("Checkout");`, SourceFunctionSupportReviewOnly},
		{"lr_get_transaction_think_time", `lr.get_transaction_think_time("Checkout");`, SourceFunctionSupportReviewOnly},
		{"lr_get_transaction_wasted_time", `lr.get_transaction_wasted_time("Checkout");`, SourceFunctionSupportExecutable},
		{"lr_wasted_time", `lr.wasted_time(250);`, SourceFunctionSupportReviewOnly},
	}

	for _, test := range tests {
		t.Run(test.function, func(t *testing.T) {
			base, ok := lookupSourceFunction(test.function)
			if !ok || base.SupportMode != test.baseMode {
				t.Fatalf("base registry = (%#v, %v), want %s", base, ok, test.baseMode)
			}
			runner, notes, ok := javaLRRunnerDisposition(test.function)
			if !ok || runner.SupportMode != SourceFunctionSupportExecutable {
				t.Fatalf("Java runner disposition = (%#v, %q, %v), want executable override", runner, notes, ok)
			}
			if descriptor := javaLRRunnerABIDescriptors[test.function]; descriptor == "" {
				t.Fatal("missing verified Java runner ABI descriptor")
			}

			result := convertCombinedJavaLRBody(t, test.call)
			assertSourceReviewSupportMode(t, result.SourceReview.Mappings, test.function, SourceFunctionSupportExecutable)
			for _, warning := range result.Warnings {
				if strings.Contains(warning, test.function) && strings.Contains(strings.ToLower(warning), "block") {
					t.Fatalf("warnings = %#v, Java sidecar call was incorrectly blocked", result.Warnings)
				}
			}
		})
	}
}
