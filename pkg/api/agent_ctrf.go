package api

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/buildinfo"
)

// agentCTRFSpecVersion is the CTRF specification version the report claims.
// Pin it to the version of the vendored CTRF schema when that lands; until
// then the report uses only CTRF's core fields (no insights).
const agentCTRFSpecVersion = "0.0.0"

// AgentCTRFReport is a Common Test Report Format document for a run: one
// test per SLA check plus a verdict test, so CI dashboards that read CTRF
// can gate on an APEX Load run.
type AgentCTRFReport struct {
	ReportFormat string        `json:"reportFormat"`
	SpecVersion  string        `json:"specVersion"`
	Results      AgentCTRFRuns `json:"results"`
}

// AgentCTRFRuns is CTRF's results object.
type AgentCTRFRuns struct {
	Tool        AgentCTRFTool         `json:"tool"`
	Summary     AgentCTRFSummary      `json:"summary"`
	Tests       []AgentCTRFTest       `json:"tests"`
	Environment *AgentCTRFEnvironment `json:"environment,omitempty"`
	Extra       map[string]any        `json:"extra,omitempty"`
}

// AgentCTRFTool names the producer.
type AgentCTRFTool struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// AgentCTRFSummary counts tests by status; start and stop are epoch ms.
type AgentCTRFSummary struct {
	Tests   int   `json:"tests"`
	Passed  int   `json:"passed"`
	Failed  int   `json:"failed"`
	Pending int   `json:"pending"`
	Skipped int   `json:"skipped"`
	Other   int   `json:"other"`
	Start   int64 `json:"start"`
	Stop    int64 `json:"stop"`
}

// AgentCTRFTest is one check. Duration is the transaction's observed p95
// in milliseconds for an SLA check (a documented APEX mapping: CTRF's
// duration is a test's own runtime, which a load test has no analogue of)
// and the run's duration for the verdict test.
type AgentCTRFTest struct {
	Name     string         `json:"name"`
	Status   string         `json:"status"`
	Duration int64          `json:"duration"`
	Message  string         `json:"message,omitempty"`
	Suite    string         `json:"suite,omitempty"`
	Extra    map[string]any `json:"extra,omitempty"`
}

// AgentCTRFEnvironment is the build the run tested.
type AgentCTRFEnvironment struct {
	AppName    string `json:"appName,omitempty"`
	AppVersion string `json:"appVersion,omitempty"`
	BuildName  string `json:"buildName,omitempty"`
	Commit     string `json:"commit,omitempty"`
	BranchName string `json:"branchName,omitempty"`
}

// buildAgentCTRF converts a brief. It needs the brief's SLA findings and
// metric rows, so it should be built from an unbounded or full brief when
// every check must appear.
func buildAgentCTRF(brief *AgentBrief) AgentCTRFReport {
	report := AgentCTRFReport{ReportFormat: "CTRF", SpecVersion: agentCTRFSpecVersion}
	report.Results.Tool = AgentCTRFTool{Name: "APEX Load", Version: buildinfo.Version}
	tests := []AgentCTRFTest{}
	// The run's goal: its p95 SLA and error-rate limit, judged per
	// transaction row.
	for _, row := range brief.Metrics {
		if row.Scope == "overall" {
			continue
		}
		name := agentSafeName(row.Scope)
		if row.SLAMs != nil {
			test := AgentCTRFTest{
				Name:   fmt.Sprintf("SLA %s p95 <= %s", name, agentFormatNumber(*row.SLAMs, "ms")),
				Suite:  "sla",
				Status: "passed",
				Extra:  map[string]any{"apexload": agentCTRFMetricExtra(row, agentFindingID(agentKindSLABreach, row.Scope, "p95_ms"), "p95_ms", row.SLAMs)},
			}
			goal := brief.ctrf.goal[row.Scope]
			switch {
			case row.Samples == 0 || row.SLAResult == "na" || row.SLAResult == "" || row.P95Ms == nil:
				test.Status = "skipped"
			case goal.p95Failed:
				test.Status = "failed"
				test.Message = fmt.Sprintf("p95 of %s is %s vs SLA %s", name, agentFormatNumber(*row.P95Ms, "ms"), agentFormatNumber(*row.SLAMs, "ms"))
			case goal.p95Warned:
				test.Status = "other"
				test.Message = fmt.Sprintf("p95 of %s is %s, within 10%% of SLA %s", name, agentFormatNumber(*row.P95Ms, "ms"), agentFormatNumber(*row.SLAMs, "ms"))
			}
			if row.P95Ms != nil {
				test.Duration = int64(*row.P95Ms + 0.5)
			}
			tests = append(tests, test)
		}
		if limit := brief.ctrf.maxErrorPct; limit != nil && row.ErrorPct != nil && row.Samples > 0 {
			test := AgentCTRFTest{
				Name:   fmt.Sprintf("SLA %s error rate <= %s", name, agentFormatNumber(*limit, "pct")),
				Suite:  "sla",
				Status: "passed",
				Extra:  map[string]any{"apexload": agentCTRFMetricExtra(row, agentFindingID(agentKindSLABreach, row.Scope, "error_pct"), "error_pct", limit)},
			}
			if brief.ctrf.goal[row.Scope].errorFailed {
				test.Status = "failed"
				test.Message = fmt.Sprintf("error rate of %s is %s vs limit %s", name, agentFormatNumber(*row.ErrorPct, "pct"), agentFormatNumber(*limit, "pct"))
			}
			tests = append(tests, test)
		}
	}
	// Transaction SLA rules, each under the threshold it judged.
	for _, result := range brief.ctrf.rules {
		rule := result.Rule
		metric, unit := agentSLAMetric(rule.Metric)
		subject := agentSafeName(rule.Transaction)
		if rule.Group != "" {
			subject = agentSafeName(rule.Group) + "/" + subject
		}
		var qualifiers []string
		for _, part := range []string{rule.Population, rule.Protocol, rule.Iterations} {
			if part = strings.TrimSpace(part); part != "" {
				qualifiers = append(qualifiers, agentSafeName(part))
			}
		}
		if len(qualifiers) > 0 {
			subject += " (" + strings.Join(qualifiers, ", ") + ")"
		}
		test := AgentCTRFTest{
			Name:  fmt.Sprintf("SLA rule %s %s %s %s", subject, metric, agentComparator(rule.Comparator), agentFormatNumber(rule.Threshold, unit)),
			Suite: "sla_rules",
		}
		extra := map[string]any{"metric": metric, "unit": unit, "threshold": agentRound(rule.Threshold, unit), "samples": result.MatchedCount, "rule_status": result.Status}
		if result.Observed != nil {
			extra["observed"] = agentRound(*result.Observed, unit)
		}
		switch result.Status {
		case "pass":
			test.Status = "passed"
		case "fail":
			test.Status = "failed"
		case "warning":
			test.Status = "other"
		default:
			test.Status = "skipped"
		}
		if test.Status == "failed" || test.Status == "other" {
			extra["finding_id"] = agentRuleFindingID(rule, metric)
			observed := "unmeasured"
			if result.Observed != nil {
				observed = agentFormatNumber(*result.Observed, unit)
			}
			test.Message = fmt.Sprintf("%s of %s is %s, rule requires %s %s", metric, agentSafeName(rule.Transaction), observed, agentComparator(rule.Comparator), agentFormatNumber(rule.Threshold, unit))
		}
		if unit == "ms" && result.Observed != nil {
			test.Duration = int64(agentRound(*result.Observed, unit) + 0.5)
		}
		test.Extra = map[string]any{"apexload": extra}
		tests = append(tests, test)
	}
	// Response-time SLAs: the share of requests within a threshold.
	for _, result := range brief.ctrf.responseTime {
		name := agentSafeName(result.Transaction)
		if result.Group != "" {
			name = agentSafeName(result.Group) + "/" + name
		}
		test := AgentCTRFTest{
			Name:   fmt.Sprintf("SLA %s within %s for >= %s of requests", name, agentFormatNumber(result.ThresholdMs, "ms"), agentFormatNumber(result.RequiredMetPct, "pct")),
			Suite:  "sla_response_time",
			Status: "skipped",
			Extra: map[string]any{"apexload": map[string]any{
				"metric": "met_pct", "unit": "pct", "observed": agentRound(result.MetPct, "pct"), "threshold": agentRound(result.RequiredMetPct, "pct"),
				"samples": result.EligibleSuccessCount, "finding_id": agentSLAFindingID(result.Transaction, "met_pct", result.Group, ""),
			}},
		}
		switch result.Status {
		case "pass":
			test.Status = "passed"
		case "fail":
			test.Status = "failed"
			test.Message = fmt.Sprintf("%s met %s in %s of requests, %s required", name, agentFormatNumber(result.ThresholdMs, "ms"), agentFormatNumber(result.MetPct, "pct"), agentFormatNumber(result.RequiredMetPct, "pct"))
		}
		tests = append(tests, test)
	}
	for _, test := range tests {
		// A finding id names a finding only for a check that was breached.
		if apexload, ok := test.Extra["apexload"].(map[string]any); ok && (test.Status == "passed" || test.Status == "skipped") {
			delete(apexload, "finding_id")
		}
	}
	sort.SliceStable(tests, func(i, j int) bool { return tests[i].Name < tests[j].Name })
	verdict := AgentCTRFTest{
		Name:    "APEX Load verdict",
		Suite:   "verdict",
		Status:  "passed",
		Message: brief.Verdict.Explanation,
		Extra:   map[string]any{"apexload": map[string]any{"status": brief.Verdict.Status, "reason_codes": brief.Verdict.ReasonCodes}},
	}
	if brief.Verdict.Blocking {
		verdict.Status = "failed"
	}
	if brief.Run.ObservedDurationS != nil {
		verdict.Duration = int64(*brief.Run.ObservedDurationS*1000 + 0.5)
	}
	tests = append(tests, verdict)
	report.Results.Tests = tests
	summary := AgentCTRFSummary{Tests: len(tests)}
	for _, test := range tests {
		switch test.Status {
		case "passed":
			summary.Passed++
		case "failed":
			summary.Failed++
		case "skipped":
			summary.Skipped++
		case "pending":
			summary.Pending++
		default:
			summary.Other++
		}
	}
	if brief.Run.StartedAt != nil {
		summary.Start = brief.Run.StartedAt.UnixMilli()
	}
	if brief.Run.CompletedAt != nil {
		summary.Stop = brief.Run.CompletedAt.UnixMilli()
	}
	report.Results.Summary = summary
	environment := AgentCTRFEnvironment{AppName: brief.Run.ApplicationID, AppVersion: brief.Run.ReleaseID, BuildName: brief.Run.CIRunID}
	if source := brief.Run.SourceContext; source != nil {
		environment.Commit, environment.BranchName = source.SHA, firstNonEmpty(source.HeadRef, source.Ref)
	}
	if environment != (AgentCTRFEnvironment{}) {
		report.Results.Environment = &environment
	}
	findingIDs := make([]string, 0, len(brief.Findings))
	for _, finding := range brief.Findings {
		findingIDs = append(findingIDs, finding.ID)
	}
	report.Results.Extra = map[string]any{"apexload": map[string]any{
		"run_id":       brief.Run.ID,
		"verdict":      brief.Verdict.Status,
		"blocking":     brief.Verdict.Blocking,
		"reason_codes": brief.Verdict.ReasonCodes,
		"findings":     findingIDs,
		"brief_uri":    brief.Links.Brief,
	}}
	return report
}

func agentCTRFMetricExtra(row AgentMetricRow, findingID, metric string, threshold *float64) map[string]any {
	unit, observed := "ms", row.P95Ms
	if metric == "error_pct" {
		unit, observed = "pct", row.ErrorPct
	}
	extra := map[string]any{"metric": metric, "unit": unit, "samples": row.Samples, "finding_id": findingID}
	put := func(key string, v *float64) {
		if v != nil {
			extra[key] = *v
		}
	}
	put("observed", observed)
	put("threshold", threshold)
	put("p50_ms", row.P50Ms)
	put("p99_ms", row.P99Ms)
	put("throughput_per_s", row.ThroughputPS)
	if metric == "p95_ms" && row.Baseline != nil {
		put("baseline", row.Baseline.P95Ms)
		put("delta_pct", row.Baseline.P95DeltaPct)
	}
	return extra
}

// agentCTRFInputsOf gathers what the CTRF rendering judges, from the report
// before the brief rounds anything.
func agentCTRFInputsOf(report *RunArchiveReport) agentCTRFInputs {
	inputs := agentCTRFInputs{goal: map[string]agentCTRFGoal{}}
	if report.Manifest != nil {
		inputs.rules = append([]TransactionSLARuleResult(nil), report.Manifest.TransactionRuleResults...)
	}
	inputs.responseTime = report.TransactionResponseTimeSLAResults
	if inputs.responseTime == nil && report.Manifest != nil {
		inputs.responseTime = report.Manifest.TransactionResponseTimeSLAResults
	}
	inputs.responseTime = append([]TransactionResponseTimeSLAResult(nil), inputs.responseTime...)
	_, _, maxErrPct, hasErrLimit := reportGoalThresholds(report)
	if hasErrLimit {
		inputs.maxErrorPct = agentFloat(maxErrPct)
	}
	rows := report.SLARows
	if rows == nil {
		rows = buildSLAComplianceRowMap(report)
	}
	for name, row := range rows {
		goal := agentCTRFGoal{errorFailed: hasErrLimit && row.ErrorPct > maxErrPct}
		if row.HasSLA && row.SLAMs > 0 {
			goal.p95Failed = row.P95Ms > row.SLAMs
			goal.p95Warned = !goal.p95Failed && row.ResultKind == "warn"
		}
		inputs.goal[name] = goal
	}
	return inputs
}
