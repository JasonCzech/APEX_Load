package api

// handlers_import_trueclient — split from handlers_scripts.go (same package, behavior-preserving).

import (
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/enterprisearchive"
	"github.com/jasonczech/apexload/pkg/convert/trueclient"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func trueClientWeb2RepairSuggestions(summary enterpriseArchiveImportSummary) []scriptImportRepairSuggestion {
	inspection := summary.TrueClientInspection
	if inspection == nil {
		return nil
	}
	suggestions := []scriptImportRepairSuggestion{
		{
			ID:          "web2:replay-evidence",
			Category:    "replay",
			Severity:    "warning",
			Title:       "Attach browser replay evidence",
			Description: "Web 2.0 browser-profile archives are review-only until selector, wait, runtime, and screenshot evidence is captured.",
			Action:      "Run browser replay from the visual editor and attach the replay report before approving the imported draft.",
		},
		{
			ID:          "web2:object-identification",
			Category:    "selector",
			Severity:    "warning",
			Title:       "Review Web 2.0 object identification",
			Description: fmt.Sprintf("%d object repository file(s), %d descriptor(s), and %d replay diagnostic(s) were preserved for selector repair.", len(inspection.ObjectRepositories), len(inspection.ObjectDescriptors), trueClientReplayDiagnosticCount(inspection)),
			Action:      "Open the object-identification review section, compare ranked selector candidates with replay evidence, and stabilize brittle selectors before promotion.",
		},
		{
			ID:          "web2:http-conversion",
			Category:    "conversion",
			Severity:    "info",
			Title:       "Gate browser-to-HTTP conversion",
			Description: "Web 2.0 browser drafts should not be promoted to high-density HTTP load until network capture and correlation findings are reviewed.",
			Action:      "Capture replay traffic, review conversion blockers and correlation candidates, then generate an HTTP draft only for eligible flows.",
		},
	}
	if len(inspection.ParameterFiles) > 0 {
		suggestions = append(suggestions, scriptImportRepairSuggestion{
			ID:          "web2:parameter-fit",
			Category:    "data",
			Severity:    "info",
			Title:       "Verify Web 2.0 parameter data fit",
			Description: fmt.Sprintf("%d parameter file(s) were discovered for the imported Web 2.0 flow.", len(inspection.ParameterFiles)),
			Action:      "Open parameter data in the script editor, confirm row strategy and exhaustion behavior, and replay with representative data.",
		})
	}
	if len(inspection.UnsupportedFragments) > 0 {
		severity := "warning"
		for _, fragment := range inspection.UnsupportedFragments {
			if fragment.Status == trueclient.ScopeOutOfScope {
				severity = "blocker"
				break
			}
		}
		suggestions = append(suggestions, scriptImportRepairSuggestion{
			ID:          "web2:source-fragments",
			Category:    "unsupported",
			Severity:    severity,
			Title:       "Resolve Web 2.0 source fragments",
			Description: fmt.Sprintf("%d review-only or unsupported source fragment(s) were preserved from the source archive.", len(inspection.UnsupportedFragments)),
			Action:      "Use the Web 2.0 source-fragment review section to replace unsupported JavaScript/API behavior with supported browser actions or documented manual steps.",
		})
	}
	return suggestions
}

func trueClientWeb2ReviewSections(summary enterpriseArchiveImportSummary) []scriptImportReviewSection {
	inspection := summary.TrueClientInspection
	if inspection == nil {
		return nil
	}
	sourceType := firstNonEmpty(summary.SourceScriptType, inspection.ScriptType)
	sourceProfile := summary.SourceProfile
	if sourceProfile == "" && inspection.ScriptTypeScope != nil {
		sourceProfile = inspection.ScriptTypeScope.APEXLoadShape
	}
	items := []string{
		fmt.Sprintf("Source script type: %s", firstNonEmpty(sourceType, "browser-profile archive")),
		fmt.Sprintf("Source profile: %s", firstNonEmpty(sourceProfile, "browser profile")),
		fmt.Sprintf("Executable steps inspected: %d", inspection.StepInventory.Total),
		fmt.Sprintf("Supported step families: %d", len(inspection.StepInventory.Supported)),
	}
	if len(inspection.StepInventory.ReviewOnly) > 0 {
		items = append(items, fmt.Sprintf("Review-only step families: %d", len(inspection.StepInventory.ReviewOnly)))
	}
	if len(inspection.StepInventory.Unsupported) > 0 {
		items = append(items, fmt.Sprintf("Unsupported step families: %d", len(inspection.StepInventory.Unsupported)))
	}
	sections := []scriptImportReviewSection{{
		Key:      "web2_profile_summary",
		Title:    "Web 2.0 Browser Profile Summary",
		Severity: "warning",
		Summary:  "This Web 2.0 browser-profile archive was converted into a review-only browser draft.",
		Items:    items,
	}}

	if len(inspection.UnsupportedFragments) > 0 {
		fragmentItems := make([]string, 0, len(inspection.UnsupportedFragments))
		severity := "warning"
		for _, fragment := range inspection.UnsupportedFragments {
			if fragment.Status == trueclient.ScopeOutOfScope {
				severity = "blocker"
			}
			label := fragment.Action
			if fragment.Index != "" {
				label += " #" + fragment.Index
			}
			fragmentItems = append(fragmentItems, fmt.Sprintf("%s: %s", label, fragment.Reason))
		}
		sections = append(sections, scriptImportReviewSection{
			Key:      "web2_unsupported_source_fragments",
			Title:    "Web 2.0 Source Fragments",
			Severity: severity,
			Summary:  "Review-only or unsupported TruClient fragments were preserved for repair before approval.",
			Items:    fragmentItems,
		})
	}

	if summary.TrueClient2CompatReport != nil && summary.TrueClient2CompatReport.JavaScriptReview != nil && summary.TrueClient2CompatReport.JavaScriptReview.ApprovalRequired {
		jsReview := summary.TrueClient2CompatReport.JavaScriptReview
		severity := "warning"
		if jsReview.Status == "rejected" {
			severity = "blocker"
		} else if jsReview.Status == "approved" {
			severity = "info"
		}
		jsItems := []string{
			"Policy: " + jsReview.Policy,
			fmt.Sprintf("Actions requiring approval: %d", jsReview.RequiredActions),
			fmt.Sprintf("Approved: %d; pending: %d; rejected: %d", jsReview.ApprovedActions, jsReview.PendingActions, jsReview.RejectedActions),
		}
		for _, review := range jsReview.ActionReviews {
			label := firstNonEmpty(review.Name, fmt.Sprintf("action_%d", review.Index))
			jsItems = append(jsItems, fmt.Sprintf("%s: %s (%s)", label, review.Status, firstNonEmpty(review.Reason, review.Gate, "JavaScript review required")))
		}
		sections = append(sections, scriptImportReviewSection{
			Key:      "web2_javascript_review",
			Title:    "JavaScript Review Gate",
			Severity: severity,
			Summary:  "Review-gated JavaScript requires explicit approval metadata before production promotion.",
			Items:    jsItems,
		})
	}

	objectItems := make([]string, 0, len(inspection.ObjectRepositories)+1)
	for _, file := range inspection.ObjectRepositories {
		objectItems = append(objectItems, "Object repository: "+file.Path)
	}
	if len(inspection.ObjectDescriptors) > 0 {
		objectItems = append(objectItems, fmt.Sprintf("Object descriptors parsed: %d", len(inspection.ObjectDescriptors)))
	}
	if candidateCount := trueClientSelectorCandidateCount(inspection); candidateCount > 0 {
		objectItems = append(objectItems, fmt.Sprintf("Selector candidates extracted: %d", candidateCount))
	}
	if diagnosticCount := trueClientReplayDiagnosticCount(inspection); diagnosticCount > 0 {
		objectItems = append(objectItems, fmt.Sprintf("Replay diagnostics prepared: %d", diagnosticCount))
	}
	objectItems = append(objectItems, "Imported selectors are ranked from step arguments and object repositories; replay remains required before promotion.")
	sections = append(sections, scriptImportReviewSection{
		Key:      "web2_object_identification",
		Title:    "Object Identification",
		Severity: "warning",
		Summary:  fmt.Sprintf("%d object repository file(s) were preserved for selector review.", len(inspection.ObjectRepositories)),
		Items:    objectItems,
	})

	if len(inspection.ParameterFiles) > 0 {
		parameterItems := make([]string, 0, len(inspection.ParameterFiles))
		for _, file := range inspection.ParameterFiles {
			parameterItems = append(parameterItems, "Parameter data: "+file.Path)
		}
		sections = append(sections, scriptImportReviewSection{
			Key:      "web2_parameter_mapping",
			Title:    "Parameter Mapping",
			Severity: "info",
			Summary:  "Parameter files were discovered and preserved for data-source fit review.",
			Items:    parameterItems,
		})
	}

	sections = append(sections, scriptImportReviewSection{
		Key:      "web2_replay_readiness",
		Title:    "Replay Readiness",
		Severity: "warning",
		Summary:  "Browser replay evidence is required before this review-only Web 2.0 draft is approved.",
		Items: []string{
			"Run browser replay against the target application.",
			"Review screenshots, selector failures, wait behavior, and runtime settings parity.",
			"Do not promote to a reusable production asset until replay evidence is attached.",
		},
	})
	sections = append(sections, scriptImportReviewSection{
		Key:      "web2_http_conversion_eligibility",
		Title:    "HTTP Conversion Eligibility",
		Severity: "info",
		Summary:  "Browser-to-HTTP conversion remains a governed follow-up flow for Web 2.0 imports.",
		Items: []string{
			"Capture network requests during replay before generating an HTTP draft.",
			"Review correlation candidates and conversion blockers before using HTTP for density load.",
		},
	})
	return sections
}

func isWeb2TrueClientInspection(inspection *trueclient.ArchiveInspection) bool {
	if inspection == nil || inspection.ScriptTypeScope == nil {
		return false
	}
	return inspection.ScriptTypeScope.CanonicalName == "truclient2_web" || inspection.ScriptTypeScope.CanonicalName == "truclient_web"
}

func trueClientSelectorCandidateCount(inspection *trueclient.ArchiveInspection) int {
	if inspection == nil {
		return 0
	}
	count := 0
	for _, step := range inspection.Steps {
		count += len(step.SelectorCandidates)
	}
	return count
}

func trueClientReplayDiagnosticCount(inspection *trueclient.ArchiveInspection) int {
	if inspection == nil {
		return 0
	}
	count := 0
	for _, step := range inspection.Steps {
		count += len(step.ReplayDiagnostics)
	}
	return count
}

func buildWeb2MigrationReadinessReport(result *enterprisearchive.ConvertResult, summary enterpriseArchiveImportSummary) *web2MigrationReadinessReport {
	inspection := summary.TrueClientInspection
	if inspection == nil {
		return nil
	}
	converted := 0
	skipped := 0
	if result != nil {
		converted = result.Stats.ConvertedActions
		skipped = result.Stats.Skipped
	}
	reviewSteps := skipped + len(inspection.UnsupportedFragments)
	for _, count := range inspection.StepInventory.ReviewOnly {
		reviewSteps += count
	}
	for _, count := range inspection.StepInventory.Unsupported {
		reviewSteps += count
	}
	runtimeStatus := "ready"
	if summary.MigrationReadiness != nil {
		if summary.MigrationReadiness.Blocked {
			runtimeStatus = "blocked"
		} else if summary.MigrationReadiness.ScorePct < 100 {
			runtimeStatus = "needs_review"
		}
	} else if len(summary.RuntimeSettingsParity) > 0 {
		runtimeStatus = "needs_review"
	}

	report := &web2MigrationReadinessReport{
		ImportedSteps:             inspection.StepInventory.Total,
		ConvertedSteps:            converted,
		SkippedReviewSteps:        reviewSteps,
		SelectorConfidencePct:     averageTrueClientSelectorConfidence(inspection),
		ReplayStatus:              "required",
		RuntimeParityStatus:       runtimeStatus,
		ConversionToHTTPReadiness: "needs_replay_network",
		Findings: []string{
			fmt.Sprintf("%d imported Web 2.0 browser-profile step(s), %d converted browser action(s), %d skipped or review step(s).", inspection.StepInventory.Total, converted, reviewSteps),
			"Browser replay evidence is required before approval.",
			"Browser-to-HTTP conversion requires replay network capture and generated HTTP validation.",
		},
		NextActions: []string{
			"Run browser replay and review screenshots, DOM, console, network, and Core Web Vitals evidence.",
			"Open object descriptors and repair low-confidence selectors before promotion.",
			"Use /api/v1/scripts/browser-to-http/preview after replay network evidence is available.",
		},
	}
	if runtimeStatus == "blocked" {
		report.Findings = append(report.Findings, "Runtime parity blockers must be resolved or the import policy relaxed before saving.")
	}
	if report.SelectorConfidencePct == 0 {
		report.Findings = append(report.Findings, "No selector confidence evidence was available in the imported object descriptors.")
	} else if report.SelectorConfidencePct < 70 {
		report.Findings = append(report.Findings, "Average selector confidence is below the recommended migration threshold.")
	}
	return report
}

func buildTrueClient2CompatReport(result *enterprisearchive.ConvertResult, summary enterpriseArchiveImportSummary) *trueClient2CompatReport {
	inspection := summary.TrueClientInspection
	if inspection == nil {
		return nil
	}
	converted := 0
	skipped := 0
	importMode := summary.ImportMode
	if result != nil {
		converted = result.Stats.ConvertedActions
		skipped = result.Stats.Skipped
		if result.DetectedProtocol != nil && importMode == "" {
			importMode = string(result.DetectedProtocol.ImportMode)
		}
	}
	reviewSteps := skipped + len(inspection.UnsupportedFragments)
	for _, count := range inspection.StepInventory.ReviewOnly {
		reviewSteps += count
	}
	for _, count := range inspection.StepInventory.Unsupported {
		reviewSteps += count
	}
	frameWindowBlockers := trueClientFrameWindowBlockerCount(inspection)
	replayStatus := trueClientReplayStatus(result)
	runtimeStatus := trueClientRuntimeParityStatus(summary)
	jsReview := buildTrueClientJavaScriptReviewPolicy(result, inspection, replayStatus)
	jsGate := "clear"
	if jsReview != nil {
		jsGate = jsReview.Status
	}
	httpReadiness := "needs_replay_network"
	if replayStatus == "passed" && (jsGate == "clear" || jsGate == "approved") {
		httpReadiness = "ready_for_preview"
	}
	promotionAllowed := importMode == string(enterprisearchive.ImportModeExecutable) &&
		replayStatus == "passed" &&
		(jsGate == "clear" || jsGate == "approved") &&
		frameWindowBlockers == 0 &&
		runtimeStatus != "blocked" &&
		len(inspection.StepInventory.Unsupported) == 0
	promotionStatus := "blocked_review_only"
	if promotionAllowed {
		promotionStatus = "promotion_ready"
	} else if replayStatus == "passed" {
		promotionStatus = "needs_final_review"
	} else if frameWindowBlockers > 0 || jsGate != "clear" || runtimeStatus == "blocked" || len(inspection.StepInventory.Unsupported) > 0 {
		promotionStatus = "blocked"
	}

	report := &trueClient2CompatReport{
		SourceScriptType:        firstNonEmpty(summary.SourceScriptType, inspection.ScriptType),
		SourceProfile:           summary.SourceProfile,
		ImportMode:              importMode,
		PromotionStatus:         promotionStatus,
		PromotionAllowed:        promotionAllowed,
		ImportedSteps:           inspection.StepInventory.Total,
		ConvertedActions:        converted,
		SkippedReviewSteps:      reviewSteps,
		SupportedStepFamilies:   len(inspection.StepInventory.Supported),
		ReviewOnlyStepFamilies:  len(inspection.StepInventory.ReviewOnly),
		UnsupportedStepFamilies: len(inspection.StepInventory.Unsupported),
		SelectorConfidencePct:   averageTrueClientSelectorConfidence(inspection),
		SelectorCandidateCount:  trueClientSelectorCandidateCount(inspection),
		RuntimeParityStatus:     runtimeStatus,
		ReplayStatus:            replayStatus,
		JavaScriptReviewGate:    jsGate,
		JavaScriptReview:        jsReview,
		FrameWindowBlockers:     frameWindowBlockers,
		HTTPConversionReadiness: httpReadiness,
		FixtureContractStatus:   trueClientFixtureContractStatus(),
		Findings: []string{
			fmt.Sprintf("%d imported step(s) produced %d browser action(s).", inspection.StepInventory.Total, converted),
		},
		NextActions: []string{
			"Run browser replay and attach screenshot, DOM, console, and network evidence.",
			"Review selector candidates and repair low-confidence locators before approval.",
		},
	}
	if jsGate != "clear" {
		switch jsGate {
		case "approved":
			report.Findings = append(report.Findings, "Review-gated JavaScript has approval metadata; replay evidence is still required before promotion.")
		case "rejected":
			report.Findings = append(report.Findings, "At least one review-gated JavaScript action was rejected and must be replaced before promotion.")
		default:
			report.Findings = append(report.Findings, "Review-gated JavaScript is present and must be approved or replaced before promotion.")
			report.NextActions = append(report.NextActions, "Approve browser_evaluate_js only after code review and replay evidence, or replace it with native browser actions.")
		}
	}
	if frameWindowBlockers > 0 {
		report.Findings = append(report.Findings, fmt.Sprintf("%d iframe/window context blocker(s) require frame or popup replay evidence.", frameWindowBlockers))
		report.NextActions = append(report.NextActions, "Replay frame and popup actions and verify active page/frame targeting.")
	}
	if runtimeStatus == "blocked" {
		report.Findings = append(report.Findings, "Runtime parity policy reports blocking settings.")
	}
	if report.SelectorConfidencePct == 0 {
		report.Findings = append(report.Findings, "No selector confidence evidence was available.")
	} else if report.SelectorConfidencePct < 70 {
		report.Findings = append(report.Findings, "Average selector confidence is below the recommended migration threshold.")
	}
	if !promotionAllowed {
		report.NextActions = append(report.NextActions, "Keep this draft review-only until blockers are resolved and replay evidence passes.")
	}
	return report
}

func trueClientRuntimeParityStatus(summary enterpriseArchiveImportSummary) string {
	if summary.MigrationReadiness != nil {
		if summary.MigrationReadiness.Blocked {
			return "blocked"
		}
		if summary.MigrationReadiness.ScorePct < 100 {
			return "needs_review"
		}
		return "ready"
	}
	if len(summary.RuntimeSettingsParity) > 0 {
		return "needs_review"
	}
	return "not_imported"
}

func trueClientFrameWindowBlockerCount(inspection *trueclient.ArchiveInspection) int {
	if inspection == nil {
		return 0
	}
	count := 0
	for _, step := range inspection.Steps {
		for _, diagnostic := range step.ReplayDiagnostics {
			if diagnostic.Code == "frame_mismatch_risk" || diagnostic.Code == "window_context_risk" {
				count++
			}
		}
	}
	return count
}

func buildTrueClientJavaScriptReviewPolicy(result *enterprisearchive.ConvertResult, inspection *trueclient.ArchiveInspection, replayStatus string) *trueClientJavaScriptReviewPolicyReport {
	report := &trueClientJavaScriptReviewPolicyReport{
		Status:                 "clear",
		Policy:                 "explicit_approval_required",
		ReplayEvidenceRequired: true,
		NextActions:            []string{"No review-gated JavaScript actions were imported."},
	}
	if inspection != nil {
		report.SourceOnlyFragments = inspection.StepInventory.ReviewOnly["evaluate_javascript"]
	}
	if result != nil && result.Script != nil {
		actions := append(append([]dsl.Action{}, result.Script.Init...), append(result.Script.Actions, result.Script.Teardown...)...)
		for index, action := range actions {
			if !trueClientActionRequiresJavaScriptReview(action) {
				continue
			}
			review := trueClientJavaScriptActionReviewFor(index+1, action)
			report.ActionReviews = append(report.ActionReviews, review)
			report.RequiredActions++
			switch review.Status {
			case "approved":
				report.ApprovedActions++
			case "rejected":
				report.RejectedActions++
			default:
				report.PendingActions++
			}
		}
	}
	if report.RequiredActions == 0 && report.SourceOnlyFragments == 0 {
		report.ApprovalRequired = false
		report.ReplayEvidenceRequired = false
		report.Findings = []string{"No browser_evaluate_js or source-only Evaluate JavaScript steps require approval."}
		return report
	}
	report.ApprovalRequired = true
	report.NextActions = []string{
		"Review each browser_evaluate_js action against source JavaScript code.",
		"Attach reviewer, approval timestamp, and replay evidence before production promotion.",
		"Prefer replacing JavaScript with native browser actions when possible.",
	}
	switch {
	case report.RejectedActions > 0:
		report.Status = "rejected"
		report.Findings = []string{fmt.Sprintf("%d review-gated JavaScript action(s) were rejected.", report.RejectedActions)}
	case report.PendingActions > 0 || report.SourceOnlyFragments > 0:
		report.Status = "required"
		report.Findings = []string{fmt.Sprintf("%d JavaScript action(s) require explicit approval; %d are pending.", report.RequiredActions, report.PendingActions)}
	case replayStatus != "passed":
		report.Status = "approved_pending_replay"
		report.Findings = []string{"All imported JavaScript actions carry approval metadata, but replay evidence has not passed."}
	default:
		report.Status = "approved"
		report.Findings = []string{"All imported JavaScript actions carry approval metadata and replay evidence has passed."}
	}
	if report.SourceOnlyFragments > 0 {
		report.Findings = append(report.Findings, fmt.Sprintf("%d source Evaluate JavaScript fragment(s) remain review-only in the archive inspection.", report.SourceOnlyFragments))
	}
	return report
}

func trueClientActionRequiresJavaScriptReview(action dsl.Action) bool {
	if action.Type == "browser_evaluate_js" {
		return true
	}
	if strings.EqualFold(stringMapAnyValue(action.Metadata, "review_gate"), "javascript") {
		return true
	}
	if strings.EqualFold(stringMapAnyValue(action.Metadata, "compatibility_gate"), "trueclient2_javascript_review") {
		return true
	}
	if strings.Contains(strings.ToLower(stringMapAnyValue(action.Metadata, "source")), "evaluate_javascript") {
		return true
	}
	return boolMapValue(action.Metadata, "promotion_blocker") && strings.Contains(strings.ToLower(stringMapAnyValue(action.Metadata, "review_reason")), "javascript")
}

func trueClientJavaScriptActionReviewFor(index int, action dsl.Action) trueClientJavaScriptActionReview {
	status := strings.ToLower(strings.TrimSpace(firstNonEmpty(
		stringMapAnyValue(action.Metadata, "javascript_review_status"),
		stringMapAnyValue(action.Metadata, "js_review_status"),
		stringMapAnyValue(action.Metadata, "review_status"),
	)))
	if status == "" {
		if boolMapValue(action.Metadata, "javascript_review_approved") || boolMapValue(action.Metadata, "js_review_approved") {
			status = "approved"
		} else {
			status = "pending"
		}
	}
	switch status {
	case "approved", "approve", "accepted":
		status = "approved"
	case "rejected", "reject", "blocked", "denied":
		status = "rejected"
	default:
		status = "pending"
	}
	reviewer := firstNonEmpty(
		stringMapAnyValue(action.Metadata, "javascript_review_reviewer"),
		stringMapAnyValue(action.Metadata, "js_review_reviewer"),
		stringMapAnyValue(action.Metadata, "approved_by"),
	)
	if status == "approved" && reviewer == "" {
		status = "pending"
	}
	return trueClientJavaScriptActionReview{
		Index:           index,
		Name:            action.Name,
		Type:            action.Type,
		Status:          status,
		Gate:            firstNonEmpty(stringMapAnyValue(action.Metadata, "compatibility_gate"), stringMapAnyValue(action.Metadata, "review_gate")),
		Reason:          stringMapAnyValue(action.Metadata, "review_reason"),
		Reviewer:        reviewer,
		ApprovedAt:      firstNonEmpty(stringMapAnyValue(action.Metadata, "javascript_review_approved_at"), stringMapAnyValue(action.Metadata, "js_review_approved_at"), stringMapAnyValue(action.Metadata, "approved_at")),
		Source:          stringMapAnyValue(action.Metadata, "source"),
		SourceStepID:    stringMapAnyValue(action.Metadata, "source_step_id"),
		SourceStepIndex: stringMapAnyValue(action.Metadata, "source_step_index"),
		ScriptPreview:   truncateReviewText(firstNonEmpty(action.Target, action.Value, action.Body), 160),
	}
}

func trueClientReplayStatus(result *enterprisearchive.ConvertResult) string {
	if result == nil || result.Script == nil || result.Script.Metadata == nil || result.Script.Metadata.Import == nil {
		return "required"
	}
	preserved := result.Script.Metadata.Import.Preserved
	if preserved == nil {
		return "required"
	}
	for _, key := range []string{"browser_replay_status", "replay_status"} {
		switch strings.ToLower(strings.TrimSpace(stringValue(preserved[key]))) {
		case "passed", "pass", "success", "successful":
			return "passed"
		case "failed", "failure":
			return "failed"
		}
	}
	if !isNilImportValue(preserved["browser_replay_evidence"]) || !isNilImportValue(preserved["replay_evidence"]) {
		return "evidence_attached"
	}
	return "required"
}

func trueClientFixtureContractStatus() map[string]string {
	status := map[string]string{}
	for _, fixture := range trueclient.Web2FixtureContracts() {
		status[fixture.ID] = string(fixture.Status)
	}
	return status
}

func boolMapValue(values map[string]any, key string) bool {
	if values == nil {
		return false
	}
	switch value := values[key].(type) {
	case bool:
		return value
	case string:
		return strings.EqualFold(strings.TrimSpace(value), "true")
	default:
		return false
	}
}

func averageTrueClientSelectorConfidence(inspection *trueclient.ArchiveInspection) int {
	if inspection == nil {
		return 0
	}
	total := 0
	count := 0
	for _, step := range inspection.Steps {
		for _, candidate := range step.SelectorCandidates {
			total += candidate.Confidence
			count++
		}
	}
	if count == 0 {
		return 0
	}
	return int(float64(total)/float64(count) + 0.5)
}

func browserImportRepairSuggestions(script *dsl.Script) []scriptImportRepairSuggestion {
	suggestions := []scriptImportRepairSuggestion{
		{
			ID:          "browser:replay-validation",
			Category:    "replay",
			Severity:    "warning",
			Title:       "Run browser replay before saving as production-ready",
			Description: "Imported browser scripts depend on source selectors, waits, and device assumptions that should be verified against a live page.",
			Action:      "Open the imported draft in the visual editor and run browser replay. Update selectors or waits for any failed action.",
		},
	}

	for index, action := range script.Actions {
		actionID := firstNonEmpty(action.Name, fmt.Sprintf("action_%d", index+1))
		switch action.Type {
		case "browser_click", "browser_fill", "browser_select", "browser_tap", "browser_long_press", "browser_swipe":
			selector := firstNonEmpty(action.Selector, action.Target)
			if selector != "" {
				score, signals := browserSelectorStabilityScore(selector)
				suggestions = append(suggestions, scriptImportRepairSuggestion{
					ID:                     fmt.Sprintf("selector:%d:%s", index+1, stableReviewKey(actionID)),
					Category:               "selector",
					Severity:               "warning",
					Title:                  "Validate selector for " + actionID,
					Description:            fmt.Sprintf("Imported selector stability score is %d/100. Recorder object metadata and decomposed JavaScript selectors should be checked before promotion.", score),
					Action:                 browserSelectorRepairAction(score),
					TargetAction:           actionID,
					SelectorStabilityScore: score,
					SelectorSignals:        signals,
				})
			}
			if action.WaitFor == nil {
				suggestions = append(suggestions, scriptImportRepairSuggestion{
					ID:           fmt.Sprintf("wait-after:%d:%s", index+1, stableReviewKey(actionID)),
					Category:     "wait",
					Severity:     "info",
					Title:        "Add readiness wait after " + actionID,
					Description:  "Browser interaction steps imported without a readiness condition can race dynamic pages during replay.",
					Action:       "Attach a wait_for visible/text/url condition to the action or insert a browser_wait step using the page-ready signal seen in replay.",
					TargetAction: actionID,
				})
			}
		case "browser_wait":
			waitType := ""
			if action.WaitFor != nil {
				waitType = action.WaitFor.Type
			}
			suggestions = append(suggestions, scriptImportRepairSuggestion{
				ID:           fmt.Sprintf("wait:%d:%s", index+1, stableReviewKey(actionID)),
				Category:     "wait",
				Severity:     "info",
				Title:        "Review wait condition for " + actionID,
				Description:  "Imported waits may represent timing intent rather than the most stable page-ready condition.",
				Action:       "Prefer visible selector or text waits over fixed durations when the application exposes a reliable readiness signal.",
				TargetAction: firstNonEmpty(actionID, waitType),
			})
		}
	}

	return suggestions
}

func browserSelectorRepairAction(score int) string {
	if score >= 80 {
		return "Keep the selector if replay is stable; prefer data-testid or aria attributes for future recorder output."
	}
	if score >= 60 {
		return "Prefer stable attributes such as data-testid, aria labels, or unique form names; confirm the selector with browser replay."
	}
	return "Replace this selector with a stable data-testid, aria label, role/name, or unique form-name selector before approving the migrated script."
}

func browserSelectorStabilityScore(selector string) (int, []string) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return 0, []string{"empty selector"}
	}
	lower := strings.ToLower(selector)
	score := 45
	signals := make([]string, 0, 5)

	if strings.Contains(lower, "data-testid") || strings.Contains(lower, "data-test") || strings.Contains(lower, "data-qa") {
		score += 35
		signals = append(signals, "stable test attribute")
	}
	if strings.Contains(lower, "aria-label") || strings.Contains(lower, "role=") {
		score += 25
		signals = append(signals, "accessible selector")
	}
	if strings.Contains(lower, "[name=") || strings.Contains(lower, "[name'") || strings.Contains(lower, "[name\"") {
		score += 20
		signals = append(signals, "named form field")
	}
	if strings.HasPrefix(selector, "#") && !looksGeneratedSelectorToken(selector) {
		score += 20
		signals = append(signals, "simple id selector")
	}
	if strings.Contains(lower, ":nth-child") || strings.Contains(lower, ":nth-of-type") {
		score -= 30
		signals = append(signals, "positional selector")
	}
	if strings.HasPrefix(lower, "/html") || strings.HasPrefix(lower, "html > body") || strings.HasPrefix(lower, "body >") {
		score -= 25
		signals = append(signals, "absolute DOM path")
	}
	if looksGeneratedSelectorToken(selector) {
		score -= 20
		signals = append(signals, "generated-looking token")
	}
	if strings.Contains(selector, " ") || strings.Contains(selector, ">") {
		score -= 5
		signals = append(signals, "composite selector")
	}
	if len(signals) == 0 {
		signals = append(signals, "generic selector")
	}
	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}
	return score, signals
}

func looksGeneratedSelectorToken(selector string) bool {
	token := strings.ToLower(selector)
	digitCount := 0
	separatorCount := 0
	for _, r := range token {
		switch {
		case r >= '0' && r <= '9':
			digitCount++
		case r == '-' || r == '_' || r == ':':
			separatorCount++
		}
	}
	if digitCount >= 4 && separatorCount >= 1 {
		return true
	}
	return strings.Contains(token, "ember") || strings.Contains(token, "react-select") || strings.Contains(token, "generated")
}
