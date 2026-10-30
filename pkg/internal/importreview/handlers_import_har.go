package importreview

import (
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/har"
	"github.com/jasonczech/apexload/pkg/convert/paramize"
	"github.com/jasonczech/apexload/pkg/dsl"
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
	if result.Diff != nil && !result.Diff.Compared {
		items := []string{}
		if result.Diff.Reason != "" {
			items = append(items, result.Diff.Reason)
		}
		sections = append(sections, ScriptImportReviewSection{
			Key:      "har_pair_diff",
			Title:    "Two-Recording Correlation Diff",
			Severity: "warning",
			Summary:  "The comparison HAR was not compared, so no correlation is labeled dynamic or static. The baseline recording was imported on its own.",
			Items:    items,
		})
	} else if result.Diff != nil {
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
			Summary:  "Each correlation's extractor is re-run on the matching response of the second recording; when the second recording reuses the value, the correlation is labeled dynamic (the value changed) or static (it stayed the same). The labels are for review only: no correlation is added, removed or re-ranked because of them.",
			Items:    items,
		})
	}
	return sections
}

// maxHARPairUnhandledInputItems bounds the unhandled request inputs the pair
// diff section lists.
const maxHARPairUnhandledInputItems = 12

// ResolveHARPairUnhandledInputs fills diff.UnhandledInputs from the final
// script: a changed request value counts as unhandled while the script still
// sends it as recorded and no automatic parameterization suggestion replaces
// it.
func ResolveHARPairUnhandledInputs(diff *har.DiffReport, script *dsl.Script, parameterization *paramize.Result) {
	if diff == nil || !diff.Compared || script == nil || !diff.ChangedRequestInputs() {
		return
	}
	sent := har.ScriptRequestValues(script)
	replaced := map[string]bool{}
	if parameterization != nil {
		for _, suggestion := range parameterization.Suggestions {
			if !suggestion.AutoApply {
				continue
			}
			replaced[suggestion.LiteralValue] = true
			for _, location := range suggestion.Locations {
				replaced[location.SearchText] = true
			}
		}
	}
	// Values a suggestion replaces need no search; the rest share one pass.
	var search []string
	for _, value := range diff.RequestInputValues() {
		if !replaced[value] {
			search = append(search, value)
		}
	}
	sent.Prepare(search)
	diff.ResolveUnhandledInputs(func(value string) bool {
		return !replaced[value] && sent.Sends(value)
	})
}

// harPairActionableInput reports an unhandled input worth a warning: every
// kind but a client counter, which replays harmlessly in most applications.
func harPairActionableInput(input har.DiffRequestInput) bool {
	return input.Kind != har.DiffInputCounter
}

// HARPairUnhandledInputsWarning summarizes the unhandled request inputs that
// need attention, and says when the request comparison stopped early, or
// returns "" when there is nothing to say.
func HARPairUnhandledInputsWarning(diff *har.DiffReport) string {
	if diff == nil {
		return ""
	}
	fields := 0
	for _, input := range diff.UnhandledInputs {
		if harPairActionableInput(input) {
			fields++
		}
	}
	var parts []string
	if fields > 0 {
		more := ""
		if diff.UnhandledInputsTruncated {
			more = " or more (only the first " + fmt.Sprint(len(diff.UnhandledInputs)) + " changed fields are listed)"
		}
		parts = append(parts, fmt.Sprintf("%d request field(s)%s changed between the two recordings and are still sent as recorded in %d request(s); replay sends values from another session there. Review them in the two-recording diff.", fields, more, diff.UnhandledRequestCount(harPairActionableInput)))
	}
	if diff.RequestComparisonTruncated {
		parts = append(parts, harPairComparisonTruncatedNote)
	}
	return strings.Join(parts, " ")
}

// harPairComparisonTruncatedNote says the request comparison did not reach
// every request.
const harPairComparisonTruncatedNote = "The request-by-request comparison of the two recordings stopped at its limit of changed values, so later requests were not compared."

// AppendHARPairUnhandledInputs adds the unhandled request inputs to the
// two-recording diff section, and raises it to a warning when any of them
// needs attention (client counters alone do not).
func AppendHARPairUnhandledInputs(sections []ScriptImportReviewSection, diff *har.DiffReport) []ScriptImportReviewSection {
	if diff == nil || (len(diff.UnhandledInputs) == 0 && !diff.RequestComparisonTruncated) {
		return sections
	}
	for index := range sections {
		if sections[index].Key != "har_pair_diff" {
			continue
		}
		section := &sections[index]
		if diff.RequestComparisonTruncated {
			section.Severity = "warning"
			section.Items = append(section.Items, harPairComparisonTruncatedNote)
		}
		if len(diff.UnhandledInputs) == 0 {
			break
		}
		actionable := 0
		for _, input := range diff.UnhandledInputs {
			if harPairActionableInput(input) {
				actionable++
			}
		}
		if actionable > 0 {
			section.Severity = "warning"
		}
		listed := fmt.Sprint(len(diff.UnhandledInputs))
		if diff.UnhandledInputsTruncated {
			listed += " or more"
		}
		section.Summary += fmt.Sprintf(" The recordings were also compared request by request: %s request field(s) changed between them but are still sent as recorded.", listed)
		for itemIndex, input := range diff.UnhandledInputs {
			if itemIndex >= maxHARPairUnhandledInputItems {
				section.Items = append(section.Items, fmt.Sprintf("%d more changed field(s) are listed in har_diff.unhandled_inputs", len(diff.UnhandledInputs)-itemIndex))
				break
			}
			section.Items = append(section.Items, fmt.Sprintf("%s: still sent as recorded in %d request(s) (%d distinct value(s)); %s", input.Field, input.Requests, input.DistinctValues, input.Reason))
		}
		break
	}
	return sections
}
