package api

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/monitorprovider"
	"github.com/jasonczech/apexload/pkg/prometheus"
	"github.com/jasonczech/apexload/pkg/storage"
)

func validatePrometheusRuleSelection(rule analysis.MonitorRule, source storage.MonitorSource, profiles ...string) error {
	if source.Prometheus == nil {
		return errors.New("Prometheus rule requires approved queries")
	}
	var matches []prometheus.Query
	for _, query := range source.Prometheus.Queries {
		if query.Name != rule.Metric || query.Unit != rule.Unit || rule.QueryID != "" && rule.QueryID != query.ID {
			continue
		}
		matches = append(matches, query)
	}
	if len(matches) == 0 {
		return errors.New("monitoring rule must match an approved query identity and unit")
	}
	if len(matches) != 1 {
		return errors.New("monitoring rule query identity is ambiguous; select a unique query_id")
	}
	query := matches[0]
	if rule.RequireFreshness && query.FreshnessExpression == "" {
		return errors.New("freshness-dependent rule requires an approved freshness expression")
	}
	if rule.EvaluationMode == "exact_window" {
		if query.TerminalExpression == "" {
			return errors.New("exact-window rule requires an approved terminal expression")
		}
		if rule.Statistic == "sum" && query.Semantics != "sum" && query.Semantics != "count" {
			return errors.New("sum requires a declared terminal total")
		}
	} else if rule.Statistic == "sum" || query.Semantics == "counter" {
		return errors.New("rolling evaluations and counter levels cannot be summed; use an approved terminal expression")
	}
	azure := len(profiles) > 0 && profiles[0] == prometheus.BackendAzureManaged
	for label := range rule.Dimensions {
		found := false
		for _, allowed := range query.AllowedLabels {
			found = found || label == allowed || azure && strings.EqualFold(label, allowed)
		}
		if !found {
			return errors.New("rule label is outside approved output scope")
		}
	}
	return nil
}

func (tr *TestRunner) validatePreparedPrometheus(ctx context.Context, source storage.MonitorSource, project string, goals *GoalConfig) error {
	connections, ok := tr.testStore.(storage.PrometheusConnectionStore)
	if !ok {
		return errors.New("Prometheus connection storage unavailable")
	}
	c, err := connections.GetPrometheusConnection(source.ConnectionID)
	if err != nil || c.ProjectID != project || c.Readiness() != "ready" {
		return errors.New("required Prometheus connection is not verified")
	}
	if err = storage.ValidatePrometheusSourceConnection(source, c.Snapshot()); err != nil {
		return err
	}
	if source.Prometheus == nil || !c.Capabilities.Metrics || source.Prometheus.Targets && !c.Capabilities.Targets || source.Prometheus.Alerts && !c.Capabilities.Alerts {
		return errors.New("required Prometheus capability has not been verified")
	}
	if source.ExecutorMode != "direct" {
		inspections, ok := tr.testStore.(storage.PrometheusInspectionStore)
		if !ok {
			return errors.New("Prometheus collector enrollment storage unavailable")
		}
		var key string
		e := tr.trackDurableWrite(func() error {
			var err error
			key, err = inspections.PrometheusCollectorPublicKey(project, source.CollectorID, source.Destination, time.Now().UTC(), c.Snapshot().RequiresCredentials())
			return err
		})
		if e != nil || key != source.CollectorPublicKey {
			return errors.New("Prometheus collector enrollment changed; update the source before preparing")
		}
	}
	if _, err = resolvePrometheusCredentials(ctx, tr.prometheusSecretResolverForProject(project), c.Snapshot()); err != nil {
		return err
	}
	if tr.prometheusPrepare != nil {
		work, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		var rules []analysis.MonitorRule
		if goals != nil {
			for _, rule := range goals.MonitorRules {
				if rule.SourceID == source.ID {
					rules = append(rules, rule)
				}
			}
		}
		return tr.prometheusPrepare(work, source, c.Snapshot(), rules)
	}
	return nil
}

func (tr *TestRunner) validatePrometheusCollectionBudgets(bindings *storage.MonitorBindings, sources []storage.MonitorSource, goals *GoalConfig) error {
	connections, ok := tr.testStore.(storage.PrometheusConnectionStore)
	if !ok {
		return nil
	}
	type budget struct {
		requests, settle, rpm int
		required              bool
	}
	budgets := map[string]*budget{}
	for _, binding := range bindings.Sources {
		for _, source := range sources {
			if source.ID != binding.SourceID || !source.Enabled || source.Pack != monitorprovider.PackPrometheusQuery || source.Prometheus == nil {
				continue
			}
			connection, err := connections.GetPrometheusConnection(source.ConnectionID)
			if err != nil {
				if binding.Required {
					return errors.New("required Prometheus connection unavailable")
				}
				continue
			}
			cfg := connection.Config.Defaults()
			// Secret identity is accounted for privately; equal origin/auth identities
			// share one conservative preparation budget even before credential resolution.
			key := prometheus.QuotaKey(cfg, prometheus.Credentials{})
			b := budgets[key]
			if b == nil {
				b = &budget{rpm: max(1, cfg.RequestsPerMinute/2)}
				budgets[key] = b
			}
			b.required = b.required || binding.Required
			b.rpm = min(b.rpm, max(1, cfg.RequestsPerMinute/2))
			b.settle = max(b.settle, source.Prometheus.Defaults().SettleSeconds)
			for _, query := range source.Prometheus.Queries {
				b.requests++
				if query.FreshnessExpression != "" {
					b.requests++
				}
			}
			start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			end := start.Add(24 * time.Hour)
			job := storage.RunMonitorJob{Source: source, StartedAt: start, LoadEndedAt: &end}
			if goals != nil {
				for _, rule := range goals.MonitorRules {
					if rule.SourceID == source.ID {
						job.FrozenMonitorRules = append(job.FrozenMonitorRules, rule)
					}
				}
			}
			for _, window := range prometheusTerminalWindows(job) {
				b.requests++
				for _, query := range source.Prometheus.Queries {
					if query.ID == window.QueryID && query.FreshnessExpression != "" {
						b.requests++
					}
				}
			}
			if source.Prometheus.Targets {
				b.requests++
			}
			if source.Prometheus.Alerts {
				b.requests++
			}
		}
	}
	for _, b := range budgets {
		seconds := (60*b.requests + b.rpm - 1) / b.rpm
		if b.required && b.settle+2*max(30, seconds)+15 > 300 {
			return fmt.Errorf("required Prometheus selection exceeds the five-minute reconciliation budget; reduce queries or settling allowance")
		}
	}
	return nil
}
