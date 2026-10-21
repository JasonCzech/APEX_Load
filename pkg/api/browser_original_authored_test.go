package api

import (
	"encoding/json"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func originalAuthoredSourceFixture() *dsl.Script {
	return &dsl.Script{ID: "browser-original", Name: "Original", Protocol: "browser", Config: dsl.ScriptConfig{BaseURL: "https://example.test", Pacing: dsl.PacingConfig{Type: "none"}}, Actions: []dsl.Action{{Name: "open", Type: "browser_navigate", Target: "/"}}}
}
func originalAuthoredRunner(source *dsl.Script) *TestRunner {
	return &TestRunner{tests: map[string]*ManagedTest{}, scripts: map[string]*StoredScript{source.ID: {ID: source.ID, Name: source.Name, Protocol: source.Protocol, Script: source}}}
}
func TestBrowserOriginalSourcePersistsAcrossCompiledRestart(t *testing.T) {
	source := originalAuthoredSourceFixture()
	want := engine.BrowserOriginalAuthoredDefinition(source)
	runner := originalAuthoredRunner(source)
	group, spec, err := runner.compileManagedTestGroupLocked(0, 1, CreateGroupEntry{Name: "browser", ScriptID: source.ID, VUsers: 1}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !want.Valid() || group.browserAuthoredDefinition != want || spec.BrowserAuthoredDefinition != want {
		t.Fatal("creation lost original source")
	}
	source.Actions[0].Target = "/changed-after-creation"
	groups := decodeStoredGroups(encodeStoredGroups([]TestGroup{group}))
	test := &ManagedTest{ID: "original-run", TotalVUsers: 1, Groups: groups}
	if err := runner.restoreManagedTestRuntime(test); err != nil {
		t.Fatal(err)
	}
	if test.runtimeSnapshotRepairRequired || test.Groups[0].browserAuthoredDefinition != want || test.groupSpecs[0].BrowserAuthoredDefinition != want {
		t.Fatal("compiled restart consulted mutable source or lost original proof")
	}
	provenance := RunArchiveProvenance{BrowserAuthoredSources: browserAuthoredDefinitionsForGroups(test.Groups)}
	masked := maskRunArchiveProvenance(provenance)
	if masked.BrowserAuthoredSources["browser"] != want {
		t.Fatal("archive lost proof")
	}
	delete(masked.BrowserAuthoredSources, "browser")
	if provenance.BrowserAuthoredSources["browser"] != want {
		t.Fatal("masked archive aliases source")
	}
}
func TestBrowserOriginalSourceNotInventedDuringLegacyRepair(t *testing.T) {
	for _, previouslyBound := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "changed-library"}[previouslyBound], func(t *testing.T) {
			source := originalAuthoredSourceFixture()
			var definition protocol.BrowserAuthoredDefinition
			if previouslyBound {
				definition = engine.BrowserOriginalAuthoredDefinition(source)
			}
			source.Actions[0].Target = "/new-library-source"
			runner := originalAuthoredRunner(source)
			test := &ManagedTest{ID: "legacy-run", TotalVUsers: 1, Groups: []TestGroup{{Name: "browser", ScriptID: source.ID, Protocol: "browser", VUsers: 1, browserAuthoredDefinition: definition}}}
			if err := runner.restoreManagedTestRuntime(test); err != nil {
				t.Fatal(err)
			}
			if !test.runtimeSnapshotRepairRequired || test.Groups[0].browserAuthoredDefinition.Valid() || test.groupSpecs[0].BrowserAuthoredDefinition.Valid() {
				t.Fatal("mutable repair manufactured historical proof")
			}
			if browserAuthoredDefinitionsForGroups(test.Groups) != nil {
				t.Fatal("legacy source published in provenance")
			}
		})
	}
}
func TestBrowserOriginalSourceCannotEnterPublicGroupJSON(t *testing.T) {
	source := originalAuthoredSourceFixture()
	definition := engine.BrowserOriginalAuthoredDefinition(source)
	body, err := json.Marshal(map[string]any{"browser_authored_definition": definition})
	if err != nil {
		t.Fatal(err)
	}
	var group TestGroup
	if err := json.Unmarshal(body, &group); err != nil {
		t.Fatal(err)
	}
	if group.browserAuthoredDefinition.Valid() {
		t.Fatal("public JSON minted source proof")
	}
	raw, err := json.Marshal(encodeStoredGroups([]TestGroup{group}))
	if err != nil {
		t.Fatal(err)
	}
	var values []map[string]any
	if err := json.Unmarshal(raw, &values); err != nil {
		t.Fatal(err)
	}
	if _, ok := values[0]["browser_authored_definition"]; ok {
		t.Fatal("legacy serialization claims a descriptor")
	}
}
