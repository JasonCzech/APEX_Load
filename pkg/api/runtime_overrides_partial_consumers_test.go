package api

import (
	"slices"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// A listed error_handling.continue_on_error with no value is "not set", so the
// stored (protected) layer must keep it nil and the policy must keep the
// scenario default instead of reading an explicit opt-out.
func TestPartialOverrideListedNilContinueOnErrorStaysUnset(t *testing.T) {
	raw := &RuntimeSettingsOverride{
		ErrorHandling:  &dsl.ErrorHandlingConfig{},
		OverrideFields: []string{"error_handling.continue_on_error"},
	}
	protected, _, err := protectRuntimeOverrideSecrets("scope", 0, nil, raw, false)
	if err != nil {
		t.Fatalf("protect: %v", err)
	}
	if protected.ErrorHandling != nil && protected.ErrorHandling.ContinueOnError != nil {
		t.Fatalf("protected continue_on_error = %v, want nil (unset)", *protected.ErrorHandling.ContinueOnError)
	}
	script := &dsl.Script{}
	rawResolution := resolveGroupContinueOnError(nil, raw, script)
	storedResolution := resolveGroupContinueOnError(nil, protected, script)
	if rawResolution != storedResolution {
		t.Fatalf("raw %#v != stored %#v", rawResolution, storedResolution)
	}
	if !storedResolution.Effective || storedResolution.Source != continueOnErrorSourceScenarioDefault {
		t.Fatalf("stored resolution = %#v, want effective true from scenario_default", storedResolution)
	}
	// Scenario-level: the same clear must not disable continue-on-error for
	// every group.
	scenarioProtected, _, err := protectRuntimeOverrideSecrets("scope", -1, nil, raw, false)
	if err != nil {
		t.Fatalf("protect scenario: %v", err)
	}
	if value, explicit := scenarioContinueOnErrorPolicy(scenarioProtected); !value || explicit {
		t.Fatalf("scenario policy = (%v, %v), want default (true, false)", value, explicit)
	}
	// An explicit false that is listed is still an operator opt-out.
	optOut := &RuntimeSettingsOverride{
		ErrorHandling:  &dsl.ErrorHandlingConfig{ContinueOnError: dsl.ContinueOnErrorValue(false)},
		OverrideFields: []string{"error_handling.continue_on_error"},
	}
	optOutProtected, _, err := protectRuntimeOverrideSecrets("scope", 0, nil, optOut, false)
	if err != nil {
		t.Fatalf("protect opt-out: %v", err)
	}
	if got := resolveGroupContinueOnError(nil, optOutProtected, script); got.Effective || got.Source != continueOnErrorSourceGroup {
		t.Fatalf("explicit opt-out resolution = %#v, want false from group", got)
	}
}

// A partial layer that carries error_handling.continue_on_error without
// listing it states nothing: raw and stored layers must resolve the same.
func TestPartialOverrideUnlistedContinueOnErrorIgnoredByPolicy(t *testing.T) {
	raw := &RuntimeSettingsOverride{
		ErrorHandling:  &dsl.ErrorHandlingConfig{ContinueOnError: dsl.ContinueOnErrorValue(false)},
		Pacing:         &dsl.PacingConfig{Type: "fixed_delay", DelayMs: 100},
		OverrideFields: []string{"pacing.type"},
	}
	script := &dsl.Script{}
	rawResolution := resolveGroupContinueOnError(nil, raw, script)
	if !rawResolution.Effective || rawResolution.Source != continueOnErrorSourceScenarioDefault || rawResolution.Conflict {
		t.Fatalf("raw resolution = %#v, want effective true from scenario_default", rawResolution)
	}
	protected, _, err := protectRuntimeOverrideSecrets("scope", 0, nil, raw, false)
	if err != nil {
		t.Fatalf("protect: %v", err)
	}
	if got := resolveGroupContinueOnError(nil, protected, script); got != rawResolution {
		t.Fatalf("stored resolution %#v != raw %#v", got, rawResolution)
	}
	if value, explicit := scenarioContinueOnErrorPolicy(raw); !value || explicit {
		t.Fatalf("scenario policy = (%v, %v), want default (true, false)", value, explicit)
	}
	// Legacy v1 (no override_fields) keeps whole-section semantics.
	legacy := &RuntimeSettingsOverride{ErrorHandling: &dsl.ErrorHandlingConfig{ContinueOnError: dsl.ContinueOnErrorValue(false)}}
	if got := resolveGroupContinueOnError(nil, legacy, script); got.Effective || got.Source != continueOnErrorSourceGroup {
		t.Fatalf("legacy resolution = %#v, want false from group", got)
	}
}

// Command Center change detection must see an edit that only lists or
// unlists a path, because under partial semantics the list decides what runs.
func TestPartialOverrideRuntimeFieldChangesSeeOverrideFieldsOnlyEdits(t *testing.T) {
	stored := &RuntimeSettingsOverride{
		ThinkTime:      &dsl.ThinkTimeConfig{Type: "recorded", Multiplier: 2},
		OverrideFields: []string{"think_time.multiplier"},
	}
	incoming := &RuntimeSettingsOverride{
		ThinkTime:      &dsl.ThinkTimeConfig{Type: "recorded", Multiplier: 2},
		OverrideFields: []string{"think_time.multiplier", "think_time.ignore"},
	}
	merged := replaceRuntimeFields(stored, incoming, []string{"think_time"})
	if !slices.Contains(merged.OverrideFields, "think_time.ignore") {
		t.Fatalf("merged override_fields = %v, want think_time.ignore", merged.OverrideFields)
	}
	changes := runtimeSettingsFieldChanges(stored, merged, []string{"think_time"})
	change, ok := changes["think_time"].(map[string]any)
	if !ok {
		t.Fatalf("changes = %#v, want a think_time change for a newly listed path", changes)
	}
	if _, ok := change["override_fields"]; !ok {
		t.Fatalf("think_time change = %#v, want override_fields from/to", change)
	}

	// Reset to inherited of a zero-valued path is a change too.
	reset := replaceRuntimeFields(merged, stored, []string{"think_time"})
	if changes := runtimeSettingsFieldChanges(merged, reset, []string{"think_time"}); len(changes) == 0 {
		t.Fatalf("unlisting think_time.ignore reported no change")
	}

	// Paths owned by other fields do not count as a change to this one.
	sibling := cloneRuntimeSettings(stored)
	sibling.OverrideFields = append(sibling.OverrideFields, "pacing.type")
	if changes := runtimeSettingsFieldChanges(stored, sibling, []string{"think_time"}); len(changes) != 0 {
		t.Fatalf("sibling path edit reported think_time changes: %#v", changes)
	}

	// Legacy rows keep pure value comparison.
	legacy := &RuntimeSettingsOverride{ThinkTime: &dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 10}}
	if changes := runtimeSettingsFieldChanges(legacy, cloneRuntimeSettings(legacy), []string{"think_time"}); len(changes) != 0 {
		t.Fatalf("legacy identical edit reported changes: %#v", changes)
	}
}

// A partial override listing a run_logic sibling must not be read as
// iterations=0, so a compact groups[].iterations is accepted.
func TestPartialOverridePartialRunLogicSiblingDoesNotClaimIterations(t *testing.T) {
	server, _ := newRuntimeOverrideTestServer(t)
	server.runner.StoreScript(httpScript("script-a", "Group A"))
	five := 5
	test, err := server.runner.CreateTest(CreateTestRequest{
		Name:     "partial run_logic",
		Duration: "5m",
		Groups: []CreateGroupEntry{{
			Name:       "A",
			ScriptID:   "script-a",
			VUsers:     1,
			Iterations: &five,
			RuntimeSettings: &RuntimeSettingsOverride{
				RunLogic:       &dsl.RunLogicConfig{WorkloadModel: "closed"},
				OverrideFields: []string{"run_logic.workload_model"},
			},
		}},
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	if got := test.Groups[0].Iterations; got == nil || *got != 5 {
		t.Fatalf("group iterations = %v, want 5", got)
	}

	if value, present := runtimeSettingsIterationValue(&RuntimeSettingsOverride{
		RunLogic:       &dsl.RunLogicConfig{WorkloadModel: "closed"},
		OverrideFields: []string{"run_logic.workload_model"},
	}); present {
		t.Fatalf("iteration value = (%d, true), want not present", value)
	}

	// The compact runtime_settings.iterations alias beside an unlisted zero
	// run_logic.iterations is folded into the canonical section, not dropped.
	three := 3
	test, err = server.runner.CreateTest(CreateTestRequest{
		Name:     "partial compact iterations",
		Duration: "5m",
		Groups: []CreateGroupEntry{{
			Name:     "B",
			ScriptID: "script-a",
			VUsers:   1,
			RuntimeSettings: &RuntimeSettingsOverride{
				RunLogic:       &dsl.RunLogicConfig{WorkloadModel: "closed"},
				Iterations:     &three,
				OverrideFields: []string{"pacing.type"},
			},
		}},
	})
	if err != nil {
		t.Fatalf("CreateTest compact: %v", err)
	}
	if got := test.Groups[0].Iterations; got == nil || *got != 3 {
		t.Fatalf("compact group iterations = %v, want 3", got)
	}

	// Legacy: a run_logic section with iterations 0 still conflicts.
	_, err = server.runner.CreateTest(CreateTestRequest{
		Name:     "legacy conflict",
		Duration: "5m",
		Groups: []CreateGroupEntry{{
			Name:            "C",
			ScriptID:        "script-a",
			VUsers:          1,
			Iterations:      &five,
			RuntimeSettings: &RuntimeSettingsOverride{RunLogic: &dsl.RunLogicConfig{WorkloadModel: "closed"}},
		}},
	})
	if err == nil {
		t.Fatalf("legacy run_logic{iterations:0} with groups[].iterations=5 was accepted")
	}

	// An unlisted open workload model does not make the group open-model.
	if runtimeOverrideDeclaresOpenModel(&RuntimeSettingsOverride{
		RunLogic:       &dsl.RunLogicConfig{WorkloadModel: "open"},
		OverrideFields: []string{"run_logic.iterations"},
	}) {
		t.Fatalf("unlisted workload_model=open counted as open model")
	}
	if !runtimeOverrideDeclaresOpenModel(&RuntimeSettingsOverride{RunLogic: &dsl.RunLogicConfig{WorkloadModel: "open"}}) {
		t.Fatalf("legacy workload_model=open not counted as open model")
	}
}

// Capacity estimates must apply a partial override like execution does.
func TestPartialOverrideCapacityHonorsPartialOverrides(t *testing.T) {
	config := dsl.ScriptConfig{
		Log:           dsl.LogConfig{Level: "extended"},
		ErrorHandling: dsl.ErrorHandlingConfig{SnapshotOnError: true},
	}
	scriptOnly := runtimeOverheadScore(config, nil)
	partial := &RuntimeSettingsOverride{
		ErrorHandling:  &dsl.ErrorHandlingConfig{MaxRetries: 2},
		OverrideFields: []string{"error_handling.max_retries"},
	}
	if got := runtimeOverheadScore(config, mergeRuntimeSettings(nil, partial)); got != scriptOnly {
		t.Fatalf("partial override score = %v, want script-only %v", got, scriptOnly)
	}
	if !config.ErrorHandling.SnapshotOnError || config.ErrorHandling.MaxRetries != 0 {
		t.Fatalf("score mutated the script config: %#v", config.ErrorHandling)
	}

	script := httpScript("s", "S")
	script.Config.HTTP.Version = "3"
	group := TestGroup{Protocol: "http", script: script, RuntimeSettings: &RuntimeSettingsOverride{
		HTTP:           &dsl.HTTPConfig{RequestTimeoutMs: 5000},
		OverrideFields: []string{"http.request_timeout_ms"},
	}}
	if got := capacityClassForGroup(group, nil); got != capacityClassHTTP3 {
		t.Fatalf("partial http override class = %q, want %q", got, capacityClassHTTP3)
	}
	if script.Config.HTTP.RequestTimeoutMs != 0 {
		t.Fatalf("capacity class mutated the group script: %#v", script.Config.HTTP)
	}
	// Legacy whole-section replacement still clears the script version.
	legacyGroup := TestGroup{Protocol: "http", script: script, RuntimeSettings: &RuntimeSettingsOverride{
		HTTP: &dsl.HTTPConfig{RequestTimeoutMs: 5000},
	}}
	if got := capacityClassForGroup(legacyGroup, nil); got != "http" {
		t.Fatalf("legacy http override class = %q, want http", got)
	}
}

// Archive attribution names the SOAP/WebSocket sections and, for a partial
// override, exactly the listed paths.
func TestPartialOverrideArchiveRuntimeOverrideFieldNames(t *testing.T) {
	soapOnly := &RuntimeSettingsOverride{SOAP: &dsl.SOAPConfig{Version: "1.2"}}
	if got := runtimeOverrideFieldNames(soapOnly); !slices.Equal(got, []string{"soap"}) {
		t.Fatalf("soap-only field names = %v, want [soap]", got)
	}
	wsOnly := &RuntimeSettingsOverride{WebSocket: &dsl.WebSocketConfig{}}
	if got := runtimeOverrideFieldNames(wsOnly); !slices.Equal(got, []string{"websocket"}) {
		t.Fatalf("websocket-only field names = %v, want [websocket]", got)
	}
	partial := &RuntimeSettingsOverride{
		SOAP:           &dsl.SOAPConfig{Version: "1.2"},
		HTTP:           &dsl.HTTPConfig{MaxMetaRefresh: 3},
		OverrideFields: []string{"soap.version"},
	}
	if got := runtimeOverrideFieldNames(partial); !slices.Equal(got, []string{"soap.version"}) {
		t.Fatalf("partial field names = %v, want [soap.version]", got)
	}
}
