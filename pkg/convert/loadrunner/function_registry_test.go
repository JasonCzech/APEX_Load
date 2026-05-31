package loadrunner

import "testing"

func TestParseLoadRunnerFunctionRegistryReasonColumn(t *testing.T) {
	content := `# comment line
# Format: name,family,support_mode[,reason]
explicit_reason_fn,web,review_only,Custom curated reason, with an embedded comma
default_reason_fn,web,review_only
external_fn,vts,external_required
bad_row_missing_mode,web

`
	registry := parseLoadRunnerFunctionRegistry(content)

	if len(registry) != 3 {
		t.Fatalf("registry size = %d, want 3 (bad/comment/blank rows skipped): %#v", len(registry), registry)
	}

	explicit, ok := registry["explicit_reason_fn"]
	if !ok {
		t.Fatal("explicit_reason_fn not parsed")
	}
	// SplitN with limit 4 keeps the trailing comma inside the reason field.
	if explicit.Reason != "Custom curated reason, with an embedded comma" {
		t.Fatalf("explicit reason = %q, want the full reason including the embedded comma", explicit.Reason)
	}

	def, ok := registry["default_reason_fn"]
	if !ok {
		t.Fatal("default_reason_fn not parsed")
	}
	if def.Reason != defaultLoadRunnerReason("web", LoadRunnerSupportReviewOnly) {
		t.Fatalf("default reason = %q, want family/mode default", def.Reason)
	}
	if def.Reason == "" {
		t.Fatal("default reason must never be empty")
	}

	if _, ok := registry["bad_row_missing_mode"]; ok {
		t.Fatal("row missing a support mode should be skipped")
	}
}

func TestDefaultLoadRunnerReasonNeverEmpty(t *testing.T) {
	modes := []LoadRunnerSupportMode{
		LoadRunnerSupportExecutable,
		LoadRunnerSupportNativeNoop,
		LoadRunnerSupportReviewOnly,
		LoadRunnerSupportExternalRequired,
		LoadRunnerSupportCustomReview,
	}
	for _, mode := range modes {
		if reason := defaultLoadRunnerReason("web", mode); reason == "" {
			t.Fatalf("defaultLoadRunnerReason(web, %s) is empty", mode)
		}
	}
}

// TestRegistryLoadsAllFamilyFiles proves the directory-based embed merges every per-family
// catalog, not just the original lr_ utility file.
func TestRegistryLoadsAllFamilyFiles(t *testing.T) {
	cases := []struct {
		name   string
		family string
		mode   LoadRunnerSupportMode
	}{
		{"lr_save_string", "parameter", LoadRunnerSupportExecutable}, // utility.csv
		{"web_set_user", "web", LoadRunnerSupportReviewOnly},         // web.csv
		{"lrs_send", "socket", LoadRunnerSupportReviewOnly},          // socket.csv
		{"lrd_exec", "database", LoadRunnerSupportReviewOnly},        // database_lrd.csv
		{"sapgui_logon", "sapgui", LoadRunnerSupportReviewOnly},      // sapgui.csv
		{"ctrx_connect_server", "citrix", LoadRunnerSupportReviewOnly},
		{"TE_connect", "rte", LoadRunnerSupportReviewOnly}, // case-insensitive lookup
		{"lrvtc_connect", "vts", LoadRunnerSupportExternalRequired},
	}
	for _, tc := range cases {
		def, ok := lookupLoadRunnerFunction(tc.name)
		if !ok {
			t.Errorf("%s: not found in catalog", tc.name)
			continue
		}
		if def.Family != tc.family || def.SupportMode != tc.mode {
			t.Errorf("%s: family=%s mode=%s, want family=%s mode=%s", tc.name, def.Family, def.SupportMode, tc.family, tc.mode)
		}
		if def.Reason == "" {
			t.Errorf("%s: reason is empty", tc.name)
		}
	}
}
