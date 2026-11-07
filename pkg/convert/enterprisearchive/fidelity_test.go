package enterprisearchive

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestBuildImportFidelityReportJavaRunnerPostureIsPromotable(t *testing.T) {
	result := combinedJavaVuserFidelityResult()

	report := BuildImportFidelityReport(result, map[string]string{"Actions.java": "class Actions {}"}, nil, nil)

	if report.PromotionStatus != ImportPromotionPromotable || len(report.Blockers) != 0 {
		t.Fatalf("report = %#v, want promotable report without a runner-posture blocker", report)
	}
}

func TestBuildImportFidelityReportJavaRunnerAllowsBoundedSourceReview(t *testing.T) {
	t.Run("omitted file", func(t *testing.T) {
		result := combinedJavaVuserFidelityResult()
		result.SourceReview = &SourceReview{SourceFiles: []SourceReviewFile{{
			Path:    "Actions.java",
			Content: "public class Actions { public int action() { return 0; } }",
		}}, TotalSourceFiles: 2, OmittedSourceFiles: []string{"Helper.java"}}

		report := BuildImportFidelityReport(result, map[string]string{
			"Actions.java": "public class Actions { public int action() { return 0; } }",
			"Helper.java":  "public class Helper {}",
		}, nil, nil)

		if report.PromotionStatus != ImportPromotionPromotable || len(report.Blockers) != 0 {
			t.Fatalf("report = %#v, bounded source-preview omissions are not conversion loss", report)
		}
	})

	t.Run("truncated file", func(t *testing.T) {
		result := combinedJavaVuserFidelityResult()
		result.SourceReview = &SourceReview{SourceFiles: []SourceReviewFile{{
			Path:      "Actions.java",
			Content:   "public class Actions {",
			Truncated: true,
		}}}

		report := BuildImportFidelityReport(result, map[string]string{
			"Actions.java": "public class Actions { public int action() { return 0; } }",
		}, nil, nil)

		if report.PromotionStatus != ImportPromotionPromotable || len(report.Blockers) != 0 {
			t.Fatalf("report = %#v, bounded source-preview truncation is not conversion loss", report)
		}
	})
}

func TestBuildImportFidelityReportJavaRunnerPreservesConcreteBlockers(t *testing.T) {
	result := combinedJavaVuserFidelityResult()
	result.Script.Actions[0].Body = "{{secret.java_payload}}"

	report := BuildImportFidelityReport(result, nil, nil, nil)

	if report.PromotionStatus != ImportPromotionBlocked || report.SecretGated != 1 || len(report.Blockers) != 1 {
		t.Fatalf("report = %#v, want the concrete secret blocker to remain", report)
	}
	if strings.Contains(report.Blockers[0], string(ImportModeExternalRunnerRequired)) {
		t.Fatalf("blockers = %#v, runner posture must not be reported as fidelity loss", report.Blockers)
	}
}

func TestBuildImportFidelityReportOtherExternalRunnerRemainsBlocked(t *testing.T) {
	result := combinedJavaVuserFidelityResult()
	result.Script.Protocol = "sap_gui"
	result.Script.Actions[0].Type = "sapgui_connect"
	result.DetectedProtocol.APEXLoadProtocol = "sap_gui"
	result.DetectedProtocol.RunnerRequirement = "windows_sap_gui_runner"

	report := BuildImportFidelityReport(result, nil, nil, nil)

	if report.PromotionStatus != ImportPromotionBlocked || len(report.Blockers) != 1 ||
		!strings.Contains(report.Blockers[0], string(ImportModeExternalRunnerRequired)) {
		t.Fatalf("report = %#v, want non-Java external runner classification blocker", report)
	}
}

func TestBuildFidelitySourceFilesKeepsQualifiedDuplicateBasenamePhases(t *testing.T) {
	files := map[string]string{
		"lifecycle/init/Action.c":   "int InitAction() { return 0; }",
		"lifecycle/main/Action.c":   "int Action() { return 0; }",
		"lifecycle/finish/Action.c": "int EndAction() { return 0; }",
	}
	project := &ProjectConfig{SourceActionFiles: []SourceActionFile{
		{Name: "lifecycle/init/Action.c", Phase: "init", Language: "c"},
		{Name: "lifecycle/main/Action.c", Phase: "action", Language: "c"},
		{Name: "lifecycle/finish/Action.c", Phase: "teardown", Language: "c"},
	}}
	report := BuildImportFidelityReport(&ConvertResult{}, files, project, nil)
	got := make(map[string]string, len(report.SourceFiles))
	for _, sourceFile := range report.SourceFiles {
		got[sourceFile.Path] = sourceFile.Phase
	}
	for path, phase := range map[string]string{
		"lifecycle/init/Action.c":   "init",
		"lifecycle/main/Action.c":   "action",
		"lifecycle/finish/Action.c": "teardown",
	} {
		if got[path] != phase {
			t.Fatalf("source phases = %#v, want %s=%s", got, path, phase)
		}
	}
}

func combinedJavaVuserFidelityResult() *ConvertResult {
	return &ConvertResult{
		DetectedProtocol: &DetectedProtocol{
			CanonicalSourceProtocol: "java_vuser",
			APEXLoadProtocol:        "java_vuser",
			ImportMode:              ImportModeExternalRunnerRequired,
			RunnerRequirement:       javaVuserRunnerRequirement,
		},
		Script: &dsl.Script{
			Name:     "Combined Java Vuser",
			Protocol: "java_vuser",
			Actions: []dsl.Action{{
				Name: "Java action",
				Type: "java_vuser_run",
			}},
		},
		SourceReview: &SourceReview{SourceFiles: []SourceReviewFile{{
			Path:    "Actions.java",
			Content: "class Actions {}",
		}}},
		Stats: ConvertStats{
			TotalFunctions:            1,
			ConvertedActions:          1,
			ExternalRequiredFunctions: 1,
		},
	}
}

// A report stored before the encrypted proxy callout became advisory lists it
// as a blocker. Readers drop that line, a report it alone held blocked turns
// promotable, and the stored report is never modified.
func TestImportFidelityReportWithoutAdvisoryBlockers(t *testing.T) {
	const sentence = "One or more source request bodies were detected but not converted."
	for _, tc := range []struct {
		name       string
		blockers   []string
		wantStatus string
		want       []string
	}{
		{name: "converter line only", blockers: []string{"encrypted_proxy_password at init[0]"}, wantStatus: ImportPromotionPromotable},
		{name: "repaired bare code only", blockers: []string{" encrypted_proxy_password "}, wantStatus: ImportPromotionPromotable},
		{name: "beside real blockers", blockers: []string{"encrypted_proxy_password at init[0]", "legacy_tls_replacement at actions[0]", sentence}, wantStatus: ImportPromotionBlocked, want: []string{"legacy_tls_replacement at actions[0]", sentence}},
		{name: "real blockers only", blockers: []string{sentence}, wantStatus: ImportPromotionBlocked, want: []string{sentence}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stored := &ImportFidelityReport{PromotionStatus: ImportPromotionBlocked, Blockers: append([]string(nil), tc.blockers...), Warnings: []string{"kept"}}
			got := stored.WithoutAdvisoryBlockers()
			if got.PromotionStatus != tc.wantStatus || strings.Join(got.Blockers, "|") != strings.Join(tc.want, "|") || len(got.Warnings) != 1 {
				t.Fatalf("normalized = %+v, want status %q blockers %q", got, tc.wantStatus, tc.want)
			}
			if stored.PromotionStatus != ImportPromotionBlocked || strings.Join(stored.Blockers, "|") != strings.Join(tc.blockers, "|") {
				t.Fatalf("stored report was modified: %+v", stored)
			}
			if len(tc.want) == len(tc.blockers) && got != stored {
				t.Fatal("a report with nothing to drop was copied")
			}
		})
	}
	if (*ImportFidelityReport)(nil).WithoutAdvisoryBlockers() != nil {
		t.Fatal("nil report normalized to non-nil")
	}
	if AdvisoryFidelityBlocker("encrypted_proxy_password is required at init[0] before promotion") {
		t.Fatal("a converter sentence was treated as an advisory callout")
	}
}
