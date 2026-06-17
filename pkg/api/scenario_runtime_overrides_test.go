package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/scrub"
	"github.com/jasonczech/apexload/pkg/storage"
)

func newRuntimeOverrideTestServer(t *testing.T) (*Server, storage.Store) {
	t.Helper()
	store, err := storage.NewSQLiteStore(t.TempDir() + "/apexload.db")
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 4, Addr: ":8080"})
	return server, store
}

func httpScript(id, name string) *dsl.Script {
	return &dsl.Script{
		ID:       id,
		Name:     name,
		Protocol: "http",
		Version:  "1.0",
		Config:   dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions:  []dsl.Action{{Name: "Home", Type: "request", Method: "GET", Target: "/"}},
	}
}

// Per-group overrides must survive scenario persistence, flow into test creation,
// and win over the scenario-root base; groups without an override inherit the root.
func TestCreateTestFromScenarioAppliesPerGroupRuntimeOverrides(t *testing.T) {
	server, _ := newRuntimeOverrideTestServer(t)
	server.runner.StoreScript(httpScript("script-a", "Group A"))
	server.runner.StoreScript(httpScript("script-b", "Group B"))

	scenario, err := server.runner.StoreScenario(CreateScenarioRequest{
		Name:         "Runtime Scenario",
		Duration:     "1m",
		ScenarioMode: "vuser_driven",
		RuntimeSettings: &RuntimeSettingsOverride{
			LogLevel: "warn",
			Pacing:   &dsl.PacingConfig{Type: "fixed_delay", DelayMs: 1000},
		},
		Groups: []CreateGroupEntry{
			{
				Name:     "Group A",
				ScriptID: "script-a",
				VUsers:   5,
				// Group override: log level wins over root; pacing inherited from root.
				RuntimeSettings: &RuntimeSettingsOverride{LogLevel: "debug"},
			},
			{
				Name:     "Group B",
				ScriptID: "script-b",
				VUsers:   5,
				// No override: inherits the scenario-root profile entirely.
			},
		},
	})
	if err != nil {
		t.Fatalf("StoreScenario: %v", err)
	}
	if len(scenario.Scripts) != 2 {
		t.Fatalf("scenario has %d scripts, want 2", len(scenario.Scripts))
	}
	if scenario.Scripts[0].RuntimeSettings == nil || scenario.Scripts[0].RuntimeSettings.LogLevel != "debug" {
		t.Fatalf("group 0 stored runtime settings = %#v, want log_level=debug", scenario.Scripts[0].RuntimeSettings)
	}
	if scenario.Scripts[1].RuntimeSettings != nil {
		t.Fatalf("group 1 stored runtime settings = %#v, want nil", scenario.Scripts[1].RuntimeSettings)
	}

	test, err := server.runner.CreateTest(CreateTestRequest{ScenarioID: scenario.ID})
	if err != nil {
		t.Fatalf("CreateTest from scenario: %v", err)
	}
	if len(test.Groups) != 2 {
		t.Fatalf("test has %d groups, want 2", len(test.Groups))
	}
	// Group 0: per-group log level won; pacing inherited from root.
	if got := test.Groups[0].script.Config.Log.Level; got != "debug" {
		t.Fatalf("group 0 effective log level = %q, want debug (group override wins)", got)
	}
	if p := test.Groups[0].script.Config.Pacing; p.Type != "fixed_delay" || p.DelayMs != 1000 {
		t.Fatalf("group 0 effective pacing = %#v, want inherited fixed_delay/1000", p)
	}
	// Group 1: inherited the root log level.
	if got := test.Groups[1].script.Config.Log.Level; got != "warn" {
		t.Fatalf("group 1 effective log level = %q, want warn (inherited from root)", got)
	}
	// The per-group override is carried onto the test group (wiring).
	if test.Groups[0].RuntimeSettings == nil || test.Groups[0].RuntimeSettings.LogLevel != "debug" {
		t.Fatalf("group 0 test runtime settings = %#v, want log_level=debug", test.Groups[0].RuntimeSettings)
	}
}

// Regression: the refkey-backfill constructor must thread per-group runtime
// settings, otherwise a backfill SaveScenario silently wipes them.
func TestStoredScenarioRefsToStorageThreadsRuntimeSettings(t *testing.T) {
	stored := &StoredScenario{
		ID: "scn-1",
		Scripts: []ScenarioScriptRef{
			{Order: 0, ScriptID: "s1", Name: "g0", RuntimeSettings: &RuntimeSettingsOverride{LogLevel: "debug"}},
			{Order: 1, ScriptID: "s2", Name: "g1"},
		},
	}
	rows := storedScenarioRefsToStorage(stored)
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	rs, ok := rows[0].RuntimeSettings.(*RuntimeSettingsOverride)
	if !ok || rs == nil || rs.LogLevel != "debug" {
		t.Fatalf("row 0 runtime settings = %#v, want *RuntimeSettingsOverride{LogLevel:debug} (backfill must not drop it)", rows[0].RuntimeSettings)
	}
	if rows[1].RuntimeSettings != nil {
		t.Fatalf("row 1 runtime settings = %#v, want nil", rows[1].RuntimeSettings)
	}
}

// Save → reload (fresh server on the same store) → per-group runtime settings
// decode back, with the script protocol populated on the ref.
func TestScenarioRuntimeSettingsSurviveReload(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir() + "/apexload.db")
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 4, Addr: ":8080"})
	server.runner.StoreScript(httpScript("script-a", "Group A"))
	scenario, err := server.runner.StoreScenario(CreateScenarioRequest{
		Name:         "Reload Scenario",
		Duration:     "1m",
		ScenarioMode: "vuser_driven",
		Groups: []CreateGroupEntry{{
			Name:            "Group A",
			ScriptID:        "script-a",
			VUsers:          5,
			RuntimeSettings: &RuntimeSettingsOverride{LogLevel: "debug", AutoTransactions: "per_action"},
		}},
	})
	if err != nil {
		t.Fatalf("StoreScenario: %v", err)
	}

	// Fresh server on the same store reloads scenarios from the DB.
	reloaded := NewServer(ServerConfig{Store: store, MaxConcurrent: 4, Addr: ":8080"})
	got, ok := reloaded.runner.GetScenario(scenario.ID)
	if !ok {
		t.Fatalf("reloaded scenario not found")
	}
	if len(got.Scripts) != 1 {
		t.Fatalf("reloaded scenario has %d scripts, want 1", len(got.Scripts))
	}
	if got.Scripts[0].Protocol != "http" {
		t.Fatalf("reloaded script protocol = %q, want http", got.Scripts[0].Protocol)
	}
	rs := got.Scripts[0].RuntimeSettings
	if rs == nil || rs.LogLevel != "debug" || rs.AutoTransactions != "per_action" {
		t.Fatalf("reloaded per-group runtime settings = %#v, want log_level=debug auto_transactions=per_action", rs)
	}
}

func TestMaskRuntimeSettingsRedactsSensitiveEnv(t *testing.T) {
	original := &RuntimeSettingsOverride{Env: map[string]string{"API_TOKEN": "s3cr3t", "REGION": "us-east"}}
	masked := maskRuntimeSettings(original)
	if masked.Env["API_TOKEN"] != scrub.Redacted {
		t.Fatalf("API_TOKEN = %q, want %q", masked.Env["API_TOKEN"], scrub.Redacted)
	}
	if masked.Env["REGION"] != "us-east" {
		t.Fatalf("REGION = %q, want us-east (non-sensitive must survive)", masked.Env["REGION"])
	}
	if original.Env["API_TOKEN"] != "s3cr3t" {
		t.Fatalf("masking mutated the input: API_TOKEN = %q", original.Env["API_TOKEN"])
	}
}

func TestReconcileGroupRuntimeSecrets(t *testing.T) {
	prior := &RuntimeSettingsOverride{Env: map[string]string{"API_TOKEN": "real-secret", "OLD_TOKEN": "x"}}
	incoming := &RuntimeSettingsOverride{Env: map[string]string{
		"API_TOKEN": scrub.Redacted, // echoed mask -> keep stored
		"REGION":    "eu-west",      // brand new value -> persist
		"OLD_TOKEN": "",             // cleared -> drop
	}}
	result := reconcileGroupRuntimeSecrets(incoming, prior)
	if result.Env["API_TOKEN"] != "real-secret" {
		t.Fatalf("API_TOKEN = %q, want preserved real-secret", result.Env["API_TOKEN"])
	}
	if result.Env["REGION"] != "eu-west" {
		t.Fatalf("REGION = %q, want eu-west", result.Env["REGION"])
	}
	if _, present := result.Env["OLD_TOKEN"]; present {
		t.Fatalf("OLD_TOKEN should have been cleared, got %q", result.Env["OLD_TOKEN"])
	}

	// A masked value with no prior match fails closed (dropped, never persisted as [REDACTED]).
	orphan := reconcileGroupRuntimeSecrets(&RuntimeSettingsOverride{Env: map[string]string{"API_TOKEN": scrub.Redacted}}, nil)
	if _, present := orphan.Env["API_TOKEN"]; present {
		t.Fatalf("orphan masked token should be dropped, got %#v", orphan.Env)
	}
	if reconcileGroupRuntimeSecrets(nil, prior) != nil {
		t.Fatalf("nil incoming should reconcile to nil")
	}
}

// On update, an echoed [REDACTED] secret is reconciled against the PRIOR stored
// value by stable group identity (ScriptID+Name), so reordering groups does not
// misroute a preserved secret.
func TestScenarioUpdatePreservesMaskedSecretsAcrossReorder(t *testing.T) {
	server, _ := newRuntimeOverrideTestServer(t)
	server.runner.StoreScript(httpScript("script-a", "Group A"))
	server.runner.StoreScript(httpScript("script-b", "Group B"))

	base := CreateScenarioRequest{
		Name:         "Secrets Scenario",
		Duration:     "1m",
		ScenarioMode: "vuser_driven",
		Groups: []CreateGroupEntry{
			{Name: "Group A", ScriptID: "script-a", VUsers: 5, RuntimeSettings: &RuntimeSettingsOverride{Env: map[string]string{"API_TOKEN": "a-secret"}}},
			{Name: "Group B", ScriptID: "script-b", VUsers: 5, RuntimeSettings: &RuntimeSettingsOverride{Env: map[string]string{"API_TOKEN": "b-secret"}}},
		},
	}
	scenario, err := server.runner.StoreScenario(base)
	if err != nil {
		t.Fatalf("StoreScenario: %v", err)
	}

	// Client echoes the masked sentinel back, with the groups REORDERED (B then A).
	update := CreateScenarioRequest{
		Name:         base.Name,
		Duration:     base.Duration,
		ScenarioMode: base.ScenarioMode,
		Groups: []CreateGroupEntry{
			{Name: "Group B", ScriptID: "script-b", VUsers: 5, RuntimeSettings: &RuntimeSettingsOverride{Env: map[string]string{"API_TOKEN": scrub.Redacted}}},
			{Name: "Group A", ScriptID: "script-a", VUsers: 5, RuntimeSettings: &RuntimeSettingsOverride{Env: map[string]string{"API_TOKEN": scrub.Redacted}}},
		},
	}
	if _, ok, err := server.runner.UpdateScenario(scenario.ID, update); err != nil || !ok {
		t.Fatalf("UpdateScenario: ok=%v err=%v", ok, err)
	}

	stored := server.runner.scenarios[scenario.ID]
	secretByScript := map[string]string{}
	for _, ref := range stored.Scripts {
		if ref.RuntimeSettings != nil {
			secretByScript[ref.ScriptID] = ref.RuntimeSettings.Env["API_TOKEN"]
		}
	}
	if secretByScript["script-a"] != "a-secret" {
		t.Fatalf("script-a secret = %q, want a-secret (preserved across reorder)", secretByScript["script-a"])
	}
	if secretByScript["script-b"] != "b-secret" {
		t.Fatalf("script-b secret = %q, want b-secret (preserved across reorder)", secretByScript["script-b"])
	}
}

// Apply-to-groups copies the source group's effective profile server-side, skips
// fields not applicable to a target group's protocol, never copies Env, and masks
// secrets in the response.
func TestApplyScenarioRuntimeToGroupsSkipsIncompatibleAndNeverLeaksSecrets(t *testing.T) {
	server, _ := newRuntimeOverrideTestServer(t)
	// Scripts exist only to satisfy the scenario_scripts foreign key.
	server.runner.StoreScript(httpScript("script-a", "Group A"))
	server.runner.StoreScript(httpScript("script-b", "Group B"))

	now := time.Now().UTC()
	headless := true
	server.runner.scenarios["scn-apply"] = &StoredScenario{
		ID:        "scn-apply",
		Name:      "Apply Scenario",
		Duration:  "1m",
		CreatedAt: now,
		UpdatedAt: now,
		Scripts: []ScenarioScriptRef{
			{
				Order: 0, ScriptID: "script-a", Name: "Group A", Protocol: "browser", VUsers: 5,
				RuntimeSettings: &RuntimeSettingsOverride{
					Pacing:  &dsl.PacingConfig{Type: "fixed_delay", DelayMs: 500},
					Browser: &RuntimeBrowserOverride{DeviceProfile: "pixel_8", Headless: &headless},
					Env:     map[string]string{"API_TOKEN": "source-secret"},
				},
			},
			{Order: 1, ScriptID: "script-b", Name: "Group B", Protocol: "http", VUsers: 5},
		},
		ScriptCount: 2,
	}

	body, _ := json.Marshal(ApplyRuntimeToGroupsRequest{
		SourceGroup:  0,
		TargetGroups: []int{1},
		Fields:       []string{"pacing", "browser"},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scenarios/scn-apply/runtime/apply-to-groups", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", resp.Code, resp.Body.String())
	}

	var out ApplyRuntimeToGroupsResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out.Applied != 1 {
		t.Fatalf("applied = %d, want 1", out.Applied)
	}
	if len(out.Skipped) != 1 || out.Skipped[0].Group != 1 || len(out.Skipped[0].Fields) != 1 || out.Skipped[0].Fields[0] != "browser" {
		t.Fatalf("skipped = %#v, want browser skipped for group 1", out.Skipped)
	}
	target := out.Scenario.Scripts[1].RuntimeSettings
	if target == nil || target.Pacing == nil || target.Pacing.Type != "fixed_delay" || target.Pacing.DelayMs != 500 {
		t.Fatalf("target pacing = %#v, want copied fixed_delay/500", target)
	}
	if target.Browser != nil {
		t.Fatalf("target browser = %#v, want nil (skipped for http protocol)", target.Browser)
	}
	if len(target.Env) != 0 {
		t.Fatalf("target env = %#v, want empty (secrets never copied across groups)", target.Env)
	}
	// Source secret is masked in the response...
	if got := out.Scenario.Scripts[0].RuntimeSettings.Env["API_TOKEN"]; got != scrub.Redacted {
		t.Fatalf("source env in response = %q, want %q", got, scrub.Redacted)
	}
	// ...but preserved unmasked in the store, and no stored field is the sentinel.
	storedScn := server.runner.scenarios["scn-apply"]
	if got := storedScn.Scripts[0].RuntimeSettings.Env["API_TOKEN"]; got != "source-secret" {
		t.Fatalf("stored source secret = %q, want source-secret", got)
	}
	if storedScn.Scripts[1].RuntimeSettings != nil {
		for k, v := range storedScn.Scripts[1].RuntimeSettings.Env {
			if v == scrub.Redacted {
				t.Fatalf("stored target env %q == [REDACTED]; sentinel must never be persisted", k)
			}
		}
	}
}
