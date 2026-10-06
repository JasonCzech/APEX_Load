package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	yaml "gopkg.in/yaml.v3"
)

func TestTelemetryPolicyOpenAPIRequiredTypedFields(t *testing.T) {
	raw, err := GenerateOpenAPIYAML("server.go")
	if err != nil {
		t.Fatal(err)
	}
	var doc oasDoc
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path string
	}{
		{"/api/v1/tests/{id}/metrics"},
		{"/api/v1/tests/{id}/manifest"},
		{"/api/v1/tests/{id}/archive/report"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			schema := doc.Paths[tc.path]["get"].Responses["200"].Content["application/json"].Schema
			if tc.path == "/api/v1/tests/{id}/metrics" {
				if containsOpenAPIField(schema.Required, "metrics") {
					t.Fatal("metrics must be absent before first publication")
				}
				if history := schema.Properties["history"]; history.Type != "array" || !history.Nullable {
					t.Fatal("metrics history must admit the existing null empty-history wire value")
				}
				schema = schema.Properties["metrics"]
			}
			if containsOpenAPIField(schema.Required, "telemetry_policy") {
				t.Fatal("legacy outer policy must remain optional")
			}
			evidence := schema.Properties["telemetry_policy"]
			if !evidence.Nullable || evidence.Properties["availability"].Type != "string" || !containsOpenAPIField(evidence.Required, "availability") {
				t.Fatalf("availability is not required typed: %#v", evidence)
			}
			policy := evidence.Properties["policy"]
			if !policy.Nullable || containsOpenAPIField(evidence.Required, "policy") {
				t.Fatal("unavailable receipt must permit absent policy")
			}
			for _, name := range []string{"schema_version", "policy_id", "revision", "hash", "captured_at", "capture_boundary", "classification_algorithm", "expected_controller_publication_interval_ms", "cadence_scope", "sustained_breach_window_ms", "coverage_rule", "budgets"} {
				if !containsOpenAPIField(policy.Required, name) || policy.Properties[name].Type == "" {
					t.Fatalf("missing required policy field %s", name)
				}
			}
			budgets := policy.Properties["budgets"]
			if budgets.Type != "array" || budgets.Items == nil {
				t.Fatal("missing typed budgets")
			}
			for _, field := range []struct{ name, kind string }{{"stage", "string"}, {"enabled", "boolean"}, {"limit_ms", "number"}} {
				if budgets.Items.Properties[field.name].Type != field.kind || !containsOpenAPIField(budgets.Items.Required, field.name) {
					t.Fatalf("required false/zero budget field opaque: %s %#v", field.name, budgets.Items)
				}
			}
			if policy.Properties["captured_at"].Format != "date-time" {
				t.Fatal("capture timestamp lost format")
			}
		})
	}
}

func TestTelemetryPolicyMetricsContractPreservesActualNullEmptyHistory(t *testing.T) {
	server := NewServer(ServerConfig{})
	var fixture *ManagedTest
	t.Cleanup(func() {
		// This wire fixture owns a published-state row, not an execution backend.
		// Retire only that exact synthetic handle before real server shutdown.
		server.runner.mu.Lock()
		if server.runner.tests["telemetry-empty-history"] == fixture {
			delete(server.runner.tests, "telemetry-empty-history")
		}
		server.runner.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	base := time.Date(2026, 10, 5, 11, 0, 0, 0, time.UTC)
	server.runner.mu.Lock()
	fixture = &ManagedTest{ID: "telemetry-empty-history", Status: "RUNNING", LiveMetrics: &LiveMetrics{Timestamp: base, TPS: 1}, History: []*LiveMetrics{{Timestamp: base, TPS: 1}}}
	server.runner.tests["telemetry-empty-history"] = fixture
	server.runner.mu.Unlock()
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/tests/telemetry-empty-history/metrics?since="+url.QueryEscape(base.Add(time.Hour).Format(time.RFC3339Nano)), nil))
	if response.Code != http.StatusOK {
		t.Fatalf("actual metrics handler: %d %s", response.Code, response.Body.String())
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if string(body["history"]) != "null" {
		t.Fatalf("wire empty history=%s, expected existing null", body["history"])
	}
	if len(body["metrics"]) == 0 || string(body["metrics"]) == "null" {
		t.Fatal("empty window lost current live metrics")
	}
}
