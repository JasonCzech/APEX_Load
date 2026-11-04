package api

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/rca"
)

// agentFindingInputs is what the finding adapters read.
type agentFindingInputs struct {
	Report       *RunArchiveReport
	Comparison   *BaselineComparisonSnapshot
	ValidityGate string
}

// collectAgentFindings runs one adapter per primary evidence source. The
// report's own ReportFinding list is not a source: it presents these same
// signals and would count them twice.
func collectAgentFindings(in agentFindingInputs) []AgentFinding {
	report := in.Report
	if report == nil {
		return []AgentFinding{}
	}
	runID := ""
	if report.Manifest != nil {
		runID = report.Manifest.TestID
	}
	var findings []AgentFinding
	findings = append(findings, agentSLARuleFindings(runID, report)...)
	findings = append(findings, agentSLARowFindings(runID, report)...)
	findings = append(findings, agentResponseTimeSLAFindings(runID, report)...)
	findings = append(findings, agentRegressionFindings(runID, in.Comparison)...)
	findings = append(findings, agentErrorPatternFindings(runID, report)...)
	findings = append(findings, agentCompletenessFindings(runID, report)...)
	findings = append(findings, agentValidityFindings(runID, report, in.ValidityGate)...)
	findings = append(findings, agentThroughputFindings(runID, report)...)
	findings = append(findings, agentEnvironmentFindings(runID, report)...)
	findings = append(findings, agentAnomalyFindings(runID, report)...)
	findings = append(findings, agentRCAFindings(runID, report)...)
	findings = append(findings, agentAutoStopFindings(runID, report)...)
	return mergeAgentFindings(findings)
}

// agentSLAMetric maps an SLA rule metric to the canonical name SLA rows use,
// so a rule and a row about the same transaction and metric share an id.
func agentSLAMetric(metric string) (name, unit string) {
	switch metric {
	case "p90_duration_ms":
		return "p90_ms", "ms"
	case "p95_duration_ms":
		return "p95_ms", "ms"
	case "p99_duration_ms":
		return "p99_ms", "ms"
	case "average_duration_ms":
		return "avg_ms", "ms"
	case "min_duration_ms":
		return "min_ms", "ms"
	case "max_duration_ms":
		return "max_ms", "ms"
	case "error_rate_pct":
		return "error_pct", "pct"
	case "tps":
		return "throughput_per_s", "per_s"
	case "bytes_sent", "bytes_received":
		return metric, "bytes"
	case "minimum_count", "error_count", "max_consumer_offset_lag":
		return metric, "count"
	}
	if strings.HasSuffix(metric, "_ms") {
		return metric, "ms"
	}
	return metric, ""
}

func agentFloat(v float64) *float64 { return &v }
func agentInt(v int64) *int64       { return &v }

func agentTestPath(runID, suffix string) string {
	return "/tests/" + url.PathEscape(runID) + suffix
}

func agentDrillDown(runID, findingID, why string) AgentNextStep {
	return AgentNextStep{Tool: "apex_get_finding", Args: map[string]string{"run_id": runID, "finding_id": findingID}, Why: why}
}

func agentSLAConfidence(quality string, samples int64) string {
	switch {
	case samples > 0 && samples < 100:
		return agentConfidenceLow
	case quality == "approximate":
		return agentConfidenceMedium
	}
	return agentConfidenceHigh
}

func agentSLARuleFindings(runID string, report *RunArchiveReport) []AgentFinding {
	if report.Manifest == nil {
		return nil
	}
	var findings []AgentFinding
	for _, result := range report.Manifest.TransactionRuleResults {
		if result.Status != "fail" && result.Status != "warning" {
			continue
		}
		rule := result.Rule
		metric, unit := agentSLAMetric(rule.Metric)
		severity := agentSeverityBlocker
		if result.Status == "warning" {
			severity = agentSeverityMinor
		}
		id := agentRuleFindingID(rule, metric)
		observed := "unmeasured"
		if result.Observed != nil {
			observed = agentFormatNumber(*result.Observed, unit)
		}
		title := fmt.Sprintf("%s of %s is %s, rule requires %s %s", metric, agentSafeName(rule.Transaction), observed, agentComparator(rule.Comparator), agentFormatNumber(rule.Threshold, unit))
		finding := AgentFinding{
			ID: id, Kind: agentKindSLABreach, Severity: severity,
			Confidence: agentSLAConfidence(result.PercentileQuality, result.MatchedCount),
			Title:      title,
			Scope:      AgentScope{Transaction: rule.Transaction, Group: rule.Group, Population: rule.Population},
			Metric:     &AgentFindingMetric{Name: metric, Unit: unit, Observed: result.Observed, Threshold: agentFloat(rule.Threshold), Samples: agentInt(result.MatchedCount)},
			Sources:    []string{"sla_rule"},
			Evidence:   []AgentEvidenceRef{{Ref: "sla_status", URI: agentTestPath(runID, "/sla-status")}},
			Next:       []AgentNextStep{agentDrillDown(runID, id, "per-interval series and the rule's evidence")},
			RuleID:     strings.Join([]string{rule.Group, rule.Transaction, rule.Metric, rule.Comparator}, "|"),
		}
		findings = append(findings, finding)
	}
	return findings
}

func agentSLARowFindings(runID string, report *RunArchiveReport) []AgentFinding {
	rows := report.SLARows
	if rows == nil {
		rows = buildSLAComplianceRowMap(report)
	}
	_, _, maxErrPct, hasErrLimit := reportGoalThresholds(report)
	names := make([]string, 0, len(rows))
	for name := range rows {
		names = append(names, name)
	}
	sort.Strings(names)
	var findings []AgentFinding
	for _, name := range names {
		row := rows[name]
		if row.ResultKind != "fail" && row.ResultKind != "warn" {
			continue
		}
		evidence := []AgentEvidenceRef{{Ref: "transactions", URI: agentTestPath(runID, "/archive/transactions")}}
		// A row fails on its p95 or its error rate; each is its own finding.
		if row.HasSLA && row.SLAMs > 0 && (row.P95Ms > row.SLAMs || row.ResultKind == "warn") {
			severity := agentSeverityBlocker
			title := fmt.Sprintf("p95 of %s is %s vs SLA %s", agentSafeName(name), agentFormatNumber(row.P95Ms, "ms"), agentFormatNumber(row.SLAMs, "ms"))
			if row.P95Ms <= row.SLAMs {
				severity = agentSeverityMinor
				title = fmt.Sprintf("p95 of %s is %s, within 10%% of SLA %s", agentSafeName(name), agentFormatNumber(row.P95Ms, "ms"), agentFormatNumber(row.SLAMs, "ms"))
			}
			id := agentFindingID(agentKindSLABreach, name, "p95_ms")
			findings = append(findings, AgentFinding{
				ID: id, Kind: agentKindSLABreach, Severity: severity,
				Confidence: agentSLAConfidence(row.PercentileQuality, row.Volume),
				Title:      title,
				Scope:      AgentScope{Transaction: name},
				Metric:     &AgentFindingMetric{Name: "p95_ms", Unit: "ms", Observed: agentFloat(row.P95Ms), Threshold: agentFloat(row.SLAMs), Samples: agentInt(row.Volume)},
				Sources:    []string{"sla_row"},
				Evidence:   evidence,
				Next:       []AgentNextStep{agentDrillDown(runID, id, "per-interval latency series")},
			})
		}
		if hasErrLimit && row.ErrorPct > maxErrPct {
			id := agentFindingID(agentKindSLABreach, name, "error_pct")
			findings = append(findings, AgentFinding{
				ID: id, Kind: agentKindSLABreach, Severity: agentSeverityBlocker,
				Confidence: agentSLAConfidence("", row.Volume),
				Title:      fmt.Sprintf("error rate of %s is %s vs limit %s", agentSafeName(name), agentFormatNumber(row.ErrorPct, "pct"), agentFormatNumber(maxErrPct, "pct")),
				Scope:      AgentScope{Transaction: name},
				Metric:     &AgentFindingMetric{Name: "error_pct", Unit: "pct", Observed: agentFloat(row.ErrorPct), Threshold: agentFloat(maxErrPct), Samples: agentInt(row.Volume)},
				Sources:    []string{"sla_row"},
				Evidence:   evidence,
				Next:       []AgentNextStep{agentDrillDown(runID, id, "error samples and series")},
			})
		}
	}
	return findings
}

func agentResponseTimeSLAFindings(runID string, report *RunArchiveReport) []AgentFinding {
	results := report.TransactionResponseTimeSLAResults
	if results == nil && report.Manifest != nil {
		results = report.Manifest.TransactionResponseTimeSLAResults
	}
	var findings []AgentFinding
	for _, result := range results {
		if result.Status != "fail" {
			continue
		}
		id := agentSLAFindingID(result.Transaction, "met_pct", result.Group, "")
		findings = append(findings, AgentFinding{
			ID: id, Kind: agentKindSLABreach, Severity: agentSeverityBlocker,
			Confidence: agentSLAConfidence("", result.EligibleSuccessCount),
			Title: fmt.Sprintf("%s met its %s response-time SLA in %s of requests, %s required", agentSafeName(result.Transaction),
				agentFormatNumber(result.ThresholdMs, "ms"), agentFormatNumber(result.MetPct, "pct"), agentFormatNumber(result.RequiredMetPct, "pct")),
			Scope:    AgentScope{Transaction: result.Transaction, Group: result.Group},
			Metric:   &AgentFindingMetric{Name: "met_pct", Unit: "pct", Observed: agentFloat(result.MetPct), Threshold: agentFloat(result.RequiredMetPct), Samples: agentInt(result.EligibleSuccessCount)},
			Sources:  []string{"response_time_sla"},
			Evidence: []AgentEvidenceRef{{Ref: "sla_status", URI: agentTestPath(runID, "/sla-status")}},
			Next:     []AgentNextStep{agentDrillDown(runID, id, "per-interval latency series")},
		})
	}
	return findings
}

// agentRegressionUnit names a baseline regression metric's unit.
func agentRegressionUnit(metric string) string {
	switch {
	case strings.HasSuffix(metric, "_pct"):
		return "pct"
	case strings.HasSuffix(metric, "_bytes"):
		return "bytes"
	case strings.HasSuffix(metric, "_ratio"):
		return ""
	case strings.Contains(metric, "throughput"), strings.HasSuffix(metric, "_rate"):
		return "per_s"
	case strings.HasSuffix(metric, "_ms"), strings.HasSuffix(metric, "_mean"), strings.HasSuffix(metric, "_p95"), strings.HasSuffix(metric, "_p99"):
		return "ms"
	}
	// Lags, pending writes and other counts.
	return "count"
}

func agentRegressionFindings(runID string, comparison *BaselineComparisonSnapshot) []AgentFinding {
	if comparison == nil || comparison.Verdict == nil {
		return nil
	}
	rows := map[string]BaselineComparisonRow{}
	for _, row := range append(append([]BaselineComparisonRow(nil), comparison.Transactions...), comparison.Resources...) {
		rows[row.ID] = row
	}
	var findings []AgentFinding
	for _, evidence := range comparison.Verdict.Evidence {
		if evidence.Status != "regressed" {
			continue
		}
		row := rows[evidence.RowID]
		name := firstNonEmpty(row.Name, evidence.RowID)
		unit := agentRegressionUnit(evidence.Metric)
		metric := &AgentFindingMetric{Name: evidence.Metric, Unit: unit, Observed: evidence.Current, Baseline: evidence.Baseline}
		observed, base := "unmeasured", "unmeasured"
		if evidence.Current != nil {
			observed = agentFormatNumber(*evidence.Current, unit)
		}
		if evidence.Baseline != nil {
			base = agentFormatNumber(*evidence.Baseline, unit)
			if evidence.Current != nil && *evidence.Baseline != 0 {
				metric.DeltaPct = agentFloat((*evidence.Current - *evidence.Baseline) / *evidence.Baseline * 100)
			}
		}
		id := agentFindingID(agentKindRegression, row.Group, name, evidence.Metric)
		findings = append(findings, AgentFinding{
			ID: id, Kind: agentKindRegression, Severity: agentSeverityMajor, Confidence: agentConfidenceHigh,
			Title:    fmt.Sprintf("%s of %s regressed to %s from baseline %s", evidence.Metric, agentSafeName(name), observed, base),
			Scope:    AgentScope{Transaction: name, Group: row.Group},
			Metric:   metric,
			Sources:  []string{"baseline_regression"},
			Evidence: []AgentEvidenceRef{{Ref: "baseline_comparison", URI: agentTestPath(runID, "/baseline-comparisons/"+url.PathEscape(comparison.ID))}},
			Next:     []AgentNextStep{agentDrillDown(runID, id, "series against the baseline")},
		})
	}
	return findings
}

func agentErrorPatternFindings(runID string, report *RunArchiveReport) []AgentFinding {
	var findings []AgentFinding
	for _, pattern := range report.ErrorPatterns {
		rate := archiveErrorPatternFailureRate(report, pattern)
		severity := agentSeverityMinor
		if rate >= 0.01 {
			severity = agentSeverityMajor
		}
		scope := AgentScope{}
		actions := append([]string(nil), pattern.Actions...)
		sort.Strings(actions)
		if len(actions) == 1 {
			scope.Transaction = actions[0]
		}
		where := "the run"
		if len(actions) == 1 {
			where = agentSafeName(actions[0])
		} else if len(actions) > 1 {
			where = fmt.Sprintf("%d transactions", len(actions))
		}
		id := agentFindingID(agentKindErrorGroup, pattern.Signature)
		finding := AgentFinding{
			ID: id, Kind: agentKindErrorGroup, Severity: severity, Confidence: agentConfidenceHigh,
			Title:   fmt.Sprintf("Recurring error in %s: %d failures, %s of requests", where, pattern.Count, agentFormatNumber(rate*100, "pct")),
			Scope:   scope,
			Metric:  &AgentFindingMetric{Name: "failure_rate_pct", Unit: "pct", Observed: agentFloat(rate * 100), Samples: agentInt(pattern.Count)},
			Sources: []string{"error_pattern"},
			// The finding detail serves this pattern's archived samples.
			Evidence: []AgentEvidenceRef{{Ref: "finding_detail", URI: agentTestPath(runID, "/archive/agent-brief/findings/"+url.PathEscape(id))}},
			// The full sample is kept here; the budget trims it per detail level.
			Untrusted: agentUntrustedText("error_pattern", pattern.Sample, 2048),
			Next:      []AgentNextStep{agentDrillDown(runID, id, "error samples and when they occurred")},
		}
		if !pattern.FirstSeen.IsZero() && !pattern.LatestSeen.IsZero() {
			finding.Scope.Window = &AgentWindow{From: pattern.FirstSeen.UTC().Truncate(time.Second), To: pattern.LatestSeen.UTC().Truncate(time.Second)}
		}
		findings = append(findings, finding)
	}
	return findings
}

func agentCompletenessFindings(runID string, report *RunArchiveReport) []AgentFinding {
	var findings []AgentFinding
	for _, gap := range report.Completeness.Findings {
		severity := completenessSeverityToFinding(gap.Severity)
		switch severity {
		case "major", "minor", "info":
		default:
			severity = agentSeverityMinor
		}
		// A completeness message can quote the run's error, which may come
		// from the system under test, so it is kept as untrusted text.
		findings = append(findings, AgentFinding{
			ID: agentFindingID(agentKindEvidenceGap, gap.Dimension), Kind: agentKindEvidenceGap, Severity: severity, Confidence: agentConfidenceHigh,
			Title:     "Evidence coverage: " + completenessDimensionLabel(gap.Dimension) + " is incomplete",
			Sources:   []string{"completeness"},
			Evidence:  []AgentEvidenceRef{{Ref: "manifest", URI: agentTestPath(runID, "/archive/manifest")}},
			Untrusted: agentUntrustedText("completeness", gap.Message, 2048),
		})
	}
	return findings
}

func agentValidityFindings(runID string, report *RunArchiveReport, gate string) []AgentFinding {
	var findings []AgentFinding
	if validity := report.Validity; validity != nil && (validity.Verdict == rca.ValidityInvalid || validity.Verdict == rca.ValiditySuspect) {
		severity := agentSeverityMinor
		if validity.Verdict == rca.ValidityInvalid {
			severity = agentSeverityMajor
			if gate != "" {
				severity = agentSeverityBlocker
			}
		} else if gate == ciValidityGateSuspect {
			severity = agentSeverityBlocker
		}
		type ruleKey struct{ rule, generator string }
		seen := map[ruleKey]rca.ValidityFinding{}
		windowFrom, windowTo := validityWindowBounds(validity)
		for _, window := range validity.Windows {
			for _, finding := range window.Findings {
				key := ruleKey{finding.Rule, finding.Generator}
				if prior, ok := seen[key]; !ok || finding.Value > prior.Value {
					seen[key] = finding
				}
			}
		}
		keys := make([]ruleKey, 0, len(seen))
		for key := range seen {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool {
			if keys[i].rule != keys[j].rule {
				return keys[i].rule < keys[j].rule
			}
			return keys[i].generator < keys[j].generator
		})
		// Validity is computed from the archive and served with the report.
		evidence := []AgentEvidenceRef{{Ref: "report_validity", URI: agentTestPath(runID, "/archive/report")}}
		for _, key := range keys {
			finding := seen[key]
			where := ""
			if key.generator != "" {
				where = " on " + agentSafeName(key.generator)
			}
			af := AgentFinding{
				ID: agentFindingID(agentKindValidity, key.rule, key.generator), Kind: agentKindValidity, Severity: severity, Confidence: agentConfidenceHigh,
				Title:    fmt.Sprintf("Run measurement %s: %s%s (%s vs threshold %s)", validity.Verdict, agentSafeName(key.rule), where, agentFormatNumber(finding.Value, ""), agentFormatNumber(finding.Threshold, "")),
				Scope:    AgentScope{Generator: key.generator},
				Metric:   &AgentFindingMetric{Name: key.rule, Observed: agentFloat(finding.Value), Threshold: agentFloat(finding.Threshold)},
				Sources:  []string{"validity"},
				Evidence: evidence,
			}
			if !windowFrom.IsZero() {
				af.Scope.Window = &AgentWindow{From: windowFrom, To: windowTo}
			}
			findings = append(findings, af)
		}
		if len(keys) == 0 {
			reasons := append([]string(nil), validity.Reasons...)
			sort.Strings(reasons)
			findings = append(findings, AgentFinding{
				ID: agentFindingID(agentKindValidity, "verdict"), Kind: agentKindValidity, Severity: severity, Confidence: agentConfidenceHigh,
				Title:    agentSafeText("Run measurement "+validity.Verdict+": "+strings.Join(reasons, "; "), agentTitleMaxRunes),
				Sources:  []string{"validity"},
				Evidence: evidence,
			})
		}
	}
	for _, finding := range report.TelemetryValidityFindings {
		if finding.Compliant {
			continue
		}
		metric := &AgentFindingMetric{Name: finding.Criterion, Unit: "ms", Observed: finding.ObservedMs}
		if finding.ThresholdMs > 0 {
			metric.Threshold = agentFloat(finding.ThresholdMs)
		}
		findings = append(findings, AgentFinding{
			ID: agentFindingID(agentKindValidity, "telemetry", finding.Criterion), Kind: agentKindValidity, Severity: agentSeverityMajor, Confidence: agentConfidenceHigh,
			Title:    agentSafeText("Telemetry evidence "+finding.Status+": "+finding.Summary, agentTitleMaxRunes),
			Metric:   metric,
			Sources:  []string{"telemetry_validity"},
			Evidence: []AgentEvidenceRef{{Ref: "report", URI: agentTestPath(runID, "/archive/report")}},
		})
	}
	return findings
}

func validityWindowBounds(validity *rca.Validity) (from, to time.Time) {
	for _, window := range validity.Windows {
		if from.IsZero() || window.Start.Before(from) {
			from = window.Start
		}
		if window.End.After(to) {
			to = window.End
		}
	}
	return from.UTC().Truncate(time.Second), to.UTC().Truncate(time.Second)
}

func agentThroughputFindings(runID string, report *RunArchiveReport) []AgentFinding {
	evidence := []AgentEvidenceRef{{Ref: "summaries", URI: agentTestPath(runID, "/archive/summaries")}}
	if attainment := report.Overview.GoalAttainment; attainment != nil {
		if attainment.OverWindows+attainment.UnderWindows == 0 {
			return nil
		}
		return []AgentFinding{{
			ID: agentFindingID(agentKindThroughputGap, "throughput"), Kind: agentKindThroughputGap, Severity: agentSeverityMinor, Confidence: agentConfidenceHigh,
			Title: fmt.Sprintf("Throughput averaged %s vs goal %s; off target in %d of %d windows", agentFormatNumber(attainment.AchievedAverageTPS, "per_s"),
				agentFormatNumber(attainment.TargetAverageTPS, "per_s"), attainment.OverWindows+attainment.UnderWindows, attainment.Windows),
			Metric:   &AgentFindingMetric{Name: "throughput_per_s", Unit: "per_s", Observed: agentFloat(attainment.AchievedAverageTPS), Threshold: agentFloat(attainment.TargetAverageTPS)},
			Sources:  []string{"goal_attainment"},
			Evidence: evidence,
		}}
	}
	if report.Overview.ThroughputMissedIntervals == 0 {
		return nil
	}
	return []AgentFinding{{
		ID: agentFindingID(agentKindThroughputGap, "throughput"), Kind: agentKindThroughputGap, Severity: agentSeverityMinor, Confidence: agentConfidenceHigh,
		Title: fmt.Sprintf("Throughput missed its %s target in %d intervals; peak gap %s", agentFormatNumber(report.Overview.PeakTargetTPS, "per_s"),
			report.Overview.ThroughputMissedIntervals, agentFormatNumber(report.Overview.MaxThroughputGapPct, "pct")),
		Metric:   &AgentFindingMetric{Name: "throughput_gap_pct", Unit: "pct", Observed: agentFloat(report.Overview.MaxThroughputGapPct)},
		Sources:  []string{"throughput"},
		Evidence: evidence,
	}}
}

func agentEnvironmentFindings(runID string, report *RunArchiveReport) []AgentFinding {
	evidence := []AgentEvidenceRef{{Ref: "report", URI: agentTestPath(runID, "/archive/report")}}
	var findings []AgentFinding
	add := func(key string, finding *ReportFinding) {
		if finding == nil {
			return
		}
		findings = append(findings, AgentFinding{
			ID: agentFindingID(agentKindEnvironment, key), Kind: agentKindEnvironment, Severity: agentSeverityMinor, Confidence: agentConfidenceMedium,
			Title:    agentSafeText(finding.Title+": "+finding.Observed, agentTitleMaxRunes),
			Sources:  []string{key},
			Evidence: evidence,
		})
	}
	add("host_network", hostNetworkFinding(report.HostNetwork))
	add("http3_preflight", http3PreflightFinding(report.HTTP3Preflight))
	if mix := report.NetworkPhases.VersionMix; mix != nil && mix.Finding != nil {
		add("protocol_mix", &ReportFinding{Title: protocolMixFindingTitle(mix.Finding), Observed: mix.Finding.Summary})
	}
	return findings
}

func agentAnomalyFindings(runID string, report *RunArchiveReport) []AgentFinding {
	var findings []AgentFinding
	for _, anomaly := range report.StatisticalFindings {
		if !anomaly.Exceeded {
			continue
		}
		confidence := agentConfidenceMedium
		if anomaly.Threshold > 0 && abs(anomaly.ZScore) >= 2*anomaly.Threshold {
			confidence = agentConfidenceHigh
		}
		findings = append(findings, AgentFinding{
			ID: agentFindingID(agentKindAnomaly, anomaly.MetricName), Kind: agentKindAnomaly, Severity: agentSeverityInfo, Confidence: confidence,
			Title: fmt.Sprintf("%s was %s against an in-run baseline mean of %s (z=%s)", agentSafeName(anomaly.MetricName),
				agentFormatNumber(anomaly.Current, ""), agentFormatNumber(anomaly.BaselineMean, ""), agentFormatNumber(anomaly.ZScore, "")),
			Metric:  &AgentFindingMetric{Name: anomaly.MetricName, Observed: agentFloat(anomaly.Current), Baseline: agentFloat(anomaly.BaselineMean)},
			Sources: []string{"statistical_finding"},
			// Statistical findings are served with the report.
			Evidence: []AgentEvidenceRef{{Ref: "report_statistical_findings", URI: agentTestPath(runID, "/archive/report")}},
		})
	}
	return findings
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

func agentRCAFindings(runID string, report *RunArchiveReport) []AgentFinding {
	view := report.RootCauseView
	if view == nil {
		return nil
	}
	// A v1 view comes from the archive's own root-cause summary; only a v2
	// view has an analysis, its assessment and bundle.
	evidence := []AgentEvidenceRef{{Ref: "root_cause", URI: agentTestPath(runID, "/archive/root-cause")}}
	if view.Source == reportRootCauseV2 {
		evidence = []AgentEvidenceRef{{Ref: "root_cause_v2", URI: agentTestPath(runID, "/archive/root-cause/v2")}}
	}
	next := []AgentNextStep(nil)
	if view.AnalysisID != "" {
		evidence = append(evidence, AgentEvidenceRef{Ref: "rca_bundle", URI: agentTestPath(runID, "/archive/root-cause/analyses/"+url.PathEscape(view.AnalysisID)+"/bundle")})
		next = []AgentNextStep{{Tool: "apex_rca_bundle", Args: map[string]string{"run_id": runID}, Why: "causal chain, supporting and contradicting evidence"}}
	}
	var findings []AgentFinding
	for _, hypothesis := range view.Hypotheses {
		title := agentSafeText(hypothesis.Title, agentTitleMaxRunes)
		if title == "" {
			continue
		}
		confidence := agentConfidenceFromLabel(hypothesis.Confidence)
		severity := agentSeverityMinor
		switch strings.ToLower(hypothesis.Severity) {
		case "critical", "high", "major":
			severity = agentSeverityMajor
		case "low", "info":
			severity = agentSeverityInfo
		case "":
			if hypothesis.Rank == 1 && confidence != agentConfidenceLow {
				severity = agentSeverityMajor
			}
		}
		if hypothesis.Severity == "" && confidence == "" {
			confidence = agentConfidenceMedium
		}
		if confidence == "" {
			confidence = agentConfidenceMedium
		}
		findings = append(findings, AgentFinding{
			ID: agentFindingID(agentKindRCAHypothesis, title), Kind: agentKindRCAHypothesis, Severity: severity, Confidence: confidence,
			Title:    fmt.Sprintf("Likely cause #%d: %s", hypothesis.Rank, title),
			Sources:  []string{"rca_" + firstNonEmpty(view.Source, "v1")},
			Evidence: evidence,
			Next:     next,
		})
	}
	return findings
}

func agentConfidenceFromLabel(label string) string {
	switch strings.ToLower(strings.TrimSpace(label)) {
	case "high", "strong":
		return agentConfidenceHigh
	case "medium", "moderate":
		return agentConfidenceMedium
	case "low", "weak", "speculative":
		return agentConfidenceLow
	}
	return ""
}

func agentAutoStopFindings(runID string, report *RunArchiveReport) []AgentFinding {
	if report.Manifest == nil || report.Manifest.StopTrigger == nil {
		return nil
	}
	trigger := report.Manifest.StopTrigger
	at := trigger.At.UTC()
	return []AgentFinding{{
		ID: agentFindingID(agentKindAutoStop, trigger.Rule), Kind: agentKindAutoStop, Severity: agentSeverityMajor, Confidence: agentConfidenceHigh,
		Title:    agentSafeText(fmt.Sprintf("Run stopped automatically by the %s rule: %s", trigger.Rule, trigger.Reason), agentTitleMaxRunes),
		Scope:    AgentScope{Window: &AgentWindow{From: at, To: at}},
		Sources:  []string{"stop_trigger"},
		Evidence: []AgentEvidenceRef{{Ref: "sla_status", URI: agentTestPath(runID, "/sla-status")}},
	}}
}

var agentSeverityRank = map[string]int{agentSeverityBlocker: 0, agentSeverityMajor: 1, agentSeverityMinor: 2, agentSeverityInfo: 3}
var agentConfidenceRank = map[string]int{agentConfidenceHigh: 0, agentConfidenceMedium: 1, agentConfidenceLow: 2}
var agentBaselineRank = map[string]int{agentBaselineNew: 0, agentBaselineRecurring: 1, agentBaselineUnknown: 2}

// agentSourcePriority orders whose metric wins when findings merge.
var agentSourcePriority = map[string]int{"sla_rule": 0, "sla_row": 1, "response_time_sla": 2, "baseline_regression": 3}

func agentSourceRank(sources []string) int {
	best := 99
	for _, source := range sources {
		if rank, ok := agentSourcePriority[source]; ok && rank < best {
			best = rank
		}
	}
	return best
}

// mergeAgentFindings folds findings with the same id into one: the worst
// severity, the strongest confidence, the union of sources and evidence, and
// the metric and title of the highest-priority source.
func mergeAgentFindings(findings []AgentFinding) []AgentFinding {
	byID := map[string]*AgentFinding{}
	order := []string{}
	for _, finding := range findings {
		existing, ok := byID[finding.ID]
		if !ok {
			copied := finding
			byID[finding.ID] = &copied
			order = append(order, finding.ID)
			continue
		}
		newRank, oldRank := agentSourceRank(finding.Sources), agentSourceRank(existing.Sources)
		newSeverity, oldSeverity := agentSeverityRank[finding.Severity], agentSeverityRank[existing.Severity]
		// The more severe breach describes the finding, so its title and
		// threshold are the ones that block; between equal severities, the
		// better source does.
		if newSeverity < oldSeverity || (newSeverity == oldSeverity && newRank < oldRank) {
			existing.Title, existing.Metric, existing.Scope = finding.Title, finding.Metric, finding.Scope
			if finding.RuleID != "" {
				existing.RuleID = finding.RuleID
			}
		}
		if agentSeverityRank[finding.Severity] < agentSeverityRank[existing.Severity] {
			existing.Severity = finding.Severity
		}
		if agentConfidenceRank[finding.Confidence] < agentConfidenceRank[existing.Confidence] {
			existing.Confidence = finding.Confidence
		}
		existing.Sources = mergeSortedStrings(existing.Sources, finding.Sources)
		existing.Evidence = mergeAgentEvidence(existing.Evidence, finding.Evidence)
		if existing.Untrusted == nil {
			existing.Untrusted = finding.Untrusted
		}
		if len(existing.Next) == 0 {
			existing.Next = finding.Next
		}
		if existing.RuleID == "" {
			existing.RuleID = finding.RuleID
		}
	}
	merged := make([]AgentFinding, 0, len(order))
	for _, id := range order {
		finding := *byID[id]
		finding.Sources = mergeSortedStrings(finding.Sources, nil)
		finding.Evidence = mergeAgentEvidence(finding.Evidence, nil)
		if finding.BaselineState == "" {
			finding.BaselineState = agentBaselineUnknown
		}
		merged = append(merged, finding)
	}
	return merged
}

func mergeSortedStrings(a, b []string) []string {
	set := map[string]bool{}
	for _, v := range append(append([]string(nil), a...), b...) {
		if v != "" {
			set[v] = true
		}
	}
	out := make([]string, 0, len(set))
	for v := range set {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func mergeAgentEvidence(a, b []AgentEvidenceRef) []AgentEvidenceRef {
	set := map[AgentEvidenceRef]bool{}
	for _, v := range append(append([]AgentEvidenceRef(nil), a...), b...) {
		set[v] = true
	}
	out := make([]AgentEvidenceRef, 0, len(set))
	for v := range set {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Ref != out[j].Ref {
			return out[i].Ref < out[j].Ref
		}
		return out[i].URI < out[j].URI
	})
	return out
}

// agentFindingOvershoot is how far a finding is past its line, for ranking:
// the baseline delta when there is one, else observed over threshold.
func agentFindingOvershoot(finding AgentFinding) float64 {
	if finding.Metric == nil {
		return 0
	}
	if finding.Metric.DeltaPct != nil {
		return abs(*finding.Metric.DeltaPct)
	}
	if finding.Metric.Observed != nil && finding.Metric.Threshold != nil && *finding.Metric.Threshold != 0 {
		return abs((*finding.Metric.Observed - *finding.Metric.Threshold) / *finding.Metric.Threshold * 100)
	}
	return 0
}

// rankAgentFindings orders findings worst first; the order is total, so the
// same findings always come out the same way.
func rankAgentFindings(findings []AgentFinding) {
	sort.SliceStable(findings, func(i, j int) bool {
		a, b := findings[i], findings[j]
		if ra, rb := agentSeverityRank[a.Severity], agentSeverityRank[b.Severity]; ra != rb {
			return ra < rb
		}
		if ra, rb := agentBaselineRank[a.BaselineState], agentBaselineRank[b.BaselineState]; ra != rb {
			return ra < rb
		}
		if ra, rb := agentConfidenceRank[a.Confidence], agentConfidenceRank[b.Confidence]; ra != rb {
			return ra < rb
		}
		if oa, ob := agentFindingOvershoot(a), agentFindingOvershoot(b); oa != ob {
			return oa > ob
		}
		return a.ID < b.ID
	})
}
