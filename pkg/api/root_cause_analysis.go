package api

import (
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/rca"
)

const rootCauseSchemaVersion = "apexload.root_cause.v1"

type RunRootCauseSummary struct {
	SchemaVersion string                 `json:"schema_version"`
	TestID        string                 `json:"test_id"`
	GeneratedAt   time.Time              `json:"generated_at"`
	Status        string                 `json:"status"`
	Confidence    string                 `json:"confidence"`
	Summary       string                 `json:"summary"`
	Signals       []RootCauseSignal      `json:"signals,omitempty"`
	TopFindings   []RootCauseFinding     `json:"top_findings,omitempty"`
	NextActions   []string               `json:"next_actions,omitempty"`
	Evidence      []string               `json:"evidence,omitempty"`
	Report        map[string]interface{} `json:"report,omitempty"`
}

type RootCauseSignal struct {
	Name     string  `json:"name"`
	Status   string  `json:"status"`
	Value    string  `json:"value,omitempty"`
	Severity string  `json:"severity,omitempty"`
	Score    float64 `json:"score,omitempty"`
}

type RootCauseFinding struct {
	Category string   `json:"category"`
	Severity string   `json:"severity"`
	Title    string   `json:"title"`
	Detail   string   `json:"detail"`
	Evidence []string `json:"evidence,omitempty"`
	Score    float64  `json:"score"`
}

func (s *Server) handleGetRunArchiveRootCause(w http.ResponseWriter, r *http.Request) {
	record, lease, ok := s.requireRunArchive(w, r)
	if !ok {
		return
	}
	defer lease.Release()
	report, err := cachedRunArchiveReport(record)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build root-cause summary: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, buildRunRootCauseSummary(r.PathValue("id"), report))
}

func buildRunRootCauseSummary(testID string, report *RunArchiveReport) RunRootCauseSummary {
	summary := RunRootCauseSummary{
		SchemaVersion: rootCauseSchemaVersion,
		TestID:        strings.TrimSpace(testID),
		GeneratedAt:   time.Now().UTC(),
		Status:        "healthy",
		Confidence:    "medium",
		Evidence:      []string{"/api/v1/tests/" + strings.TrimSpace(testID) + "/archive/report"},
		Report: map[string]interface{}{
			"profile": "engineer",
		},
	}
	if report == nil || report.Manifest == nil {
		summary.Status = "unknown"
		summary.Confidence = "low"
		summary.Summary = "No archived report evidence is available for root-cause correlation."
		return summary
	}
	addSignal := func(name string, status string, value string, severity string, score float64) {
		summary.Signals = append(summary.Signals, RootCauseSignal{Name: name, Status: status, Value: value, Severity: severity, Score: score})
	}
	addFinding := func(category string, severity string, title string, detail string, evidence []string, score float64) {
		summary.TopFindings = append(summary.TopFindings, RootCauseFinding{
			Category: category,
			Severity: severity,
			Title:    title,
			Detail:   detail,
			Evidence: evidence,
			Score:    score,
		})
	}

	for _, name := range sortedArchiveReportStatKeys(report.ByAction) {
		for _, finding := range report.ByAction[name].DiagnosticFindings {
			addFinding("protocol", "warning", "Advisory: "+finding.Rule, name+": "+finding.Message, []string{"by_action." + name + ".diagnostic_findings"}, 50)
		}
	}
	for i, finding := range report.StatisticalFindings {
		if i >= 10 {
			break
		}
		addFinding("statistical", "warning", "Advisory: "+finding.MetricName,
			fmt.Sprintf("%.2f standard deviations from %d comparable buckets (%s to %s). Association does not establish causation.", finding.ZScore, finding.BaselineBuckets, finding.BaselineStart.Format(time.RFC3339), finding.BaselineEnd.Format(time.RFC3339)),
			[]string{fmt.Sprintf("statistical_findings[%d]", i)}, 30)
	}
	for _, coverage := range report.Manifest.MonitorCoverage {
		if coverage.Reason != "" {
			addFinding("monitoring", "warning", "Incomplete monitor coverage", coverage.SourceID+": "+coverage.Reason, []string{"manifest.monitor_coverage"}, 35)
		}
	}
	slaState := archiveSLAState(report.Manifest)
	switch slaState {
	case archiveSLABreached:
		addSignal("SLA", slaState, strings.Join(report.Manifest.SLADetails, "; "), "critical", 95)
		addFinding("sla", "critical", "Release SLA breached", firstNonEmpty(strings.Join(report.Manifest.SLADetails, "; "), "The run archive marked one or more SLA checks as breached."), []string{"manifest.sla_breached", "manifest.sla_details"}, 95)
	case archiveSLAPassed:
		addSignal("SLA", slaState, "No archived SLA breach", "healthy", 5)
	case archiveSLANotEvaluated:
		addSignal("SLA", slaState, "SLA criteria are configured but the archive could not evaluate them", "warning", 40)
		addFinding("sla", "warning", "SLA criteria could not be evaluated", "SLA criteria are configured for this run, but the archived evidence could not evaluate every criterion, so no acceptance verdict is available.", []string{"manifest.sla_evaluated", "manifest.transaction_response_time_sla_results"}, 40)
	case archiveSLANotConfigured:
		addSignal("SLA", slaState, "No SLA criteria were configured for this run", "info", 0)
	default:
		addSignal("SLA", slaState, "The archive does not record whether SLA criteria were configured", "info", 0)
	}
	if report.Overview.Errors > 0 || report.Overview.ErrorPct > 0 {
		severity := "warning"
		score := math.Min(90, 45+report.Overview.ErrorPct)
		if report.Overview.ErrorPct >= 5 {
			severity = "critical"
		}
		addSignal("Errors", "elevated", fmt.Sprintf("%d errors, %.2f%%", report.Overview.Errors, report.Overview.ErrorPct), severity, score)
		addFinding("errors", severity, "Transaction errors contributed to the run outcome", fmt.Sprintf("Archived transactions include %d errors across %.2f%% of completed requests.", report.Overview.Errors, report.Overview.ErrorPct), []string{"overview.errors", "error_patterns"}, score)
	}
	if len(report.ErrorPatterns) > 0 {
		pattern := report.ErrorPatterns[0]
		addFinding("errors", "warning", "Dominant error pattern detected", fmt.Sprintf("%q occurred %d time(s).", pattern.Sample, pattern.Count), []string{"error_patterns[0]"}, 60)
	}
	if telemetry := report.Overview.Telemetry; telemetry != nil {
		addSignal("Telemetry", telemetry.Status, fmt.Sprintf("%d samples", telemetry.Samples), telemetrySeverity(telemetry.Status), telemetryScore(telemetry.Status))
		if telemetry.Status == "breached" || telemetry.Status == "degraded" {
			addFinding("telemetry", telemetrySeverity(telemetry.Status), "Telemetry health degraded during the run", telemetryRootCauseDetail(telemetry), []string{"overview.telemetry"}, telemetryScore(telemetry.Status))
		}
	}
	// The validity verdict replaces the peak generator-CPU rule: sustained
	// saturation is suspect, and lost or shed load is invalid.
	if validity := report.Validity; validity != nil {
		detail := validityRootCauseDetail(validity)
		switch validity.Verdict {
		case rca.ValidityInvalid:
			addSignal("Run validity", validity.Verdict, detail, "critical", 92)
			addFinding("resource", "critical", "Run is not a valid measurement", detail, []string{"validity"}, 92)
		case rca.ValiditySuspect:
			addSignal("Run validity", validity.Verdict, detail, "warning", 80)
			addFinding("resource", "warning", "Load generator evidence is suspect", detail, []string{"validity"}, 80)
		case rca.ValidityUnknown:
			addSignal("Run validity", validity.Verdict, detail, "info", 20)
		default:
			addSignal("Run validity", validity.Verdict, detail, "healthy", 5)
		}
	}
	if oracle := report.OracleDB; oracle != nil && oracle.Samples > 0 {
		severity := oracleDBSignalSeverity(oracle)
		status := oracle.Status
		if severity == "critical" {
			status = "saturated"
		} else if severity == "warning" && status == "ready" {
			status = "degraded"
		}
		addSignal("Oracle DB", status, fmt.Sprintf("%d resource sample(s), %d metric(s)", oracle.Samples, len(oracle.MetricStats)), severity, 55)
		if findingSeverity, title, detail, score, ok := oracleDBRootCauseFinding(oracle); ok {
			addFinding("oracle_db", findingSeverity, title, detail, []string{"oracle_db", "resource_samples.external_metrics", "summary_timeline"}, score)
		}
	}
	if report.Overview.ThroughputMissedIntervals > 0 {
		addSignal("Load model", "missed_target", fmt.Sprintf("%d interval(s)", report.Overview.ThroughputMissedIntervals), "warning", 55)
		addFinding("load_model", "warning", "Configured throughput target was missed", fmt.Sprintf("The run missed configured throughput in %d archived interval(s); max throughput gap was %.1f%%.", report.Overview.ThroughputMissedIntervals, report.Overview.MaxThroughputGapPct), []string{"overview.throughput_missed_intervals", "summary_timeline"}, 55)
	}
	// Legacy summaries use non-presence-aware floats and one action-sample
	// denominator. They cannot establish document-final metric eligibility,
	// even when every reported average happens to be below a threshold.
	browserUnqualified := report.Overview.Browser != nil
	if browser := report.Overview.Browser; browser != nil {
		summary.Report["browser_ux_model"] = "legacy-summary-availability-v2"
		summary.Report["browser_ux_applicability"] = "legacy action summaries; document metric eligibility unavailable"
		addSignal("Browser UX", "unscored", "Document metric eligibility is unavailable in legacy action summaries.", "unknown", 0)
		if observations := legacyBrowserAdvisoryThresholds(browser); len(observations) > 0 {
			addSignal("Browser timing thresholds", "observed_above_threshold", strings.Join(observations, "; "), "warning", 58)
			addFinding("browser", "warning", "Legacy browser timing exceeds advisory thresholds", "Reported action-summary averages exceeded legacy canary thresholds. Document-finality and per-metric eligibility are unavailable; this is not a standards-qualified Core Web Vitals result.", []string{"overview.browser", "browser_by_device"}, 58)
		}
	}
	if report.Completeness.Status != "" && report.Completeness.Status != "complete" {
		addFinding("reporting", "warning", "Archive evidence is incomplete", fmt.Sprintf("Report completeness is %s at %.0f%%.", report.Completeness.Status, report.Completeness.ScorePct), []string{"completeness"}, 35)
	}

	sort.SliceStable(summary.TopFindings, func(left, right int) bool {
		if summary.TopFindings[left].Score != summary.TopFindings[right].Score {
			return summary.TopFindings[left].Score > summary.TopFindings[right].Score
		}
		return rootCauseSeverityRank(summary.TopFindings[left].Severity) > rootCauseSeverityRank(summary.TopFindings[right].Severity)
	})
	// Status, confidence, and next actions are derived from every finding
	// before the display list is truncated, so a critical finding that ties
	// with earlier advisories can never be cut out of the verdict.
	critical := false
	warning := false
	categories := map[string]struct{}{}
	for _, finding := range summary.TopFindings {
		categories[finding.Category] = struct{}{}
		if finding.Severity == "critical" {
			critical = true
		}
		if finding.Severity == "warning" {
			warning = true
		}
	}
	switch {
	case critical:
		summary.Status = "breached"
	case warning:
		summary.Status = "degraded"
	case browserUnqualified:
		summary.Status = "unknown"
	default:
		summary.Status = "healthy"
	}
	summary.Confidence = rootCauseConfidence(report, summary.TopFindings)
	omitted := 0
	if len(summary.TopFindings) > rootCauseMaxTopFindings {
		omitted = len(summary.TopFindings) - rootCauseMaxTopFindings
		summary.TopFindings = summary.TopFindings[:rootCauseMaxTopFindings]
	}
	summary.Summary = rootCauseNarrative(summary.Status, summary.TopFindings, slaState, omitted)
	if summary.Status == "unknown" && browserUnqualified {
		summary.Confidence = "low"
		summary.Summary = "Browser action summaries are available, but document metric eligibility is missing. Browser UX remains unscored."
	}
	summary.NextActions = rootCauseNextActions(categories)
	if browserUnqualified {
		summary.NextActions = append(summary.NextActions, "Review browser evidence availability and document metric eligibility before assigning a browser UX verdict.")
	}
	if len(summary.NextActions) == 0 {
		summary.NextActions = []string{"Use the engineer report profile to review timeline, action, and resource evidence before closing the release gate."}
	}
	return summary
}

const rootCauseMaxTopFindings = 6

const (
	archiveSLABreached      = "breached"
	archiveSLAPassed        = "passed"
	archiveSLANotEvaluated  = "not_evaluated"
	archiveSLANotConfigured = "not_configured"
	archiveSLAUnknown       = "unknown"
)

// archiveSLAState separates "passed" from "nothing to evaluate". The manifest's
// SLAEvaluated flag is vacuously true when no criteria exist, so configuration
// is derived from the recorded goal config, per-group response-time SLAs, and
// evaluated results. Archives that predate run provenance cannot say whether
// criteria existed and report unknown rather than a verdict.
func archiveSLAState(manifest *RunArchiveManifest) string {
	if manifest == nil {
		return archiveSLAUnknown
	}
	if manifest.SLABreached {
		return archiveSLABreached
	}
	goals, _ := transactionResponseTimeSLAConfig(manifest)
	configured := goalConfigHasArchiveIndependentSLA(goals) ||
		len(transactionRulesFromManifest(manifest)) > 0 ||
		len(effectiveTransactionResponseTimeSLAThresholds(manifest)) > 0 ||
		len(manifest.TransactionResponseTimeSLAResults) > 0 ||
		len(manifest.SLADetails) > 0
	if !configured {
		if len(manifest.Provenance.Groups) == 0 && manifest.InitialProvenance == nil {
			return archiveSLAUnknown
		}
		return archiveSLANotConfigured
	}
	if manifest.SLAEvaluated {
		return archiveSLAPassed
	}
	return archiveSLANotEvaluated
}

func rootCauseSeverityRank(severity string) int {
	switch severity {
	case "critical":
		return 2
	case "warning":
		return 1
	default:
		return 0
	}
}

// Only finite, above-threshold reported values with a valid legacy sample
// count can produce an advisory component finding. Missing/zero values have
// no eligibility proof in this historical contract and never prove health.
func legacyBrowserAdvisoryThresholds(browser *BrowserLiveMetrics) []string {
	if browser == nil || browser.Samples <= 0 || browser.Samples > (1<<53)-1 {
		return nil
	}
	var observations []string
	for _, metric := range []struct {
		label     string
		value     float64
		threshold float64
		suffix    string
		digits    int
	}{
		{"Reported LCP", browser.AvgLCPMs, 2500, " ms", 0},
		{"Reported INP", browser.AvgINPMs, 200, " ms", 0},
		{"Reported CLS", browser.AvgCLSScore, 0.1, "", 3},
	} {
		if !math.IsNaN(metric.value) && !math.IsInf(metric.value, 0) && metric.value > metric.threshold {
			observations = append(observations, fmt.Sprintf("%s %.*f%s", metric.label, metric.digits, metric.value, metric.suffix))
		}
	}
	return observations
}

func telemetrySeverity(status string) string {
	switch status {
	case "breached":
		return "critical"
	case "degraded":
		return "warning"
	default:
		return "healthy"
	}
}

func telemetryScore(status string) float64 {
	switch status {
	case "breached":
		return 80
	case "degraded":
		return 50
	default:
		return 5
	}
}

func telemetryRootCauseDetail(telemetry *RunArchiveTelemetrySummary) string {
	if telemetry == nil {
		return ""
	}
	parts := []string{}
	if telemetry.DroppedResults > 0 {
		parts = append(parts, fmt.Sprintf("%d dropped result(s)", telemetry.DroppedResults))
	}
	if telemetry.WebsocketDroppedMessages > 0 {
		parts = append(parts, fmt.Sprintf("%d dropped websocket message(s)", telemetry.WebsocketDroppedMessages))
	}
	if telemetry.MaxSampleAgeMs > 0 {
		parts = append(parts, fmt.Sprintf("max sample age %.0f ms", telemetry.MaxSampleAgeMs))
	}
	if len(telemetry.ConditionCounts) > 0 {
		keys := make([]string, 0, len(telemetry.ConditionCounts))
		for key := range telemetry.ConditionCounts {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		parts = append(parts, "conditions: "+strings.Join(keys, ", "))
	}
	if len(parts) == 0 {
		return "Telemetry health status was " + telemetry.Status + " for one or more intervals."
	}
	return strings.Join(parts, "; ") + "."
}

// rootCauseConfidence is the lower of two bands: how complete the archive is,
// and how strong the evidence behind the lead finding is. A complete archive
// does not make an advisory lead finding, or a thinly evidenced one, certain.
func rootCauseConfidence(report *RunArchiveReport, findings []RootCauseFinding) string {
	if report == nil {
		return "low"
	}
	completeness := "medium"
	if report.Completeness.ScorePct >= 90 && report.Overview.IntervalSummaries > 0 && report.Overview.Transactions > 0 {
		completeness = "high"
	} else if report.Completeness.ScorePct < 60 {
		completeness = "low"
	}
	evidence := "high"
	if len(findings) == 0 {
		// Absence of findings is only reassuring when the evidence is complete.
		if report.Completeness.ScorePct < 90 {
			evidence = "low"
		}
	} else if lead := findings[0]; !rootCauseMeasuredCategory(lead.Category) || len(lead.Evidence) < 2 {
		evidence = "medium"
	}
	if rootCauseConfidenceRank(evidence) < rootCauseConfidenceRank(completeness) {
		return evidence
	}
	return completeness
}

// Measured categories rest on archived measurements; the rest are advisory
// heuristics or statements about missing evidence.
func rootCauseMeasuredCategory(category string) bool {
	switch category {
	case "sla", "errors", "resource", "load_model", "telemetry", "browser", "oracle_db":
		return true
	default:
		return false
	}
}

func rootCauseConfidenceRank(confidence string) int {
	switch confidence {
	case "high":
		return 2
	case "medium":
		return 1
	default:
		return 0
	}
}

func rootCauseNarrative(status string, findings []RootCauseFinding, slaState string, omitted int) string {
	if len(findings) == 0 {
		switch slaState {
		case archiveSLAPassed:
			return "No root-cause findings were detected from the archived run evidence, and the archived SLA checks passed."
		case archiveSLANotConfigured:
			return "No root-cause findings were detected from the archived run evidence. No SLA criteria were configured, so this is not an acceptance verdict."
		default:
			return "No root-cause findings were detected from the archived run evidence. The archive does not record SLA criteria, so this is not an acceptance verdict."
		}
	}
	lead := findings[0]
	var narrative string
	switch status {
	case "breached":
		narrative = "The strongest release-blocking signal is " + strings.ToLower(lead.Title) + "."
	case "degraded":
		narrative = "The run needs review; the strongest signal is " + strings.ToLower(lead.Title) + "."
	default:
		narrative = "No release-blocking or review findings were detected from the archived run evidence."
	}
	if omitted > 0 {
		narrative += fmt.Sprintf(" %d further finding(s) are in the engineer report.", omitted)
	}
	return narrative
}

func rootCauseNextActions(categories map[string]struct{}) []string {
	actions := []string{}
	if _, ok := categories["sla"]; ok {
		actions = append(actions, "Review SLA thresholds and waiver status before promoting the release.")
	}
	if _, ok := categories["errors"]; ok {
		actions = append(actions, "Open the transaction archive and group failures by action, dependency target, and trace ID.")
	}
	if _, ok := categories["telemetry"]; ok {
		actions = append(actions, "Inspect collector lag, dropped results, and websocket fan-out before trusting live dashboards.")
	}
	if _, ok := categories["resource"]; ok {
		actions = append(actions, "Compare generator CPU and memory peaks against the fleet capacity envelope for this protocol mix.")
	}
	if _, ok := categories["oracle_db"]; ok {
		actions = append(actions, "Review Oracle wait, lock, session, and IO metrics beside transaction P95 latency before assigning the bottleneck.")
	}
	if _, ok := categories["load_model"]; ok {
		actions = append(actions, "Compare achieved throughput against configured staged-ramp and arrival-rate goals.")
	}
	if _, ok := categories["browser"]; ok {
		actions = append(actions, "Review browser diagnostics by device profile and isolate client-side regressions from backend latency.")
	}
	return actions
}

// validityRuleText describes each validity rule for the summary.
var validityRuleText = map[string]string{
	rca.RuleGeneratorCPU:          "a load generator held CPU at or above %.0f%%",
	rca.RuleGeneratorSteal:        "a load generator lost at least %.0f%% of CPU to steal",
	rca.RuleGeneratorThrottled:    "a load generator was throttled in at least %.0f%% of CPU periods",
	rca.RuleGeneratorOOMKill:      "a load generator container was OOM-killed",
	rca.RuleDroppedResults:        "more than %.1f%% of an interval's results were dropped",
	rca.RuleShedArrivals:          "the open model shed arrivals",
	rca.RuleDataExhausted:         "test data ran out",
	rca.RuleGeneratorStatsMissing: "generator statistics are missing for more than %.0f%% of judged intervals",
	rca.RuleEvidenceIncomplete:    "run evidence is incomplete",
	rca.RuleNoIntervals:           "no intervals were archived",
}

func validityRootCauseDetail(validity *rca.Validity) string {
	if validity.Verdict == rca.ValidityValid {
		return fmt.Sprintf("Generators and telemetry carried the load across %d judged interval(s).", validity.IntervalsJudged)
	}
	parts := make([]string, 0, len(validity.Reasons))
	for _, reason := range validity.Reasons {
		text := validityRuleText[reason]
		switch reason {
		case rca.RuleGeneratorCPU:
			text = fmt.Sprintf(text, validity.Rules.CPUPct)
		case rca.RuleGeneratorSteal:
			text = fmt.Sprintf(text, validity.Rules.StealPct)
		case rca.RuleGeneratorThrottled:
			text = fmt.Sprintf(text, validity.Rules.ThrottledFraction*100)
		case rca.RuleDroppedResults:
			text = fmt.Sprintf(text, validity.Rules.DroppedFraction*100)
		case rca.RuleGeneratorStatsMissing:
			text = fmt.Sprintf(text, validity.Rules.MissingFraction*100)
		}
		if text == "" {
			text = reason
		}
		parts = append(parts, text)
	}
	if validity.Rules.SustainIntervals > 1 && (validity.Verdict == rca.ValiditySuspect) {
		return fmt.Sprintf("%s for %d or more consecutive intervals at a steady load (%d interval(s) flagged).", capitalizeFirst(strings.Join(parts, "; ")), validity.Rules.SustainIntervals, len(validity.Windows))
	}
	return capitalizeFirst(strings.Join(parts, "; ")) + "."
}

func capitalizeFirst(value string) string {
	if value == "" {
		return value
	}
	return strings.ToUpper(value[:1]) + value[1:]
}
