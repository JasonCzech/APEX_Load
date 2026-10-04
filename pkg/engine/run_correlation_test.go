package engine

import (
	"reflect"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestValidateRunCorrelationHeaders(t *testing.T) {
	cases := []struct {
		name      string
		script    *dsl.Script
		wantError bool
	}{
		{"no headers", &dsl.Script{}, false},
		{"matching default", &dsl.Script{Config: dsl.ScriptConfig{HTTP: dsl.HTTPConfig{DefaultHeaders: map[string]string{"X-Apex-Test-Run": "run-123"}}}}, false},
		{"template default", &dsl.Script{Config: dsl.ScriptConfig{HTTP: dsl.HTTPConfig{DefaultHeaders: map[string]string{"x-apex-test-run": "{{ run.id }}"}}}}, false},
		{"conflicting default", &dsl.Script{Config: dsl.ScriptConfig{HTTP: dsl.HTTPConfig{DefaultHeaders: map[string]string{"X-Apex-Test-Run": "another-run"}}}}, true},
		{"conflicting list", &dsl.Script{Actions: []dsl.Action{{Type: "http", HeaderList: dsl.HeaderList{{Name: "x-APEX-test-run", Value: "another-run"}}}}}, true},
		{"sequence request", &dsl.Script{Actions: []dsl.Action{{Type: "http_sequence", Params: map[string]any{"requests": []any{map[string]any{"headers": map[string]any{"X-Apex-Test-Run": "another-run"}}}}}}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateRunCorrelationHeaders(tc.script, "run-123")
			if (err != nil) != tc.wantError {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestGroupRunCorrelationConfigIsControllerOwned(t *testing.T) {
	group := GroupConfig{RunID: "run-123", RunCorrelationOrigins: []string{"https://app.example"}, Script: &dsl.Script{}}
	config := buildGroupProtoConfig(group, "")
	if config["apexload_run_correlation_id"] != "run-123" || !reflect.DeepEqual(config["apexload_run_correlation_origins"], group.RunCorrelationOrigins) {
		t.Fatalf("config=%+v", config)
	}
	group.RunCorrelationOrigins[0] = "https://changed.example"
	if got := config["apexload_run_correlation_origins"].([]string)[0]; got != "https://app.example" {
		t.Fatalf("origins aliased group: %q", got)
	}
	disabled := buildGroupProtoConfig(GroupConfig{RunID: "run-123", Script: &dsl.Script{}}, "")
	if _, ok := disabled["apexload_run_correlation_id"]; ok {
		t.Fatal("ordinary run gained correlation header config")
	}
}
