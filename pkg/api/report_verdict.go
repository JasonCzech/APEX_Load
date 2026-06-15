package api

import (
	"fmt"
	"sort"
	"strings"
)

// Report verdict labels rendered in the report's headline verdict bar.
const (
	reportVerdictPass        = "Pass"
	reportVerdictConditional = "Conditional Pass"
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

// reportVerdict carries the headline pass / conditional / fail verdict and a
// short explanation rendered in the report's verdict bar. Conditional and
// Failed drive the badge styling (warn / danger); neither set means a clean
// pass (good styling).
type reportVerdict struct {
	Status      string
	Conditional bool
	Failed      bool
	Explanation string
}

// deriveReportVerdict computes the headline verdict from the archived run
// signals. A confirmed SLA breach is a Fail. Absent a breach, soft concerns
// (degraded telemetry, incomplete data, throughput misses) yield a Conditional
// Pass; an otherwise clean run is a Pass. Analysts can override the result for
// nuance (e.g. a transient, self-recovered breach) via report annotations.
func deriveReportVerdict(report *RunArchiveReport) reportVerdict {
	if report == nil {
		return reportVerdict{Status: reportVerdictConditional, Conditional: true,
			Explanation: "No archived run evidence was available to evaluate this test."}
	}

	slaBreached := false
	var slaDetails []string
	if report.Manifest != nil {
		slaBreached = report.Manifest.SLABreached
		slaDetails = report.Manifest.SLADetails
	}

	if slaBreached {
		explanation := "One or more SLA targets were breached during the run."
		if len(slaDetails) > 0 {
			explanation = "SLA breached: " + strings.Join(slaDetails, "; ") + "."
		}
		return reportVerdict{Status: reportVerdictFail, Failed: true, Explanation: explanation}
	}

	concerns := reportConditionalConcerns(report)
	if len(concerns) > 0 {
		return reportVerdict{
			Status:      reportVerdictConditional,
			Conditional: true,
			Explanation: "All configured SLA targets were met, but the run warrants review: " + strings.Join(concerns, "; ") + ".",
		}
	}

	return reportVerdict{
		Status:      reportVerdictPass,
		Explanation: "All configured SLA targets were met and the run completed cleanly with healthy telemetry and complete data.",
	}
}

// reportConditionalConcerns lists the soft signals that downgrade an
// otherwise-passing run to a Conditional Pass.
func reportConditionalConcerns(report *RunArchiveReport) []string {
	concerns := make([]string, 0, 3)
	if t := report.Overview.Telemetry; t != nil && (t.DegradedIntervals > 0 || t.BreachedIntervals > 0) {
		concerns = append(concerns, fmt.Sprintf("telemetry was degraded for %d interval(s)", t.DegradedIntervals+t.BreachedIntervals))
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
		if strings.TrimSpace(findings[i].ID) == "" {
			findings[i].ID = fmt.Sprintf("F-%02d", i+1)
		}
	}
	return findings
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

// slaComplianceRow is one row of the per-transaction SLA compliance table
// (report Section 3). SLAMs is the configured global p95 SLA; HasSLA is false
// when no latency SLA was configured for the run.
type slaComplianceRow struct {
	Name       string
	Volume     int64
	P95Ms      float64
	SLAMs      float64
	HasSLA     bool
	ErrorPct   float64
	Result     string
	ResultKind string // "pass" | "warn" | "fail" | "na"
}

// buildSLAComplianceRows builds the per-transaction SLA table from the archived
// per-action stats, evaluated against the run's global goal config (p95 latency
// and max error rate). Rows are ordered by volume descending.
func buildSLAComplianceRows(report *RunArchiveReport) []slaComplianceRow {
	if report == nil {
		return nil
	}
	slaMs, hasSLA, maxErrPct, hasErrLimit := reportGoalThresholds(report)

	rows := make([]slaComplianceRow, 0, len(report.ByAction))
	for name, stats := range report.ByAction {
		row := slaComplianceRow{
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
	if gc.MaxErrorPct != nil && *gc.MaxErrorPct > 0 {
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
	errorFail := hasErrLimit && stats.ErrorPct > maxErrPct
	if !hasSLA {
		if errorFail {
			return "Fail — error rate", "fail"
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
