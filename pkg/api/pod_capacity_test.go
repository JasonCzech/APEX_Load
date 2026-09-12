package api

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestPodCapacityConfiguredBudgetIsBoundedByRuntime(t *testing.T) {
	for _, tc := range []struct {
		name                string
		cpu                 float64
		memory              int64
		wantCPU, wantMemory float64
	}{
		{"larger pod respects configuration", 16, 32000, 3.2, 6400},
		{"smaller pod bounds configuration", 2, 4000, 1.6, 3200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			settings := defaultSystemSettings(10)
			settings.CapacityPlanning.PodReservationsEnabled = true
			settings.CapacityPlanning.Generator.CPUCores = 4
			settings.CapacityPlanning.Generator.MemoryMB = 8000
			load := generatorLoadSnapshot{HeartbeatOK: true, CPULimitCores: tc.cpu, MemoryLimitBytes: tc.memory * 1024 * 1024, MaxVUsers: 10000, SupportedProtocols: []string{"soap"}, CompatibleFeatures: []string{generatorrpc.FeaturePodCapacityV1}, PodCapacity: &protocol.CapacitySnapshot{State: "unconfigured", Incarnation: "pod"}}
			got := configurePodCapacity(settings, load)
			if got.State != "ready" || math.Abs(got.Schedulable.CPUCores-tc.wantCPU) > 1e-9 || got.Schedulable.MemoryMB != tc.wantMemory {
				t.Fatalf("configured/runtime budget: %+v", got)
			}
		})
	}
}

func TestPodCapacityDedicatedPreview(t *testing.T) {
	settings := defaultSystemSettings(10)
	settings.CapacityPlanning.PodReservationsEnabled = true
	tr := &TestRunner{settings: settings, execution: newDistributedExecutionBackend([]string{"generator:9090"}, time.Second, nil), liveInventoryAt: time.Now(), liveInventory: []GeneratorInventoryEntry{{Target: "generator:9090", Status: "connected", DrainState: "active", AvailableCapacity: 10000, MaxVUsers: 10000, CPULimitCores: 8, MemoryLimitBytes: 16000 * 1024 * 1024, SupportedProtocols: []string{"soap"}, CompatibleFeatures: []string{generatorrpc.FeaturePodCapacityV1}, PodCapacity: &protocol.CapacitySnapshot{State: "unconfigured", Incarnation: "pod"}}}}
	test := &ManagedTest{Groups: []TestGroup{{Name: "soap", Protocol: "soap", VUsers: 10}}, Assignment: &GeneratorAssignmentSettings{SharingMode: "dedicated"}}
	placements, warnings := tr.previewPodPlacementLocked(test)
	if len(warnings) != 0 || len(placements) != 1 {
		t.Fatalf("preview: %+v %v", placements, warnings)
	}
	if placements[0].Before.Dedicated || !placements[0].After.Dedicated || placements[0].After.ReservedPct != 100 {
		t.Fatalf("dedicated occupancy lost: %+v", placements[0])
	}
}

func TestPodCapacityDemandMetrics(t *testing.T) {
	var body strings.Builder
	writePodCapacityDemand(&body, "reserved", protocol.CapacityVector{CPUCores: 4, MemoryMB: 8000, Protocols: map[string]float64{"soap": 100}})
	if !strings.Contains(body.String(), `state="reserved",resource="cpu"} 4`) || !strings.Contains(body.String(), `state="reserved",resource="memory"} 8000`) {
		t.Fatalf("resource dimensions absent: %s", body.String())
	}
}

func TestPodCapacitySharingModeIsTestScoped(t *testing.T) {
	for _, mode := range []string{"shared", "dedicated"} {
		assignment := &GeneratorAssignmentSettings{SharingMode: mode}
		if _, err := normalizeGeneratorAssignmentSettings(assignment); err != nil {
			t.Fatal(err)
		}
		if _, err := normalizeGroupGeneratorAssignmentSettings(assignment); err == nil {
			t.Fatal("accepted group-scoped isolation directive")
		}
	}
}

func TestPodCapacityAdaptiveAndStagePeaks(t *testing.T) {
	maximum := 100
	test := &ManagedTest{ScenarioMode: "goal_oriented", Groups: []TestGroup{{Name: "soap", Protocol: "soap", VUsers: 10}}}
	if _, err := podReservationGroups(test); err == nil {
		t.Fatal("unbounded adaptive admission accepted")
	}
	test.GoalConfig = &GoalConfig{MaxVUsers: &maximum}
	groups, err := podReservationGroups(test)
	if err != nil || groups[0].VUsers != maximum || test.Groups[0].VUsers != 10 {
		t.Fatalf("adaptive peak mutated initial load: %+v %v", groups, err)
	}
	shards := initialGroupShardsForRun(test.Groups[0], []GeneratorShardSummary{{Target: "a", VUsers: 60}, {Target: "b", VUsers: 40}}, nil)
	total := 0
	for _, s := range shards {
		total += s.VUsers
	}
	if total != 10 {
		t.Fatalf("started adaptive maximum instead of initial load: %+v", shards)
	}
	test.ScenarioMode = "manual"
	test.Groups[0].Stages = []storage.LoadStage{{TargetVUsers: 40}, {TargetVUsers: 5}}
	groups, err = podReservationGroups(test)
	if err != nil || groups[0].VUsers != 40 {
		t.Fatalf("stage peak: %+v %v", groups, err)
	}
}

func TestPodCapacityPlacementUsesPeakVectorAndPacks(t *testing.T) {
	settings := defaultSystemSettings(10)
	settings.CapacityPlanning.PodReservationsEnabled = true
	settings.CapacityPlanning.Generator.CPUCores = 8
	settings.CapacityPlanning.Generator.MemoryMB = 16000
	settings.CapacityPlanning.Generator.NetworkMbps = 1000
	settings.CapacityPlanning.Generator.MaxVUsers = 10000
	load := generatorLoadSnapshot{HeartbeatOK: true, AvailableCapacity: 10000, CPULimitCores: 8, MemoryLimitBytes: 16000 * 1024 * 1024, MaxVUsers: 10000, SupportedProtocols: []string{"soap", "http"}, CompatibleFeatures: []string{generatorrpc.FeaturePodCapacityV1}, PodCapacity: &protocol.CapacitySnapshot{State: "unconfigured", Incarnation: "one", ObservedAtUnixMS: time.Now().UnixMilli()}}
	s := configurePodCapacity(settings, load)
	if s.State != "ready" || s.Schedulable.Protocols["soap"] != 4000 {
		t.Fatalf("snapshot %+v", s)
	}
	load.PodCapacity = s
	busy := load
	consumePodGroupCapacity(&busy, TestGroup{Name: "existing", Protocol: "soap", VUsers: 1000}, 1000)
	busy.podPlacementOwned = false
	loads := map[string]generatorLoadSnapshot{"empty": load, "busy": busy}
	group := TestGroup{Name: "new", Protocol: "soap", VUsers: 1000}
	shards := podCapacityShards(group, loads, []string{"empty", "busy"}, nil, nil, nil)
	if len(shards) != 1 || shards[0].Target != "busy" {
		t.Fatalf("did not pack busy pod: %+v", shards)
	}
	shards = podCapacityShards(group, loads, []string{"empty", "busy"}, &GeneratorAssignmentSettings{SharingMode: "dedicated"}, nil, nil)
	if len(shards) != 1 || shards[0].Target != "empty" {
		t.Fatalf("dedicated placement: %+v", shards)
	}
	group.VUsers = 4001
	if got := podCapacityShards(group, map[string]generatorLoadSnapshot{"empty": load}, []string{"empty"}, nil, nil, nil); len(got) > 0 {
		t.Fatalf("exceeded protocol peak: %+v", got)
	}
	if load.PodCapacity.ReservedPct != 0 {
		t.Fatal("placement mutated shared snapshot")
	}
}

func TestPodCapacityReplicaFloorProtectsHighestOrdinal(t *testing.T) {
	claims := []storage.PodCapacityReservation{{Target: "generator-7.generators.svc:9090"}, {Target: "generator-2.generators.svc:9090"}}
	if got := reservedReplicaFloor(claims, 2); got != 8 {
		t.Fatalf("replica floor %d, want 8", got)
	}
	if got := reservedReplicaFloor([]storage.PodCapacityReservation{{Target: "external-generator:9090"}}, 12); got != 12 {
		t.Fatalf("external floor %d", got)
	}
	if got := reservedReplicaFloor(nil, 12); got != 0 {
		t.Fatalf("empty floor %d", got)
	}
}

func TestPodCapacityUnavailableAndFrozenBudget(t *testing.T) {
	settings := defaultSystemSettings(10)
	settings.CapacityPlanning.PodReservationsEnabled = true
	if got := configurePodCapacity(settings, generatorLoadSnapshot{}); got.State != "unavailable" {
		t.Fatalf("missing capacity accepted: %+v", got)
	}
	budget := protocol.CapacityVector{CPUCores: 4, MemoryMB: 8000, NetworkMbps: 800, VUsers: 8000}
	s := protocol.NewCapacitySnapshot(budget, budget.Scale(.5), 20)
	s.Incarnation = "frozen"
	load := generatorLoadSnapshot{HeartbeatOK: true, PodCapacity: &s, CompatibleFeatures: []string{generatorrpc.FeaturePodCapacityV1}}
	settings.CapacityPlanning.HeadroomPct = 40
	got := configurePodCapacity(settings, load)
	if got.HeadroomPct != 20 || got.ReservedPct != 50 {
		t.Fatalf("changed admitted model: %+v", got)
	}
}
