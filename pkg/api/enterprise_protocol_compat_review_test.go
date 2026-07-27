package api

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/enterprisearchive"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestEnterpriseProtocolCompatStatusIncludesConverterReviewOnlyFunctions(t *testing.T) {
	result := &enterprisearchive.ConvertResult{
		Script: &dsl.Script{Protocol: "http"},
		Stats: enterprisearchive.ConvertStats{
			ConvertedActions:    1,
			ReviewOnlyFunctions: 1,
		},
		DetectedProtocol: &enterprisearchive.DetectedProtocol{
			CanonicalSourceProtocol: "web_http_html",
			ImportMode:              enterprisearchive.ImportModeExecutable,
		},
	}

	report := buildEnterpriseArchiveProtocolCompatReport(result, nil, enterpriseArchiveImportSummary{})
	if report == nil {
		t.Fatal("missing protocol compatibility report")
	}
	if report.ReviewGatedSteps != 1 || report.PromotionStatus != "review_required" {
		t.Fatalf("report = %#v, converter review-only functions must require review", report)
	}
}
