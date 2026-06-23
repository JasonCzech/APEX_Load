package api

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestScenarioGroupAssignmentPersistsIntoMaterializedTest(t *testing.T) {
	runner := NewTestRunner(10, NewHub(), nil)
	runner.StoreScript(&dsl.Script{
		ID:       "script-group-assignment",
		Name:     "JVM Flow",
		Protocol: "http",
		Version:  "1.0.0",
		Config:   dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions:  []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})

	scenario, err := runner.StoreScenario(CreateScenarioRequest{
		Name:     "Persisted Placement",
		Duration: "5m",
		Groups: []CreateGroupEntry{{
			Name:     "JVM Group",
			ScriptID: "script-group-assignment",
			VUsers:   5,
			Assignment: &GeneratorAssignmentSettings{
				Mode:            generatorAssignmentModeSpecificHosts,
				SelectedTargets: []string{"gen-jvm:9090"},
				RequiredLabels:  map[string]string{"jvm": "17"},
			},
		}},
	})
	if err != nil {
		t.Fatalf("StoreScenario returned error: %v", err)
	}
	if got := scenario.Scripts[0].Assignment; got == nil || got.Mode != generatorAssignmentModeSpecificHosts || got.SelectedTargets[0] != "gen-jvm:9090" || got.RequiredLabels["jvm"] != "17" {
		t.Fatalf("stored group assignment = %#v", got)
	}

	testRun, err := runner.CreateTest(CreateTestRequest{ScenarioID: scenario.ID})
	if err != nil {
		t.Fatalf("CreateTest returned error: %v", err)
	}
	if got := testRun.Groups[0].Assignment; got == nil || got.Mode != generatorAssignmentModeSpecificHosts || got.SelectedTargets[0] != "gen-jvm:9090" || got.RequiredLabels["jvm"] != "17" {
		t.Fatalf("materialized group assignment = %#v", got)
	}
}

func TestPlanDistributedGroupShardsAllowsGroupPinOutsideScenarioDefaultWithoutLeakage(t *testing.T) {
	groups := []TestGroup{
		{Name: "default-users", Protocol: "http", VUsers: 10},
		{
			Name:     "jvm-users",
			Protocol: "http",
			VUsers:   5,
			Assignment: &GeneratorAssignmentSettings{
				Mode:            generatorAssignmentModeSpecificHosts,
				SelectedTargets: []string{"gen-jvm"},
			},
		},
	}
	allLoad := map[string]generatorLoadSnapshot{
		"gen-default": {Target: "gen-default", AvailableCapacity: 100, SupportedProtocols: []string{"http"}},
		"gen-jvm":     {Target: "gen-jvm", AvailableCapacity: 1000, SupportedProtocols: []string{"http"}},
	}
	defaultLoad := map[string]generatorLoadSnapshot{
		"gen-default": allLoad["gen-default"],
	}
	shards := planDistributedGroupShardsForCandidates(
		groups,
		allLoad,
		defaultLoad,
		[]string{"gen-default", "gen-jvm"},
		[]string{"gen-default"},
		&GeneratorAssignmentSettings{Mode: generatorAssignmentModeSpecificHosts, SelectedTargets: []string{"gen-default"}},
		[]generatorTargetSpec{{Address: "gen-default", Protocols: []string{"http"}}, {Address: "gen-jvm", Protocols: []string{"http"}}},
	)

	if got := shards["default-users"][0].Target; got != "gen-default" {
		t.Fatalf("default-users target = %q, want gen-default (no leakage from group pin)", got)
	}
	if got := shards["jvm-users"][0].Target; got != "gen-jvm" {
		t.Fatalf("jvm-users target = %q, want gen-jvm", got)
	}
}

func TestPlanDistributedGroupShardsExplicitAutoOverridesScenarioSpecificHosts(t *testing.T) {
	groups := []TestGroup{{
		Name:       "auto-users",
		Protocol:   "http",
		VUsers:     10,
		Assignment: &GeneratorAssignmentSettings{Mode: generatorAssignmentModeAuto},
	}}
	allLoad := map[string]generatorLoadSnapshot{
		"gen-default": {Target: "gen-default", AvailableCapacity: 10, SupportedProtocols: []string{"http"}},
		"gen-spare":   {Target: "gen-spare", AvailableCapacity: 1000, SupportedProtocols: []string{"http"}},
	}
	defaultLoad := map[string]generatorLoadSnapshot{"gen-default": allLoad["gen-default"]}

	shards := planDistributedGroupShardsForCandidates(
		groups,
		allLoad,
		defaultLoad,
		[]string{"gen-default", "gen-spare"},
		[]string{"gen-default"},
		&GeneratorAssignmentSettings{Mode: generatorAssignmentModeSpecificHosts, SelectedTargets: []string{"gen-default"}},
		[]generatorTargetSpec{{Address: "gen-default", Protocols: []string{"http"}}, {Address: "gen-spare", Protocols: []string{"http"}}},
	)

	if got := shards["auto-users"][0].Target; got != "gen-spare" {
		t.Fatalf("auto-users target = %q, want gen-spare from explicit auto candidates", got)
	}
}

func TestPlanDistributedGroupShardsWeightedDefaultExcludesPinnedGroups(t *testing.T) {
	groups := []TestGroup{
		{
			Name:     "pinned-west",
			Protocol: "http",
			VUsers:   1000,
			Assignment: &GeneratorAssignmentSettings{
				Mode:            generatorAssignmentModeSpecificHosts,
				SelectedTargets: []string{"gen-west"},
			},
		},
		{Name: "inherited-a", Protocol: "http", VUsers: 10},
		{Name: "inherited-b", Protocol: "http", VUsers: 10},
	}
	allLoad := map[string]generatorLoadSnapshot{
		"gen-east": {Target: "gen-east", AvailableCapacity: 2000, SupportedProtocols: []string{"http"}},
		"gen-west": {Target: "gen-west", AvailableCapacity: 2000, SupportedProtocols: []string{"http"}},
	}
	assignment := &GeneratorAssignmentSettings{
		Mode:            generatorAssignmentModeLoadZoneWeighted,
		LoadZoneWeights: map[string]int{"east": 50, "west": 50},
	}
	specs := []generatorTargetSpec{
		{Address: "gen-east", Region: "east", Protocols: []string{"http"}},
		{Address: "gen-west", Region: "west", Protocols: []string{"http"}},
	}

	shards := planDistributedGroupShardsForCandidates(groups, allLoad, allLoad, []string{"gen-east", "gen-west"}, []string{"gen-east", "gen-west"}, assignment, specs)

	if got := shards["pinned-west"][0].Target; got != "gen-west" {
		t.Fatalf("pinned-west target = %q, want gen-west", got)
	}
	inheritedTargets := map[string]bool{
		shards["inherited-a"][0].Target: true,
		shards["inherited-b"][0].Target: true,
	}
	if !inheritedTargets["gen-east"] || !inheritedTargets["gen-west"] {
		t.Fatalf("inherited targets = %#v, want weighted split across east and west excluding pinned demand", inheritedTargets)
	}
}

func TestPlanDistributedGroupShardsMatchesRequiredLabels(t *testing.T) {
	groups := []TestGroup{{
		Name:     "jvm-users",
		Protocol: "http",
		VUsers:   10,
		Assignment: &GeneratorAssignmentSettings{
			Mode:           generatorAssignmentModeAuto,
			RequiredLabels: map[string]string{"jvm": "17"},
		},
	}}
	allLoad := map[string]generatorLoadSnapshot{
		"gen-old": {Target: "gen-old", AvailableCapacity: 100, SupportedProtocols: []string{"http"}},
		"gen-new": {Target: "gen-new", AvailableCapacity: 100, SupportedProtocols: []string{"http"}},
	}
	specs := []generatorTargetSpec{
		{Address: "gen-old", Protocols: []string{"http"}, Labels: map[string]string{"jvm": "11"}},
		{Address: "gen-new", Protocols: []string{"http"}, Labels: map[string]string{"jvm": "17"}},
	}

	shards := planDistributedGroupShardsForCandidates(groups, allLoad, allLoad, []string{"gen-old", "gen-new"}, []string{"gen-old", "gen-new"}, nil, specs)

	if got := shards["jvm-users"][0].Target; got != "gen-new" {
		t.Fatalf("jvm-users target = %q, want gen-new", got)
	}
}
