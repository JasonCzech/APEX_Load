package importreview

import (
	"slices"
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
