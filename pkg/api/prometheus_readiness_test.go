package api

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/prometheus"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestPrometheusRuleReadinessRequiresUnambiguousQuery(t *testing.T) {
	query := prometheus.Query{ID: "first", Name: "cpu", Unit: "percent", Semantics: "gauge", Expression: "vector(0)"}
	duplicate := query
	duplicate.ID = "second"
	source := storage.MonitorSource{Prometheus: &prometheus.Selection{Queries: []prometheus.Query{query, duplicate}}}
	rule := analysis.MonitorRule{Metric: "cpu", Unit: "percent", Statistic: "avg"}
	if err := validatePrometheusRuleSelection(rule, source); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatal("first matching definition selected silently", err)
	}
	// Identity ambiguity is checked before validating a particular candidate's
	// optional capabilities, so list ordering cannot choose the error or query.
	rule.RequireFreshness = true
	if err := validatePrometheusRuleSelection(rule, source); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatal("candidate capability validation preceded ambiguity", err)
	}
	rule.RequireFreshness = false
	rule.QueryID = "second"
	if err := validatePrometheusRuleSelection(rule, source); err != nil {
		t.Fatal("explicit query identity rejected", err)
	}
	rule.QueryID = "unknown"
	if err := validatePrometheusRuleSelection(rule, source); err == nil {
		t.Fatal("unknown query selected")
	}
	rule.QueryID = ""
	source.Prometheus.Queries[1].Unit = "bytes"
	if err := validatePrometheusRuleSelection(rule, source); err != nil {
		t.Fatal("distinct unit was treated as an identity collision", err)
	}
}

func TestPrometheusRuleReadinessUsesBackendLabelCase(t *testing.T) {
	for _, tc := range []struct {
		name, profile, label string
		wantError            bool
	}{
		{"upstream exact", prometheus.BackendPrometheus, "job", false},
		{"upstream different case", prometheus.BackendPrometheus, "JOB", true},
		{"azure different case", prometheus.BackendAzureManaged, "JOB", false},
		{"azure outside scope", prometheus.BackendAzureManaged, "region", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, db := newTimeslotServer(t)
			config := prometheus.Config{Endpoint: "https://prom.example", BackendProfile: tc.profile}
			if tc.profile == prometheus.BackendAzureManaged {
				config.Endpoint = "https://workspace.eastus.prometheus.monitor.azure.com"
				config.AuthType = prometheus.AuthAzure
				config.AzureCredentialMode = "managed_identity"
			}
			connection := &storage.PrometheusConnection{ID: "rule-connection", ProjectID: "proj-a", Name: "Rules", Config: config}
			if err := db.(storage.PrometheusConnectionStore).CreatePrometheusConnection(connection); err != nil {
				t.Fatal(err)
			}
			source := storage.MonitorSource{ID: "s", ProjectID: "proj-a", Enabled: true, Pack: "prometheus-query", ConnectionID: connection.ID,
				Prometheus: &prometheus.Selection{Queries: []prometheus.Query{{ID: "cpu", Name: "cpu", Unit: "percent", Semantics: "gauge", Expression: "vector(0)", AllowedLabels: []string{"job"}}}}}
			rule := analysis.MonitorRule{SourceID: source.ID, QueryID: "cpu", Metric: "cpu", Unit: "percent", Statistic: "avg", Comparator: "lte", Threshold: 80, Dimensions: map[string]string{tc.label: "sut"}}
			bindings := &storage.MonitorBindings{Sources: []storage.MonitorBinding{{SourceID: source.ID}}}
			err := server.runner.validateSelectedMonitorRules(bindings, []storage.MonitorSource{source}, &GoalConfig{MonitorRules: []analysis.MonitorRule{rule}})
			if (err != nil) != tc.wantError {
				t.Fatalf("validation = %v; wantError %v", err, tc.wantError)
			}
		})
	}
}
