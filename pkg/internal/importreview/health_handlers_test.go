package importreview

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	convertreview "github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// promotionReviewedImport is an enterprise-archive import after a guided
// repair cleared its last replay gate: one promotion-only review code
// (producer order) remains, so the repair restamped its fidelity report as
// blocked with no named blockers.
func promotionReviewedImport() *dsl.Script {
	script := &dsl.Script{
		ID:       "promotion-reviewed",
		Name:     "Store lookup",
		Version:  "1.0",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://shop.example.test"},
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "enterprise_script_archive",
			Preserved: map[string]any{"fidelity_report": map[string]any{
				"promotion_status": "blocked",
				"blockers":         []any{},
				"warnings":         []any{"conditional_producer:C_StoreId"},
			}},
		}},
		Actions: []dsl.Action{
			{Name: "store", Type: "GET", Method: "GET", Target: "/stores/{{var.C_StoreId}}"},
			{Name: "stores", Type: "GET", Method: "GET", Target: "/stores", Extractors: []dsl.Extractor{{Name: "C_StoreId", Source: "body", Type: "regex", Expression: `id=(\d+)`}}},
		},
	}
	convertreview.ApplyToAction(&script.Actions[0], []convertreview.Finding{{
		Code:     "conditional_producer:C_StoreId",
		Severity: diag.Warning,
		Gate:     convertreview.GatePromotion,
		Detail:   "Producer ordering or conditional execution for C_StoreId requires review.",
	}})
	return script
}

// A validation (Recheck) reports an import fidelity blocker as an issue, but
// its replay preflight judges only what refuses a replay, as a debug start
// does: a promotion-only review code never makes the preflight invalid.
func TestValidationPreflightIgnoresPromotionOnlyFidelityBlockers(t *testing.T) {
	// The report names no blocker, so the fold names the review code it is
	// held for.
	const marked = "import fidelity blocker: Imported script needs import review before promotion: conditional_producer:C_StoreId."
	for _, tc := range []struct {
		name        string
		change      func(*dsl.Script)
		wantIssue   string
		replayValid bool
	}{
		{name: "blocked without named blockers", wantIssue: marked, replayValid: true},
		{name: "named fidelity blocker", wantIssue: "import fidelity blocker: Request body was detected but not converted.", replayValid: true, change: func(s *dsl.Script) {
			s.Metadata.Import.Preserved["fidelity_report"] = map[string]any{
				"promotion_status": "blocked",
				"blockers":         []any{"Request body was detected but not converted."},
			}
		}},
		{name: "replay gate still refuses", wantIssue: marked, replayValid: false, change: func(s *dsl.Script) {
			s.Actions[1].Metadata = map[string]any{"execution_blocker": "legacy_tls_replacement"}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script := promotionReviewedImport()
			if tc.change != nil {
				tc.change(script)
			}
			response := BuildScriptValidationResponse(script.ID, script)

			if response.Valid || !slices.Contains(response.Issues, tc.wantIssue) {
				t.Fatalf("valid=%v issues=%#v, want the fidelity blocker %q kept as an issue", response.Valid, response.Issues, tc.wantIssue)
			}
			preflight := response.Preflight
			if preflight == nil || preflight.Valid != tc.replayValid {
				t.Fatalf("preflight = %+v, want valid=%v", preflight, tc.replayValid)
			}
			for _, issue := range preflight.Issues {
				if strings.HasPrefix(issue, "import fidelity blocker: ") {
					t.Fatalf("preflight issues = %#v, want no promotion-only fidelity blocker", preflight.Issues)
				}
			}
			for _, check := range preflight.Checks {
				if check.ID == "validation" && check.Status == "fail" {
					t.Fatalf("validation check = %+v, want promotion-only blockers left out", check)
				}
			}
			// The same replay facts a debug start judges the stored script on.
			replay := BuildReplayPreflight(script, nil, ValidateScript(script), ValidationWarnings(script))
			if !reflect.DeepEqual(preflight, replay) {
				t.Fatalf("validation preflight = %+v\nwant replay preflight %+v", preflight, replay)
			}
			if !tc.replayValid && !slices.ContainsFunc(preflight.Issues, func(issue string) bool {
				return strings.Contains(issue, "execution blocker legacy_tls_replacement blocks replay")
			}) {
				t.Fatalf("preflight issues = %#v, want the replay gate", preflight.Issues)
			}
		})
	}
}

// A caller's own issues still fail the preflight's validation check; only the
// fidelity blockers folded in after them are left out of it.
func TestValidationPreflightCountsOnlyTheCallersIssues(t *testing.T) {
	script := promotionReviewedImport()
	response := BuildScriptValidationResponseFromIssues(script.ID, script, nil, []string{"actions[0]: synthetic issue"})
	if !slices.Equal(response.Issues, []string{
		"actions[0]: synthetic issue",
		"import fidelity blocker: Imported script needs import review before promotion: conditional_producer:C_StoreId.",
	}) {
		t.Fatalf("issues = %#v, want the caller's issue then the fidelity blocker", response.Issues)
	}
	if response.Preflight.Valid || !slices.Equal(response.Preflight.Issues, []string{"actions[0]: synthetic issue"}) {
		t.Fatalf("preflight = %+v, want invalid on the caller's issue alone", response.Preflight)
	}
	for _, check := range response.Preflight.Checks {
		if check.ID == "validation" && (check.Status != "fail" || !strings.HasPrefix(check.Detail, "1 validation issue")) {
			t.Fatalf("validation check = %+v, want one failing validation issue", check)
		}
	}
}
