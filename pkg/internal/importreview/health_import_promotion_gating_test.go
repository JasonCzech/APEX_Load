package importreview

import (
	"slices"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func promotionReportScript(report map[string]any) *dsl.Script {
	return &dsl.Script{
		Name: "Promotion report", Version: "1", Protocol: "http",
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source:    "enterprise_script_archive",
			Preserved: map[string]any{"fidelity_report": report},
		}},
		Actions: []dsl.Action{{Name: "home", Type: "GET", Method: "GET", Target: "https://example.test/"}},
	}
}

// A blocked report that names no blocker states its cause from its warnings,
// the review codes a guided repair keeps it blocked for, so a launch refusal
// names them. Only a report with neither falls back to the bare marker, and
// named blockers are reported as they are.
func TestScriptImportPromotionBlockersNameTheReviewCodesOfABlockedReport(t *testing.T) {
	for _, tc := range []struct {
		name   string
		report map[string]any
		want   []string
	}{
		{
			name:   "review codes only",
			report: map[string]any{"promotion_status": "blocked", "warnings": []any{"missing_producer:b", " conditional_producer:a ", "missing_producer:b", ""}},
			want:   []string{"Imported script needs import review before promotion: conditional_producer:a, missing_producer:b."},
		},
		{
			name:   "nothing named",
			report: map[string]any{"promotion_status": "blocked"},
			want:   []string{"Imported script is marked not promotable to executable."},
		},
		{
			// A converter report's warnings are free text that can quote the
			// source, and a credential-preserving import stores them unmasked.
			name: "converter free text",
			report: map[string]any{"promotion_status": "blocked", "warnings": []any{
				`line 40: condition strcmp(lr_eval_string("{Pwd}"), "Summer2024!") == 0 was kept as review evidence`,
				`strcmp(pwd,"Summer2024!")`,
				`"Summer2024!"`,
			}},
			want: []string{"Imported script is marked not promotable to executable."},
		},
		{
			name: "review codes beside converter free text",
			report: map[string]any{"promotion_status": "blocked", "warnings": []any{
				"missing_producer:TxName",
				`line 40: condition strcmp(lr_eval_string("{Pwd}"), "Summer2024!") == 0 was kept as review evidence`,
				" import_review_required ",
			}},
			want: []string{"Imported script needs import review before promotion: import_review_required, missing_producer:TxName."},
		},
		{
			name:   "named blocker",
			report: map[string]any{"promotion_status": "blocked", "blockers": []any{"One or more source request bodies were detected but not converted."}, "warnings": []any{"missing_producer:b"}},
			want:   []string{"One or more source request bodies were detected but not converted."},
		},
		{
			name:   "promotable with review codes",
			report: map[string]any{"promotion_status": "promotable", "warnings": []any{"missing_producer:b"}},
			want:   []string{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ScriptImportPromotionBlockers(promotionReportScript(tc.report)); !slices.Equal(got, tc.want) {
				t.Fatalf("promotion blockers = %q, want %q", got, tc.want)
			}
		})
	}
}

// legacyProxyCalloutScript is a script saved before the encrypted proxy
// callout became advisory: its stored fidelity report lists the callout as a
// blocker, word for word as the converter wrote it then.
func legacyProxyCalloutScript(mode string, blockers ...any) *dsl.Script {
	script := promotionReportScript(map[string]any{"promotion_status": "blocked", "blockers": blockers})
	script.Config.BaseURL = "https://example.test"
	script.Config.HTTP = dsl.HTTPConfig{ProxyMode: mode, Proxy: dsl.ProxyConfig{URL: "http://proxy.example.test:8080", Password: "{{secret.ENTERPRISE_PROXY_PASSWORD}}"}}
	return script
}

func TestScriptImportPromotionBlockersIgnoreLegacyAdvisoryProxyCallout(t *testing.T) {
	for _, mode := range []string{"system", "pac", "none", "custom"} {
		for _, blocker := range []any{"encrypted_proxy_password at init[0]", "encrypted_proxy_password"} {
			script := legacyProxyCalloutScript(mode, blocker)
			if got := ScriptImportPromotionBlockers(script); len(got) != 0 {
				t.Fatalf("%s proxy, stored blocker %q: blockers = %#v, want none", mode, blocker, got)
			}
			response := BuildScriptValidationResponseFromIssues(script.ID, script, nil, nil)
			for _, issue := range response.Issues {
				if strings.Contains(issue, "encrypted_proxy_password") {
					t.Fatalf("%s proxy: validation issues = %#v, want no legacy callout", mode, response.Issues)
				}
			}
			if mode != "custom" && !response.Valid {
				t.Fatalf("%s proxy: validation = %+v, want valid", mode, response.Issues)
			}
		}
	}
	// A real blocker beside the legacy callout still blocks, and only it is named.
	const sentence = "One or more source request bodies were detected but not converted."
	script := legacyProxyCalloutScript("system", "encrypted_proxy_password at init[0]", "legacy_tls_replacement at actions[0]", sentence)
	if got := ScriptImportPromotionBlockers(script); !slices.Equal(got, []string{sentence, "legacy_tls_replacement at actions[0]"}) {
		t.Fatalf("mixed blockers = %#v, want the real blockers only", got)
	}
	if response := BuildScriptValidationResponseFromIssues(script.ID, script, nil, nil); response.Valid {
		t.Fatalf("validation = %+v, want the real blockers to keep it invalid", response.Issues)
	}
	// The stored report is never rewritten by a read.
	stored := script.Metadata.Import.Preserved["fidelity_report"].(map[string]any)["blockers"].([]any)
	if len(stored) != 3 {
		t.Fatalf("stored blockers = %#v, want the read to leave them untouched", stored)
	}
}
