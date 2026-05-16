package main

import (
	"testing"
	"time"

	"github.com/jasonczech/stormrunner/pkg/api"
	"github.com/jasonczech/stormrunner/pkg/dsl"
	"github.com/jasonczech/stormrunner/pkg/engine"
)

func TestParseRunArgs_RuntimeOverrides(t *testing.T) {
	args := parseRunArgs([]string{
		"--script", "example.json",
		"-env", "target_namespace=pr-123",
		"--env", "api_host=preview.example.com",
		"--base-url", "https://preview.example.com",
	})

	if args.script != "example.json" {
		t.Fatalf("script = %q, want example.json", args.script)
	}
	if args.baseURL != "https://preview.example.com" {
		t.Fatalf("baseURL = %q, want https://preview.example.com", args.baseURL)
	}
	if args.env["target_namespace"] != "pr-123" {
		t.Fatalf("target_namespace = %q, want pr-123", args.env["target_namespace"])
	}
	if args.env["api_host"] != "preview.example.com" {
		t.Fatalf("api_host = %q, want preview.example.com", args.env["api_host"])
	}
}

func TestParseRunArgs_SLAFlags(t *testing.T) {
	args := parseRunArgs([]string{
		"--script", "example.json",
		"--p95-latency-ms", "800",
		"--max-error-pct", "2.5",
		"--max-avg-server-processing-ms", "300",
		"--max-avg-transfer-ms", "120",
		"--max-error-count", "5",
	})

	if args.p95LatencyMs == nil || *args.p95LatencyMs != 800 {
		t.Fatalf("p95LatencyMs = %#v, want 800", args.p95LatencyMs)
	}
	if args.maxErrorPct == nil || *args.maxErrorPct != 2.5 {
		t.Fatalf("maxErrorPct = %#v, want 2.5", args.maxErrorPct)
	}
	if args.maxAvgServerProcessingMs == nil || *args.maxAvgServerProcessingMs != 300 {
		t.Fatalf("maxAvgServerProcessingMs = %#v, want 300", args.maxAvgServerProcessingMs)
	}
	if args.maxAvgTransferMs == nil || *args.maxAvgTransferMs != 120 {
		t.Fatalf("maxAvgTransferMs = %#v, want 120", args.maxAvgTransferMs)
	}
	if args.maxErrorCount == nil || *args.maxErrorCount != 5 {
		t.Fatalf("maxErrorCount = %#v, want 5", args.maxErrorCount)
	}

	goalConfig := buildGoalConfigFromArgs(args)
	if goalConfig == nil || goalConfig.MaxErrorCount == nil || *goalConfig.MaxErrorCount != 5 {
		t.Fatalf("goalConfig = %#v, want populated thresholds", goalConfig)
	}
}

func TestApplyRuntimeOverrides_ReplacesEnvPlaceholdersAndBaseURL(t *testing.T) {
	script := &dsl.Script{
		Name:     "preview",
		Protocol: "http",
		Config: dsl.ScriptConfig{
			BaseURL: "https://{{env.api_host}}",
			HTTP: dsl.HTTPConfig{
				DefaultHeaders: map[string]string{"X-Namespace": "{{env.target_namespace}}"},
			},
		},
		Actions: []dsl.Action{{
			Name:   "open",
			Type:   "request",
			Target: "/namespaces/{{env.target_namespace}}/health",
			Body:   `{"host":"{{env.api_host}}"}`,
		}},
	}

	overridden, err := applyRuntimeOverrides(script, runArgs{
		baseURL: "https://override.example.com",
		env: map[string]string{
			"target_namespace": "pr-123",
			"api_host":         "preview.example.com",
		},
	})
	if err != nil {
		t.Fatalf("applyRuntimeOverrides returned error: %v", err)
	}

	if overridden.Config.BaseURL != "https://override.example.com" {
		t.Fatalf("BaseURL = %q, want https://override.example.com", overridden.Config.BaseURL)
	}
	if overridden.Actions[0].Target != "/namespaces/pr-123/health" {
		t.Fatalf("Target = %q, want /namespaces/pr-123/health", overridden.Actions[0].Target)
	}
	if overridden.Actions[0].Body != `{"host":"preview.example.com"}` {
		t.Fatalf("Body = %q, want host replacement", overridden.Actions[0].Body)
	}
	if overridden.Config.HTTP.DefaultHeaders["X-Namespace"] != "pr-123" {
		t.Fatalf("X-Namespace = %q, want pr-123", overridden.Config.HTTP.DefaultHeaders["X-Namespace"])
	}
	if script.Actions[0].Target != "/namespaces/{{env.target_namespace}}/health" {
		t.Fatalf("original Target mutated to %q", script.Actions[0].Target)
	}
}

func TestParseEnvAssignment(t *testing.T) {
	key, value, ok := parseEnvAssignment("target_namespace=pr-123")
	if !ok {
		t.Fatal("parseEnvAssignment returned ok=false, want true")
	}
	if key != "target_namespace" || value != "pr-123" {
		t.Fatalf("parseEnvAssignment = (%q, %q), want (target_namespace, pr-123)", key, value)
	}
	if _, _, ok := parseEnvAssignment("missing-separator"); ok {
		t.Fatal("parseEnvAssignment accepted invalid input")
	}
}

func TestEvaluateLocalSLAReportsBreaches(t *testing.T) {
	maxErrorPct := 2.0
	maxServerProcessingMs := 300.0
	maxTransferMs := 120.0
	maxErrorCount := 3
	p95LatencyMs := 800.0

	summary := evaluateLocalSLA(&engine.RunResult{
		TotalTransactions: 100,
		TotalErrors:       5,
		FinalSnapshot: engine.MetricsSnapshot{
			P95: 950 * time.Millisecond,
			ByAction: map[string]*engine.ActionMetrics{
				"Checkout": {
					ServerTelemetry: &engine.ServerTelemetrySummary{
						AvgServerProcessingMs: 320,
						AvgTransferMs:         130,
					},
				},
			},
		},
	}, &api.GoalConfig{
		P95LatencyMs:             &p95LatencyMs,
		MaxErrorPct:              &maxErrorPct,
		MaxAvgServerProcessingMs: &maxServerProcessingMs,
		MaxAvgTransferMs:         &maxTransferMs,
		MaxErrorCount:            &maxErrorCount,
	})

	if summary == nil || !summary.Evaluated {
		t.Fatalf("summary = %#v, want evaluated SLA summary", summary)
	}
	if !summary.Breached {
		t.Fatalf("summary = %#v, want SLA breach", summary)
	}
	if len(summary.Details) != 5 {
		t.Fatalf("details = %#v, want 5 breach details", summary.Details)
	}
}
