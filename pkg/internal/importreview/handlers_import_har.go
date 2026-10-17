package importreview

import (
	"fmt"
	"github.com/jasonczech/apexload/pkg/convert/har"
	"github.com/jasonczech/apexload/pkg/scrub"
)

func HarImportSummary(result *har.ConvertResult) map[string]any {
	summary := map[string]any{
		"artifact_mode":      har.ArtifactModeDesignHAR,
		"generation_mode":    "deterministic_har_spine",
		"protocol_scope":     "http_family_only",
		"llm_role":           "naming_assertions_parameterization_semantic_review",
		"non_http_protocols": []string{"solace", "kafka", "ibm_mq"},
	}
	if result == nil {
		return summary
	}
	if result.ArtifactMode != "" {
		summary["artifact_mode"] = result.ArtifactMode
	}
	if result.Governance != nil {
		summary["artifact_sensitivity"] = result.Governance.Sensitivity
		summary["retention_days"] = result.Governance.RetentionDays
		summary["governance"] = result.Governance
	}
	if result.Diff != nil {
		summary["har_diff"] = result.Diff
	}
	return summary
}

func HarGovernanceItems(governance *har.ArtifactGovernance) []string {
	items := []string{}
	switch governance.ArtifactMode {
	case har.ArtifactModeDesignHAR:
		items = append(items, "Imported as a design HAR: the recording is the source for the generated script.")
	case har.ArtifactModeDesignHARPair:
		items = append(items, "Imported as a design HAR pair: two recordings are compared to find values that change between sessions.")
	case har.ArtifactModeRuntimeCanaryHAR:
		items = append(items, "Imported as a runtime canary HAR: the recording is compared with live replay.")
	default:
		items = append(items, fmt.Sprintf("Artifact mode: %s", governance.ArtifactMode))
	}
	switch governance.Sensitivity {
	case scrub.SensitivityRaw:
		items = append(items, "The stored HAR keeps the recorded values, so it can contain credentials, cookies and tokens. Treat it as sensitive.")
	case scrub.SensitivityScrubbed:
		items = append(items, "Sensitive values were scrubbed from the stored HAR before review.")
	default:
		items = append(items, fmt.Sprintf("Sensitivity: %s", governance.Sensitivity))
	}
	if governance.RetentionDays == 1 {
		items = append(items, "Retention period: 1 day.")
	} else {
		items = append(items, fmt.Sprintf("Retention period: %d days.", governance.RetentionDays))
	}
	if governance.Scope == "http_family_only" {
		items = append(items, "Only HTTP traffic in the recording is converted.")
	} else if governance.Scope != "" {
		items = append(items, fmt.Sprintf("Scope: %s", governance.Scope))
	}
	return items
}

func HarImportReviewSections(result *har.ConvertResult) []ScriptImportReviewSection {
	if result == nil {
		return []ScriptImportReviewSection{}
	}
	sections := []ScriptImportReviewSection{}
	if result.Governance != nil {
		items := HarGovernanceItems(result.Governance)
		if result.Governance.RedactionEnabled {
			items = append(items, "Scrub-on-write redaction was applied before the HAR source review was created.")
		}
		sections = append(sections, ScriptImportReviewSection{
			Key:      "har_governance",
			Title:    "HAR Artifact Governance",
			Severity: "info",
			Summary:  "HAR-with-bodies artifacts are treated as sensitive and governed before review.",
			Items:    items,
		})
	}
	if result.Diff != nil {
		items := []string{
			fmt.Sprintf("%d dynamic candidate(s)", result.Diff.DynamicCandidates),
			fmt.Sprintf("%d static candidate(s)", result.Diff.StaticCandidates),
			fmt.Sprintf("%d unmatched baseline candidate(s)", result.Diff.UnmatchedCandidates),
		}
		for _, classification := range result.Diff.Classifications {
			if classification.Classification != har.DiffClassificationDynamic {
				continue
			}
			items = append(items, fmt.Sprintf("%s changed between recordings with %.0f%% confidence", classification.Name, classification.DiffConfidence*100))
			if len(items) >= 8 {
				break
			}
		}
		sections = append(sections, ScriptImportReviewSection{
			Key:      "har_pair_diff",
			Title:    "Two-Recording Correlation Diff",
			Severity: "info",
			Summary:  "Values that change between otherwise matching recordings are promoted as dynamic correlation candidates.",
			Items:    items,
		})
	}
	return sections
}
