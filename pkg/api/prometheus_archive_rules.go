package api

import (
	"fmt"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Finite canonical values remain useful evidence when their upstream query was
// partial. Acceptance additionally needs a completed query for this criterion's
// own identity and window, independently from unrelated capture failures.
func guardPrometheusRuleCoverage(result analysis.MonitorRuleResult, jobs []storage.RunMonitorJob, start, end time.Time) analysis.MonitorRuleResult {
	rule := result.Rule
	for _, job := range jobs {
		if job.Source.ID != rule.SourceID || job.Source.Prometheus == nil {
			continue
		}
		queryID := ""
		for _, query := range job.Source.Prometheus.Queries {
			if query.Name == rule.Metric && query.Unit == rule.Unit && (rule.QueryID == "" || query.ID == rule.QueryID) {
				queryID = query.ID
				break
			}
		}
		from, to := start.Add(time.Duration(rule.StartOffsetSeconds)*time.Second), end
		if rule.EndOffsetSeconds != nil {
			candidate := start.Add(time.Duration(*rule.EndOffsetSeconds) * time.Second)
			if candidate.Before(to) {
				to = candidate
			}
		}
		key := queryID
		if rule.EvaluationMode == "exact_window" {
			key = ""
			for index, window := range prometheusTerminalWindows(job) {
				if window.QueryID == queryID && window.From.Equal(from) && window.To.Equal(to) {
					key = fmt.Sprintf("terminal:%d:%s", index, queryID)
					break
				}
			}
		}
		coverage, exists := job.Coverage[key]
		covered := exists && queryID != "" && key != "" && coverage.Status == "complete" && !coverage.Truncated && len(coverage.Warnings) == 0 && !coverage.From.After(from) && !coverage.To.Before(to)
		if rule.EvaluationMode == "exact_window" {
			covered = covered && coverage.From.Equal(from) && coverage.To.Equal(to)
		}
		if !covered {
			result.Status = "not_evaluated"
			if rule.Severity == "warning" {
				result.Status = "warning"
			}
			result.Message = "Prometheus query coverage is incomplete for the monitoring criterion"
		}
		return result
	}
	return result
}
