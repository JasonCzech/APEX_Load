package api

import (
	"fmt"
	"sort"
	"strings"
)

// Report verdict labels rendered in the report's headline verdict bar.
const (
	reportVerdictPass        = "Pass"
	reportVerdictConditional = "Inconclusive"
	reportVerdictNotAssessed = "Not assessed"
	reportVerdictFail        = "Fail"
)

// reportMaxAutoFindings caps the number of auto-derived findings so the report
// stays readable; the analyst can curate the full set via annotations.
const reportMaxAutoFindings = 6

// ReportFinding is a structured observation surfaced in the report's Findings
// section. Findings are auto-derived from run signals (SLA breaches, error
// patterns, completeness findings, throughput gaps) but can be overridden by an
// analyst via report annotations (ReportAnnotations.Findings).
type ReportFinding struct {
	ID        string `json:"id"`       // e.g. "F-01"
	Title     string `json:"title"`    // short headline
	Severity  string `json:"severity"` // "major" | "minor" | "info"
	Observed  string `json:"observed,omitempty"`
	Evidence  string `json:"evidence,omitempty"`
	RootCause string `json:"root_cause,omitempty"`
	Impact    string `json:"impact,omitempty"`
}

// ReportVerdict carries the headline pass / conditional / fail verdict and a
// short explanation rendered in the report's verdict bar. Conditional and
// Failed drive the badge styling (warn / danger); neither set means a clean
// pass (good styling).
type ReportVerdict struct {
	Status      string `json:"status"`
	Conditional bool   `json:"conditional"`
	Failed      bool   `json:"failed"`
	Explanation string `json:"explanation"`
}

// deriveReportVerdict separates acceptance from execution and evidence coverage.
// Analyst sign-off is retained separately and never changes the computed result.
func deriveReportVerdict(report *RunArchiveReport) ReportVerdict {
	if report == nil {
		return ReportVerdict{Status: reportVerdictConditional, Conditional: true,
			Explanation: "No archived run evidence was available to evaluate this test."}
	}

	rollup := buildSLARollup(buildSLAComplianceRowMap(report))
	slaBreached := rollup.Failed > 0
	var slaDetails []string
	var panelConfigured, panelEvaluated, panelUnevaluated bool
	if report.Manifest != nil {
		slaBreached = slaBreached || report.Manifest.SLABreached
		slaDetails = report.Manifest.SLADetails
	}
	if report.DashboardPanels != nil {
		for _, dashboard := range report.DashboardPanels.Dashboards {
			for _, panel := range dashboard.Panels {
				panelConfigured = panelConfigured || panel.ThresholdConfigured || panel.ThresholdEvaluated
				panelEvaluated = panelEvaluated || panel.ThresholdEvaluated
				panelUnevaluated = panelUnevaluated || (panel.ThresholdConfigured && !panel.ThresholdEvaluated)
				if !panel.ThresholdBreached {
					continue
				}
				slaBreached = true
				prefix := strings.Trim(strings.TrimSpace(dashboard.Title)+" / "+strings.TrimSpace(panel.Title), " / ")
				detail := strings.TrimSpace(panel.ThresholdDetail)
				if detail == "" {
					detail = prefix + " breached its imported threshold"
				} else if prefix != "" {
					detail = prefix + ": " + detail
				}
				slaDetails = compactNonEmptyStrings(append(slaDetails, detail)...)
			}
		}
	}

	if slaBreached {
		explanation := "One or more SLA targets were breached during the run."
		if len(slaDetails) > 0 {
			explanation = "SLA breached: " + strings.Join(slaDetails, "; ") + "."
		}
		return ReportVerdict{Status: reportVerdictFail, Failed: true, Explanation: explanation}
	}
	if report.Manifest != nil && monitoringAcceptanceIncomplete(report.Manifest) {
		return ReportVerdict{Status: reportVerdictConditional, Conditional: true, Explanation: "Required monitoring evidence or configured monitoring criteria were incomplete."}
	}

	_, latencyConfigured, _, errorConfigured := reportGoalThresholds(report)
	configured := latencyConfigured || errorConfigured || panelConfigured
	evaluated := rollup.Evaluated > 0 || panelEvaluated
	if report.Manifest != nil {
		configured = configured || len(monitorRulesFromManifest(report.Manifest)) > 0 || hasReportAcceptanceCriteria(report.Manifest.Provenance.GoalConfig) || len(report.Manifest.TransactionRuleResults) > 0 || (report.Manifest.Provenance.GoalConfig == nil && report.Manifest.SLAEvaluated)
		evaluated = evaluated || report.Manifest.SLAEvaluated
	}
	if !configured {
		return ReportVerdict{Status: reportVerdictNotAssessed, Conditional: true, Explanation: "No acceptance criteria were configured. Performance acceptance has not been assessed."}
	}
	if !evaluated {
		return ReportVerdict{Status: reportVerdictConditional, Conditional: true, Explanation: "Acceptance criteria were configured but not evaluated."}
	}
	concerns := reportConditionalConcerns(report)
	if panelUnevaluated {
		concerns = append(concerns, "a configured dashboard threshold was not evaluated")
	}
	if (latencyConfigured || errorConfigured) && rollup.NA > 0 {
		concerns = append(concerns, "a configured transaction criterion has no samples")
	}
	if report.Manifest != nil {
		if goals := report.Manifest.Provenance.GoalConfig; goals != nil {
			if len(goals.TransactionRules) > len(report.Manifest.TransactionRuleResults) {
				concerns = append(concerns, "configured transaction rules have missing evaluations")
			}
			remaining := *goals
			remaining.P95LatencyMs, remaining.MaxErrorPct, remaining.TransactionRules = nil, nil, nil
			if hasReportAcceptanceCriteria(&remaining) && !report.Manifest.SLAEvaluated {
				concerns = append(concerns, "additional configured acceptance criteria were not evaluated")
			}
		}
		for _, rule := range report.Manifest.TransactionRuleResults {
			if rule.Status == "not_evaluated" {
				concerns = append(concerns, "a configured transaction criterion was not evaluated")
				break
			}
		}
		for _, result := range report.Manifest.TransactionResponseTimeSLAResults {
			if result.Status == "not_evaluated" {
				concerns = append(concerns, "a configured transaction response-time SLA was not evaluated")
				break
			}
		}
	}
	if report.Manifest == nil || report.Manifest.TerminationReason != "workload_completed" {
		concerns = append(concerns, "intended workload completion is not confirmed")
	}
	if report.Manifest != nil && report.Manifest.Provenance.TotalVUsers > 0 && report.Overview.PeakActiveVUsers < int64(report.Manifest.Provenance.TotalVUsers) {
		concerns = append(concerns, "the intended VUser load was not observed")
	}
	if report.Completeness.Status == "" {
		concerns = append(concerns, "evidence coverage is unknown")
	}
	if len(concerns) > 0 {
		return ReportVerdict{
			Status:      reportVerdictConditional,
			Conditional: true,
			Explanation: "Acceptance requires review: " + strings.Join(concerns, "; ") + ".",
		}
	}

	return ReportVerdict{
		Status:      reportVerdictPass,
		Explanation: "All configured SLA targets were met and the run completed cleanly with healthy telemetry and complete data.",
	}
}

// Load bounds and stop switches control execution; they are not acceptance targets.
func hasReportAcceptanceCriteria(config *GoalConfig) bool {
	if config == nil {
		return false
	}
	goals := *config
	goals.MinVUsers, goals.MaxVUsers = nil, nil
	goals.StopOnSLABreach, goals.StopOnMaxErrors = false, false
	return hasGoalConfig(&goals)
}

// reportConditionalConcerns lists the soft signals that downgrade an
// otherwise-passing run to Inconclusive.
func reportConditionalConcerns(report *RunArchiveReport) []string {
	concerns := make([]string, 0, 3)
	if t := report.Overview.Telemetry; t != nil && (t.DegradedIntervals > 0 || t.BreachedIntervals > 0) {
		concerns = append(concerns, fmt.Sprintf("telemetry was degraded for %d interval(s)", t.DegradedIntervals+t.BreachedIntervals))
	}
	// Unscored telemetry is never healthy: whether it met its budget is
	// unknown, so it cannot leave a run passing outright.
	if t := report.Overview.Telemetry; t != nil && t.UnscoredIntervals > 0 {
		concerns = append(concerns, fmt.Sprintf("telemetry could not be scored for %d interval(s)", t.UnscoredIntervals))
	}
	if report.Overview.ThroughputMissedIntervals > 0 {
		concerns = append(concerns, fmt.Sprintf("throughput missed its target in %d interval(s)", report.Overview.ThroughputMissedIntervals))
	}
	if status := strings.ToLower(strings.TrimSpace(report.Completeness.Status)); status != "" && status != "complete" && status != "ok" {
		concerns = append(concerns, fmt.Sprintf("data completeness was %.0f%% (%s)", report.Completeness.ScorePct, report.Completeness.Status))
	}
	return concerns
}

// deriveReportFindings synthesizes a first-pass F-01… findings list from the
// available run signals: SLA detail messages, the most frequent error
// patterns, completeness findings, and throughput shortfalls. The result is
// capped at reportMaxAutoFindings and IDs are assigned sequentially.
func deriveReportFindings(report *RunArchiveReport) []ReportFinding {
	if report == nil {
		return nil
	}
	findings := make([]ReportFinding, 0, reportMaxAutoFindings)

	if report.Manifest != nil && report.Manifest.SLABreached {
		observed := "One or more SLA targets were breached during the run."
		if len(report.Manifest.SLADetails) > 0 {
			observed = strings.Join(report.Manifest.SLADetails, "; ") + "."
		}
		findings = append(findings, ReportFinding{
			Title:    "SLA target breached",
			Severity: "major",
			Observed: observed,
			Impact:   "Service-level objectives were not met; remediation is required before certification.",
		})
	}

	for i, pattern := range report.ErrorPatterns {
		if i >= 2 {
			break
		}
		severity := "minor"
		if report.Overview.Transactions > 0 && float64(pattern.Count)/float64(report.Overview.Transactions) >= 0.01 {
			severity = "major"
		}
		evidence := fmt.Sprintf("Signature %q", pattern.Signature)
		if len(pattern.Actions) > 0 {
			evidence += "; affected transactions: " + strings.Join(pattern.Actions, ", ")
		}
		findings = append(findings, ReportFinding{
			Title:    "Recurring error pattern",
			Severity: severity,
			Observed: fmt.Sprintf("%q occurred %d time(s) (first seen %s, latest %s).",
				pattern.Sample, pattern.Count, formatChartClock(pattern.FirstSeen), formatChartClock(pattern.LatestSeen)),
			Evidence: evidence,
		})
	}

	for _, finding := range report.Completeness.Findings {
		findings = append(findings, ReportFinding{
			Title:    "Data completeness: " + finding.Dimension,
			Severity: completenessSeverityToFinding(finding.Severity),
			Observed: finding.Message,
		})
	}

	if report.Overview.ThroughputMissedIntervals > 0 {
		findings = append(findings, ReportFinding{
			Title:    "Throughput below target",
			Severity: "minor",
			Observed: fmt.Sprintf("Throughput missed its target in %d interval(s); peak gap %.1f%% against a %.1f TPS target.",
				report.Overview.ThroughputMissedIntervals, report.Overview.MaxThroughputGapPct, report.Overview.PeakTargetTPS),
		})
	}

	if len(findings) > reportMaxAutoFindings {
		findings = findings[:reportMaxAutoFindings]
	}
	for i := range findings {
		findings[i].ID = fmt.Sprintf("F-%02d", i+1)
	}
	return findings
}

// normalizeReportFindings trims finding text and backfills any missing IDs with
// sequential F-0n identifiers, so analyst-curated findings submitted without IDs
// still render correctly. Existing non-empty IDs are preserved.
func normalizeReportFindings(findings []ReportFinding) []ReportFinding {
	for i := range findings {
		findings[i].Title = strings.TrimSpace(findings[i].Title)
		findings[i].Severity = canonicalReportFindingSeverity(findings[i].Severity)
		if strings.TrimSpace(findings[i].ID) == "" {
			findings[i].ID = fmt.Sprintf("F-%02d", i+1)
		}
	}
	return findings
}

// canonicalReportFindingSeverity clamps a finding severity to the report's
// vocabulary (major/minor/info). An out-of-vocabulary value is surfaced as major
// rather than silently downgraded to a benign info pill on a sign-off document.
func canonicalReportFindingSeverity(severity string) string {
	switch strings.ToLower(strings.TrimSpace(severity)) {
	case "minor", "warning", "warn", "medium", "low":
		return "minor"
	case "info", "informational", "note":
		return "info"
	case "":
		return "info"
	default:
		return "major"
	}
}

// completenessSeverityToFinding maps a completeness-finding severity onto the
// report's finding severity vocabulary.
func completenessSeverityToFinding(severity string) string {
	switch strings.ToLower(strings.TrimSpace(severity)) {
	case "critical", "error", "high":
		return "major"
	case "warning", "warn", "medium":
		return "minor"
	default:
		return "info"
	}
}

// SLAComplianceRow is one row of the per-transaction SLA compliance table
// (report Section 3). SLAMs is the configured global p95 SLA; HasSLA is false
// when no latency SLA was configured for the run.
type SLAComplianceRow struct {
	Name       string  `json:"name"`
	Volume     int64   `json:"volume"`
	P95Ms      float64 `json:"p95_ms"`
	SLAMs      float64 `json:"sla_ms,omitempty"`
	HasSLA     bool    `json:"has_sla"`
	ErrorPct   float64 `json:"error_pct"`
	Result     string  `json:"result"`
	ResultKind string  `json:"result_kind"` // "pass" | "warn" | "fail" | "na"
}

// RunArchiveSLARollup summarizes the per-action SLA rows for dashboard
// scoreboards. Evaluated excludes "na" rows, which have no configured latency
// SLA or failing error threshold to evaluate.
type RunArchiveSLARollup struct {
	Evaluated int64  `json:"evaluated"`
	Passed    int64  `json:"passed"`
	Warned    int64  `json:"warned"`
	Failed    int64  `json:"failed"`
	NA        int64  `json:"na"`
	WorstKind string `json:"worst_kind"`
}

// buildSLAComplianceRows builds the per-transaction SLA table from the archived
// per-action stats, evaluated against the run's global goal config (p95 latency
// and max error rate). Rows are ordered by volume descending.
func buildSLAComplianceRows(report *RunArchiveReport) []SLAComplianceRow {
	if report == nil {
		return nil
	}
	slaMs, hasSLA, maxErrPct, hasErrLimit := reportGoalThresholds(report)

	statsByName := report.ByAction
	if report.MetricPopulations != nil {
		statsByName = report.ByTransaction
	}
	rows := make([]SLAComplianceRow, 0, len(statsByName))
	for name, stats := range statsByName {
		row := SLAComplianceRow{
			Name:     name,
			Volume:   stats.Transactions,
			P95Ms:    stats.P95Ms,
			ErrorPct: stats.ErrorPct,
			SLAMs:    slaMs,
			HasSLA:   hasSLA,
		}
		row.Result, row.ResultKind = evaluateSLARow(stats, hasSLA, slaMs, hasErrLimit, maxErrPct)
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Volume == rows[j].Volume {
			return rows[i].Name < rows[j].Name
		}
		return rows[i].Volume > rows[j].Volume
	})
	return rows
}

func buildSLAComplianceRowMap(report *RunArchiveReport) map[string]SLAComplianceRow {
	rows := buildSLAComplianceRows(report)
	if len(rows) == 0 {
		return nil
	}
	byAction := make(map[string]SLAComplianceRow, len(rows))
	for _, row := range rows {
		byAction[row.Name] = row
	}
	return byAction
}

func buildSLARollup(rows map[string]SLAComplianceRow) RunArchiveSLARollup {
	rollup := RunArchiveSLARollup{WorstKind: "na"}
	if len(rows) == 0 {
		return rollup
	}
	worstRank := 0
	for _, row := range rows {
		switch row.ResultKind {
		case "fail":
			rollup.Failed++
			rollup.Evaluated++
			if worstRank < 3 {
				worstRank = 3
				rollup.WorstKind = "fail"
			}
		case "warn":
			rollup.Warned++
			rollup.Evaluated++
			if worstRank < 2 {
				worstRank = 2
				rollup.WorstKind = "warn"
			}
		case "pass":
			rollup.Passed++
			rollup.Evaluated++
			if worstRank < 1 {
				worstRank = 1
				rollup.WorstKind = "pass"
			}
		default:
			rollup.NA++
		}
	}
	return rollup
}

func attachReportVerdictAndSLA(report *RunArchiveReport) {
	if report == nil {
		return
	}
	verdict := deriveReportVerdict(report)
	report.Verdict = &verdict
	report.SLARows = buildSLAComplianceRowMap(report)
	if report.Manifest != nil {
		report.TransactionResponseTimeSLAResults = append([]TransactionResponseTimeSLAResult(nil), report.Manifest.TransactionResponseTimeSLAResults...)
	}
	rollup := buildSLARollup(report.SLARows)
	report.SLARollup = &rollup
}

// reportGoalThresholds extracts the global p95 latency SLA and max error-rate
// limit from the run's persisted goal config (via archive provenance).
func reportGoalThresholds(report *RunArchiveReport) (slaMs float64, hasSLA bool, maxErrPct float64, hasErrLimit bool) {
	if report == nil || report.Manifest == nil {
		return 0, false, 0, false
	}
	gc := report.Manifest.Provenance.GoalConfig
	if gc == nil {
		return 0, false, 0, false
	}
	if gc.P95LatencyMs != nil && *gc.P95LatencyMs > 0 {
		slaMs = *gc.P95LatencyMs
		hasSLA = true
	}
	if gc.MaxErrorPct != nil {
		maxErrPct = *gc.MaxErrorPct
		hasErrLimit = true
	}
	return slaMs, hasSLA, maxErrPct, hasErrLimit
}

// evaluateSLARow returns the per-transaction result pill text and its kind. A
// transaction fails on a p95 over the SLA or an error rate over the limit;
// within 10% of the SLA p95 is flagged as a warning; otherwise it passes. With
// no latency SLA configured the result is "—" unless the error limit is broken.
func evaluateSLARow(stats RunArchiveActionStats, hasSLA bool, slaMs float64, hasErrLimit bool, maxErrPct float64) (string, string) {
	if stats.Transactions == 0 {
		return "Not evaluated", "na"
	}
	errorFail := hasErrLimit && stats.ErrorPct > maxErrPct
	if !hasSLA {
		if errorFail {
			return "Fail — error rate", "fail"
		}
		if hasErrLimit {
			return "Pass", "pass"
		}
		return "—", "na"
	}
	if stats.P95Ms > slaMs || errorFail {
		return "Fail", "fail"
	}
	pct := stats.P95Ms / slaMs * 100
	if pct >= 90 {
		return fmt.Sprintf("Pass — %.0f%% of SLA", pct), "warn"
	}
	return "Pass", "pass"
}

func reportExecutionLabel(reason string) string {
	switch reason {
	case "workload_completed":
		return "Finished"
	case "operator_stopped":
		return "Stopped by operator"
	case "stopped_early":
		return "Stopped early"
	case "aborted":
		return "Aborted"
	case "failed":
		return "Failed"
	default:
		return "Unknown termination reason"
	}
}
func reportElapsedDuration(manifest *RunArchiveManifest) string {
	if manifest == nil || manifest.Provenance.StartedAt == nil || manifest.CompletedAt.IsZero() {
		return "Unknown"
	}
	elapsed := manifest.CompletedAt.Sub(*manifest.Provenance.StartedAt)
	if elapsed < 0 {
		return "Unknown"
	}
	return fmt.Sprintf("%.0f seconds", elapsed.Seconds())
}
