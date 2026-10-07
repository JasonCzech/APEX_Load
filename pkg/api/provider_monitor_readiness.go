package api

import (
	"errors"

	"os"
	"strings"

	"github.com/jasonczech/apexload/pkg/dynatrace"
	"github.com/jasonczech/apexload/pkg/elastic"
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
		if source.Elastic != nil {
			if monitorRuleOperation(rule) == "value" {
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

func (tr *TestRunner) validateProviderSharedArtifacts(sources []storage.MonitorSource, bindings *storage.MonitorBindings) error {
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
