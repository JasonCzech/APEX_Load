package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestParsePrometheusMetricValueSelectsLabeledSample(t *testing.T) {
	body := `
# HELP app_queue_depth Queue depth
app_queue_depth{queue="orders",region="east"} 42
app_queue_depth{queue="returns",region="east"} 7
target_cpu_pct 81.5
`
	value, ok, err := parsePrometheusMetricValue(strings.NewReader(body), "app_queue_depth", map[string]string{"queue": "orders"})
	if err != nil {
		t.Fatalf("parsePrometheusMetricValue returned error: %v", err)
	}
	if !ok || value != 42 {
		t.Fatalf("metric value = (%v, %v), want 42 true", value, ok)
	}
}

func TestToParquetResourcesIncludesExternalMetricsJSON(t *testing.T) {
	rows := toParquetResources([]ResourceSample{{
		External: map[string]float64{"queue.orders_depth": 42},
	}})
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	var external map[string]float64
	if err := json.Unmarshal([]byte(rows[0].ExternalJSON), &external); err != nil {
		t.Fatalf("external metrics json = %q: %v", rows[0].ExternalJSON, err)
	}
	if external["queue.orders_depth"] != 42 {
		t.Fatalf("external metrics = %#v, want queue depth", external)
	}
}

func TestExternalMetricCollectorCollectsConfiguredPrometheusMetrics(t *testing.T) {
	// httptest binds to loopback; allow private outbound for the SSRF guard.
	t.Setenv("APEXLOAD_ALLOW_PRIVATE_WEBHOOKS", "true")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`target_cpu_pct 81.5
app_queue_depth{queue="orders"} 42
`))
	}))
	defer server.Close()

	testRun := &ManagedTest{
		Groups: []TestGroup{{
			script: &dsl.Script{
				Config: dsl.ScriptConfig{
					Observability: dsl.ObservabilityConfig{
						ExternalMetrics: []dsl.ExternalMetricConfig{{
							Name:       "target.cpu_pct",
							URL:        server.URL,
							MetricName: "target_cpu_pct",
						}, {
							Name:       "queue.orders_depth",
							URL:        server.URL,
							MetricName: "app_queue_depth",
							Labels:     map[string]string{"queue": "orders"},
						}},
					},
				},
			},
		}},
	}

	values := newExternalMetricCollector(server.Client()).collect(context.Background(), externalMetricTargetsForTest(testRun))
	if values["target.cpu_pct"] != 81.5 {
		t.Fatalf("target.cpu_pct = %v, want 81.5", values["target.cpu_pct"])
	}
	if values["queue.orders_depth"] != 42 {
		t.Fatalf("queue.orders_depth = %v, want 42", values["queue.orders_depth"])
	}
}

func TestExternalMetricTargetsDeduplicateOracleMetricPackEntries(t *testing.T) {
	metric := dsl.ExternalMetricConfig{
		Name:       "oracle.db.wait_time_ms",
		URL:        "https://metrics.example.test/metrics",
		Format:     "prometheus",
		MetricName: "oracle_db_wait_time_ms",
		Labels:     map[string]string{"service_name": "checkout"},
	}
	testRun := &ManagedTest{
		Groups: []TestGroup{{
			script: &dsl.Script{Config: dsl.ScriptConfig{Observability: dsl.ObservabilityConfig{ExternalMetrics: []dsl.ExternalMetricConfig{metric}}}},
		}, {
			script: &dsl.Script{Config: dsl.ScriptConfig{Observability: dsl.ObservabilityConfig{ExternalMetrics: []dsl.ExternalMetricConfig{metric}}}},
		}},
	}
	targets := externalMetricTargetsForTest(testRun)
	if len(targets) != 1 {
		t.Fatalf("targets len = %d, want deduplicated Oracle metric target", len(targets))
	}
	if targets[0].Name != "oracle.db.wait_time_ms" || targets[0].MetricName != "oracle_db_wait_time_ms" {
		t.Fatalf("target = %#v, want canonical Oracle wait metric", targets[0])
	}
}

func TestAttachExternalMetricsCreatesResourceSnapshot(t *testing.T) {
	resources := attachExternalMetrics(nil, map[string]float64{"target.cpu_pct": 81.5})
	if resources == nil || resources.External["target.cpu_pct"] != 81.5 {
		t.Fatalf("resources = %#v, want external metric", resources)
	}
}

func TestRunArtifactsEnrichLiveMetricsAttachesExternalResources(t *testing.T) {
	// httptest binds to loopback; allow private outbound for the SSRF guard.
	t.Setenv("APEXLOAD_ALLOW_PRIVATE_WEBHOOKS", "true")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("target_mem_mb 512\n"))
	}))
	defer server.Close()

	artifacts := &runArtifacts{externalMetrics: newExternalMetricCollector(server.Client())}
	live := &LiveMetrics{}
	artifacts.enrichLiveMetrics(&ManagedTest{
		Groups: []TestGroup{{
			script: &dsl.Script{
				Config: dsl.ScriptConfig{
					Observability: dsl.ObservabilityConfig{
						ExternalMetrics: []dsl.ExternalMetricConfig{{
							Name:       "target.mem_mb",
							URL:        server.URL,
							MetricName: "target_mem_mb",
						}},
					},
				},
			},
		}},
	}, live)

	if live.Resources == nil || live.Resources.External["target.mem_mb"] != 512 {
		t.Fatalf("live resources = %#v, want external target metric", live.Resources)
	}
}
