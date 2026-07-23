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
