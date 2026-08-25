package storage

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestScenarioScriptIterationsRoundTripAcrossRestart(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "apexload.db")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}

	script := &dsl.Script{
		ID:       "script-iterations",
		Name:     "Iterative Checkout",
		Protocol: "http",
		Version:  "1.0",
		Config:   dsl.ScriptConfig{BaseURL: "https://example.com"},
		Actions:  []dsl.Action{{Name: "Home", Type: "request", Method: "GET", Target: "/"}},
	}
	if err := store.SaveScript(script); err != nil {
		t.Fatalf("SaveScript: %v", err)
	}
	iterations := 7
	scenario := &Scenario{ID: "scn-iterations", Name: "Iteration Round Trip", Duration: "1m", CreatedAt: time.Now()}
	if err := store.SaveScenario(scenario, []ScenarioScript{
		{Order: 0, ScriptID: script.ID, VUsers: 10, Iterations: &iterations, StartOffset: "30s"},
		{Order: 1, ScriptID: script.ID, VUsers: 5},
	}); err != nil {
		t.Fatalf("SaveScenario: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("reopen NewSQLiteStore: %v", err)
	}
	defer reopened.Close()
	_, groups, err := reopened.GetScenario(scenario.ID)
	if err != nil {
		t.Fatalf("GetScenario after restart: %v", err)
	}
	if len(groups) != 2 {
		t.Fatalf("groups = %d, want 2", len(groups))
	}
	if groups[0].Iterations == nil || *groups[0].Iterations != iterations {
		t.Fatalf("group 0 iterations = %#v, want %d", groups[0].Iterations, iterations)
	}
	if groups[0].StartOffset != "30s" {
		t.Fatalf("group 0 start_offset = %q, want 30s", groups[0].StartOffset)
	}
	if groups[1].Iterations != nil {
		t.Fatalf("group 1 iterations = %#v, want nil", groups[1].Iterations)
	}
}

func TestSQLiteStoreMigratesScenarioScriptIterationsColumn(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	_, err = db.Exec(`CREATE TABLE scenario_scripts (
		scenario_id TEXT NOT NULL,
		sort_order INTEGER NOT NULL,
		name TEXT,
		script_id TEXT NOT NULL,
		vusers INTEGER,
		ramp_up TEXT,
		ramp_down TEXT,
		stages_json TEXT,
		initialization TEXT,
		role TEXT,
		runtime_settings_json TEXT,
		assignment_json TEXT,
		PRIMARY KEY (scenario_id, sort_order)
	)`)
	if err != nil {
		t.Fatalf("create legacy scenario_scripts: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy DB: %v", err)
	}

	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore migration: %v", err)
	}
	defer store.Close()
	rows, err := store.db.Query(`PRAGMA table_info(scenario_scripts)`)
	if err != nil {
		t.Fatalf("table_info: %v", err)
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatalf("scan table_info: %v", err)
		}
		if name == "iterations" {
			found = true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("table_info rows: %v", err)
	}
	if !found {
		t.Fatal("scenario_scripts.iterations was not added")
	}
}

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

func TestScenarioScriptAssignmentRoundTrip(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	script := &dsl.Script{
		ID:       "script-assignment",
		Name:     "JVM Flow",
		Protocol: "java_http",
		Version:  "1.0",
		Config:   dsl.ScriptConfig{BaseURL: "https://example.com"},
		Actions:  []dsl.Action{{Name: "Home", Type: "request", Method: "GET", Target: "/"}},
	}
	if err := store.SaveScript(script); err != nil {
		t.Fatalf("SaveScript: %v", err)
	}

	scenario := &Scenario{ID: "scn-assignment", Name: "Assignment Round Trip", Duration: "1m", CreatedAt: time.Now()}
	groups := []ScenarioScript{
		{
			Order:    0,
			ScriptID: script.ID,
			VUsers:   10,
			Assignment: map[string]any{
				"mode":             "specific_hosts",
				"selected_targets": []any{"gen-jvm:9090", "gen-spare:9090"},
				"shard_strategy":   "capacity_weighted",
				"required_labels":  map[string]any{"jvm": "17"},
			},
		},
		{Order: 1, ScriptID: script.ID, VUsers: 5},
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
	assignment, ok := scripts[0].Assignment.(map[string]any)
	if !ok {
		t.Fatalf("group 0 assignment type = %T, want map[string]any (%#v)", scripts[0].Assignment, scripts[0].Assignment)
	}
	if assignment["mode"] != "specific_hosts" || assignment["shard_strategy"] != "capacity_weighted" {
		t.Fatalf("assignment did not round-trip: %#v", assignment)
	}
	targets, ok := assignment["selected_targets"].([]any)
	if !ok || len(targets) != 2 || targets[0] != "gen-jvm:9090" {
		t.Fatalf("selected targets did not round-trip: %#v", assignment["selected_targets"])
	}
	labels, ok := assignment["required_labels"].(map[string]any)
	if !ok || labels["jvm"] != "17" {
		t.Fatalf("required labels did not round-trip: %#v", assignment["required_labels"])
	}
	if scripts[1].Assignment != nil {
		t.Fatalf("group 1 assignment = %#v, want nil", scripts[1].Assignment)
	}
}
