package api

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

func javaHostPlacementFixture(t *testing.T) (TestGroup, generatorLoadSnapshot) {
	t.Helper()
	script := &dsl.Script{ID: "approved", Protocol: "java_vuser", Init: []dsl.Action{{Type: "java_vuser_run", Target: "Actions", Params: map[string]any{"method": "init"}}}, Actions: []dsl.Action{{Type: "java_vuser_run", Target: "Actions", Params: map[string]any{"method": "action"}}}, Teardown: []dsl.Action{{Type: "java_vuser_run", Target: "Actions", Params: map[string]any{"method": "end"}}}}
	digest, err := engine.JavaPooledScriptSHA256(script)
	if err != nil {
		t.Fatal(err)
	}
	caps := testJavaHostCaps()
	caps.Profiles[0].ScriptSha256 = digest
	features := []string{generatorrpc.FeatureJavaHostBrokerV1}
	return TestGroup{Name: "buyers", Protocol: "java_vuser", VUsers: 2, script: script, Assignment: &GeneratorAssignmentSettings{Mode: generatorAssignmentModeAuto, JavaHost: &JavaHostAssignmentSettings{ProfileID: "approved", PreparationMS: 120000, RunLifetimeMS: 600000}}}, generatorLoadSnapshot{Target: "gen-a", HeartbeatOK: true, AvailableCapacity: 100, CompatibleFeatures: features, JavaHost: javaHostCapabilitySummary(caps, features)}
}

func TestJavaHostGroupSettingsRoundTripAndScope(t *testing.T) {
	g, _ := javaHostPlacementFixture(t)
	if _, err := normalizeGeneratorAssignmentSettings(g.Assignment); err == nil {
		t.Fatal("root selection accepted")
	}
	got, err := normalizeGroupGeneratorAssignmentSettings(g.Assignment)
	if err != nil {
		t.Fatal(err)
	}
	saved := decodeStoredGeneratorAssignment(generatorAssignmentForStorage(got))
	if saved == nil || saved.JavaHost == nil || *saved.JavaHost != *got.JavaHost {
		t.Fatal("group selection lost on storage roundtrip")
	}
	got.JavaHost.ProfileID = "changed"
	if saved.JavaHost.ProfileID != "approved" || g.Assignment.JavaHost.ProfileID != "approved" {
		t.Fatal("settings alias")
	}
	for _, s := range []*JavaHostAssignmentSettings{{}, {ProfileID: "approved", PreparationMS: 0, RunLifetimeMS: 100}, {ProfileID: " approved", PreparationMS: 1, RunLifetimeMS: 1}, {ProfileID: "approved", PreparationMS: 1, RunLifetimeMS: 86400001}} {
		if validateJavaHostAssignmentSettings(s) == nil {
			t.Fatalf("accepted invalid settings %#v", s)
		}
	}
}

func TestJavaHostProfileCanInheritScenarioPlacement(t *testing.T) {
	g, a := javaHostPlacementFixture(t)
	g.Assignment.Mode = generatorAssignmentModeInherit
	approved, err := normalizeGroupGeneratorAssignmentSettings(g.Assignment)
	if err != nil || approved.Mode != generatorAssignmentModeInherit {
		t.Fatalf("inherit profile rejected: assignment=%+v err=%v", approved, err)
	}
	implicit := *g.Assignment
	implicit.Mode = ""
	if got, err := normalizeGroupGeneratorAssignmentSettings(&implicit); err != nil || got.Mode != generatorAssignmentModeAuto {
		t.Fatalf("legacy profile-only placement changed: assignment=%+v err=%v", got, err)
	}
	stored := decodeStoredGeneratorAssignment(generatorAssignmentForStorage(approved))
	if stored == nil || stored.Mode != generatorAssignmentModeInherit || stored.JavaHost == nil {
		t.Fatalf("stored group lost inheritance: %+v", stored)
	}
	if _, err := normalizeGeneratorAssignmentSettings(approved); err == nil {
		t.Fatal("test-level inherit accepted")
	}
	invalid := *approved
	invalid.RequiredLabels = map[string]string{"zone": "other"}
	if _, err := normalizeGroupGeneratorAssignmentSettings(&invalid); err == nil {
		t.Fatal("inherit profile accepted a competing group label constraint")
	}

	b := a
	b.Target = "gen-b"
	loads := map[string]generatorLoadSnapshot{"gen-a": a, "gen-b": b}
	root := &GeneratorAssignmentSettings{Mode: generatorAssignmentModeSpecificHosts, PoolID: "pool-a", SelectedTargets: []string{"gen-a"}, RequiredLabels: map[string]string{"zone": "a"}}
	specs := []generatorTargetSpec{{Address: "gen-a", Labels: map[string]string{"zone": "a"}}, {Address: "gen-b", Labels: map[string]string{"zone": "b"}}}
	compatibility := newGeneratorCompatibilityIndex(specs, loads)
	if !groupInheritsAssignment(g) || effectiveGroupRequiredLabels(g, root)["zone"] != "a" {
		t.Fatal("group profile erased scenario placement")
	}
	for _, mode := range []string{generatorAssignmentModeInherit, generatorAssignmentModeAuto, generatorAssignmentModeSpecificHosts} {
		g.Assignment.Mode = mode
		g.Assignment.SelectedTargets = nil
		if mode == generatorAssignmentModeSpecificHosts {
			g.Assignment.SelectedTargets = []string{"gen-b"}
		}
		shards := chooseJavaHostShards(g, loads, loads, []string{"gen-b", "gen-a"}, []string{"gen-a"}, root, compatibility)
		if mode == generatorAssignmentModeSpecificHosts {
			if len(shards) != 0 {
				t.Fatalf("group pin escaped pool: %+v", shards)
			}
			continue
		}
		if len(shards) != 1 || shards[0].Target != "gen-a" {
			t.Fatalf("mode %q escaped scenario pool: %+v", mode, shards)
		}
	}
	// Pool membership alone does not erase inherited labels.
	g.Assignment.Mode = generatorAssignmentModeInherit
	noLabels := newGeneratorCompatibilityIndex([]generatorTargetSpec{{Address: "gen-a", Labels: map[string]string{"zone": "b"}}}, loads)
	if shards := chooseJavaHostShards(g, loads, loads, []string{"gen-a", "gen-b"}, []string{"gen-a"}, root, noLabels); len(shards) != 0 {
		t.Fatalf("incompatible pool member admitted: %+v", shards)
	}
	root.Mode = generatorAssignmentModeAuto // Dynamic pool has no resolved members.
	root.SelectedTargets = nil
	if shards := chooseJavaHostShards(g, loads, loads, []string{"gen-a", "gen-b"}, []string{"gen-a", "gen-b"}, root, nil); len(shards) != 0 {
		t.Fatalf("unresolved pool widened Java placement: %+v", shards)
	}
	if message := generatorPlacementRejectionMessage(g, loads, nil, root); !strings.Contains(message, "java_host_pool_membership_unresolved") {
		t.Fatal(message)
	}
	root.PoolID = ""
	for _, mode := range []string{generatorAssignmentModeRegionWeighted, generatorAssignmentModeLoadZoneWeighted} {
		root.Mode = mode
		if shards := chooseJavaHostShards(g, loads, loads, []string{"gen-a", "gen-b"}, []string{"gen-a", "gen-b"}, root, nil); len(shards) != 0 {
			t.Fatalf("inherited %s weight was ignored: %+v", mode, shards)
		}
		if message := generatorPlacementRejectionMessage(g, loads, nil, root); !strings.Contains(message, "java_host_inherited_weighted_placement_unsupported") {
			t.Fatal(message)
		}
	}
}

func TestJavaHostPlacementFrozenAndIndependentOfLocalJVMCapacity(t *testing.T) {
	g, load := javaHostPlacementFixture(t)
	load.RuntimeCapacities = []generatorrpc.ProtocolRuntimeCapacity{{Protocol: "java_vuser", AvailableVUsers: 0}}
	loads := map[string]generatorLoadSnapshot{"gen-a": load}
	shards := chooseGeneratorShardsForGroup(g, loads, loads, []string{"gen-a"}, []string{"gen-a"}, nil, nil, newGeneratorCompatibilityIndex([]generatorTargetSpec{{Address: "gen-a"}}, loads))
	if len(shards) != 1 || shards[0].JavaHost == nil {
		t.Fatal("approved remote backend required local JVM capacity")
	}
	copy := cloneGeneratorShardSummaries(shards)
	copy[0].JavaHost.Profile.ProfileID = "changed"
	if shards[0].JavaHost.Profile.ProfileID != "approved" {
		t.Fatal("frozen placement alias")
	}
	consumeGroupRuntimeCapacity(&load, g, 2)
	if load.RuntimeCapacities[0].AvailableVUsers != 0 || load.JavaHost.MaxHosts != 23 || load.JavaHost.MaxMemoryBytes != 1<<29 {
		t.Fatalf("wrong accounting %#v", load)
	}
	if loads["gen-a"].JavaHost.MaxHosts != 25 {
		t.Fatal("projected capacity changed heartbeat")
	}
	load.JavaHost.Profiles[0].PolicySHA256 = strings.Repeat("d", 64)
	if javaHostPlacementStillAdvertised(shards[0].JavaHost, load) {
		t.Fatal("drift accepted")
	}
}

func TestJavaHostPlacementRejectsUnsupportedOrUnapproved(t *testing.T) {
	for _, name := range []string{"old_peer", "wrong_script", "wrong_profile", "memory", "hosts", "generator_capacity", "pod_ledger", "stage", "protocol", "deadline", "missing_script"} {
		t.Run(name, func(t *testing.T) {
			g, load := javaHostPlacementFixture(t)
			switch name {
			case "old_peer":
				load.CompatibleFeatures = nil
			case "wrong_script":
				g.script.ID = "changed"
			case "wrong_profile":
				g.Assignment.JavaHost.ProfileID = "missing"
			case "memory":
				load.JavaHost.MaxMemoryBytes = 1
			case "hosts":
				load.JavaHost.MaxHosts = 1
			case "generator_capacity":
				load.AvailableCapacity = 1
			case "pod_ledger":
				load.PodCapacity = &protocol.CapacitySnapshot{}
			case "stage":
				g.stageDriven = true
			case "protocol":
				g.Protocol = "http"
			case "deadline":
				g.Assignment.JavaHost.PreparationMS = 120001
			case "missing_script":
				g.script = nil
			}
			if _, err := javaHostPlacementForGroup(g, load); err == nil {
				t.Fatal("unsafe placement accepted")
			}
		})
	}
}

func TestJavaHostPlacementAcknowledgementAndRunBounds(t *testing.T) {
	g, load := javaHostPlacementFixture(t)
	p, err := javaHostPlacementForGroup(g, load)
	if err != nil {
		t.Fatal(err)
	}
	ack := &pb.JavaHostPlacement{Selection: p.selection(), ReservationId: "reservation", Mode: "dedicated", Containment: "execution_pod", MaxHosts: 25, MaxVusers: 25}
	if !javaHostPlacementAcknowledged(p, ack) {
		t.Fatal("valid ack rejected")
	}
	ack.Selection.PolicySha256 = strings.Repeat("d", 64)
	if javaHostPlacementAcknowledged(p, ack) {
		t.Fatal("drifted ack accepted")
	}
	run := &ManagedTest{Duration: time.Minute}
	if err := validateJavaHostRun(run, g, p); err != nil {
		t.Fatal(err)
	}
	run.Duration = 10 * time.Minute
	if validateJavaHostRun(run, g, p) == nil {
		t.Fatal("lifetime margin ignored")
	}
	run.Duration = time.Minute
	run.Assignment = &GeneratorAssignmentSettings{TolerateGeneratorLoss: true}
	if validateJavaHostRun(run, g, p) == nil {
		t.Fatal("session migration allowed")
	}
}

type javaHostScaleTestClient struct {
	generatorClient
	calls  int
	reject bool
	err    error
}

func (c *javaHostScaleTestClient) AdjustVUsers(context.Context, generatorrpc.VUserAdjustment) (generatorrpc.AdjustmentResult, error) {
	c.calls++
	return generatorrpc.AdjustmentResult{Accepted: c.err == nil && !c.reject}, c.err
}

func TestJavaHostScaleDownDoesNotReplayOnUncertainty(t *testing.T) {
	g, load := javaHostPlacementFixture(t)
	p, _ := javaHostPlacementForGroup(g, load)
	shards := []GeneratorShardSummary{{Group: g.Name, Target: "gen-a", VUsers: 2, JavaHost: p}}
	run := &ManagedTest{ID: "run", Status: "RUNNING", Groups: []TestGroup{g}, remoteGroupShards: map[string][]GeneratorShardSummary{g.Name: shards}}
	tr := &TestRunner{tests: map[string]*ManagedTest{run.ID: run}}
	b := &distributedExecutionBackend{timeout: time.Second}
	c := &javaHostScaleTestClient{err: errors.New("lost response")}
	clients := map[string]generatorClient{"gen-a": c}
	err := b.scaleJavaHostGroup(tr, run, g, shards, clients, context.Background(), 0, 0, nil, 1)
	var uncertain *distributedVUserUncertainError
	if !errors.As(err, &uncertain) || c.calls != 1 {
		t.Fatalf("uncertain scale replayed or swallowed: calls=%d err=%v", c.calls, err)
	}
	c.err = nil
	if err := b.scaleJavaHostGroup(tr, run, g, shards, clients, context.Background(), 0, 0, nil, 1); err != nil {
		t.Fatal(err)
	}
	if run.remoteGroupShards[g.Name][0].VUsers != 1 || run.remoteGroupShards[g.Name][0].JavaHost == nil {
		t.Fatal("placement lost after closure")
	}
	if err := b.scaleJavaHostGroup(tr, run, g, run.remoteGroupShards[g.Name], clients, context.Background(), 0, 0, nil, 2); err == nil {
		t.Fatal("closed sessions recreated")
	}
}

func TestJavaHostShardsRespectIndependentCeilingsAndApprovals(t *testing.T) {
	g, a := javaHostPlacementFixture(t)
	g.VUsers = 7
	a.JavaHost.MaxHosts = 1
	_, b := javaHostPlacementFixture(t)
	b.Target = "gen-b"
	b.JavaHost.MaxMemoryBytes = 6 * b.JavaHost.Profiles[0].MemoryBytes
	b.JavaHost.Profiles[0].PolicySHA256 = strings.Repeat("e", 64)
	loads := map[string]generatorLoadSnapshot{"gen-a": a, "gen-b": b}
	targets := []string{"gen-a", "gen-a", "gen-b"}
	shards := chooseJavaHostShards(g, loads, loads, targets, targets, nil, nil)
	if len(shards) != 2 || shards[0].VUsers != 1 || shards[1].VUsers != 6 {
		t.Fatalf("hard ceilings or duplicate targets mishandled: %+v", shards)
	}
	if shards[0].JavaHost.Profile.PolicySHA256 != a.JavaHost.Profiles[0].PolicySHA256 || shards[1].JavaHost.Profile.PolicySHA256 != b.JavaHost.Profiles[0].PolicySHA256 {
		t.Fatal("per-generator approval identity lost")
	}
	g.VUsers = 8
	if got := chooseJavaHostShards(g, loads, loads, targets, targets, nil, nil); len(got) != 0 {
		t.Fatalf("accepted partial capacity: %+v", got)
	}
	g.VUsers = 2
	g.Assignment.Mode = generatorAssignmentModeSpecificHosts
	g.Assignment.SelectedTargets = []string{"gen-a"}
	if got := chooseJavaHostShards(g, loads, loads, targets, targets, nil, nil); len(got) != 0 {
		t.Fatal("escaped selected targets")
	}
}

func TestJavaHostAllocationNeverResurrectsClosedShard(t *testing.T) {
	// Exercise rounding near every capacity boundary, including empty shards.
	targets := []string{"a", "b", "c"}
	for a := 0; a <= 8; a++ {
		for b := 0; b <= 8; b++ {
			for c := 0; c <= 8; c++ {
				ceilings := map[string]int{"a": a, "b": b, "c": c}
				for total := 0; total <= a+b+c+1; total++ {
					allocation := boundedJavaHostAllocations(total, targets, ceilings)
					if total > a+b+c {
						if allocation != nil {
							t.Fatal("accepted excess capacity")
						}
						continue
					}
					sum := 0
					for _, target := range targets {
						n := allocation[target]
						if n < 0 || n > ceilings[target] {
							t.Fatalf("ceiling violated: %v -> %v", ceilings, allocation)
						}
						sum += n
					}
					if sum != total {
						t.Fatalf("lost population: %d -> %v", total, allocation)
					}
				}
			}
		}
	}
}

func TestJavaHostMultiShardScaleDownPreservesIdentityAndStopsOnPartialFailure(t *testing.T) {
	for _, name := range []string{"success", "rejected", "ramping", "transport"} {
		t.Run(name, func(t *testing.T) {
			g, load := javaHostPlacementFixture(t)
			p, _ := javaHostPlacementForGroup(g, load)
			p.ReservationID = "original"
			shards := []GeneratorShardSummary{{Group: g.Name, Target: "a", VUsers: 2, ShardOrdinal: 0, ShardCount: 2, JavaHost: p}, {Group: g.Name, Target: "b", VUsers: 2, ShardOrdinal: 1, ShardCount: 2, JavaHost: p}}
			run := &ManagedTest{ID: "run", Status: "RUNNING", remoteGroupShards: map[string][]GeneratorShardSummary{g.Name: shards}}
			tr := &TestRunner{tests: map[string]*ManagedTest{run.ID: run}}
			backend := &distributedExecutionBackend{timeout: time.Second}
			a, b := &javaHostScaleTestClient{}, &javaHostScaleTestClient{}
			switch name {
			case "rejected":
				b.reject = true
			case "ramping":
				b.err = engine.ErrInitialRampInProgress
			case "transport":
				b.err = errors.New("lost ack")
			}
			err := backend.scaleJavaHostGroup(tr, run, g, shards, map[string]generatorClient{"a": a, "b": b}, context.Background(), 0, 0, nil, 2)
			if a.calls != 1 || b.calls != 1 {
				t.Fatalf("compensation or missing call: %d/%d", a.calls, b.calls)
			}
			if name != "success" {
				var uncertain *distributedVUserUncertainError
				if !errors.As(err, &uncertain) {
					t.Fatalf("partial change did not stop run: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for i, shard := range run.remoteGroupShards[g.Name] {
				if shard.VUsers != 1 || shard.ShardOrdinal != uint32(i) || shard.ShardCount != 2 || shard.JavaHost.ReservationID != "original" {
					t.Fatalf("lost identity: %+v", shard)
				}
			}
		})
	}
}

func TestJavaHostStagesReserveFreshLifetimeAdmissions(t *testing.T) {
	g, load := javaHostPlacementFixture(t)
	g.stageDriven = true
	g.currentVUsers = 0
	g.Stages = []storage.LoadStage{{Type: "ramp_up", TargetVUsers: 2}, {Type: "ramp_down", TargetVUsers: 0}, {Type: "ramp_up", TargetVUsers: 2}}
	load.JavaHost.PreparedPopulation = true
	load.CompatibleFeatures = append(load.CompatibleFeatures, generatorrpc.FeatureJavaHostPreparedPopulationV1)
	loads := map[string]generatorLoadSnapshot{"a": load, "b": load}
	peak := chooseJavaHostShards(g, loads, loads, []string{"a", "b"}, []string{"a", "b"}, nil, nil)
	if len(peak) != 2 || peak[0].VUsers+peak[1].VUsers != 4 {
		t.Fatalf("schedule reused closed hosts: %+v", peak)
	}
	initial := initialGroupShardsForRun(g, peak, loads)
	if len(initial) != 2 || initial[0].VUsers != 0 || initial[1].VUsers != 0 || initial[0].JavaHost.PreparedVUsers != 2 {
		t.Fatal("cold peak lost", initial)
	}
	run := &ManagedTest{ID: "run", Status: "RUNNING", remoteGroupShards: map[string][]GeneratorShardSummary{g.Name: initial}}
	tr := &TestRunner{tests: map[string]*ManagedTest{run.ID: run}}
	backend := &distributedExecutionBackend{timeout: time.Second}
	clients := map[string]generatorClient{"a": &javaHostScaleTestClient{}, "b": &javaHostScaleTestClient{}}
	for _, n := range []int{2, 0, 2, 0} {
		if err := backend.scaleJavaHostGroup(tr, run, g, run.remoteGroupShards[g.Name], clients, t.Context(), 0, 0, nil, n); err != nil {
			t.Fatal(n, err)
		}
	}
	if err := backend.scaleJavaHostGroup(tr, run, g, run.remoteGroupShards[g.Name], clients, t.Context(), 0, 0, nil, 1); err == nil {
		t.Fatal("exhausted admissions were reused")
	}
}

func TestJavaHostInitialShardsRejectMissingMetadata(t *testing.T) {
	g, _ := javaHostPlacementFixture(t)
	g.stageDriven = true
	g.currentVUsers = 0
	if initialJavaHostShards(g, []GeneratorShardSummary{{Target: "a", VUsers: 2}}) != nil {
		t.Fatal("missing prepared metadata accepted")
	}
}

func TestJavaHostPlacementFailureAlwaysExplainsAdmissionDemand(t *testing.T) {
	g, load := javaHostPlacementFixture(t)
	message := generatorPlacementRejectionMessage(g, map[string]generatorLoadSnapshot{"a": load}, nil, nil)
	if !strings.Contains(message, "2 lifetime admissions") || !strings.Contains(message, "java_host_aggregate_capacity_or_placement_unavailable") {
		t.Fatal(message)
	}
}
