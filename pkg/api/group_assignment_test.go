package api

import (
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestScenarioPoolAssignmentRoundTripsIntoMaterializedTest(t *testing.T) {
	server, store := newTimeslotServer(t)
	const poolID = "tier0-pool"
	if err := store.SaveFleetPool(&storage.FleetPool{ID: poolID, Name: "Tier-0 Pool", CloudProvider: "kubernetes"}); err != nil {
		t.Fatalf("SaveFleetPool: %v", err)
	}
	if err := store.SaveFleetPoolMembers(poolID, []*storage.FleetPoolMember{{ID: "tier0-member", PoolID: poolID, GeneratorID: "tier0-generator", Target: "tier0-generator:9090"}}); err != nil {
		t.Fatalf("SaveFleetPoolMembers: %v", err)
	}
	server.runner.StoreScript(&dsl.Script{
		ID: "script-pool-assignment", Name: "Pool Flow", Protocol: "http", Version: "1.0.0",
		Config: dsl.ScriptConfig{BaseURL: "https://example.test"}, Actions: []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})

	scenario, err := server.runner.StoreScenario(CreateScenarioRequest{
		Name: "Pool Placement", Duration: "5m",
		Assignment: &GeneratorAssignmentSettings{Mode: generatorAssignmentModeAuto, PoolID: poolID},
		Groups:     []CreateGroupEntry{{Name: "users", ScriptID: "script-pool-assignment", VUsers: 2}},
	})
	if err != nil {
		t.Fatalf("StoreScenario: %v", err)
	}
	stored, ok := server.runner.GetScenario(scenario.ID)
	if !ok || stored == nil {
		t.Fatal("stored scenario is missing")
	}
	if stored.Assignment == nil || stored.Assignment.PoolID != poolID {
		t.Fatalf("stored scenario assignment = %#v, want pool_id %q", stored.Assignment, poolID)
	}

	run, err := server.runner.CreateTest(CreateTestRequest{ScenarioID: scenario.ID})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	if run.Assignment == nil || run.Assignment.PoolID != poolID {
		t.Fatalf("materialized assignment = %#v, want pool_id %q", run.Assignment, poolID)
	}
	if run.Assignment.Mode != generatorAssignmentModeSpecificHosts || len(run.Assignment.SelectedTargets) != 1 || run.Assignment.SelectedTargets[0] != "tier0-generator:9090" {
		t.Fatalf("resolved materialized assignment = %#v, want the pool target", run.Assignment)
	}
}

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

func TestScenarioGroupStartOffsetPersistsIntoMaterializedTest(t *testing.T) {
	runner := NewTestRunner(10, NewHub(), nil)
	runner.StoreScript(&dsl.Script{
		ID: "script-offset", Name: "Offset Flow", Protocol: "http", Version: "1.0.0",
		Config:  dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})
	scenario, err := runner.StoreScenario(CreateScenarioRequest{
		Name: "Timed Composition", Duration: "5m",
		Groups: []CreateGroupEntry{{Name: "later", ScriptID: "script-offset", VUsers: 2, StartOffset: "1m"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if scenario.Scripts[0].StartOffset != "1m" {
		t.Fatalf("stored start_offset = %q, want 1m", scenario.Scripts[0].StartOffset)
	}
	run, err := runner.CreateTest(CreateTestRequest{ScenarioID: scenario.ID})
	if err != nil {
		t.Fatal(err)
	}
	if run.Groups[0].StartOffset != "1m" || len(run.groupSpecs) != 1 || run.groupSpecs[0].StartOffset != time.Minute {
		t.Fatalf("materialized offset = %q / %v", run.Groups[0].StartOffset, run.groupSpecs)
	}

	_, err = runner.StoreScenario(CreateScenarioRequest{
		Name: "Invalid Timed Composition", Duration: "30s",
		Groups: []CreateGroupEntry{{Name: "too late", ScriptID: "script-offset", VUsers: 1, StartOffset: "31s"}},
	})
	if err == nil || !strings.Contains(err.Error(), "begins after") {
		t.Fatalf("invalid offset error = %v", err)
	}
}

// TestScenarioInheritingGroupAssignmentStaysNil guards against typed-nil-in-interface
// boxing (H1): a group with NO per-group assignment must round-trip and materialize as
// a nil assignment (inherit scenario default), not a corrupted explicit-auto {Mode:"auto"}
// which would place the group fleet-wide and violate the no-leakage invariant.
func TestScenarioInheritingGroupAssignmentStaysNil(t *testing.T) {
	runner := NewTestRunner(10, NewHub(), nil)
	runner.StoreScript(&dsl.Script{
		ID:       "script-inherit",
		Name:     "Inherit Flow",
		Protocol: "http",
		Version:  "1.0.0",
		Config:   dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions:  []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})

	scenario, err := runner.StoreScenario(CreateScenarioRequest{
		Name:     "Inherit Placement",
		Duration: "5m",
		Groups: []CreateGroupEntry{
			{Name: "inheriting", ScriptID: "script-inherit", VUsers: 5}, // no Assignment -> inherit
			{
				Name:     "pinned",
				ScriptID: "script-inherit",
				VUsers:   5,
				Assignment: &GeneratorAssignmentSettings{
					Mode:            generatorAssignmentModeSpecificHosts,
					SelectedTargets: []string{"gen-jvm:9090"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("StoreScenario returned error: %v", err)
	}
	if got := scenario.Scripts[0].Assignment; got != nil {
		t.Fatalf("stored inheriting group assignment = %#v, want nil (inherit)", got)
	}
	if got := scenario.Scripts[1].Assignment; got == nil || got.Mode != generatorAssignmentModeSpecificHosts {
		t.Fatalf("stored pinned group assignment = %#v, want specific_hosts", got)
	}

	testRun, err := runner.CreateTest(CreateTestRequest{ScenarioID: scenario.ID})
	if err != nil {
		t.Fatalf("CreateTest returned error: %v", err)
	}
	byName := map[string]*GeneratorAssignmentSettings{}
	for i := range testRun.Groups {
		byName[testRun.Groups[i].Name] = testRun.Groups[i].Assignment
	}
	if got := byName["inheriting"]; got != nil {
		t.Fatalf("materialized inheriting group assignment = %#v, want nil (inherit)", got)
	}
	if got := byName["pinned"]; got == nil || got.Mode != generatorAssignmentModeSpecificHosts {
		t.Fatalf("materialized pinned group assignment = %#v, want specific_hosts", got)
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

// TestGeneratorPlacementRejectionMessageReportsInheritedRequiredLabels guards L1:
// an inheriting group (no per-group Assignment) blocked by the scenario-level (root)
// required_labels must name those labels in the rejection message.
func TestGeneratorPlacementRejectionMessageReportsInheritedRequiredLabels(t *testing.T) {
	group := TestGroup{Name: "jvm-users", Protocol: "http", VUsers: 10}
	root := &GeneratorAssignmentSettings{Mode: generatorAssignmentModeAuto, RequiredLabels: map[string]string{"jvm": "17"}}
	loadByTarget := map[string]generatorLoadSnapshot{
		"gen-old": {Target: "gen-old", AvailableCapacity: 100, SupportedProtocols: []string{"http"}},
	}
	specs := []generatorTargetSpec{
		{Address: "gen-old", Protocols: []string{"http"}, Labels: map[string]string{"jvm": "11"}},
	}

	withRoot := generatorPlacementRejectionMessage(group, loadByTarget, specs, root)
	if !strings.Contains(withRoot, "required labels") || !strings.Contains(withRoot, "jvm=17") {
		t.Fatalf("rejection message = %q, want inherited required labels jvm=17", withRoot)
	}
	// Regression guard: without the threaded root the inherited label reason is absent.
	if withoutRoot := generatorPlacementRejectionMessage(group, loadByTarget, specs, nil); strings.Contains(withoutRoot, "jvm=17") {
		t.Fatalf("rejection message without root = %q, did not expect inherited labels", withoutRoot)
	}
}

// TestValidateScenarioLoadModelReportsPerGroupBlockedPlacement guards M1: the
// scenario-builder load-model report must surface a per-group "blocked"
// generator_placement item (with the group name) for a group pinned to an
// incompatible host — matching what launch preflight would block.
func TestValidateScenarioLoadModelReportsPerGroupBlockedPlacement(t *testing.T) {
	runner := NewTestRunner(10, NewHub(), nil)
	runner.setExecutionBackend(newDistributedExecutionBackend([]string{"gen-browser:9090|protocols=browser"}, time.Second, nil))
	storePreflightScript(runner, "script-loadmodel-placement", "")

	report, err := runner.ValidateScenarioLoadModel(CreateScenarioRequest{
		Name:     "Pinned HTTP",
		Duration: "5m",
		Groups: []CreateGroupEntry{{
			Name:     "HTTP Group",
			ScriptID: "script-loadmodel-placement",
			VUsers:   1,
			Assignment: &GeneratorAssignmentSettings{
				Mode:            generatorAssignmentModeSpecificHosts,
				SelectedTargets: []string{"gen-browser:9090"},
			},
		}},
	})
	if err != nil {
		t.Fatalf("ValidateScenarioLoadModel returned error: %v", err)
	}
	found := false
	for _, item := range report.Items {
		if item.Dimension == "generator_placement" && item.Group == "HTTP Group" && item.Status == "blocked" {
			found = true
			if !strings.Contains(item.Summary, "does not support protocol http") {
				t.Fatalf("blocked item summary = %q, want protocol detail", item.Summary)
			}
		}
	}
	if !found {
		t.Fatalf("report items = %#v, want per-group blocked generator_placement for HTTP Group", report.Items)
	}
}

func TestPersistentJavaPlacementUsesAdvertisedRuntimeCapacity(t *testing.T) {
	group := persistentJavaPlacementGroup("java-users", 5)
	allLoad := map[string]generatorLoadSnapshot{
		"gen-full":  persistentJavaLoadSnapshot("gen-full", 100, 1, 25),
		"gen-roomy": persistentJavaLoadSnapshot("gen-roomy", 100, 10, 25),
	}
	specs := persistentJavaTargetSpecs("gen-full", "gen-roomy")

	shards := planDistributedGroupShardsForCandidates(
		[]TestGroup{group},
		allLoad,
		allLoad,
		[]string{"gen-full", "gen-roomy"},
		[]string{"gen-full", "gen-roomy"},
		nil,
		specs,
	)
	if len(shards[group.Name]) != 1 || shards[group.Name][0].Target != "gen-roomy" {
		t.Fatalf("java shards = %#v, want gen-roomy with sufficient JVM slots", shards[group.Name])
	}
}

func TestPersistentJavaPlacementConsumesRuntimeCapacityAcrossGroups(t *testing.T) {
	first := persistentJavaPlacementGroup("first-java", 4)
	second := persistentJavaPlacementGroup("second-java", 3)
	allLoad := map[string]generatorLoadSnapshot{
		"gen-java": persistentJavaLoadSnapshot("gen-java", 100, 6, 25),
	}

	shards := planDistributedGroupShardsForCandidates(
		[]TestGroup{first, second},
		allLoad,
		allLoad,
		[]string{"gen-java"},
		[]string{"gen-java"},
		nil,
		persistentJavaTargetSpecs("gen-java"),
	)
	if len(shards[first.Name]) != 1 || shards[first.Name][0].VUsers != 4 {
		t.Fatalf("first java shards = %#v, want four admitted VUsers", shards[first.Name])
	}
	if len(shards[second.Name]) != 0 {
		t.Fatalf("second java shards = %#v, want rejection after first group consumes four of six slots", shards[second.Name])
	}
}

func TestPersistentJavaSpecificHostShardsRespectPerGeneratorRuntimeCapacity(t *testing.T) {
	group := persistentJavaPlacementGroup("split-java", 5)
	group.Assignment = &GeneratorAssignmentSettings{
		Mode:            generatorAssignmentModeSpecificHosts,
		SelectedTargets: []string{"gen-small", "gen-large"},
	}
	allLoad := map[string]generatorLoadSnapshot{
		"gen-small": persistentJavaLoadSnapshot("gen-small", 100, 1, 25),
		"gen-large": persistentJavaLoadSnapshot("gen-large", 100, 4, 25),
	}

	shards := planDistributedGroupShardsForCandidates(
		[]TestGroup{group},
		allLoad,
		allLoad,
		[]string{"gen-small", "gen-large"},
		[]string{"gen-small", "gen-large"},
		nil,
		persistentJavaTargetSpecs("gen-small", "gen-large"),
	)
	got := map[string]int{}
	for _, shard := range shards[group.Name] {
		got[shard.Target] = shard.VUsers
	}
	if got["gen-small"] != 1 || got["gen-large"] != 4 {
		t.Fatalf("java shard allocation = %#v, want 1/4 runtime-bounded split", got)
	}
}

func TestPersistentJavaPlacementRequiresRuntimeCapacityAdvertisement(t *testing.T) {
	group := persistentJavaPlacementGroup("legacy-java", 3)
	legacy := persistentJavaLoadSnapshot("gen-legacy", 100, 25, 25)
	legacy.RuntimeCapacities = nil
	allLoad := map[string]generatorLoadSnapshot{"gen-legacy": legacy}

	shards := planDistributedGroupShardsForCandidates(
		[]TestGroup{group},
		allLoad,
		allLoad,
		[]string{"gen-legacy"},
		[]string{"gen-legacy"},
		nil,
		persistentJavaTargetSpecs("gen-legacy"),
	)
	if len(shards[group.Name]) != 0 {
		t.Fatalf("missing-capacity heartbeat shards = %#v, want fail-closed rejection", shards[group.Name])
	}
}

func TestPersistentJavaSpecificHostPlacementSkipsUnavailableRuntimeCapacityAdvertisement(t *testing.T) {
	group := persistentJavaPlacementGroup("split-java", 3)
	group.Assignment = &GeneratorAssignmentSettings{
		Mode:            generatorAssignmentModeSpecificHosts,
		SelectedTargets: []string{"gen-unavailable", "gen-qualified"},
	}
	unavailable := persistentJavaLoadSnapshot("gen-unavailable", 100, 25, 25)
	unavailable.RuntimeCapacities[0].Available = false
	unavailable.RuntimeCapacities[0].AvailableVUsers = 0
	allLoad := map[string]generatorLoadSnapshot{
		"gen-unavailable": unavailable,
		"gen-qualified":   persistentJavaLoadSnapshot("gen-qualified", 100, 3, 25),
	}

	shards := planDistributedGroupShardsForCandidates(
		[]TestGroup{group},
		allLoad,
		allLoad,
		[]string{"gen-unavailable", "gen-qualified"},
		[]string{"gen-unavailable", "gen-qualified"},
		nil,
		persistentJavaTargetSpecs("gen-unavailable", "gen-qualified"),
	)
	if got := shards[group.Name]; len(got) != 1 || got[0].Target != "gen-qualified" || got[0].VUsers != 3 {
		t.Fatalf("specific-host shards = %#v, want all VUsers on capacity-advertising target", got)
	}
}

func TestPersistentJavaPlacementRequiresRuntimeOccupancyAdvertisement(t *testing.T) {
	group := persistentJavaPlacementGroup("persistent-java", 1)
	missingOccupancy := persistentJavaLoadSnapshot("gen-missing-occupancy", 100, 25, 25)
	missingOccupancy.RuntimeCapacities[0].OccupancyMode = ""
	allLoad := map[string]generatorLoadSnapshot{"gen-missing-occupancy": missingOccupancy}

	shards := planDistributedGroupShardsForCandidates(
		[]TestGroup{group},
		allLoad,
		allLoad,
		[]string{"gen-missing-occupancy"},
		[]string{"gen-missing-occupancy"},
		nil,
		persistentJavaTargetSpecs("gen-missing-occupancy"),
	)
	if len(shards[group.Name]) != 0 {
		t.Fatalf("missing-occupancy heartbeat shards = %#v, want fail-closed rejection", shards[group.Name])
	}
}

func TestLegacyJavaPlacementRejectsPersistentCapableTargetWithoutOccupancy(t *testing.T) {
	group := legacyJavaPlacementGroup("legacy-java", 1)
	missingOccupancy := persistentJavaLoadSnapshot("gen-missing-occupancy", 100, 25, 25)
	missingOccupancy.RuntimeCapacities[0].OccupancyMode = ""
	allLoad := map[string]generatorLoadSnapshot{"gen-missing-occupancy": missingOccupancy}

	shards := planDistributedGroupShardsForCandidates(
		[]TestGroup{group},
		allLoad,
		allLoad,
		[]string{"gen-missing-occupancy"},
		[]string{"gen-missing-occupancy"},
		nil,
		persistentJavaTargetSpecs("gen-missing-occupancy"),
	)
	if len(shards[group.Name]) != 0 {
		t.Fatalf("legacy shards on persistent-capable target = %#v, want missing-occupancy rejection", shards[group.Name])
	}
}

func TestLegacyJavaPlacementRejectsTokenlessPersistentTargetWithoutOccupancy(t *testing.T) {
	group := legacyJavaPlacementGroup("legacy-java", 1)
	target := persistentJavaLoadSnapshot("gen-tokenless", 100, 25, 25)
	target.CompatibleFeatures = []string{protocol.FeatureJavaVuserPersistentLifecycleV1}
	target.RuntimeCapacities[0].Features = []string{protocol.FeatureJavaVuserPersistentLifecycleV1}
	target.RuntimeCapacities[0].OccupancyMode = ""
	allLoad := map[string]generatorLoadSnapshot{"gen-tokenless": target}

	shards := planDistributedGroupShardsForCandidates(
		[]TestGroup{group},
		allLoad,
		allLoad,
		[]string{"gen-tokenless"},
		[]string{"gen-tokenless"},
		nil,
		[]generatorTargetSpec{{
			Address:   "gen-tokenless",
			Protocols: []string{"java_vuser"},
			Features:  []string{protocol.FeatureJavaVuserPersistentLifecycleV1},
		}},
	)
	if len(shards[group.Name]) != 0 {
		t.Fatalf("legacy shards on tokenless persistent target = %#v, want missing-occupancy rejection", shards[group.Name])
	}
}

func TestLegacyJavaPlacementRetainsMissingOccupancyCompatibility(t *testing.T) {
	group := legacyJavaPlacementGroup("legacy-java", 1)
	legacy := generatorLoadSnapshot{
		Target:             "gen-legacy",
		HeartbeatOK:        true,
		AvailableCapacity:  100,
		SupportedProtocols: []string{"java_vuser"},
		CompatibleFeatures: []string{"java_vuser"},
	}
	allLoad := map[string]generatorLoadSnapshot{"gen-legacy": legacy}
	specs := []generatorTargetSpec{{
		Address:   "gen-legacy",
		Protocols: []string{"java_vuser"},
		Features:  []string{"java_vuser"},
	}}

	shards := planDistributedGroupShardsForCandidates(
		[]TestGroup{group},
		allLoad,
		allLoad,
		[]string{"gen-legacy"},
		[]string{"gen-legacy"},
		nil,
		specs,
	)
	if got := shards[group.Name]; len(got) != 1 || got[0].Target != "gen-legacy" || got[0].VUsers != 1 {
		t.Fatalf("legacy missing-occupancy shards = %#v, want rolling-upgrade fallback", got)
	}
}

func TestJavaPlacementSkipsIncompatibleKnownOccupancy(t *testing.T) {
	group := legacyJavaPlacementGroup("legacy-java", 2)
	persistent := persistentJavaLoadSnapshot("gen-persistent", 100, 20, 25)
	persistent.RuntimeCapacities[0].OccupancyMode = generatorrpc.RuntimeOccupancyModePersistent
	idle := persistentJavaLoadSnapshot("gen-idle", 50, 25, 25)
	allLoad := map[string]generatorLoadSnapshot{
		"gen-persistent": persistent,
		"gen-idle":       idle,
	}

	shards := planDistributedGroupShardsForCandidates(
		[]TestGroup{group},
		allLoad,
		allLoad,
		[]string{"gen-persistent", "gen-idle"},
		[]string{"gen-persistent", "gen-idle"},
		nil,
		persistentJavaTargetSpecs("gen-persistent", "gen-idle"),
	)
	if got := shards[group.Name]; len(got) != 1 || got[0].Target != "gen-idle" {
		t.Fatalf("legacy Java shards = %#v, want idle generator", got)
	}
}

func TestJavaSpecificHostPlacementUsesRemainingOccupancyCompatibleHosts(t *testing.T) {
	group := legacyJavaPlacementGroup("legacy-java", 2)
	group.Assignment = &GeneratorAssignmentSettings{
		Mode:            generatorAssignmentModeSpecificHosts,
		SelectedTargets: []string{"gen-persistent", "gen-idle"},
	}
	persistent := persistentJavaLoadSnapshot("gen-persistent", 100, 20, 25)
	persistent.RuntimeCapacities[0].OccupancyMode = generatorrpc.RuntimeOccupancyModePersistent
	idle := persistentJavaLoadSnapshot("gen-idle", 50, 25, 25)
	allLoad := map[string]generatorLoadSnapshot{
		"gen-persistent": persistent,
		"gen-idle":       idle,
	}

	shards := planDistributedGroupShardsForCandidates(
		[]TestGroup{group},
		allLoad,
		allLoad,
		[]string{"gen-persistent", "gen-idle"},
		[]string{"gen-persistent", "gen-idle"},
		nil,
		persistentJavaTargetSpecs("gen-persistent", "gen-idle"),
	)
	if got := shards[group.Name]; len(got) != 1 || got[0].Target != "gen-idle" || got[0].VUsers != 2 {
		t.Fatalf("legacy Java specific-host shards = %#v, want both VUsers on remaining idle host", got)
	}
}

func TestJavaPlacementProjectsLifecycleModeAcrossGroups(t *testing.T) {
	persistent := persistentJavaPlacementGroup("persistent-java", 2)
	legacy := legacyJavaPlacementGroup("legacy-java", 2)
	allLoad := map[string]generatorLoadSnapshot{
		"gen-primary":  persistentJavaLoadSnapshot("gen-primary", 100, 25, 25),
		"gen-fallback": persistentJavaLoadSnapshot("gen-fallback", 50, 25, 25),
	}

	shards := planDistributedGroupShardsForCandidates(
		[]TestGroup{persistent, legacy},
		allLoad,
		allLoad,
		[]string{"gen-primary", "gen-fallback"},
		[]string{"gen-primary", "gen-fallback"},
		nil,
		persistentJavaTargetSpecs("gen-primary", "gen-fallback"),
	)
	if got := shards[persistent.Name]; len(got) != 1 || got[0].Target != "gen-primary" {
		t.Fatalf("persistent Java shards = %#v, want primary generator", got)
	}
	if got := shards[legacy.Name]; len(got) != 1 || got[0].Target != "gen-fallback" {
		t.Fatalf("legacy Java shards = %#v, want projected idle fallback generator", got)
	}
}

func TestZeroStartJavaPlacementProjectsLifecycleMode(t *testing.T) {
	persistent := persistentJavaPlacementGroup("persistent-zero", 0)
	legacy := legacyJavaPlacementGroup("legacy-after-zero", 1)
	load := map[string]generatorLoadSnapshot{
		"gen-java": persistentJavaLoadSnapshot("gen-java", 100, 25, 25),
	}

	shards := planDistributedGroupShardsForCandidates(
		[]TestGroup{persistent, legacy},
		load,
		load,
		[]string{"gen-java"},
		[]string{"gen-java"},
		nil,
		persistentJavaTargetSpecs("gen-java"),
	)
	if got := shards[persistent.Name]; len(got) != 1 || got[0].Target != "gen-java" || got[0].VUsers != 0 {
		t.Fatalf("zero-start persistent Java shards = %#v, want zero shard on generator", got)
	}
	if got := shards[legacy.Name]; len(got) != 0 {
		t.Fatalf("legacy Java shards = %#v, want rejection after projected zero-start persistent occupancy", got)
	}
}

func TestPersistentJavaLaunchPlacementProjectsCapacityAcrossGroups(t *testing.T) {
	runner := NewTestRunner(10, NewHub(), nil)
	runner.setExecutionBackend(newDistributedExecutionBackend(
		[]string{"gen-java|protocols=java_vuser"},
		time.Second,
		nil,
	))
	runner.setCachedGeneratorInventory([]GeneratorInventoryEntry{{
		Target:             "gen-java",
		GeneratorID:        "gen-java",
		Status:             "connected",
		DrainState:         "active",
		AvailableCapacity:  100,
		MaxVUsers:          100,
		SupportedProtocols: []string{"java_vuser"},
		CompatibleFeatures: governedPersistentJavaRuntimeFeatures(),
		RuntimeCapacities: []generatorrpc.ProtocolRuntimeCapacity{{
			Protocol:        "java_vuser",
			Available:       true,
			MaxVUsers:       6,
			AvailableVUsers: 6,
			Features:        governedPersistentJavaRuntimeFeatures(),
			OccupancyMode:   generatorrpc.RuntimeOccupancyModeIdle,
		}},
	}}, time.Now())
	test := &ManagedTest{Groups: []TestGroup{
		persistentJavaPlacementGroup("first-java", 4),
		persistentJavaPlacementGroup("second-java", 3),
	}}

	conflicts := runner.evaluateGeneratorPlacementConflictsLocked(test)
	if len(conflicts) != 1 || conflicts[0].Group != "second-java" ||
		!strings.Contains(conflicts[0].Message, "available java_vuser runtime slots") {
		t.Fatalf("placement conflicts = %#v, want second group runtime-capacity conflict", conflicts)
	}
}

func TestJavaLaunchPlacementProjectsLifecycleModeAcrossGroups(t *testing.T) {
	runner := NewTestRunner(10, NewHub(), nil)
	runner.setExecutionBackend(newDistributedExecutionBackend(
		[]string{"gen-java|protocols=java_vuser"},
		time.Second,
		nil,
	))
	runner.setCachedGeneratorInventory([]GeneratorInventoryEntry{{
		Target:             "gen-java",
		GeneratorID:        "gen-java",
		Status:             "connected",
		DrainState:         "active",
		AvailableCapacity:  100,
		MaxVUsers:          100,
		SupportedProtocols: []string{"java_vuser"},
		CompatibleFeatures: governedPersistentJavaRuntimeFeatures(),
		RuntimeCapacities: []generatorrpc.ProtocolRuntimeCapacity{{
			Protocol:        "java_vuser",
			Available:       true,
			MaxVUsers:       25,
			AvailableVUsers: 25,
			Features:        governedPersistentJavaRuntimeFeatures(),
			OccupancyMode:   generatorrpc.RuntimeOccupancyModeIdle,
		}},
	}}, time.Now())
	test := &ManagedTest{Groups: []TestGroup{
		persistentJavaPlacementGroup("persistent-java", 1),
		legacyJavaPlacementGroup("legacy-java", 1),
	}}

	conflicts := runner.evaluateGeneratorPlacementConflictsLocked(test)
	if len(conflicts) != 1 || conflicts[0].Group != "legacy-java" ||
		!strings.Contains(conflicts[0].Message, "runtime occupancy") {
		t.Fatalf("placement conflicts = %#v, want legacy lifecycle-mode conflict", conflicts)
	}
}

func TestInitialPersistentJavaShardsUseProjectedRuntimeCapacity(t *testing.T) {
	first := persistentJavaPlacementGroup("first-stage", 5)
	first.stageDriven = true
	first.currentVUsers = 4
	second := persistentJavaPlacementGroup("second-stage", 5)
	second.stageDriven = true
	second.currentVUsers = 3
	peak := []GeneratorShardSummary{{Group: first.Name, Target: "gen-java", VUsers: 5}}
	load := map[string]generatorLoadSnapshot{
		"gen-java": persistentJavaLoadSnapshot("gen-java", 100, 6, 25),
	}

	firstShards := initialGroupShardsForRun(first, peak, load)
	if len(firstShards) != 1 || firstShards[0].VUsers != 4 {
		t.Fatalf("first initial shards = %#v, want four VUsers", firstShards)
	}
	snapshot := load["gen-java"]
	consumeGroupRuntimeCapacity(&snapshot, first, firstShards[0].VUsers)
	load["gen-java"] = snapshot
	secondPeak := []GeneratorShardSummary{{Group: second.Name, Target: "gen-java", VUsers: 5}}
	if got := initialGroupShardsForRun(second, secondPeak, load); len(got) != 0 {
		t.Fatalf("second initial shards = %#v, want rejection with only two projected JVM slots", got)
	}
}

func persistentJavaPlacementGroup(name string, vusers int) TestGroup {
	script := &dsl.Script{
		Protocol: "java_vuser",
		Actions: []dsl.Action{{
			Name:   "Action",
			Type:   "java_vuser_run",
			Target: "Actions",
			Params: map[string]any{"method": "action"},
		}},
	}
	return TestGroup{
		Name:     name,
		Protocol: "java_vuser",
		VUsers:   vusers,
		script:   script,
	}
}

func legacyJavaPlacementGroup(name string, vusers int) TestGroup {
	script := &dsl.Script{
		Protocol: "java_vuser",
		Actions: []dsl.Action{{
			Name:   "Full lifecycle",
			Type:   "java_vuser_run",
			Target: "Actions",
			Params: map[string]any{"method": "full_lifecycle"},
		}},
	}
	return TestGroup{
		Name:     name,
		Protocol: "java_vuser",
		VUsers:   vusers,
		script:   script,
	}
}

func persistentJavaLoadSnapshot(target string, generalCapacity int32, javaAvailable, javaMaximum int64) generatorLoadSnapshot {
	return generatorLoadSnapshot{
		Target:             target,
		HeartbeatOK:        true,
		AvailableCapacity:  generalCapacity,
		SupportedProtocols: []string{"java_vuser"},
		CompatibleFeatures: governedPersistentJavaRuntimeFeatures(),
		RuntimeCapacities: []generatorrpc.ProtocolRuntimeCapacity{{
			Protocol:        "java_vuser",
			Available:       true,
			MaxVUsers:       javaMaximum,
			AvailableVUsers: javaAvailable,
			Features:        governedPersistentJavaRuntimeFeatures(),
			OccupancyMode:   generatorrpc.RuntimeOccupancyModeIdle,
		}},
	}
}

func persistentJavaTargetSpecs(targets ...string) []generatorTargetSpec {
	specs := make([]generatorTargetSpec, 0, len(targets))
	for _, target := range targets {
		specs = append(specs, generatorTargetSpec{
			Address:   target,
			Protocols: []string{"java_vuser"},
			Features:  governedPersistentJavaRuntimeFeatures(),
		})
	}
	return specs
}

func governedPersistentJavaRuntimeFeatures() []string {
	return []string{
		protocol.FeatureJavaVuserPersistentLifecycleV1,
		protocol.FeatureJavaVuserSessionFrameTokenV1,
	}
}

func TestHTTP3GroupsNeedGeneratorsAdvertisingNativeHTTP3(t *testing.T) {
	script := &dsl.Script{Name: "h3", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: "https://app.example.test", HTTP: dsl.HTTPConfig{Version: "auto+h3"}},
		Actions: []dsl.Action{{Name: "home", Type: "GET", Target: "/"}}}
	group := TestGroup{Name: "h3-users", Protocol: "http", VUsers: 10, script: script}
	if required := groupRequiredRuntimeFeature(group); !strings.Contains(required, dsl.FeatureNativeHTTP3V1) {
		t.Fatalf("required feature = %q, want native_http3_v1", required)
	}
	loadByTarget := map[string]generatorLoadSnapshot{
		"gen-old": {Target: "gen-old", AvailableCapacity: 100, SupportedProtocols: []string{"http"}},
	}
	specs := []generatorTargetSpec{{Address: "gen-old", Protocols: []string{"http"}}}
	message := generatorPlacementRejectionMessage(group, loadByTarget, specs, nil)
	if !strings.Contains(message, "gen-old does not advertise required runtime feature") || !strings.Contains(message, httpprotocol.EnvHTTP3) {
		t.Fatalf("rejection = %q, want the generator and the flag named", message)
	}
	// A capacity rejection of an HTTP/3 group does not blame the flag.
	specs = []generatorTargetSpec{{Address: "gen-new", Protocols: []string{"http"}, Features: []string{dsl.FeatureNativeHTTP3V1}}}
	loadByTarget = map[string]generatorLoadSnapshot{"gen-new": {Target: "gen-new", AvailableCapacity: 1, SupportedProtocols: []string{"http"}}}
	if message := generatorPlacementRejectionMessage(group, loadByTarget, specs, nil); strings.Contains(message, httpprotocol.EnvHTTP3) {
		t.Fatalf("capacity rejection = %q blamed the flag", message)
	}
	group.script = &dsl.Script{Name: "h2", Protocol: "http", Config: dsl.ScriptConfig{HTTP: dsl.HTTPConfig{Version: "2"}}, Actions: script.Actions}
	if required := groupRequiredRuntimeFeature(group); strings.Contains(required, dsl.FeatureNativeHTTP3V1) {
		t.Fatalf("an HTTP/2 group required %q", required)
	}
}
