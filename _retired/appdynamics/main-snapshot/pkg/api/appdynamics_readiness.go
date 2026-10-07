package api

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/jasonczech/apexload/pkg/appdynamics"
	"github.com/jasonczech/apexload/pkg/dynatrace"
	"github.com/jasonczech/apexload/pkg/monitorprovider"
	"github.com/jasonczech/apexload/pkg/storage"
)

func (tr *TestRunner) validateSelectedMonitorRules(bindings *storage.MonitorBindings, sources []storage.MonitorSource, goals *GoalConfig) error {
	if goals == nil || len(goals.MonitorRules) == 0 {
		return nil
	}
	if bindings == nil {
		return errors.New("monitoring rules require selected sources")
	}
	selected := map[string]storage.MonitorSource{}
	for _, b := range bindings.Sources {
		for _, s := range sources {
			if s.ID == b.SourceID && s.Enabled && s.CanonicalProvider() {
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
			return errors.New("monitoring rule must select an enabled approved source attached to this test")
		}
		if source.Pack == monitorprovider.PackPrometheusQuery {
			connections, available := tr.testStore.(storage.PrometheusConnectionStore)
			if !available {
				return errors.New("Prometheus connection storage unavailable")
			}
			connection, err := connections.GetPrometheusConnection(source.ConnectionID)
			if err != nil || connection.ProjectID != source.ProjectID {
				return errors.New("monitoring rule Prometheus connection unavailable")
			}
			profile := connection.Config.Defaults().BackendProfile
			if err := validatePrometheusRuleSelection(rule, source, profile); err != nil {
				return err
			}
			continue
		}
		found := false
		if source.AppDynamics != nil {
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
		}
		if source.Dynatrace != nil {
			windowAggregate := false
			for _, query := range source.Dynatrace.Grail {
				windowAggregate = windowAggregate || query.Kind == "metric" && query.BucketSeconds == 0 && query.Name == rule.Metric && rule.MetricID == dynatrace.GrailMetricID(query)
			}
			if rule.Provider != monitorprovider.ProviderDynatrace || rule.MetricID == "" || rule.SourceStatistic == "" || rule.ResolutionSeconds <= 0 && !windowAggregate || rule.Backend != "classic" && rule.Backend != "grail" {
				return errors.New("Dynatrace rules require an exact provider, backend, metric identity, source statistic and resolution")
			}
			for _, metric := range source.Dynatrace.Metrics {
				// Classic descriptor units may be discovered at preparation time.
				// The rule still declares an exact unit, which actual observations
				// must match before they become eligible for evaluation.
				unitMatches := metric.Unit == "" || dynatrace.NormalizeUnit(metric.Unit) == rule.Unit
				found = found || metric.Name == rule.Metric && unitMatches && rule.Backend == "classic" && rule.MetricID == dynatrace.MetricID(metric) && rule.SourceStatistic == dynatrace.MetricStatistic(metric)
			}
			for _, query := range source.Dynatrace.Grail {
				found = found || query.Kind == "metric" && query.Name == rule.Metric && dynatrace.NormalizeUnit(query.Unit) == rule.Unit && rule.Backend == "grail" && rule.MetricID == dynatrace.GrailMetricID(query) && rule.SourceStatistic == query.Statistic
			}
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
			if source.ID != binding.SourceID || !source.Enabled || source.Pack != monitorprovider.PackAppDynamics || source.AppDynamics == nil {
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
			if source.ID == binding.SourceID && source.Enabled && source.CanonicalProvider() && !strings.EqualFold(os.Getenv("APEXLOAD_MONITOR_SHARED_FILESYSTEM"), "true") {
				return errors.New("Provider monitoring in HA requires shared monitoring artifacts; configure object storage or attest the shared filesystem with APEXLOAD_MONITOR_SHARED_FILESYSTEM=true")
			}
		}
	}
	return nil
}
