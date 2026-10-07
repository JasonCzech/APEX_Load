package api

import (
	"encoding/json"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/elastic"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Terminal windows come from the frozen start provenance, never a source edit
// or a mutable live dashboard selection.
func (s *Server) elasticTerminalWindows(job storage.RunMonitorJob) []elastic.TerminalWindow {
	if job.State != "reconciling" || job.LoadEndedAt == nil {
		return nil
	}
	rules := job.FrozenMonitorRules
	if !job.RulesFrozen {
		record, err := s.store.GetTest(job.RunID)
		if err != nil || record == nil {
			return nil
		}
		var provenance struct {
			GoalConfig *GoalConfig `json:"goal_config"`
		}
		body, _ := json.Marshal(record.InitialProvenance)
		_ = json.Unmarshal(body, &provenance)
		if provenance.GoalConfig == nil {
			body, _ = json.Marshal(record.GoalConfig)
			_ = json.Unmarshal(body, &provenance.GoalConfig)
		}
		if provenance.GoalConfig == nil {
			return nil
		}
		rules = provenance.GoalConfig.MonitorRules
	}
	windows := []elastic.TerminalWindow{}
	for _, rule := range rules {
		if rule.SourceID != job.Source.ID || monitorRuleOperation(rule) != "value" {
			continue
		}
		from, until := job.StartedAt.Add(time.Duration(rule.StartOffsetSeconds)*time.Second), *job.LoadEndedAt
		if rule.EndOffsetSeconds != nil {
			candidate := job.StartedAt.Add(time.Duration(*rule.EndOffsetSeconds) * time.Second)
			if candidate.Before(until) {
				until = candidate
			}
		}
		if !until.After(from) {
			continue
		}
		w := elastic.TerminalWindow{Metric: rule.Metric, FromUTC: from, UntilUTC: until}
		found := false
		for _, existing := range windows {
			found = found || existing == w
		}
		if !found {
			windows = append(windows, w)
		}
	}
	return windows
}

func elasticHealthWatermark(job storage.RunMonitorJob) time.Time {
	if job.Source.Elastic == nil {
		return time.Time{}
	}
	var earliest time.Time
	for _, definition := range elastic.QueryDefinitions(*job.Source.Elastic) {
		if definition.Kind != "health" {
			continue
		}
		coverage, ok := job.Coverage[definition.QueryID]
		if !ok || coverage.AsOf == nil {
			return time.Time{}
		}
		if earliest.IsZero() || coverage.AsOf.Before(earliest) {
			earliest = *coverage.AsOf
		}
	}
	return earliest
}

// Health remains an instantaneous observation. Carry its original observation
// time through skipped polls and freeze only a successful settled final reading.
func elasticHealthCoverage(job storage.RunMonitorJob) map[string]elastic.Coverage {
	if job.Source.Elastic == nil {
		return nil
	}
	coverage := map[string]elastic.Coverage{}
	for _, definition := range elastic.QueryDefinitions(*job.Source.Elastic) {
		if definition.Kind == "health" {
			if previous, ok := job.Coverage[definition.QueryID]; ok {
				coverage[definition.QueryID] = previous
			}
		}
	}
	return coverage
}

func elasticFinalHealthAfter(job storage.RunMonitorJob) time.Time {
	if job.State != "reconciling" || job.LoadEndedAt == nil || job.Source.Elastic == nil {
		return time.Time{}
	}
	settle := job.Source.Elastic.Defaults().SettleSeconds
	if settle == 0 {
		settle = 60
	}
	return job.LoadEndedAt.Add(time.Duration(settle) * time.Second)
}

func elasticIngestionFrom(job storage.RunMonitorJob) time.Time {
	if job.State == "reconciling" || job.Source.Elastic == nil || job.Source.Elastic.IngestedField == "" || job.ElasticIngestionWatermark.IsZero() {
		return time.Time{}
	}
	return job.ElasticIngestionWatermark.Add(-time.Duration(job.Source.Elastic.Defaults().OverlapSeconds) * time.Second)
}

func monitorRuleOperation(rule analysis.MonitorRule) string {
	if rule.EvaluationOperation != "" {
		return rule.EvaluationOperation
	}
	return rule.Statistic
}
