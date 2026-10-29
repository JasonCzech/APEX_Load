package api

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/jasonczech/apexload/pkg/appdynamics"
	"github.com/jasonczech/apexload/pkg/elastic"
	"github.com/jasonczech/apexload/pkg/storage"
)

func validateSelectedMonitorRules(bindings *storage.MonitorBindings, sources []storage.MonitorSource, goals *GoalConfig) error {
	if goals == nil || len(goals.MonitorRules) == 0 {
		return nil
	}
	selected := map[string]storage.MonitorSource{}
	for _, b := range bindings.Sources {
		for _, s := range sources {
			if s.ID == b.SourceID && s.Enabled && ((s.Pack == "appdynamics" && s.AppDynamics != nil) || (s.Pack == "elastic" && s.Elastic != nil)) {
				selected[s.ID] = s
			}
		}
	}
	for _, rule := range goals.MonitorRules {
		if err := rule.Validate(); err != nil {
			return err
		}
		source, ok := selected[rule.SourceID]
		if !ok {
			return errors.New("monitoring rule must select an enabled provider source attached to this test")
		}
		found := false
		if source.Elastic != nil {
			if rule.Statistic == "value" {
				for _, query := range source.Elastic.Queries {
					if query.Name == rule.Metric && query.Language == "esql" && !query.ExactWindow {
						return errors.New("value rules require an authored exact-window ES|QL aggregate")
					}
				}
			}
			for _, definition := range elastic.QueryMetricDefinitions(*source.Elastic) {
				found = found || definition.Name == rule.Metric && (definition.Unit == rule.Unit || definition.Unit == "" && rule.Unit == "count")
			}

			if !found {
				return errors.New("monitoring rule metric and unit must match an approved numeric selection")
			}
			continue
		}
		for _, metric := range source.AppDynamics.Metrics {
			found = found || metric.Name == rule.Metric && appDynamicsExpectedUnit(metric, metric.Path) == rule.Unit
		}
		for _, query := range source.AppDynamics.Analytics {
			unit := query.Unit
			if unit == "" {
				unit = "count"
			}
			found = found || query.Kind == "metric" && query.Name == rule.Metric && unit == rule.Unit
		}
		if !found {
			return errors.New("monitoring rule metric and unit must match an approved numeric selection")
		}
	}
	return nil
}

func (tr *TestRunner) validateAppDynamicsCollectionBudgets(bindings *storage.MonitorBindings, sources []storage.MonitorSource) error {
	connections, ok := tr.testStore.(storage.AppDynamicsConnectionStore)
	if !ok {
		return nil
	}
	type accountBudget struct {
		queries, analytics, settle, rpm, analyticsRPM int
		required                                      bool
	}
	budgets := map[string]*accountBudget{}
	for _, binding := range bindings.Sources {
		for _, source := range sources {
			if source.ID != binding.SourceID || !source.Enabled || source.Pack != "appdynamics" || source.AppDynamics == nil {
				continue
			}
			connection, err := connections.GetAppDynamicsConnection(source.ConnectionID)
			if err != nil {
				if binding.Required {
					return errors.New("required AppDynamics connection unavailable")
				}
				continue
			}
			cfg := connection.Config.Defaults()
			key := appdynamics.ControllerQuotaKey(cfg)
			budget := budgets[key]
			if budget == nil {
				budget = &accountBudget{rpm: max(1, cfg.RequestsPerMinute/2), analyticsRPM: max(1, cfg.AnalyticsRequestsPerMinute/2)}
				budgets[key] = budget
			}
			budget.rpm = min(budget.rpm, max(1, cfg.RequestsPerMinute/2))
			budget.analyticsRPM = min(budget.analyticsRPM, max(1, cfg.AnalyticsRequestsPerMinute/2))
			budget.required = budget.required || binding.Required
			selection := source.AppDynamics
			// Reserve authentication and bounded topology lookups as well as the
			// configured queries. Saturated diagnostic windows can cost more;
			// their incomplete coverage is reported at collection time.
			budget.queries += 2 + 2*len(selection.Applications) + len(selection.Metrics)
			for _, enabled := range []bool{selection.Events, selection.Violations, selection.Snapshots} {
				if enabled {
					budget.queries += len(selection.Applications)
				}
			}
			budget.analytics += len(selection.Analytics)
			for _, query := range selection.Analytics {
				if query.ApplicationName != "" {
					budget.queries++ // Verify the named application against its selected numeric ID.
				}
			}
			settle := selection.SettleSeconds
			if settle == 0 {
				settle = 120
			}
			budget.settle = max(budget.settle, settle)
		}
	}
	for _, b := range budgets {
		pollSeconds := max((60*b.queries+b.rpm-1)/b.rpm, (60*b.analytics+b.analyticsRPM-1)/b.analyticsRPM)
		if b.required && b.settle+2*max(30, pollSeconds)+30 > 300 {
			return fmt.Errorf("required AppDynamics selection exceeds the five-minute reconciliation budget; reduce queries or settling allowance")
		}
	}
	return nil
}

func (tr *TestRunner) validateAppDynamicsSharedArtifacts(sources []storage.MonitorSource, bindings *storage.MonitorBindings) error {
	if tr.leadership == nil || currentArchiveBackend().Backend != archiveBackendFilesystem {
		return nil
	}
	for _, binding := range bindings.Sources {
		for _, source := range sources {
			if source.ID == binding.SourceID && source.Enabled && canonicalProviderPack(source.Pack) && !strings.EqualFold(os.Getenv("APEXLOAD_MONITOR_SHARED_FILESYSTEM"), "true") {
				return errors.New("Provider monitoring in HA requires shared monitoring artifacts; configure object storage or attest the shared filesystem with APEXLOAD_MONITOR_SHARED_FILESYSTEM=true")
			}
		}
	}
	return nil
}
