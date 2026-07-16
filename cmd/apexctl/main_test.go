package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/api"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestApexctlBundlesExecutableProtocols(t *testing.T) {
	want := []string{
		"browser",
		"database",
		"eventhubs",
		"grpc",
		"http",
		"java_http",
		"java_vuser",
		"jms-ibmmq",
		"jms-servicebus",
		"jms-solace",
		"kafka",
		"network",
		"soap",
		"websocket",
	}

	for _, name := range want {
		t.Run(name, func(t *testing.T) {
			p, err := protocol.Get(name)
			if err != nil {
				t.Fatalf("protocol.Get(%q) returned error: %v", name, err)
			}
			if p.Name() != name {
				t.Fatalf("protocol name = %q, want %q", p.Name(), name)
			}
		})
	}
}

func TestParseRunArgs_RuntimeOverrides(t *testing.T) {
	args := parseRunArgs([]string{
		"--script", "example.json",
		"--api", "http://localhost:8080",
		"--api-key", "key-123",
		"--bearer-token", "token-456",
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
	if args.api != "http://localhost:8080" || args.apiKey != "key-123" || args.bearerToken != "token-456" {
		t.Fatalf("api auth args = %#v", args)
	}
	if args.env["target_namespace"] != "pr-123" {
		t.Fatalf("target_namespace = %q, want pr-123", args.env["target_namespace"])
	}
	if args.env["api_host"] != "preview.example.com" {
		t.Fatalf("api_host = %q, want preview.example.com", args.env["api_host"])
	}
}

func TestCommandArgsUseCredentialEnvironmentDefaults(t *testing.T) {
	t.Setenv("APEXLOAD_API", "https://controller.example")
	t.Setenv("APEXLOAD_API_KEY", "env-key")
	t.Setenv("APEXLOAD_BEARER_TOKEN", "env-token")

	run := parseRunArgs([]string{"--script", "example.json"})
	if run.api != "https://controller.example" || run.apiKey != "env-key" || run.bearerToken != "env-token" {
		t.Fatalf("run auth defaults = %#v", run)
	}
	ctl := parseCtlArgs([]string{"--test", "test-1"})
	if ctl.api != "https://controller.example" || ctl.apiKey != "env-key" || ctl.bearerToken != "env-token" {
		t.Fatalf("control auth defaults = %#v", ctl)
	}
	github := parseGithubArgs([]string{"diagnostics"})
	if github.apiKey != "env-key" || github.bearerToken != "env-token" {
		t.Fatalf("github auth defaults = %#v", github)
	}
	record := parseRecordArgs([]string{"--url", "https://example.com"})
	if record.apiKey != "env-key" || record.bearerToken != "env-token" {
		t.Fatalf("record auth defaults = %#v", record)
	}
}

func TestParseScriptArgs(t *testing.T) {
	args := parseScriptArgs([]string{"validate", "--script", "examples/web-api/ecommerce-http.json", "--format", "json"})
	if args.command != "validate" || args.script != "examples/web-api/ecommerce-http.json" || args.format != "json" {
		t.Fatalf("args = %#v", args)
	}
}

func TestParseGithubArgs(t *testing.T) {
	t.Setenv("APEXLOAD_API", "http://controller.local")
	args := parseGithubArgs([]string{
		"sync",
		"--project", "project-a",
		"--kind", "push",
		"--owner", "octo",
		"--repo", "scripts",
		"--branch", "main",
		"--path", "apexload/scripts",
		"--strategy", "pr",
		"--api-key", "gh-key",
		"--bearer-token", "gh-token",
	})

	if args.command != "sync" || args.api != "http://controller.local" || args.projectID != "project-a" {
		t.Fatalf("args = %#v", args)
	}
	if args.kind != "push" || args.owner != "octo" || args.repo != "scripts" || args.strategy != "pr" {
		t.Fatalf("github target args = %#v", args)
	}
	if args.apiKey != "gh-key" || args.bearerToken != "gh-token" {
		t.Fatalf("github auth args = %#v", args)
	}
}

func TestValidateScriptFileAcceptsPlainScriptJSON(t *testing.T) {
	path := writeTempScript(t, `{
		"name": "plain",
		"protocol": "http",
		"config": {"base_url": "https://example.com"},
		"actions": [{"name": "home", "type": "request", "method": "GET", "target": "/"}]
	}`)
	result := validateScriptFile(path)
	if !result.Valid || result.Script != "plain" || result.Protocol != "http" || len(result.Issues) != 0 {
		t.Fatalf("validation result = %#v", result)
	}
}

func TestValidateScriptFileReportsIssues(t *testing.T) {
	path := writeTempScript(t, `{"name": "", "protocol": "http", "actions": []}`)
	result := validateScriptFile(path)
	if result.Valid || len(result.Issues) == 0 {
		t.Fatalf("validation result = %#v, want invalid issues", result)
	}
}

func TestExplainScriptFileSummarizesAuthoringSignals(t *testing.T) {
	path := writeTempScript(t, `{
		"name": "explain",
		"protocol": "http",
		"generated_by": "unit-test",
		"config": {"base_url": "https://example.com"},
		"actions": [{
			"name": "create",
			"type": "request",
			"method": "POST",
			"target": "/orders",
			"extractors": [{"name": "id", "source": "body", "type": "json_path", "expression": "$.id"}],
			"assertions": [{"type": "status_code", "expected": 201}]
		}]
	}`)
	result, err := explainScriptFile(path)
	if err != nil {
		t.Fatalf("explainScriptFile returned error: %v", err)
	}
	if result.Actions != 1 || result.Assertions != 1 || result.Extractors != 1 || result.GeneratedBy != "unit-test" {
		t.Fatalf("explain result = %#v", result)
	}
}

func writeTempScript(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "script.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write script: %v", err)
	}
	return path
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

func TestResolveGoalConfig_UsesScriptDefaultsAndCLIOverrides(t *testing.T) {
	scriptP95 := 900.0
	scriptErrorPct := 1.5
	cliErrorPct := 2.5
	maxErrorCount := 3

	goalConfig := resolveGoalConfig(&dsl.Script{
		GoalConfig: &dsl.GoalConfig{
			P95LatencyMs:    &scriptP95,
			MaxErrorPct:     &scriptErrorPct,
			MaxErrorCount:   &maxErrorCount,
			StopOnMaxErrors: true,
		},
	}, runArgs{
		maxErrorPct: &cliErrorPct,
	})

	if goalConfig == nil {
		t.Fatal("goalConfig = nil, want merged config")
	}
	if goalConfig.P95LatencyMs == nil || *goalConfig.P95LatencyMs != scriptP95 {
		t.Fatalf("P95LatencyMs = %#v, want %v", goalConfig.P95LatencyMs, scriptP95)
	}
	if goalConfig.MaxErrorPct == nil || *goalConfig.MaxErrorPct != cliErrorPct {
		t.Fatalf("MaxErrorPct = %#v, want CLI override %v", goalConfig.MaxErrorPct, cliErrorPct)
	}
	if goalConfig.MaxErrorCount == nil || *goalConfig.MaxErrorCount != maxErrorCount {
		t.Fatalf("MaxErrorCount = %#v, want script value %d", goalConfig.MaxErrorCount, maxErrorCount)
	}
	if !goalConfig.StopOnMaxErrors {
		t.Fatalf("StopOnMaxErrors = %v, want true", goalConfig.StopOnMaxErrors)
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
						Samples:               1,
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

func TestEvaluateLocalSLARequiresEvidenceForEveryConfiguredThreshold(t *testing.T) {
	p95LatencyMs := 800.0
	maxServerProcessingMs := 300.0
	maxErrorPct := 2.0

	partial := evaluateLocalSLA(&engine.RunResult{
		TotalTransactions: 10,
		FinalSnapshot:     engine.MetricsSnapshot{P95: 100 * time.Millisecond},
	}, &api.GoalConfig{
		P95LatencyMs:             &p95LatencyMs,
		MaxAvgServerProcessingMs: &maxServerProcessingMs,
	})
	if partial == nil || partial.Evaluated {
		t.Fatalf("partial SLA evidence = %#v, want not evaluated", partial)
	}

	allFailed := evaluateLocalSLA(&engine.RunResult{TotalErrors: 5}, &api.GoalConfig{MaxErrorPct: &maxErrorPct})
	if allFailed == nil || !allFailed.Evaluated || !allFailed.Breached {
		t.Fatalf("all-failed SLA evidence = %#v, want evaluated 100%% error-rate breach", allFailed)
	}
}
