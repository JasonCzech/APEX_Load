package api

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/scrub"
)

func TestDecodeStoredInitialProvenanceMasksAndClones(t *testing.T) {
	const secret = "stored-initial-secret"
	stored := map[string]any{
		"total_vusers": 7,
		"runtime_settings": map[string]any{
			"http": map[string]any{"auth": map[string]any{"password": secret}},
		},
	}
	decoded := decodeStoredInitialProvenance(stored)
	if decoded == nil || decoded.TotalVUsers != 7 || decoded.RuntimeSettings == nil || decoded.RuntimeSettings.HTTP == nil {
		t.Fatalf("decoded provenance = %#v", decoded)
	}
	if got := decoded.RuntimeSettings.HTTP.Auth.Password; got != scrub.Redacted {
		t.Fatalf("decoded password = %q, want redaction", got)
	}
	original := &RunArchiveProvenance{RuntimeSettings: &RuntimeSettingsOverride{HTTP: &dsl.HTTPConfig{Auth: dsl.AuthConfig{Password: secret}}}}
	boxed := initialProvenanceForStorage(original)
	if boxed == original {
		t.Fatal("storage projection aliases original provenance")
	}
	if got := boxed.(*RunArchiveProvenance).RuntimeSettings.HTTP.Auth.Password; got != scrub.Redacted {
		t.Fatalf("storage projection password = %q, want redaction", got)
	}
}

func TestDecodeStoredGroups(t *testing.T) {
	if got := decodeStoredGroups(nil); got != nil {
		t.Fatalf("nil groups = %#v, want nil", got)
	}
	if got := decodeStoredGroups(func() {}); got != nil {
		t.Fatalf("invalid groups = %#v, want nil", got)
	}

	groups := decodeStoredGroups([]map[string]any{{
		"name":      "Checkout",
		"script_id": "script-1",
		"vusers":    25,
		"ramp_up":   "30s",
	}})
	if len(groups) != 1 {
		t.Fatalf("groups = %#v, want one group", groups)
	}
	if groups[0].Name != "Checkout" || groups[0].ScriptID != "script-1" || groups[0].VUsers != 25 || groups[0].RampUp != "30s" {
		t.Fatalf("decoded group = %#v", groups[0])
	}
}

func TestDecodeStoredRuntimeSettings(t *testing.T) {
	if got := decodeStoredRuntimeSettings(nil); got != nil {
		t.Fatalf("nil runtime settings = %#v, want nil", got)
	}
	var nilSettings *RuntimeSettingsOverride
	if boxed := runtimeSettingsForStorage(nilSettings); boxed != nil {
		t.Fatalf("runtimeSettingsForStorage(nil typed pointer) = %#v, want nil interface", boxed)
	}
	if got := decodeStoredRuntimeSettings(nilSettings); got != nil {
		t.Fatalf("typed-nil runtime settings = %#v, want nil", got)
	}
	if got := decodeStoredRuntimeSettings(map[string]any{"iterations": "not-a-number"}); got != nil {
		t.Fatalf("invalid runtime settings = %#v, want nil", got)
	}

	settings := decodeStoredRuntimeSettings(map[string]any{
		"iterations":        3,
		"base_url":          "https://run.example",
		"log_level":         "extended",
		"auto_transactions": "per_action",
		"env":               map[string]any{"namespace": "blue"},
		"browser": map[string]any{
			"headless":       false,
			"device_profile": "iphone_15_pro",
			"touch_mode":     true,
		},
	})
	if settings == nil {
		t.Fatal("runtime settings = nil, want decoded settings")
	}
	if settings.Iterations == nil || *settings.Iterations != 3 {
		t.Fatalf("iterations = %#v, want 3", settings.Iterations)
	}
	if settings.BaseURL != "https://run.example" || settings.LogLevel != "extended" || settings.AutoTransactions != "per_action" {
		t.Fatalf("settings = %#v, want URL/log/auto overrides", settings)
	}
	if settings.Env["namespace"] != "blue" {
		t.Fatalf("env = %#v, want namespace blue", settings.Env)
	}
	if settings.Browser == nil || settings.Browser.Headless == nil || *settings.Browser.Headless != false {
		t.Fatalf("browser = %#v, want headless false", settings.Browser)
	}
	if settings.Browser.TouchMode == nil || *settings.Browser.TouchMode != true || settings.Browser.DeviceProfile != "iphone_15_pro" {
		t.Fatalf("browser = %#v, want mobile browser overrides", settings.Browser)
	}
}

func TestDecodeStoredGoalConfig(t *testing.T) {
	if got := decodeStoredGoalConfig(nil); got != nil {
		t.Fatalf("nil goal config = %#v, want nil", got)
	}
	var nilGoal *GoalConfig
	if boxed := goalConfigForStorage(nilGoal); boxed != nil {
		t.Fatalf("goalConfigForStorage(nil typed pointer) = %#v, want nil interface", boxed)
	}
	if got := decodeStoredGoalConfig(nilGoal); got != nil {
		t.Fatalf("typed-nil goal config = %#v, want nil", got)
	}
	if got := decodeStoredGoalConfig(map[string]any{"target_average_tps": []string{"bad"}}); got != nil {
		t.Fatalf("invalid goal config = %#v, want nil", got)
	}

	config := decodeStoredGoalConfig(map[string]any{
		"target_average_tps":           300,
		"p95_latency_ms":               900,
		"max_error_count":              10,
		"stop_on_max_errors":           true,
		"stop_on_sla_breach":           true,
		"sla_breach_window":            "45s",
		"max_avg_transfer_ms":          125,
		"max_error_pct":                2.5,
		"min_vusers":                   5,
		"max_vusers":                   100,
		"max_avg_server_processing_ms": 250,
	})
	if config == nil {
		t.Fatal("goal config = nil, want decoded config")
	}
	if config.TargetAverageTPS == nil || *config.TargetAverageTPS != 300 {
		t.Fatalf("target TPS = %#v, want 300", config.TargetAverageTPS)
	}
	if config.MaxErrorCount == nil || *config.MaxErrorCount != 10 || !config.StopOnMaxErrors || !config.StopOnSLABreach {
		t.Fatalf("goal config = %#v, want stop settings", config)
	}
	if config.SLABreachWindow != "45s" {
		t.Fatalf("SLA window = %q, want 45s", config.SLABreachWindow)
	}
}

func TestDecodeStoredGeneratorAssignment(t *testing.T) {
	if got := decodeStoredGeneratorAssignment(nil); got != nil {
		t.Fatalf("nil assignment = %#v, want nil", got)
	}
	if got := decodeStoredGeneratorAssignment(map[string]any{
		"mode":           generatorAssignmentModeRegionWeighted,
		"region_weights": map[string]any{"east": 0},
	}); got != nil {
		t.Fatalf("invalid assignment = %#v, want nil", got)
	}

	assignment := decodeStoredGeneratorAssignment(map[string]any{
		"mode":             generatorAssignmentModeSpecificHosts,
		"selected_targets": []string{" gen-a ", "", "gen-a", "gen-b"},
		"region_weights":   map[string]int{"ignored": 100},
	})
	if assignment == nil {
		t.Fatal("assignment = nil, want decoded assignment")
	}
	if assignment.Mode != generatorAssignmentModeSpecificHosts {
		t.Fatalf("mode = %q, want specific_hosts", assignment.Mode)
	}
	if len(assignment.SelectedTargets) != 2 || assignment.SelectedTargets[0] != "gen-a" || assignment.SelectedTargets[1] != "gen-b" {
		t.Fatalf("selected targets = %#v, want trimmed unique targets", assignment.SelectedTargets)
	}
	if assignment.RegionWeights != nil {
		t.Fatalf("region weights = %#v, want nil for specific_hosts", assignment.RegionWeights)
	}
}
