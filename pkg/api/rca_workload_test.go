package api

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/rca"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestWorkloadGroupDescribesPlannedLoad(t *testing.T) {
	tps := 40.0
	open := &dsl.Script{Config: dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{WorkloadModel: "open"}, Pacing: dsl.PacingConfig{Type: "tps", TPS: 20}}}
	closed := &dsl.Script{Config: dsl.ScriptConfig{Pacing: dsl.PacingConfig{Type: "fixed_interval", IntervalMs: 1000}}}
	tests := []struct {
		name   string
		group  TestGroup
		script *dsl.Script
		model  string
		unit   string
		levels []float64
	}{
		{name: "plain closed group", group: TestGroup{Name: "g", VUsers: 25}, script: closed, model: rca.ArrivalClosed, unit: rca.LoadUnitVUsers, levels: []float64{25}},
		{
			name: "staged closed group drops ramp-down and repeated targets",
			group: TestGroup{Name: "g", VUsers: 100, Stages: []storage.LoadStage{
				{Type: "ramp_up", Duration: "1m", TargetVUsers: 50}, {Type: "hold", Duration: "5m", TargetVUsers: 50},
				{Type: "ramp_up", Duration: "1m", TargetVUsers: 100}, {Type: "hold", Duration: "5m", TargetVUsers: 100},
				{Type: "ramp_down", Duration: "1m"},
			}},
			script: closed, model: rca.ArrivalClosed, unit: rca.LoadUnitVUsers, levels: []float64{50, 100},
		},
		{name: "open arrival rate", group: TestGroup{Name: "g"}, script: open, model: rca.ArrivalOpen, unit: rca.LoadUnitTPS, levels: []float64{20}},
		{
			name:   "goal-oriented rate stages",
			group:  TestGroup{Name: "g", VUsers: 10, Stages: []storage.LoadStage{{Type: "hold", Duration: "5m", TargetTPS: &tps}}},
			script: closed, model: rca.ArrivalClosed, unit: rca.LoadUnitTPS, levels: []float64{40},
		},
		{name: "derived without the compiled script", group: TestGroup{Name: "g", VUsers: 25}, model: rca.ArrivalUnknown, unit: rca.LoadUnitVUsers, levels: []float64{25}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := workloadGroup(tt.group, tt.script)
			if got.ArrivalModel != tt.model || got.Unit != tt.unit || !reflect.DeepEqual(got.Levels, tt.levels) {
				t.Fatalf("group = %+v, want %s %s %v", got, tt.model, tt.unit, tt.levels)
			}
		})
	}
	// The open model's rate is its offered load, so pacing never repeats it.
	if strings.Contains(workloadGroup(TestGroup{Name: "g"}, open).Pacing, `"tps":20`) {
		t.Fatal("open pacing policy repeats the offered rate")
	}
}

func TestCaptureBaselineComparisonMetadataFreezesWorkload(t *testing.T) {
	script := &dsl.Script{Name: "API", Protocol: "http", Config: dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{WorkloadModel: "open"}, Pacing: dsl.PacingConfig{Type: "tps", TPS: 15}}}
	groups := []TestGroup{{Name: "api", Protocol: "http", script: script}}
	p := RunArchiveProvenance{Groups: groups}
	captureBaselineComparisonMetadata(&p, &ManagedTest{Groups: groups})
	if p.Workload == nil || p.Workload.Source != rca.WorkloadKeyFrozen || !p.Workload.Recorded() || p.Workload.RuntimeDigest == "" {
		t.Fatalf("workload = %+v", p.Workload)
	}
	group := p.Workload.Groups["api"]
	if group.ArrivalModel != rca.ArrivalOpen || !reflect.DeepEqual(group.Levels, []float64{15}) || group.ScriptDigest == "" {
		t.Fatalf("api group = %+v", group)
	}
}

func TestBaselineComparisonQualifiesWorkload(t *testing.T) {
	server, store := baselineTestServer(t)
	current := comparisonArchiveFixture(t, server, store, "current", 200, 150)
	baseline := comparisonArchiveFixture(t, server, store, "baseline", 100, 100)
	request := BaselineComparisonRequest{BaselineIDs: []string{baseline.ID}, Population: "transaction"}
	p := defaultBaselineComparisonPolicy()
	a, err := server.readBaselineComparisonRun(context.Background(), current, request, p)
	if err != nil {
		t.Fatal(err)
	}
	b, err := server.readBaselineComparisonRun(context.Background(), baseline, request, p)
	if err != nil {
		t.Fatal(err)
	}
	// Archives from before keys were frozen get a derived key: the load matches,
	// but the arrival model and scripts cannot be confirmed.
	legacy := compareBaselineRuns(a, []*baselineComparisonRun{b}, request, p)
	got := legacy.Workload.Baselines[baseline.ID]
	if got.Status != rca.BaselinePartial || !reflect.DeepEqual(got.Reasons, []string{rca.ReasonArrivalModelUnrecorded, rca.ReasonScriptUnrecorded}) {
		t.Fatalf("legacy qualification = %+v", got)
	}
	if got.AchievedThroughput == nil {
		t.Fatal("achieved throughput not reported")
	}
	if legacy.Workload.Current.Population != "transaction" || legacy.Workload.Current.Source != rca.WorkloadKeyDerived {
		t.Fatalf("current workload = %+v", legacy.Workload.Current)
	}

	frozen := func(run *baselineComparisonRun, pacing string) {
		key := &rca.WorkloadKey{Version: rca.WorkloadKeyVersion, Source: rca.WorkloadKeyFrozen, RuntimeDigest: "v1:runtime", Groups: map[string]rca.WorkloadGroup{}}
		for _, name := range []string{"checkout", "search"} {
			key.Groups[name] = rca.WorkloadGroup{ArrivalModel: rca.ArrivalClosed, Unit: rca.LoadUnitVUsers, Levels: []float64{100}, Pacing: pacing, ScriptDigest: "v1:" + name}
		}
		initial := *run.manifest.InitialProvenance
		initial.Workload = key
		run.manifest.InitialProvenance = &initial
	}
	frozen(a, "fixed_interval:1000")
	frozen(b, "fixed_interval:1000")
	if got := compareBaselineRuns(a, []*baselineComparisonRun{b}, request, p).Workload.Baselines[baseline.ID]; got.Status != rca.BaselineQualified {
		t.Fatalf("same frozen workload = %+v", got)
	}
	frozen(b, "fixed_interval:2000")
	paced := compareBaselineRuns(a, []*baselineComparisonRun{b}, request, p)
	if got := paced.Workload.Baselines[baseline.ID]; got.Status != rca.BaselinePartial || !reflect.DeepEqual(got.Reasons, []string{rca.ReasonPacingChanged}) {
		t.Fatalf("different pacing = %+v", got)
	}
	// The block is additive: row qualifications and findings are untouched.
	for _, row := range paced.Transactions {
		for _, reason := range row.Baselines[baseline.ID].Qualification {
			if strings.Contains(reason, "pacing") {
				t.Fatalf("workload reason leaked into row qualification: %v", row.Baselines[baseline.ID].Qualification)
			}
		}
	}
	if !reflect.DeepEqual(paced.Findings, legacy.Findings) {
		t.Fatal("workload qualification changed the findings")
	}

	// Group mappings rename baseline groups before comparison.
	p.GroupMappings = map[string]string{baseline.ID + "/search": "lookup", current.ID + "/search": "lookup"}
	mapped := comparisonWorkloadKey(b, p.GroupMappings, "transaction")
	if _, ok := mapped.Groups["lookup"]; !ok || len(mapped.Groups) != 2 {
		t.Fatalf("mapped groups = %v", mapped.Groups)
	}
}

func TestAchievedComparisonTPSWeightsActiveIntervals(t *testing.T) {
	start := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	summaries := comparisonSummaries(start, []int{10, 10, 0, 10})
	for i, tps := range []float64{10, 20, 500, 30} {
		summaries[i].TPS = tps
	}
	// The idle interval is excluded; the three active 5-second intervals average.
	if got := achievedComparisonTPS(summaries, start); got != 20 {
		t.Fatalf("achieved TPS = %v, want 20", got)
	}
	if got := achievedComparisonTPS(nil, start); got != 0 {
		t.Fatalf("empty achieved TPS = %v", got)
	}
}

func TestWorkloadRuntimeDigestFollowsGroupMappings(t *testing.T) {
	runtime := &RuntimeSettingsOverride{Log: &dsl.LogConfig{Level: "debug"}}
	run := func(id, group string) *baselineComparisonRun {
		r := &baselineComparisonRun{identity: BaselineRunIdentity{ID: id}}
		r.manifest = &RunArchiveManifest{Provenance: RunArchiveProvenance{Groups: []TestGroup{{Name: group, VUsers: 10, RuntimeSettings: runtime}}}}
		return r
	}
	current, baseline := run("current", "checkout-v2"), run("baseline", "checkout")
	mappings := map[string]string{"baseline/checkout": "checkout-v2"}
	got := rca.QualifyBaseline(comparisonWorkloadKey(current, mappings, "transaction"), comparisonWorkloadKey(baseline, mappings, "transaction"))
	for _, reason := range got.Reasons {
		if reason == rca.ReasonConfigChanged {
			t.Fatalf("a renamed group with identical runtime settings reported %v", got.Reasons)
		}
	}
	// Without the mapping the groups are different groups, and so is the config.
	got = rca.QualifyBaseline(comparisonWorkloadKey(current, nil, "transaction"), comparisonWorkloadKey(baseline, nil, "transaction"))
	if !slicesContains(got.Reasons, rca.ReasonConfigChanged) {
		t.Fatalf("unmapped groups = %v, want config_changed", got.Reasons)
	}
}

func slicesContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
