package enterprisearchive

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

// publishedJavaLRFunctionExpectations pins the disposition of every method in the
// published OpenText/Micro Focus VuGen 26.1 Java Vuser lr API (the lrapi.lr class,
// "Alphabetical Listing - Java Utility Functions"). 40 methods resolve through the
// shared C lr_* registry; 13 are the Java-only supplement. Any reclassification
// must be a deliberate edit here AND in the registry data.
var publishedJavaLRFunctionExpectations = map[string]struct {
	family string
	mode   SourceFunctionSupportMode
}{
	// --- shared with the C lr_* baseline (40) ---
	"lr_abort":                    {"runtime", SourceFunctionSupportExecutable},
	"lr_advance_param":            {"parameter", SourceFunctionSupportExecutable},
	"lr_aws_get_secret":           {"secret", SourceFunctionSupportExternalRequired},
	"lr_cyberark_get_vault":       {"secret", SourceFunctionSupportExternalRequired},
	"lr_debug_message":            {"message", SourceFunctionSupportExecutable},
	"lr_decrypt":                  {"parameter", SourceFunctionSupportReviewOnly},
	"lr_decrypt_ex":               {"parameter", SourceFunctionSupportReviewOnly},
	"lr_encrypt_ex":               {"parameter", SourceFunctionSupportReviewOnly},
	"lr_end_sub_transaction":      {"transaction", SourceFunctionSupportExecutable},
	"lr_end_timer":                {"timer", SourceFunctionSupportExecutable},
	"lr_end_transaction":          {"transaction", SourceFunctionSupportExecutable},
	"lr_error_message":            {"message", SourceFunctionSupportExecutable},
	"lr_eval_string":              {"parameter", SourceFunctionSupportExecutable},
	"lr_exit":                     {"runtime", SourceFunctionSupportExecutable},
	"lr_get_attrib_double":        {"attribute", SourceFunctionSupportReviewOnly},
	"lr_get_attrib_long":          {"attribute", SourceFunctionSupportReviewOnly},
	"lr_get_attrib_string":        {"attribute", SourceFunctionSupportReviewOnly},
	"lr_get_debug_message":        {"message", SourceFunctionSupportExecutable},
	"lr_get_host_name":            {"info", SourceFunctionSupportExecutable},
	"lr_get_master_host_name":     {"info", SourceFunctionSupportExecutable},
	"lr_get_transaction_duration": {"transaction", SourceFunctionSupportExecutable},
	"lr_log_message":              {"message", SourceFunctionSupportExecutable},
	"lr_message":                  {"message", SourceFunctionSupportExecutable},
	"lr_next_row":                 {"parameter", SourceFunctionSupportExecutable},
	"lr_output_message":           {"message", SourceFunctionSupportExecutable},
	"lr_peek_events":              {"runtime", SourceFunctionSupportNativeNoop},
	"lr_read_file":                {"parameter", SourceFunctionSupportReviewOnly},
	"lr_rendezvous":               {"runtime", SourceFunctionSupportExecutable},
	"lr_save_int":                 {"parameter", SourceFunctionSupportExecutable},
	"lr_save_string":              {"parameter", SourceFunctionSupportExecutable},
	"lr_set_debug_message":        {"message", SourceFunctionSupportExecutable},
	"lr_set_transaction":          {"transaction", SourceFunctionSupportExecutable},
	"lr_set_transaction_status":   {"transaction", SourceFunctionSupportExecutable},
	"lr_start_sub_transaction":    {"transaction", SourceFunctionSupportExecutable},
	"lr_start_timer":              {"timer", SourceFunctionSupportExecutable},
	"lr_start_transaction":        {"transaction", SourceFunctionSupportExecutable},
	"lr_think_time":               {"runtime", SourceFunctionSupportExecutable},
	"lr_unmask":                   {"parameter", SourceFunctionSupportReviewOnly},
	"lr_user_data_point":          {"metric", SourceFunctionSupportExecutable},
	"lr_vuser_status_message":     {"message", SourceFunctionSupportExecutable},
	// --- Java-only supplement (13) ---
	"lr_deserialize":        {"parameter", SourceFunctionSupportReviewOnly},
	"lr_enable_redirection": {"runtime", SourceFunctionSupportNativeNoop},
	"lr_eval_data":          {"parameter", SourceFunctionSupportReviewOnly},
	"lr_eval_int":           {"parameter", SourceFunctionSupportExecutable},
	"lr_get_group_name":     {"info", SourceFunctionSupportExecutable},
	"lr_get_object":         {"parameter", SourceFunctionSupportReviewOnly},
	"lr_get_scenario_id":    {"info", SourceFunctionSupportExecutable},
	"lr_get_vuser_id":       {"info", SourceFunctionSupportExecutable},
	"lr_log":                {"message", SourceFunctionSupportExecutable},
	"lr_read_xml":           {"xml", SourceFunctionSupportReviewOnly},
	"lr_redirect":           {"runtime", SourceFunctionSupportNativeNoop},
	"lr_save_data":          {"parameter", SourceFunctionSupportReviewOnly},
	"lr_value_check":        {"parameter", SourceFunctionSupportReviewOnly},
}

// javaOnlyLRSupplement is the subset expected to live in the Java supplement file
// (everything not in the C baseline).
var javaOnlyLRSupplement = map[string]struct {
	family string
	mode   SourceFunctionSupportMode
}{
	"lr_deserialize":        {"parameter", SourceFunctionSupportReviewOnly},
	"lr_enable_redirection": {"runtime", SourceFunctionSupportNativeNoop},
	"lr_eval_data":          {"parameter", SourceFunctionSupportReviewOnly},
	"lr_eval_int":           {"parameter", SourceFunctionSupportExecutable},
	"lr_get_group_name":     {"info", SourceFunctionSupportExecutable},
	"lr_get_object":         {"parameter", SourceFunctionSupportReviewOnly},
	"lr_get_scenario_id":    {"info", SourceFunctionSupportExecutable},
	"lr_get_vuser_id":       {"info", SourceFunctionSupportExecutable},
	"lr_log":                {"message", SourceFunctionSupportExecutable},
	"lr_read_xml":           {"xml", SourceFunctionSupportReviewOnly},
	"lr_redirect":           {"runtime", SourceFunctionSupportNativeNoop},
	"lr_save_data":          {"parameter", SourceFunctionSupportReviewOnly},
	"lr_value_check":        {"parameter", SourceFunctionSupportReviewOnly},
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

// convertJavaLRBody wraps a Java action-method body in a minimal Java Vuser archive
// and runs it through the real ConvertZip Java import path.
func convertJavaLRBody(t *testing.T, body string) *ConvertResult {
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
// concrete disposition (never a custom_call stub) with the registry's support mode.
func TestJavaLRResolvesEveryPublishedFunction(t *testing.T) {
	for name, want := range publishedJavaLRFunctionExpectations {
		name, want := name, want
		t.Run(name, func(t *testing.T) {
			call := "lr." + strings.TrimPrefix(name, "lr_") + "(\"arg\");"
			result := convertJavaLRBody(t, call)
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
			assertSourceReviewSupportMode(t, result.SourceReview.Mappings, name, want.mode)
		})
	}
}
