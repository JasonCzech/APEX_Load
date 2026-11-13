package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// A compact iterations alias in a partial override is applied by
// applyRuntimeOverridesPartial, so a debug start must run that many
// iterations even when override_fields lists only an unrelated path.
func TestPartialOverrideDebugStartHonorsCompactIterationsInPartialOverride(t *testing.T) {
	executed := 0
	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		executed++
		return &protocol.Result{StatusCode: 200, Success: true}, nil
	})
	server := NewServer(ServerConfig{})
	stored := server.runner.StoreScript(&dsl.Script{
		Name:     "Compact Iterations Debug Target",
		Version:  "1.0.0",
		Protocol: protocolName,
		Config: dsl.ScriptConfig{
			BaseURL:  "https://stored.example.test",
			Log:      dsl.LogConfig{Level: "info"},
			RunLogic: dsl.RunLogicConfig{Iterations: 1},
		},
		Actions: []dsl.Action{{Name: "step-1", Type: "GET", Method: "GET", Target: "/items"}},
	})
	if stored == nil {
		t.Fatal("StoreScript returned nil")
	}
	three := 3
	const sessionID = "55555555-5555-4555-8555-5555555555a3"
	body, err := json.Marshal(ScriptDebugStartRequest{
		ReplaySessionID: sessionID,
		RuntimeOverrides: &RuntimeSettingsOverride{
			Iterations:     &three,
			Log:            &dsl.LogConfig{Level: "debug"},
			OverrideFields: []string{"log.level"},
		},
	})
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/"+stored.ID+"/debug/sessions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", resp.Code, resp.Body.String())
	}
	session := waitForScriptDebugStatus(t, server.scriptDebug, sessionID, ScriptDebugCompleted)
	if session.LogLevel != "debug" {
		t.Fatalf("session.LogLevel = %q, want debug from the listed path", session.LogLevel)
	}
	if executed != 3 {
		t.Fatalf("executed actions = %d, want 3 from the compact iterations alias", executed)
	}

	// The predicate itself: alias applies, unrelated-only partial does not,
	// legacy v1 keeps its value-based rule.
	if !runtimeOverridesSpecifiesIterations(&RuntimeSettingsOverride{Iterations: &three, OverrideFields: []string{"pacing.type"}}) {
		t.Fatal("compact iterations alias beside an unrelated override_fields entry not reported")
	}
	if runtimeOverridesSpecifiesIterations(&RuntimeSettingsOverride{RunLogic: &dsl.RunLogicConfig{Iterations: 4}, OverrideFields: []string{"pacing.type"}}) {
		t.Fatal("unlisted run_logic.iterations reported as specified")
	}
	if !runtimeOverridesSpecifiesIterations(&RuntimeSettingsOverride{RunLogic: &dsl.RunLogicConfig{Iterations: 4}}) {
		t.Fatal("legacy run_logic.iterations not reported")
	}
}

// A Command Center PATCH whose only change is listing a new override_fields
// path must be applied and persisted, not reported as no change.
func TestPartialOverridePatchOverrideFieldsOnlyEditPersists(t *testing.T) {
	server, store := newRuntimeOverrideTestServer(t)
	server.runner.StoreScript(httpScript("script-a", "Checkout"))
	test, err := server.runner.CreateTest(CreateTestRequest{
		Name:     "override_fields-only edit",
		Duration: "5m",
		Groups: []CreateGroupEntry{{
			Name:     "Buyers",
			ScriptID: "script-a",
			VUsers:   2,
			RuntimeSettings: &RuntimeSettingsOverride{
				Pacing:         &dsl.PacingConfig{Type: "fixed_delay", DelayMs: 500},
				ThinkTime:      &dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 250},
				OverrideFields: []string{"pacing.type", "think_time.type"},
			},
		}},
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	if test.Status != "QUEUED" {
		t.Fatalf("status = %q, want QUEUED", test.Status)
	}
	view, err := server.runner.GetTestGroupRuntimeSettingsView(test.ID, "Buyers")
	if err != nil {
		t.Fatalf("GetTestGroupRuntimeSettingsView: %v", err)
	}
	if view.RuntimeSettings == nil || len(view.RuntimeSettings.OverrideFields) == 0 {
		t.Fatalf("stored group settings = %#v, want a partial row", view.RuntimeSettings)
	}
	if slices.Contains(view.RuntimeSettings.OverrideFields, "think_time.ignore") {
		t.Fatalf("precondition: think_time.ignore already listed: %v", view.RuntimeSettings.OverrideFields)
	}
	incoming := cloneRuntimeSettings(view.RuntimeSettings)
	if incoming.ThinkTime == nil {
		incoming.ThinkTime = &dsl.ThinkTimeConfig{}
	}
	incoming.OverrideFields = append(incoming.OverrideFields, "think_time.ignore")

	result, err := server.runner.UpdateTestGroupRuntimeSettings(test.ID, "Buyers", view.Revision, incoming, []string{"think_time"})
	if err != nil {
		t.Fatalf("UpdateTestGroupRuntimeSettings: %v", err)
	}
	if result.NoChanges {
		t.Fatal("override_fields-only edit reported no_changes")
	}
	if result.Revision == "" || result.Revision == view.Revision {
		t.Fatalf("revision = %q, want a bump from %q", result.Revision, view.Revision)
	}
	if after := mustGroupSettingsRevision(t, server.runner, test.ID, "Buyers"); after == view.Revision {
		t.Fatalf("view revision did not change: %q", after)
	}
	managed, ok := server.runner.GetTest(test.ID)
	if !ok {
		t.Fatal("test disappeared")
	}
	group := managed.Groups[0].RuntimeSettings
	if group == nil || !slices.Contains(group.OverrideFields, "think_time.ignore") {
		t.Fatalf("in-memory override_fields = %#v, want think_time.ignore", group)
	}
	if !slices.Contains(group.OverrideFields, "pacing.type") {
		t.Fatalf("in-memory override_fields lost the sibling pacing.type: %v", group.OverrideFields)
	}
	record, err := store.GetTest(test.ID)
	if err != nil {
		t.Fatalf("store.GetTest: %v", err)
	}
	storedGroups := decodeStoredGroups(record.Groups)
	if len(storedGroups) != 1 || storedGroups[0].RuntimeSettings == nil ||
		!slices.Contains(storedGroups[0].RuntimeSettings.OverrideFields, "think_time.ignore") {
		t.Fatalf("persisted group settings = %#v, want think_time.ignore in override_fields", storedGroups)
	}
}
