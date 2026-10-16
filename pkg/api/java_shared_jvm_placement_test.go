package api

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func sharedJVMTestGroup(vusers int, sharing string) TestGroup {
	action := func(method string) []dsl.Action {
		return []dsl.Action{{Type: "java_vuser_run", Target: "Actions", Params: map[string]any{"method": method}}}
	}
	script := &dsl.Script{ID: "java", Protocol: "java_vuser", Config: dsl.ScriptConfig{JavaVuser: dsl.JavaVuserConfig{JVMSharing: sharing}}, Init: action("init"), Actions: action("action"), Teardown: action("end")}
	return TestGroup{Name: "java", Protocol: "java_vuser", VUsers: vusers, script: script}
}

func javaRuntimeLoad(dedicatedAvailable int64, shared *generatorrpc.SharedJVMCapacity) generatorLoadSnapshot {
	return generatorLoadSnapshot{AvailableCapacity: 10000, RuntimeCapacities: []generatorrpc.ProtocolRuntimeCapacity{{Protocol: "java_vuser", Available: true, MaxVUsers: 25, AvailableVUsers: dedicatedAvailable, OccupancyMode: generatorrpc.RuntimeOccupancyModeIdle, SharedJVM: shared}}}
}

func TestSharedJVMGroupsPlaceOnSharedCapacity(t *testing.T) {
	shared := sharedJVMTestGroup(5000, dsl.JavaVuserJVMSharingShared)
	loads := map[string]generatorLoadSnapshot{
		"gen-a": javaRuntimeLoad(25, &generatorrpc.SharedJVMCapacity{VUsersPerJVM: 250, MaxVUsers: 6250, AvailableVUsers: 3000}),
		"gen-b": javaRuntimeLoad(25, &generatorrpc.SharedJVMCapacity{VUsersPerJVM: 250, MaxVUsers: 6250, AvailableVUsers: 3000}),
	}
	shards := capacityWeightedShardsForGroup(shared, []string{"gen-a", "gen-b"}, loads, "")
	total := 0
	for _, shard := range shards {
		if shard.VUsers > 3000 {
			t.Fatalf("shard exceeds shared capacity: %+v", shard)
		}
		total += shard.VUsers
	}
	if len(shards) != 2 || total != 5000 {
		t.Fatalf("5,000 shared VUsers not placed on two generators: %+v", shards)
	}
	if !runtimeCapacityAllowsGroup(shared, loads["gen-a"], 3000) || runtimeCapacityAllowsGroup(shared, loads["gen-a"], 3001) {
		t.Fatal("shared admission bound wrong")
	}

	// A dedicated script keeps one JVM per VUser on the same generators.
	dedicated := sharedJVMTestGroup(5000, "")
	if shards := capacityWeightedShardsForGroup(dedicated, []string{"gen-a", "gen-b"}, loads, ""); shards != nil {
		t.Fatalf("dedicated group placed beyond 50 JVMs: %+v", shards)
	}
	// Without a shared envelope a shared script falls back to dedicated slots.
	plain := map[string]generatorLoadSnapshot{"gen-a": javaRuntimeLoad(25, nil)}
	if runtimeCapacityAllowsGroup(shared, plain["gen-a"], 26) || !runtimeCapacityAllowsGroup(shared, plain["gen-a"], 25) {
		t.Fatal("shared script without an envelope ignored dedicated capacity")
	}
}

func TestSharedJVMCapacityConsumptionIsIsolated(t *testing.T) {
	group := sharedJVMTestGroup(1000, dsl.JavaVuserJVMSharingShared)
	original := javaRuntimeLoad(25, &generatorrpc.SharedJVMCapacity{VUsersPerJVM: 250, MaxVUsers: 6250, AvailableVUsers: 3000})
	snapshot := original
	snapshot.RuntimeCapacities = cloneProtocolRuntimeCapacities(original.RuntimeCapacities)
	consumeGroupRuntimeCapacity(&snapshot, group, 1000)
	java := snapshot.RuntimeCapacities[0]
	if java.SharedJVM.AvailableVUsers != 2000 || java.AvailableVUsers != 25 || java.OccupancyMode != generatorrpc.RuntimeOccupancyModePersistent {
		t.Fatalf("consumption %+v shared %+v", java, java.SharedJVM)
	}
	if original.RuntimeCapacities[0].SharedJVM.AvailableVUsers != 3000 {
		t.Fatal("clone shared the envelope with the inventory snapshot")
	}
	restoreGroupRuntimeCapacity(&snapshot, group, 1000)
	if snapshot.RuntimeCapacities[0].SharedJVM.AvailableVUsers != 3000 {
		t.Fatalf("restore %+v", snapshot.RuntimeCapacities[0].SharedJVM)
	}
	restoreGroupRuntimeCapacity(&snapshot, group, 10000)
	if snapshot.RuntimeCapacities[0].SharedJVM.AvailableVUsers != 6250 {
		t.Fatal("restore exceeded the envelope maximum")
	}
}

func TestJavaVuserOverrideSharingRules(t *testing.T) {
	script := dsl.JavaVuserConfig{JVMSharing: dsl.JavaVuserJVMSharingShared, VUsersPerJVM: 120}
	applied := script
	applyJavaVuserRuntimeOverride(&applied, &RuntimeJavaVuserOverride{JVMSharing: dsl.JavaVuserJVMSharingDedicated})
	if applied.JVMSharing != dsl.JavaVuserJVMSharingDedicated || applied.VUsersPerJVM != 0 {
		t.Fatalf("dedicated override kept the per-JVM count: %+v", applied)
	}
	applied = script
	applyJavaVuserRuntimeOverride(&applied, &RuntimeJavaVuserOverride{VUsersPerJVM: 40})
	if applied.JVMSharing != dsl.JavaVuserJVMSharingShared || applied.VUsersPerJVM != 40 {
		t.Fatalf("count override %+v", applied)
	}
	// Thread mode also shares JVMs, so the per-JVM count carries over.
	applied = script
	applyJavaVuserRuntimeOverride(&applied, &RuntimeJavaVuserOverride{JVMSharing: dsl.JavaVuserJVMSharingThreads})
	if applied.JVMSharing != dsl.JavaVuserJVMSharingThreads || applied.VUsersPerJVM != 120 {
		t.Fatalf("thread mode override %+v", applied)
	}
	if merged := mergeJavaVuserRuntimeOverride(&RuntimeJavaVuserOverride{VUsersPerJVM: 80}, &RuntimeJavaVuserOverride{JVMSharing: dsl.JavaVuserJVMSharingThreads}); merged.JVMSharing != dsl.JavaVuserJVMSharingThreads || merged.VUsersPerJVM != 80 {
		t.Fatalf("thread mode merge %+v", merged)
	}
	merged := mergeJavaVuserRuntimeOverride(&RuntimeJavaVuserOverride{JVMSharing: dsl.JavaVuserJVMSharingShared, VUsersPerJVM: 80}, &RuntimeJavaVuserOverride{JVMSharing: dsl.JavaVuserJVMSharingDedicated})
	if merged.JVMSharing != dsl.JavaVuserJVMSharingDedicated || merged.VUsersPerJVM != 0 {
		t.Fatalf("merge %+v", merged)
	}
	if projected := runtimeJavaVuserOverrideFromConfig(script); projected == nil || projected.JVMSharing != dsl.JavaVuserJVMSharingShared || projected.VUsersPerJVM != 120 {
		t.Fatalf("projection %+v", projected)
	}
	if runtimeJavaVuserOverrideFromConfig(dsl.JavaVuserConfig{}) != nil {
		t.Fatal("empty config projected an override")
	}
}
