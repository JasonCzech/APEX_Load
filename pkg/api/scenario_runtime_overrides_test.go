package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
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
	secrets := &runtimeProjectionSecretStore{values: make(map[string]string), attemptStore: store}
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 4, Addr: ":8080", ProjectSecretStore: secrets})
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
	if secretByScript["script-a"] != "{{secret."+runtimeOverrideSecretName(scenario.ID, 0, "env.API_TOKEN")+"}}" {
		t.Fatalf("script-a secret reference = %q, want stable protected reference", secretByScript["script-a"])
	}
	if secretByScript["script-b"] != "{{secret."+runtimeOverrideSecretName(scenario.ID, 1, "env.API_TOKEN")+"}}" {
		t.Fatalf("script-b secret reference = %q, want stable protected reference", secretByScript["script-b"])
	}
	managed := server.projectSecretStore.(*runtimeProjectionSecretStore)
	for name, want := range map[string]string{
		runtimeOverrideSecretName(scenario.ID, 0, "env.API_TOKEN"): "a-secret",
		runtimeOverrideSecretName(scenario.ID, 1, "env.API_TOKEN"): "b-secret",
	} {
		got, _, getErr := managed.Get(context.Background(), storage.DefaultProjectID, name)
		if getErr != nil || got != want {
			t.Fatalf("governed %s = %q, %v; want %q", name, got, getErr, want)
		}
	}
}

// Apply-to-groups copies the source group's effective profile server-side, skips
// fields not applicable to a target group's protocol, never copies Env, and masks
// secrets in the response.
func TestApplyScenarioRuntimeToGroupsSkipsIncompatibleAndNeverLeaksSecrets(t *testing.T) {
	server, store := newRuntimeOverrideTestServer(t)
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
	persisted := storedScenarioToStorage(server.runner.scenarios["scn-apply"])
	if err := store.SaveScenario(persisted, storedScenarioRefsToStorage(server.runner.scenarios["scn-apply"])); err != nil {
		t.Fatalf("SaveScenario: %v", err)
	}
	server.runner.scenarios["scn-apply"].UpdatedAt = persisted.UpdatedAt

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

func TestApplyScenarioRuntimeToGroupsMaterializesInheritedSourceScriptRuntime(t *testing.T) {
	server, _ := newRuntimeOverrideTestServer(t)
	source := httpScript("script-inherited-source", "Inherited Source")
	source.Config.Pacing = dsl.PacingConfig{Type: "fixed_delay", DelayMs: 500}
	target := httpScript("script-different-target", "Different Target")
	target.Config.Pacing = dsl.PacingConfig{Type: "fixed_delay", DelayMs: 900}
	server.runner.StoreScript(source)
	server.runner.StoreScript(target)

	scenario, err := server.runner.StoreScenario(CreateScenarioRequest{
		Name:     "Inherited source runtime",
		Duration: "1m",
		Groups: []CreateGroupEntry{
			{Name: "Source", ScriptID: source.ID, VUsers: 1},
			{
				Name: "Target", ScriptID: target.ID, VUsers: 1,
				RuntimeSettings: &RuntimeSettingsOverride{
					Pacing:    &dsl.PacingConfig{Type: "fixed_delay", DelayMs: 1200},
					ThinkTime: &dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 250},
					Env:       map[string]string{"TARGET_REGION": "west"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("StoreScenario: %v", err)
	}

	out, ok, err := server.runner.ApplyScenarioRuntimeToGroups(scenario.ID, ApplyRuntimeToGroupsRequest{
		SourceGroup:  0,
		TargetGroups: []int{1},
		Fields:       []string{"pacing"},
	})
	if err != nil || !ok {
		t.Fatalf("ApplyScenarioRuntimeToGroups: ok=%v err=%v", ok, err)
	}
	targetRuntime := out.Scenario.Scripts[1].RuntimeSettings
	if targetRuntime == nil || targetRuntime.Pacing == nil || targetRuntime.Pacing.Type != "fixed_delay" || targetRuntime.Pacing.DelayMs != 500 {
		t.Fatalf("target pacing = %#v, want inherited source-script fixed_delay/500", targetRuntime)
	}
	if targetRuntime.ThinkTime == nil || targetRuntime.ThinkTime.MeanMs != 250 || targetRuntime.Env["TARGET_REGION"] != "west" {
		t.Fatalf("target-only runtime siblings were not preserved: %#v", targetRuntime)
	}

	created, err := server.runner.CreateTest(CreateTestRequest{ScenarioID: scenario.ID})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	if got := created.Groups[1].script.Config.Pacing; got.Type != "fixed_delay" || got.DelayMs != 500 {
		t.Fatalf("target executable pacing = %#v, want copied source effective fixed_delay/500", got)
	}
}

func TestApplyScenarioRuntimeToGroupsMaterializesEngineDefaultsAndPreservesTargetLocalFields(t *testing.T) {
	server, store := newRuntimeOverrideTestServer(t)
	server.runner.StoreScript(httpScript("script-clear-source", "Source"))
	server.runner.StoreScript(httpScript("script-clear-target", "Target"))

	now := time.Now().UTC()
	server.runner.scenarios["scn-apply-clear"] = &StoredScenario{
		ID:        "scn-apply-clear",
		Name:      "Apply Clear Scenario",
		Duration:  "1m",
		CreatedAt: now,
		UpdatedAt: now,
		Scripts: []ScenarioScriptRef{
			{Order: 0, ScriptID: "script-clear-source", Name: "Source", Protocol: "http", VUsers: 1},
			{
				Order: 1, ScriptID: "script-clear-target", Name: "Target", Protocol: "http", VUsers: 1,
				RuntimeSettings: &RuntimeSettingsOverride{
					Pacing:    &dsl.PacingConfig{Type: "fixed_delay", DelayMs: 900},
					LogLevel:  "extended",
					HTTP:      &dsl.HTTPConfig{Version: "2"},
					ThinkTime: &dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 250},
					Env:       map[string]string{"TARGET_ONLY": "secret"},
					RedactionRules: []engine.RedactionRule{
						{Name: "target-local", Pattern: `customer-[0-9]+`},
					},
				},
			},
		},
		ScriptCount: 2,
	}
	persisted := storedScenarioToStorage(server.runner.scenarios["scn-apply-clear"])
	if err := store.SaveScenario(persisted, storedScenarioRefsToStorage(server.runner.scenarios["scn-apply-clear"])); err != nil {
		t.Fatalf("SaveScenario: %v", err)
	}
	server.runner.scenarios["scn-apply-clear"].UpdatedAt = persisted.UpdatedAt

	out, ok, err := server.runner.ApplyScenarioRuntimeToGroups("scn-apply-clear", ApplyRuntimeToGroupsRequest{
		SourceGroup:  0,
		TargetGroups: []int{1},
		Fields:       []string{"pacing", "log_level", "http"},
	})
	if err != nil || !ok {
		t.Fatalf("ApplyScenarioRuntimeToGroups: ok=%v err=%v", ok, err)
	}
	if out.Applied != 1 {
		t.Fatalf("applied = %d, want 1", out.Applied)
	}

	target := server.runner.scenarios["scn-apply-clear"].Scripts[1].RuntimeSettings
	if target == nil {
		t.Fatal("target runtime settings = nil, want unselected target-local fields preserved")
	}
	if target.Pacing == nil || target.Pacing.Type != "none" {
		t.Fatalf("target pacing = %#v, want source effective engine default none", target.Pacing)
	}
	if target.LogLevel != "standard" {
		t.Fatalf("target log level = %q, want source effective engine default standard", target.LogLevel)
	}
	if target.HTTP == nil || target.HTTP.ConnectTimeoutMs != 30000 || target.HTTP.TLS.MinVersion != "1.2" {
		t.Fatalf("target HTTP = %#v, want source effective engine defaults", target.HTTP)
	}
	if target.ThinkTime == nil || target.ThinkTime.MeanMs != 250 {
		t.Fatalf("unselected think time = %#v, want fixed/250 preserved", target.ThinkTime)
	}
	if got := target.Env["TARGET_ONLY"]; got != "secret" {
		t.Fatalf("target-local env = %q, want secret preserved", got)
	}
	if len(target.RedactionRules) != 1 || target.RedactionRules[0].Name != "target-local" {
		t.Fatalf("target-local redaction rules = %#v, want preserved", target.RedactionRules)
	}
}

func TestApplyScenarioRuntimeToGroupsRejectsInvalidCoupledSettingsAtomically(t *testing.T) {
	server, store := newRuntimeOverrideTestServer(t)
	server.runner.StoreScript(httpScript("script-open-source", "Open Source"))
	server.runner.StoreScript(httpScript("script-closed-target", "Closed Target"))

	now := time.Now().UTC()
	scenario := &StoredScenario{
		ID:        "scn-apply-invalid",
		Name:      "Apply Invalid Scenario",
		Duration:  "1m",
		CreatedAt: now,
		UpdatedAt: now,
		Scripts: []ScenarioScriptRef{
			{
				Order: 0, ScriptID: "script-open-source", Name: "Open Source", Protocol: "http", VUsers: 1,
				RuntimeSettings: &RuntimeSettingsOverride{
					RunLogic: &dsl.RunLogicConfig{WorkloadModel: "open"},
					Pacing:   &dsl.PacingConfig{Type: "tps", TPS: 10},
				},
			},
			{
				Order: 1, ScriptID: "script-closed-target", Name: "Closed Target", Protocol: "http", VUsers: 1,
				RuntimeSettings: &RuntimeSettingsOverride{
					RunLogic: &dsl.RunLogicConfig{WorkloadModel: "closed"},
					Pacing:   &dsl.PacingConfig{Type: "fixed_delay", DelayMs: 100},
				},
			},
		},
		ScriptCount: 2,
	}
	server.runner.scenarios[scenario.ID] = scenario
	persisted := storedScenarioToStorage(scenario)
	if err := store.SaveScenario(persisted, storedScenarioRefsToStorage(scenario)); err != nil {
		t.Fatalf("SaveScenario: %v", err)
	}
	scenario.UpdatedAt = persisted.UpdatedAt

	_, ok, err := server.runner.ApplyScenarioRuntimeToGroups(scenario.ID, ApplyRuntimeToGroupsRequest{
		SourceGroup:  0,
		TargetGroups: []int{1},
		Fields:       []string{"run_logic"},
	})
	if !ok || err == nil {
		t.Fatalf("ApplyScenarioRuntimeToGroups: ok=%v err=%v, want validation error", ok, err)
	}
	if got := server.runner.scenarios[scenario.ID].Scripts[1].RuntimeSettings.RunLogic.WorkloadModel; got != "closed" {
		t.Fatalf("stored target workload_model = %q, want closed after atomic rejection", got)
	}
	if got := server.runner.scenarios[scenario.ID].Scripts[1].RuntimeSettings.Pacing.Type; got != "fixed_delay" {
		t.Fatalf("stored target pacing = %q, want fixed_delay after atomic rejection", got)
	}
}

func TestApplyScenarioRuntimeToGroupsRejectsOutOfRangeTargetAtomically(t *testing.T) {
	server, store := newRuntimeOverrideTestServer(t)
	server.runner.StoreScript(httpScript("script-range-source", "Range Source"))
	server.runner.StoreScript(httpScript("script-range-target", "Range Target"))

	now := time.Now().UTC()
	scenario := &StoredScenario{
		ID:        "scn-apply-target-range",
		Name:      "Apply Target Range Scenario",
		Duration:  "1m",
		CreatedAt: now,
		UpdatedAt: now,
		Scripts: []ScenarioScriptRef{
			{
				Order: 0, ScriptID: "script-range-source", Name: "Range Source", Protocol: "http", VUsers: 1,
				RuntimeSettings: &RuntimeSettingsOverride{Pacing: &dsl.PacingConfig{Type: "fixed_delay", DelayMs: 250}},
			},
			{
				Order: 1, ScriptID: "script-range-target", Name: "Range Target", Protocol: "http", VUsers: 1,
				RuntimeSettings: &RuntimeSettingsOverride{
					Pacing:    &dsl.PacingConfig{Type: "fixed_delay", DelayMs: 900},
					ThinkTime: &dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 125},
				},
			},
		},
		ScriptCount: 2,
	}
	server.runner.scenarios[scenario.ID] = scenario
	persisted := storedScenarioToStorage(scenario)
	if err := store.SaveScenario(persisted, storedScenarioRefsToStorage(scenario)); err != nil {
		t.Fatalf("SaveScenario: %v", err)
	}
	scenario.UpdatedAt = persisted.UpdatedAt

	memoryBefore, err := json.Marshal(server.runner.scenarios[scenario.ID])
	if err != nil {
		t.Fatalf("marshal in-memory baseline: %v", err)
	}
	durableScenarioBefore, durableChildrenBefore, err := store.GetScenario(scenario.ID)
	if err != nil {
		t.Fatalf("GetScenario baseline: %v", err)
	}
	durableBefore, err := json.Marshal(struct {
		Scenario *storage.Scenario        `json:"scenario"`
		Scripts  []storage.ScenarioScript `json:"scripts"`
	}{durableScenarioBefore, durableChildrenBefore})
	if err != nil {
		t.Fatalf("marshal durable baseline: %v", err)
	}
	revisionBefore := server.runner.contentRevision

	tests := []struct {
		name    string
		targets []int
		wantErr string
	}{
		{name: "valid followed by upper bound", targets: []int{1, 2}, wantErr: "target_groups[1] 2 out of range [0,2)"},
		{name: "valid followed by negative index", targets: []int{1, -1}, wantErr: "target_groups[1] -1 out of range [0,2)"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			out, ok, err := server.runner.ApplyScenarioRuntimeToGroups(scenario.ID, ApplyRuntimeToGroupsRequest{
				SourceGroup:  0,
				TargetGroups: test.targets,
				Fields:       []string{"pacing"},
			})
			if !ok || err == nil || err.Error() != test.wantErr {
				t.Fatalf("ApplyScenarioRuntimeToGroups: response=%#v ok=%v err=%v, want nil/true/%q", out, ok, err, test.wantErr)
			}
			if out != nil {
				t.Fatalf("response = %#v, want nil after atomic rejection", out)
			}

			memoryAfter, marshalErr := json.Marshal(server.runner.scenarios[scenario.ID])
			if marshalErr != nil {
				t.Fatalf("marshal in-memory state: %v", marshalErr)
			}
			if !bytes.Equal(memoryAfter, memoryBefore) {
				t.Fatalf("in-memory scenario changed after rejected request:\n before=%s\n  after=%s", memoryBefore, memoryAfter)
			}
			if server.runner.contentRevision != revisionBefore {
				t.Fatalf("content revision = %d, want unchanged %d", server.runner.contentRevision, revisionBefore)
			}

			durableScenarioAfter, durableChildrenAfter, getErr := store.GetScenario(scenario.ID)
			if getErr != nil {
				t.Fatalf("GetScenario after rejection: %v", getErr)
			}
			durableAfter, marshalErr := json.Marshal(struct {
				Scenario *storage.Scenario        `json:"scenario"`
				Scripts  []storage.ScenarioScript `json:"scripts"`
			}{durableScenarioAfter, durableChildrenAfter})
			if marshalErr != nil {
				t.Fatalf("marshal durable state: %v", marshalErr)
			}
			if !bytes.Equal(durableAfter, durableBefore) {
				t.Fatalf("durable scenario changed after rejected request:\n before=%s\n  after=%s", durableBefore, durableAfter)
			}
		})
	}
}

func TestCreateTestRequestFromScenarioReconcilesMaskedCompleteRuntimeProfiles(t *testing.T) {
	prior := &StoredScenario{
		Name:     "Scenario",
		Duration: "1m",
		RuntimeSettings: &RuntimeSettingsOverride{
			Log: &dsl.LogConfig{Level: "standard", ExtendedOptions: []string{"advanced_trace"}},
			Env: map[string]string{"ROOT_TOKEN": "root-secret"},
		},
		Scripts: []ScenarioScriptRef{{
			Order: 0, ScriptID: "script-a", Name: "A", VUsers: 1,
			RuntimeSettings: &RuntimeSettingsOverride{
				HTTP: &dsl.HTTPConfig{Version: "2"},
				Env:  map[string]string{"GROUP_TOKEN": "group-secret"},
			},
		}},
	}
	req := CreateTestRequest{
		RuntimeSettings: maskRuntimeSettings(prior.RuntimeSettings),
		Groups: []CreateGroupEntry{{
			ScriptID:        "script-a",
			VUsers:          1,
			RuntimeSettings: maskRuntimeSettings(prior.Scripts[0].RuntimeSettings),
		}},
	}
	req.RuntimeSettings.Log.Level = "extended"

	got := createTestRequestFromScenario(req, prior)
	if got.RuntimeSettings == nil || got.RuntimeSettings.Env["ROOT_TOKEN"] != "root-secret" {
		t.Fatalf("root runtime credentials = %#v, want authoritative secret restored", got.RuntimeSettings)
	}
	if got.RuntimeSettings.Log == nil || got.RuntimeSettings.Log.Level != "extended" || len(got.RuntimeSettings.Log.ExtendedOptions) != 1 {
		t.Fatalf("root log = %#v, want edited level with advanced sibling preserved", got.RuntimeSettings.Log)
	}
	groupRuntime := got.Groups[0].RuntimeSettings
	if groupRuntime == nil || groupRuntime.Env["GROUP_TOKEN"] != "group-secret" || groupRuntime.HTTP == nil || groupRuntime.HTTP.Version != "2" {
		t.Fatalf("group runtime = %#v, want masked credential reconciled and advanced HTTP preserved", groupRuntime)
	}
}

func TestCreateTestRequestFromScenarioReconcilesReorderedDuplicateScriptGroupsByStableIdentity(t *testing.T) {
	prior := &StoredScenario{
		Name: "Duplicate script groups", Duration: "1m",
		Scripts: []ScenarioScriptRef{
			{
				Order: 0, ScriptID: "shared-script", Name: "Primary", VUsers: 1,
				RuntimeSettings: &RuntimeSettingsOverride{Env: map[string]string{"GROUP_TOKEN": "primary-secret"}},
			},
			{
				Order: 1, ScriptID: "shared-script", Name: "Secondary", VUsers: 1,
				RuntimeSettings: &RuntimeSettingsOverride{Env: map[string]string{"GROUP_TOKEN": "secondary-secret"}},
			},
		},
	}
	req := CreateTestRequest{Groups: []CreateGroupEntry{
		{Name: "Secondary", ScriptID: "shared-script", VUsers: 1, RuntimeSettings: maskRuntimeSettings(prior.Scripts[1].RuntimeSettings)},
		{Name: "Primary", ScriptID: "shared-script", VUsers: 1, RuntimeSettings: maskRuntimeSettings(prior.Scripts[0].RuntimeSettings)},
	}}

	got := createTestRequestFromScenario(req, prior)
	if value := got.Groups[0].RuntimeSettings.Env["GROUP_TOKEN"]; value != "secondary-secret" {
		t.Fatalf("reordered Secondary credential = %q, want secondary-secret", value)
	}
	if value := got.Groups[1].RuntimeSettings.Env["GROUP_TOKEN"]; value != "primary-secret" {
		t.Fatalf("reordered Primary credential = %q, want primary-secret", value)
	}
}

func TestCreateTestRequestFromScenarioRevisionUsesQueuedRunCredentialBaseline(t *testing.T) {
	scenario := &StoredScenario{
		Name: "Mutable scenario", Duration: "1m",
		RuntimeSettings: &RuntimeSettingsOverride{
			HTTP: &dsl.HTTPConfig{Auth: dsl.AuthConfig{Token: "scenario-current-token"}},
			Env:  map[string]string{"ROOT_TOKEN": "scenario-current-env"},
		},
		Scripts: []ScenarioScriptRef{{
			ScriptID: "script-a", Name: "Primary", VUsers: 1,
			RuntimeSettings: &RuntimeSettingsOverride{
				HTTP: &dsl.HTTPConfig{Auth: dsl.AuthConfig{Token: "scenario-current-group-token"}},
				Env:  map[string]string{"GROUP_TOKEN": "scenario-current-group-env"},
			},
		}},
	}
	source := &ManagedTest{
		ID: "queued-source", Name: "Queued source", Status: "QUEUED", DurationStr: "1m",
		RuntimeSettings: &RuntimeSettingsOverride{
			HTTP: &dsl.HTTPConfig{Auth: dsl.AuthConfig{Token: "queued-run-token"}},
			Env:  map[string]string{"ROOT_TOKEN": "queued-run-env"},
		},
		Groups: []TestGroup{{
			ScriptID: "script-a", Name: "Primary", VUsers: 1,
			RuntimeSettings: &RuntimeSettingsOverride{
				HTTP: &dsl.HTTPConfig{Auth: dsl.AuthConfig{Token: "queued-run-group-token"}},
				Env:  map[string]string{"GROUP_TOKEN": "queued-run-group-env"},
			},
		}},
	}
	req := CreateTestRequest{
		RuntimeSettings: maskRuntimeSettings(source.RuntimeSettings),
		Groups: []CreateGroupEntry{{
			ScriptID: "script-a", Name: "Primary", VUsers: 1,
			RuntimeSettings: maskRuntimeSettings(source.Groups[0].RuntimeSettings),
		}},
	}

	got := createTestRequestFromScenarioRevision(req, scenario, source)
	if got.RuntimeSettings == nil || got.RuntimeSettings.HTTP == nil || got.RuntimeSettings.HTTP.Auth.Token != "queued-run-token" || got.RuntimeSettings.Env["ROOT_TOKEN"] != "queued-run-env" {
		t.Fatalf("root revision credentials = %#v, want queued-run baseline", got.RuntimeSettings)
	}
	if group := got.Groups[0].RuntimeSettings; group == nil || group.HTTP == nil || group.HTTP.Auth.Token != "queued-run-group-token" || group.Env["GROUP_TOKEN"] != "queued-run-group-env" {
		t.Fatalf("group revision credentials = %#v, want queued-run baseline", group)
	}
}

func TestReplaceRuntimeFieldsCopiesCanonicalSectionValuesThroughCompactAliases(t *testing.T) {
	headed := false
	touch := true
	source := &RuntimeSettingsOverride{
		RunLogic: &dsl.RunLogicConfig{Iterations: 7, AutoTransactions: "per_iteration", WorkloadModel: "open"},
		Log:      &dsl.LogConfig{Level: "extended", OnErrorOnly: true},
		BrowserConfig: &dsl.BrowserConfig{
			Headless: &headed, DeviceProfile: "pixel_8", TouchMode: &touch, Orientation: "portrait",
		},
	}
	target := &RuntimeSettingsOverride{
		RunLogic: &dsl.RunLogicConfig{Iterations: 2, AutoTransactions: "none", MaxTransactions: 99},
		Log:      &dsl.LogConfig{Level: "standard", ExtendedOptions: []string{"parameter_substitution"}},
		BrowserConfig: &dsl.BrowserConfig{
			Engine: "firefox", DeviceProfile: "desktop",
		},
	}

	got := replaceRuntimeFields(target, source, []string{"iterations", "auto_transactions", "log_level", "browser"})
	if got.RunLogic == nil || got.RunLogic.Iterations != 7 || got.RunLogic.AutoTransactions != "per_iteration" || got.RunLogic.MaxTransactions != 99 {
		t.Fatalf("run_logic = %#v, want compact values copied with target sibling preserved", got.RunLogic)
	}
	if got.Iterations != nil || got.AutoTransactions != "" {
		t.Fatalf("legacy run aliases remained authoritative: iterations=%v auto_transactions=%q", got.Iterations, got.AutoTransactions)
	}
	if got.Log == nil || got.Log.Level != "extended" || len(got.Log.ExtendedOptions) != 1 {
		t.Fatalf("log = %#v, want canonical level copied with target sibling preserved", got.Log)
	}
	if got.BrowserConfig == nil || got.BrowserConfig.Engine != "firefox" || got.BrowserConfig.DeviceProfile != "pixel_8" || got.BrowserConfig.Headless == nil || *got.BrowserConfig.Headless || got.BrowserConfig.TouchMode == nil || !*got.BrowserConfig.TouchMode {
		t.Fatalf("browser_config = %#v, want compact browser fields copied with advanced target siblings", got.BrowserConfig)
	}
	if got.Browser != nil {
		t.Fatalf("legacy browser alias remained alongside canonical section: %#v", got.Browser)
	}
}

func TestApplyScenarioRuntimeToGroupsRejectsStaleHASnapshot(t *testing.T) {
	server, store := newRuntimeOverrideTestServer(t)
	server.runner.StoreScript(httpScript("script-ha-source", "Source"))
	server.runner.StoreScript(httpScript("script-ha-target", "Target"))

	scenario, err := server.runner.StoreScenario(CreateScenarioRequest{
		Name:     "HA runtime scenario",
		Duration: "1m",
		Groups: []CreateGroupEntry{
			{
				Name: "Source", ScriptID: "script-ha-source", VUsers: 1,
				RuntimeSettings: &RuntimeSettingsOverride{Pacing: &dsl.PacingConfig{Type: "fixed_delay", DelayMs: 250}},
			},
			{Name: "Target", ScriptID: "script-ha-target", VUsers: 1},
		},
	})
	if err != nil {
		t.Fatalf("StoreScenario: %v", err)
	}

	external, children, err := store.GetScenario(scenario.ID)
	if err != nil {
		t.Fatalf("GetScenario: %v", err)
	}
	external.Name = "Concurrent editor wins"
	if err := store.SaveScenario(external, children); err != nil {
		t.Fatalf("external SaveScenario: %v", err)
	}

	body, _ := json.Marshal(ApplyRuntimeToGroupsRequest{
		SourceGroup: 0, TargetGroups: []int{1}, Fields: []string{"pacing"},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scenarios/"+scenario.ID+"/runtime/apply-to-groups", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 body=%s", resp.Code, resp.Body.String())
	}

	durable, durableChildren, err := store.GetScenario(scenario.ID)
	if err != nil {
		t.Fatalf("GetScenario after conflict: %v", err)
	}
	if durable.Name != "Concurrent editor wins" {
		t.Fatalf("concurrent scenario edit was overwritten: %+v", durable)
	}
	if len(durableChildren) != 2 || durableChildren[1].RuntimeSettings != nil {
		t.Fatalf("stale runtime update changed durable children: %+v", durableChildren)
	}
}
