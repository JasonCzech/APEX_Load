package api

import (
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"
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
	record, ok := s.requireRunArchive(w, r)
	if !ok {
		return
	}
	report, err := buildRunArchiveReport(record)
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

	if report.Manifest.SLABreached {
		addSignal("SLA", "breached", strings.Join(report.Manifest.SLADetails, "; "), "critical", 95)
		addFinding("sla", "critical", "Release SLA breached", firstNonEmpty(strings.Join(report.Manifest.SLADetails, "; "), "The run archive marked one or more SLA checks as breached."), []string{"manifest.sla_breached", "manifest.sla_details"}, 95)
	} else {
		addSignal("SLA", "passed", "No archived SLA breach", "healthy", 5)
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
	if report.ResourcePeaks.CPUProcessPct >= 80 {
		severity := "warning"
		if report.ResourcePeaks.CPUProcessPct >= 90 {
			severity = "critical"
		}
		addSignal("Generator CPU", "saturated", fmt.Sprintf("%.1f%% process CPU", report.ResourcePeaks.CPUProcessPct), severity, report.ResourcePeaks.CPUProcessPct)
		addFinding("resource", severity, "Generator resource saturation detected", fmt.Sprintf("Peak generator process CPU reached %.1f%%.", report.ResourcePeaks.CPUProcessPct), []string{"resource_peaks.cpu_process_pct"}, report.ResourcePeaks.CPUProcessPct)
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
	if browser := report.Overview.Browser; browser != nil {
		if browser.AvgLCPMs > 2500 || browser.AvgINPMs > 200 || browser.AvgCLSScore > 0.1 {
			addSignal("Browser UX", "degraded", fmt.Sprintf("LCP %.0f ms, INP %.0f ms, CLS %.3f", browser.AvgLCPMs, browser.AvgINPMs, browser.AvgCLSScore), "warning", 58)
			addFinding("browser", "warning", "Browser canary experience degraded", "Archived browser diagnostics exceeded common Core Web Vitals guidance for one or more client-side metrics.", []string{"overview.browser", "browser_by_device"}, 58)
		} else if browser.Samples > 0 {
			addSignal("Browser UX", "healthy", fmt.Sprintf("%d samples", browser.Samples), "healthy", 5)
		}
	}
	if report.Completeness.Status != "" && report.Completeness.Status != "complete" {
		addFinding("reporting", "warning", "Archive evidence is incomplete", fmt.Sprintf("Report completeness is %s at %.0f%%.", report.Completeness.Status, report.Completeness.ScorePct), []string{"completeness"}, 35)
	}

	sort.SliceStable(summary.TopFindings, func(left, right int) bool {
		return summary.TopFindings[left].Score > summary.TopFindings[right].Score
	})
	if len(summary.TopFindings) > 6 {
		summary.TopFindings = summary.TopFindings[:6]
	}
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
	default:
		summary.Status = "healthy"
	}
	summary.Confidence = rootCauseConfidence(report)
	summary.Summary = rootCauseNarrative(summary.Status, summary.TopFindings)
	summary.NextActions = rootCauseNextActions(categories)
	if len(summary.NextActions) == 0 {
		summary.NextActions = []string{"Use the engineer report profile to review timeline, action, and resource evidence before closing the release gate."}
	}
	return summary
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

func rootCauseConfidence(report *RunArchiveReport) string {
	if report == nil {
		return "low"
	}
	if report.Completeness.ScorePct >= 90 && report.Overview.IntervalSummaries > 0 && report.Overview.Transactions > 0 {
		return "high"
	}
	if report.Completeness.ScorePct < 60 {
		return "low"
	}
	return "medium"
}

func rootCauseNarrative(status string, findings []RootCauseFinding) string {
	if len(findings) == 0 {
		return "No root-cause findings were detected from the archived run evidence."
	}
	lead := findings[0]
	switch status {
	case "breached":
		return "The strongest release-blocking signal is " + strings.ToLower(lead.Title) + "."
	case "degraded":
		return "The run needs review; the strongest signal is " + strings.ToLower(lead.Title) + "."
	default:
		return "The run appears healthy across archived SLA, telemetry, resource, and browser evidence."
	}
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
