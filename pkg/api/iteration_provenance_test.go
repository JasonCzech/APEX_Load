package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestQueuedRevisionPreservesIterationOverrideProvenance(t *testing.T) {
	for _, tc := range []struct {
		name                string
		sourceOverride      *int
		wantSourceEffective int
		wantRevisionValue   int
	}{
		{name: "inherited root iterations remain inherited", wantSourceEffective: 5, wantRevisionValue: 10},
		{name: "explicit group iterations remain pinned", sourceOverride: iterationIntPointer(3), wantSourceEffective: 3, wantRevisionValue: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, _ := newRuntimeOverrideTestServer(t)
			script := httpScript("iteration-provenance-script", "Iteration provenance")
			if _, err := server.runner.StoreScriptDurable(script); err != nil {
				t.Fatalf("StoreScriptDurable: %v", err)
			}

			source, err := server.runner.CreateTest(CreateTestRequest{
				Name: "Iteration source", Duration: "1m",
				RuntimeSettings: &RuntimeSettingsOverride{RunLogic: &dsl.RunLogicConfig{Iterations: 5}},
				Groups: []CreateGroupEntry{{
					ScriptID: script.ID, VUsers: 1, Iterations: cloneIntPtr(tc.sourceOverride),
				}},
			})
			if err != nil {
				t.Fatalf("CreateTest(source): %v", err)
			}
			if got := intPointerValue(source.Groups[0].Iterations); got != tc.wantSourceEffective {
				t.Fatalf("source effective iterations = %d, want %d", got, tc.wantSourceEffective)
			}
			if !source.Groups[0].IterationOverrideKnown || !intPointersEqual(source.Groups[0].IterationOverride, tc.sourceOverride) {
				t.Fatalf("source override provenance = known %t value %v, want known true value %v", source.Groups[0].IterationOverrideKnown, source.Groups[0].IterationOverride, tc.sourceOverride)
			}

			sourceIndex := 0
			revision, err := server.runner.CreateTest(CreateTestRequest{
				Name: "Iteration revision", RevisionSourceTestID: source.ID,
				RuntimeSettings: &RuntimeSettingsOverride{RunLogic: &dsl.RunLogicConfig{Iterations: 10}},
				Groups: []CreateGroupEntry{{
					ScriptID: source.Groups[0].ScriptID, VUsers: 1,
					RevisionSourceGroupIndex: &sourceIndex,
					Iterations:               cloneIntPtr(source.Groups[0].IterationOverride),
				}},
			})
			if err != nil {
				t.Fatalf("CreateTest(revision): %v", err)
			}
			if got := intPointerValue(revision.Groups[0].Iterations); got != tc.wantRevisionValue {
				t.Fatalf("revision effective iterations = %d, want %d", got, tc.wantRevisionValue)
			}
			if !revision.Groups[0].IterationOverrideKnown || !intPointersEqual(revision.Groups[0].IterationOverride, tc.sourceOverride) {
				t.Fatalf("revision override provenance = known %t value %v, want known true value %v", revision.Groups[0].IterationOverrideKnown, revision.Groups[0].IterationOverride, tc.sourceOverride)
			}
			if got := revision.groupSpecs[0].Script.Config.RunLogic.Iterations; got != tc.wantRevisionValue {
				t.Fatalf("compiled revision iterations = %d, want %d", got, tc.wantRevisionValue)
			}
		})
	}
}

func TestQueuedRevisionFailsClosedForLegacyIterationProvenance(t *testing.T) {
	server, _ := newRuntimeOverrideTestServer(t)
	script := httpScript("legacy-iteration-script", "Legacy iteration source")
	if _, err := server.runner.StoreScriptDurable(script); err != nil {
		t.Fatalf("StoreScriptDurable: %v", err)
	}
	source, err := server.runner.CreateTest(CreateTestRequest{
		Name: "Legacy iteration source", Duration: "1m",
		RuntimeSettings: &RuntimeSettingsOverride{RunLogic: &dsl.RunLogicConfig{Iterations: 5}},
		Groups:          []CreateGroupEntry{{ScriptID: script.ID, VUsers: 1}},
	})
	if err != nil {
		t.Fatalf("CreateTest(source): %v", err)
	}
	// Simulate a rollout-era persisted group: only the effective value survived.
	server.runner.mu.Lock()
	legacySource := server.runner.tests[source.ID]
	legacySource.Groups[0].IterationOverride = nil
	legacySource.Groups[0].IterationOverrideKnown = false
	server.runner.mu.Unlock()

	sourceIndex := 0
	exact, err := server.runner.CreateTest(CreateTestRequest{
		Name: "Exact legacy copy", RevisionSourceTestID: source.ID,
		Groups: []CreateGroupEntry{{
			ScriptID: source.Groups[0].ScriptID, VUsers: 1, RevisionSourceGroupIndex: &sourceIndex,
		}},
	})
	if err != nil {
		t.Fatalf("CreateTest(exact legacy revision): %v", err)
	}
	if exact.Groups[0].IterationOverrideKnown || intPointerValue(exact.Groups[0].Iterations) != 5 || exact.groupSpecs[0].Script.Config.RunLogic.Iterations != 5 {
		t.Fatalf("exact legacy revision changed provenance or execution: group=%+v compiled=%d", exact.Groups[0], exact.groupSpecs[0].Script.Config.RunLogic.Iterations)
	}

	_, err = server.runner.CreateTest(CreateTestRequest{
		Name: "Ambiguous legacy edit", RevisionSourceTestID: source.ID,
		RuntimeSettings: &RuntimeSettingsOverride{RunLogic: &dsl.RunLogicConfig{Iterations: 10}},
		Groups: []CreateGroupEntry{{
			ScriptID: source.Groups[0].ScriptID, VUsers: 1, RevisionSourceGroupIndex: &sourceIndex,
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "lacks iteration-override provenance") {
		t.Fatalf("legacy root iteration edit error = %v, want provenance failure", err)
	}
}

func TestStoredGroupsRoundTripIterationOverrideProvenance(t *testing.T) {
	seven := 7
	groups := []TestGroup{
		{ScriptID: "known-inherited", Iterations: &seven, IterationOverrideKnown: true},
		{ScriptID: "legacy-unknown", Iterations: &seven},
	}
	payload, err := json.Marshal(encodeStoredGroups(groups))
	if err != nil {
		t.Fatalf("marshal stored groups: %v", err)
	}
	var stored any
	if err := json.Unmarshal(payload, &stored); err != nil {
		t.Fatalf("unmarshal stored groups: %v", err)
	}
	decoded := decodeStoredGroups(stored)
	if len(decoded) != 2 {
		t.Fatalf("decoded groups = %d, want 2", len(decoded))
	}
	if !decoded[0].IterationOverrideKnown || decoded[0].IterationOverride != nil {
		t.Fatalf("known inherited provenance changed: %+v", decoded[0])
	}
	if decoded[1].IterationOverrideKnown || intPointerValue(decoded[1].Iterations) != seven {
		t.Fatalf("legacy unknown provenance changed: %+v", decoded[1])
	}
}

func intPointerValue(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func iterationIntPointer(value int) *int {
	return &value
}
