package api

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/enterprisearchive"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func scriptWithPromotion(name, status string, blockers []string) *dsl.Script {
	return &dsl.Script{
		Name: name,
		Metadata: &dsl.ScriptMetadata{
			Import: &dsl.ImportMetadata{
				Preserved: map[string]any{
					"fidelity_report": &enterprisearchive.ImportFidelityReport{
						PromotionStatus: status,
						Blockers:        blockers,
					},
				},
			},
		},
	}
}

func TestImportPromotionLaunchConflicts_BlockedScript(t *testing.T) {
	groups := []TestGroup{{script: scriptWithPromotion("legacy", enterprisearchive.ImportPromotionBlocked,
		[]string{"One or more lr_unmask request bodies require governed secret resolution before executable promotion."})}}

	conflicts := importPromotionLaunchConflicts(groups)
	if len(conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(conflicts))
	}
	if conflicts[0].Code != "import_promotion_blocked" || conflicts[0].Severity != "blocking" {
		t.Errorf("unexpected conflict: %+v", conflicts[0])
	}
}

func TestImportPromotionLaunchConflicts_PromotableScript(t *testing.T) {
	groups := []TestGroup{{script: scriptWithPromotion("ok", enterprisearchive.ImportPromotionPromotable, nil)}}
	if conflicts := importPromotionLaunchConflicts(groups); len(conflicts) != 0 {
		t.Fatalf("promotable script should not conflict, got %v", conflicts)
	}
}

func TestImportPromotionLaunchConflicts_JavaRunnerClassificationIsNotFidelityLoss(t *testing.T) {
	script := scriptWithPromotion("java-combined", enterprisearchive.ImportPromotionBlocked,
		[]string{"Source protocol is classified as external_runner_required and cannot be promoted as executable."})
	script.Protocol = "java_vuser"
	script.Actions = []dsl.Action{{Name: "Run action", Type: "java_vuser_run"}}

	if conflicts := importPromotionLaunchConflicts([]TestGroup{{script: script}}); len(conflicts) != 0 {
		t.Fatalf("Java runner posture should not be treated as import fidelity loss, got %v", conflicts)
	}
}

func TestImportPromotionLaunchConflicts_JavaDraftRetainsRealFidelityBlocker(t *testing.T) {
	script := scriptWithPromotion("java-combined", enterprisearchive.ImportPromotionBlocked, []string{
		"Source protocol is classified as external_runner_required and cannot be promoted as executable.",
		"One or more source request bodies were detected but not converted.",
	})
	script.Protocol = "java_vuser"
	script.Actions = []dsl.Action{{Name: "Run action", Type: "java_vuser_run"}}

	conflicts := importPromotionLaunchConflicts([]TestGroup{{script: script}})
	if len(conflicts) != 1 || !strings.Contains(conflicts[0].Message, "request bodies") {
		t.Fatalf("real Java fidelity blocker should remain, got %v", conflicts)
	}
	if strings.Contains(conflicts[0].Message, "external_runner_required") {
		t.Fatalf("runner classification blocker should be filtered, got %v", conflicts)
	}
}

func TestImportPromotionLaunchConflicts_NoReport(t *testing.T) {
	groups := []TestGroup{{script: &dsl.Script{Name: "plain"}}}
	if conflicts := importPromotionLaunchConflicts(groups); len(conflicts) != 0 {
		t.Fatalf("script without a fidelity report should not conflict, got %v", conflicts)
	}
}

func TestImportPromotionLaunchConflicts_LegacyJavaOneShotDoesNotRequireCombinedReviewEvidence(t *testing.T) {
	script := &dsl.Script{
		Name:          "legacy-java",
		Protocol:      "java_vuser",
		GeneratedFrom: "enterprise_script_archive",
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "enterprise_script_archive",
		}},
		Actions: []dsl.Action{{
			Name: "Legacy lifecycle", Type: "java_vuser_run", Target: "Actions",
			Params: map[string]any{"method": "full_lifecycle"},
		}},
	}
	if blockers := scriptImportPromotionBlockers(script); len(blockers) != 0 {
		t.Fatalf("legacy one-shot Java script gained combined-review blockers: %v", blockers)
	}
}

func TestImportPromotionLaunchConflicts_OptOut(t *testing.T) {
	t.Setenv(allowBlockedImportEnv, "true")
	groups := []TestGroup{{script: scriptWithPromotion("legacy", enterprisearchive.ImportPromotionBlocked, []string{"blocked"})}}
	if conflicts := importPromotionLaunchConflicts(groups); len(conflicts) != 0 {
		t.Fatalf("opt-out should clear conflicts, got %v", conflicts)
	}
}

func TestImportPromotionLaunchConflicts_RoundTrippedMap(t *testing.T) {
	// After a storage round-trip the report is a map[string]any, not the typed
	// struct; fidelityReportValue must still recover the status/blockers.
	groups := []TestGroup{{script: &dsl.Script{
		Name: "legacy-map",
		Metadata: &dsl.ScriptMetadata{
			Import: &dsl.ImportMetadata{
				Preserved: map[string]any{
					"fidelity_report": map[string]any{
						"promotion_status": "blocked",
						"blockers":         []any{"adapter required before executable promotion"},
					},
				},
			},
		},
	}}}
	if conflicts := importPromotionLaunchConflicts(groups); len(conflicts) != 1 {
		t.Fatalf("expected 1 conflict from a round-tripped map report, got %d", len(conflicts))
	}
}
