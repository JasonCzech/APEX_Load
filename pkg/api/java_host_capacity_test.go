package api

import (
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/protocol"
	"testing"
	"time"
)

func javaHostFacadeCapacityFixture(t *testing.T) (TestGroup, generatorLoadSnapshot) {
	t.Helper()
	group, load := javaHostPlacementFixture(t)
	r := generatorrpc.JavaHostFacadeResources{CPU: 100, MemoryBytes: 8 << 20, NetworkKbps: 100}
	p := &load.JavaHost.Profiles[0]
	p.FacadeResourcesSHA256, _ = r.Digest()
	p.FacadeCPUMillicores, p.FacadeMemoryBytes, p.FacadeNetworkKbps = r.CPU, r.MemoryBytes, r.NetworkKbps
	capacity := protocol.NewCapacitySnapshot(protocol.CapacityVector{CPUCores: 1, MemoryMB: 16, NetworkMbps: 10, VUsers: 100, Protocols: map[string]float64{javaHostFacadeCapacityProtocol: 100}}, protocol.CapacityVector{}, 0)
	capacity.Incarnation = "pod"
	load.PodCapacity = &capacity
	load.CompatibleFeatures = append(load.CompatibleFeatures, generatorrpc.FeaturePodCapacityV1)
	load.MaxVUsers, load.CPULimitCores, load.MemoryLimitBytes = 100, 4, 1<<30
	return group, load
}

func TestJavaHostPodAndExecutionBudgetsAreIndependent(t *testing.T) {
	g, load := javaHostFacadeCapacityFixture(t)
	placement, err := javaHostPlacementForGroup(g, load)
	if err != nil {
		t.Fatal(err)
	}
	demand := podShardDemand(g, GeneratorShardSummary{VUsers: 2, JavaHost: placement})
	if demand.MemoryMB != 16 || demand.CPUCores != .2 || demand.Protocols[javaHostFacadeCapacityProtocol] != 2 || demand.Protocols["java_vuser"] != 0 {
		t.Fatal(demand)
	}
	original := load
	consumeGroupRuntimeCapacity(&load, g, 2)
	consumePodGroupCapacity(&load, g, 2)
	if load.JavaHost.MaxMemoryBytes != 1<<29 || load.PodCapacity.Reserved.MemoryMB != 16 || original.PodCapacity.Reserved.MemoryMB != 0 || original.JavaHost.MaxMemoryBytes != 1<<30 {
		t.Fatal("budget accounting aliased or mixed")
	}
	if _, err := javaHostPlacementForGroup(g, load); err == nil {
		t.Fatal("exhausted facade capacity accepted")
	}
	load = original
	load.JavaHost = cloneJavaHostCapabilitySummary(load.JavaHost)
	load.JavaHost.MaxMemoryBytes = 1
	if _, err := javaHostPlacementForGroup(g, load); err == nil {
		t.Fatal("free generator budget bypassed exhausted execution budget")
	}
}

func TestJavaHostPodPlacementProjectsAcrossGroups(t *testing.T) {
	g, load := javaHostFacadeCapacityFixture(t)
	g.VUsers = 3
	loads := map[string]generatorLoadSnapshot{"a": load, "b": load}
	specs := []generatorTargetSpec{{Address: "a"}, {Address: "b"}}
	next := g
	next.Name = "second"
	shards := planDistributedGroupShards([]TestGroup{g, next}, loads, []string{"a", "b"}, nil, specs)
	if len(shards[g.Name]) != 2 || len(shards[next.Name]) != 0 {
		t.Fatalf("combined facade budgets not enforced: %+v", shards)
	}
	if loads["a"].PodCapacity.Reserved.MemoryMB != 0 {
		t.Fatal("mutated heartbeat")
	}
}

func TestJavaHostPodPreviewAndConfigurationNeedNoLocalJavaSlots(t *testing.T) {
	g, load := javaHostFacadeCapacityFixture(t)
	settings := defaultSystemSettings(10)
	settings.CapacityPlanning.PodReservationsEnabled = true
	load.PodCapacity = &protocol.CapacitySnapshot{State: "unconfigured", Incarnation: "pod"}
	capacity := configurePodCapacity(settings, load)
	if capacity.State != "ready" || capacity.Schedulable.Protocols[javaHostFacadeCapacityProtocol] == 0 || capacity.Schedulable.Protocols["java_vuser"] != 0 {
		t.Fatalf("remote facade required local JVM advertisement: %+v", capacity)
	}
	tr := &TestRunner{settings: settings, execution: newDistributedExecutionBackend([]string{"gen-a"}, time.Second, nil), liveInventoryAt: time.Now(), liveInventory: []GeneratorInventoryEntry{{Target: "gen-a", Status: "connected", DrainState: "active", AvailableCapacity: 100, MaxVUsers: 100, CPULimitCores: 4, MemoryLimitBytes: 1 << 30, CompatibleFeatures: load.CompatibleFeatures, JavaHost: load.JavaHost, PodCapacity: load.PodCapacity}}}
	preview, reasons := tr.previewPodPlacementLocked(&ManagedTest{Groups: []TestGroup{g}})
	if len(reasons) != 0 || len(preview) != 1 || preview[0].Proposed.MemoryMB != 16 {
		t.Fatalf("preview lost remote budget: %+v / %v", preview, reasons)
	}
	tr.enrichPodCapacityInventory(tr.liveInventory)
	if tr.liveInventory[0].PodCapacity.Schedulable.Protocols[javaHostFacadeCapacityProtocol] == 0 {
		t.Fatal("inventory lost facade budget")
	}
}
