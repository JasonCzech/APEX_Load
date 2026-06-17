package storage

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// TestScenarioScriptRuntimeSettingsRoundTrip verifies the per-group
// runtime_settings_json column persists and reloads an opaque override, and that
// a group with no override loads back as nil (not an empty value) — exercising the
// COALESCE/"null" guard for legacy rows.
func TestScenarioScriptRuntimeSettingsRoundTrip(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	script := &dsl.Script{
		ID:       "script-rt",
		Name:     "Checkout",
		Protocol: "http",
		Version:  "1.0",
		Config:   dsl.ScriptConfig{BaseURL: "https://example.com"},
		Actions:  []dsl.Action{{Name: "Home", Type: "request", Method: "GET", Target: "/"}},
	}
	if err := store.SaveScript(script); err != nil {
		t.Fatalf("SaveScript: %v", err)
	}

	scenario := &Scenario{ID: "scn-rt", Name: "Runtime Round Trip", Duration: "1m", CreatedAt: time.Now()}
	groups := []ScenarioScript{
		{
			Order:    0,
			ScriptID: script.ID,
			VUsers:   10,
			RuntimeSettings: map[string]any{
				"log_level":         "debug",
				"auto_transactions": "per_action",
				"pacing":            map[string]any{"type": "fixed_delay", "delay_ms": float64(250)},
			},
		},
		{
			Order:    1,
			ScriptID: script.ID,
			VUsers:   5,
			// no runtime settings -> must load back as nil
		},
	}
	if err := store.SaveScenario(scenario, groups); err != nil {
		t.Fatalf("SaveScenario: %v", err)
	}

	_, scripts, err := store.GetScenario(scenario.ID)
	if err != nil {
		t.Fatalf("GetScenario: %v", err)
	}
	if len(scripts) != 2 {
		t.Fatalf("got %d scripts, want 2", len(scripts))
	}

	rs, ok := scripts[0].RuntimeSettings.(map[string]any)
	if !ok {
		t.Fatalf("group 0 runtime settings type = %T, want map[string]any (%#v)", scripts[0].RuntimeSettings, scripts[0].RuntimeSettings)
	}
	if rs["log_level"] != "debug" || rs["auto_transactions"] != "per_action" {
		t.Fatalf("group 0 runtime settings did not round-trip: %#v", rs)
	}
	pacing, ok := rs["pacing"].(map[string]any)
	if !ok || pacing["type"] != "fixed_delay" || pacing["delay_ms"] != float64(250) {
		t.Fatalf("group 0 pacing did not round-trip: %#v", rs["pacing"])
	}

	if scripts[1].RuntimeSettings != nil {
		t.Fatalf("group 1 runtime settings = %#v, want nil (no override persisted)", scripts[1].RuntimeSettings)
	}
}
