package api

import (
	"fmt"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestRecoveryAdmissionReplicaFloorRequestsWholeSourceAndSparePods(t *testing.T) {
	for _, mode := range []string{"queued", "initializing", "running", "off", "capped", "headroom", "missing limit", "stale", "no groups"} {
		t.Run(mode, func(t *testing.T) {
			tr, test, _, _ := recoveryAdmissionFixture()
			test.Status = "QUEUED"
			test.Groups[0].VUsers = 100
			test.TotalVUsers = 100
			test.Assignment.RecoverySpareVUsers = 160
			for i := range tr.liveInventory {
				e := &tr.liveInventory[i]
				c := *e.PodCapacity
				c.Schedulable.VUsers = 80
				c.Schedulable.Protocols = map[string]float64{"http": 80}
				e.PodCapacity = &c
			}
			want, complete := 4, true
			switch mode {
			case "initializing":
				test.Status = "INITIALIZING"
			case "running":
				test.Status = "RUNNING"
				want = 0
			case "off":
				test.Assignment.RecoverySpareVUsers = 0
				want = 0
			case "capped":
				for i := range tr.liveInventory {
					tr.liveInventory[i].PodPlacement.scaleLimit.MaximumReplicas = 3
				}
				want = 3
			case "headroom":
				test.Groups[0].VUsers = 50
				test.TotalVUsers = 50
				want = 2
			case "missing limit":
				for i := range tr.liveInventory {
					tr.liveInventory[i].PodPlacement.scaleLimit.WorkloadUID = ""
				}
				want, complete = 0, false
			case "stale":
				tr.liveInventoryAt = time.Now().Add(-time.Hour)
				want, complete = 0, false
			case "no groups":
				test.Groups = nil
				want, complete = 0, false
			}
			floor, ok := tr.recoveryAdmissionReplicaFloor([]*ManagedTest{test}, tr.settings, 0, time.Now())
			if floor != want || ok != complete {
				t.Fatalf("floor=%d complete=%v want=%d,%v", floor, ok, want, complete)
			}
		})
	}
}

func TestRecoveryAdmissionReplicaFloorPreservesExistingClaims(t *testing.T) {
	tr, test, _, _ := recoveryAdmissionFixture()
	test.Status = "PENDING"
	floor, ok := tr.recoveryAdmissionReplicaFloor([]*ManagedTest{test}, tr.settings, 5, time.Now())
	if !ok || floor != 5 {
		t.Fatalf("existing floor lowered: %d %v", floor, ok)
	}
}

func TestRecoveryAdmissionReplicaFloorEscapesSingleWorkerQueue(t *testing.T) {
	for _, tc := range []struct {
		name       string
		vusers     int
		capacity   float64
		maximum    int
		high       bool
		secondNode bool
		want       int
	}{
		{"small", 50, 100, 4, false, false, 3},
		{"headroom", 50, 80, 4, false, false, 3},
		{"two sources", 100, 80, 4, false, false, 4},
		{"bounded", 50, 100, 2, false, false, 2},
		{"high ordinal", 50, 100, 6, true, false, 5},
		{"already spread", 50, 100, 4, false, true, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr, test, _, _ := recoveryAdmissionFixture()
			tr.liveInventory = tr.liveInventory[:2]
			test.Status = "QUEUED"
			test.TotalVUsers = tc.vusers
			test.Groups[0].VUsers = tc.vusers
			for i := range tr.liveInventory {
				e := &tr.liveInventory[i]
				e.PodPlacement.scaleLimit.MaximumReplicas = tc.maximum
				e.PodCapacity.Schedulable.VUsers = tc.capacity
				e.PodCapacity.Schedulable.Protocols["http"] = tc.capacity
			}
			if tc.high {
				// Transport aliases do not necessarily carry StatefulSet ordinals.
				tr.liveInventory[1].PodPlacement.Name = "generator-3"
			}
			if tc.secondNode {
				tr.liveInventory[1].PodPlacement.NodeName = "worker-2"
			}
			got, complete := tr.recoveryAdmissionReplicaFloor([]*ManagedTest{test}, tr.settings, 0, time.Now())
			if !complete || got != tc.want {
				t.Fatalf("floor=%d complete=%v want=%d", got, complete, tc.want)
			}
		})
	}
}

func TestSingleWorkerRecoveryReplicaFloorRequiresKnownPlacement(t *testing.T) {
	for _, pod := range []*GeneratorPodPlacement{nil, {}, {NodeName: " "}} {
		if got := singleWorkerRecoveryReplicaFloor(map[string]*GeneratorPodPlacement{"a": pod}, 1, 1, 4); got != 0 {
			t.Fatalf("unknown placement produced floor %d", got)
		}
	}
	if got := singleWorkerRecoveryReplicaFloor(nil, 0, 1, 4); got != 0 {
		t.Fatalf("empty inventory produced floor %d", got)
	}
}

func TestRecoverySingleWorkerQueueStillRequiresObservedSpare(t *testing.T) {
	tr, test, claims, loads := recoveryAdmissionFixture()
	test.Status = "QUEUED"
	test.TotalVUsers = 50
	test.Groups[0].VUsers = 50
	test.Assignment.RecoverySpareVUsers = 100
	claims = claims[:1]
	claims[0].Demand = claims[0].Demand.Scale(0.5)
	claims[0].GroupPeaks["http"] = 50
	claims[0].GroupDemands["http"] = claims[0].Demand
	future := tr.liveInventory[2]
	tr.liveInventory = tr.liveInventory[:2]
	if floor, complete := tr.recoveryAdmissionReplicaFloor([]*ManagedTest{test}, tr.settings, 0, time.Now()); !complete || floor != 3 {
		t.Fatalf("did not request a new pod: %d %v", floor, complete)
	}
	if _, err := tr.addRecoverySpareClaims(test, claims, loads); err == nil {
		t.Fatal("replica request was mistaken for reserved cross-worker capacity")
	}
	tr.liveInventory = append(tr.liveInventory, future)
	got, err := tr.addRecoverySpareClaims(test, claims, loads)
	if err != nil || len(got) != 2 || got[1].Target != "c" || got[1].RecoveryPlacement.NodeName != "worker-2" {
		t.Fatalf("observed spare not admitted: claims=%+v error=%v", got, err)
	}
}

func TestRecoveryAdmissionReplicaFloorIgnoresIneligibleWorker(t *testing.T) {
	for _, mode := range []string{"disconnected", "cordoned", "incompatible", "draining", "wrong protocol"} {
		t.Run(mode, func(t *testing.T) {
			tr, test, _, _ := recoveryAdmissionFixture()
			tr.liveInventory = tr.liveInventory[:3]
			test.Status = "QUEUED"
			test.TotalVUsers = 50
			test.Groups[0].VUsers = 50
			e := &tr.liveInventory[2]
			switch mode {
			case "disconnected":
				e.Status = "disconnected"
			case "cordoned":
				e.Cordoned = true
			case "incompatible":
				e.CompatibilityState = "incompatible"
			case "draining":
				e.DrainState = "draining"
			case "wrong protocol":
				e.SupportedProtocols = []string{"database"}
			}
			floor, complete := tr.recoveryAdmissionReplicaFloor([]*ManagedTest{test}, tr.settings, 0, time.Now())
			if !complete || floor != 4 {
				t.Fatalf("unusable worker prevented spare request: floor=%d complete=%v", floor, complete)
			}
		})
	}
}

func TestRecoveryAdmissionReplicaFloorRequestsBeyondOccupiedIneligibleOrdinals(t *testing.T) {
	for _, mode := range []string{"incompatible", "not ready", "terminating"} {
		t.Run(mode, func(t *testing.T) {
			tr, run, _, _ := recoveryAdmissionFixture()
			run.Status = "QUEUED"
			run.TotalVUsers = 200
			run.Groups[0].VUsers = 200
			for i := range tr.liveInventory {
				tr.liveInventory[i].PodPlacement.scaleLimit.MaximumReplicas = 5
			}
			// The two usable pods are on separate workers and each is needed as a
			// source. The other two StatefulSet ordinals occupy replicas but cannot
			// serve as the required recovery spare.
			for _, i := range []int{1, 3} {
				if mode == "incompatible" {
					tr.liveInventory[i].CompatibilityState = "incompatible"
				} else if mode == "not ready" {
					tr.liveInventory[i].PodPlacement.Ready = false
				} else {
					tr.liveInventory[i].PodPlacement.Terminating = true
				}
			}
			floor, complete := tr.recoveryAdmissionReplicaFloor([]*ManagedTest{run}, tr.settings, 0, time.Now())
			if !complete || floor != 5 {
				t.Fatalf("floor=%d complete=%v, want a fifth ordinal", floor, complete)
			}
		})
	}
}

func TestRecoveryAdmissionReplicaFloorCountsPodsAbsentFromDNSInventory(t *testing.T) {
	tr, run, _, _ := recoveryAdmissionFixture()
	run.Status = "QUEUED"
	run.TotalVUsers = 200
	run.Groups[0].VUsers = 200
	entries := []GeneratorInventoryEntry{tr.liveInventory[0], tr.liveInventory[2]}
	limit := entries[0].PodPlacement.scaleLimit
	limit.MaximumReplicas = 5
	for i := range entries {
		entries[i].PodPlacement.scaleLimit = limit
	}
	entries[0].PodPlacement.Name = "gen-0"
	entries[1].PodPlacement.Name = "gen-2"
	pods := []KubePodRef{
		{Name: "gen-0", Namespace: "apex", UID: "pod-a", StatefulSetName: "gen", StatefulSetUID: "workload", Ready: true, Phase: "Running"},
		{Name: "gen-1", Namespace: "apex", UID: "pod-hidden-notready", StatefulSetName: "gen", StatefulSetUID: "workload", Ready: false, Phase: "Pending"},
		{Name: "gen-2", Namespace: "apex", UID: "pod-c", StatefulSetName: "gen", StatefulSetUID: "workload", Ready: true, Phase: "Running"},
		{Name: "gen-3", Namespace: "apex", UID: "pod-hidden-terminating", StatefulSetName: "gen", StatefulSetUID: "workload", Terminating: true, Phase: "Running"},
	}
	tr.setCachedGeneratorInventoryWithPodSnapshot(entries, recoveryPodSnapshot{pods: pods, limit: limit, available: true}, time.Now())
	floor, complete := tr.recoveryAdmissionReplicaFloor([]*ManagedTest{run}, tr.settings, 0, time.Now())
	if !complete || floor != 5 {
		t.Fatalf("DNS-absent occupied ordinals produced floor=%d complete=%v, want fifth replica", floor, complete)
	}
	// A snapshot from a different StatefulSet cannot stand in for occupied pods.
	wrong := append([]KubePodRef(nil), pods...)
	wrong[1].StatefulSetUID = "other-workload"
	tr.setCachedGeneratorInventoryWithPodSnapshot(entries, recoveryPodSnapshot{pods: wrong, limit: limit, available: true}, time.Now())
	floor, complete = tr.recoveryAdmissionReplicaFloor([]*ManagedTest{run}, tr.settings, 0, time.Now())
	if !complete || floor != 5 {
		t.Fatalf("unrelated pod changed source floor=%d complete=%v", floor, complete)
	}
	duplicate := append(append([]KubePodRef(nil), pods...), pods[0])
	tr.setCachedGeneratorInventoryWithPodSnapshot(entries, recoveryPodSnapshot{pods: duplicate, limit: limit, available: true}, time.Now())
	if floor, complete := tr.recoveryAdmissionReplicaFloor([]*ManagedTest{run}, tr.settings, 0, time.Now()); complete || floor != 0 {
		t.Fatalf("ambiguous duplicate ordinal was trusted: floor=%d complete=%v", floor, complete)
	}
}

// An IP-discovered ordinal must remain requested after its Ready transition.
// Otherwise discovery of the first cross-worker spare removes the single-worker
// floor and KEDA deletes that spare before admission can finish.
func TestRecoveryAdmissionRetainsIPDiscoveredCrossWorkerSpare(t *testing.T) {
	tr, run, _, _ := recoveryAdmissionFixture()
	run.Status = "PENDING"
	run.TotalVUsers = 50
	run.Groups[0].VUsers = 50
	entries := tr.liveInventory[:3]
	targets := []string{}
	for i := range entries {
		entries[i].Target = fmt.Sprintf("10.244.%d.%d:9090", i/2+1, i+20)
		entries[i].PodPlacement.Name = fmt.Sprintf("generator-%d", i)
		targets = append(targets, entries[i].Target)
	}
	tr.execution = newDistributedExecutionBackend(targets, time.Second, nil)
	tr.liveInventory = entries[:2]
	before, ok := tr.recoveryAdmissionReplicaFloor([]*ManagedTest{run}, tr.settings, 0, time.Now())
	if !ok || before != 3 {
		t.Fatalf("before spare: %d %v", before, ok)
	}
	tr.liveInventory = entries
	after, ok := tr.recoveryAdmissionReplicaFloor([]*ManagedTest{run}, tr.settings, 0, time.Now())
	if !ok || after != 3 {
		t.Fatalf("ready spare lost ordinal: %d %v", after, ok)
	}
	entries[2].PodPlacement.Name = "my.generator-7"
	for i := range entries {
		entries[i].PodPlacement.scaleLimit.MaximumReplicas = 8
	}
	after, ok = tr.recoveryAdmissionReplicaFloor([]*ManagedTest{run}, tr.settings, 0, time.Now())
	if !ok || after != 8 {
		t.Fatalf("high ordinal lost: %d %v", after, ok)
	}
}

func TestReservedReplicaFloorUsesDurableObservedPodName(t *testing.T) {
	for _, target := range []string{"10.244.2.123:9090", "[fd00::12]:9090", "transport-alias:9090"} {
		claims := []storage.PodCapacityReservation{{Target: target, RecoveryPlacement: &storage.PodCapacityRecoveryPlacement{Namespace: "ns", WorkloadUID: "workload", PodUID: "uid", PodName: "my.generator-7", NodeName: "worker-2"}}}
		if got := reservedReplicaFloor(claims, 2); got != 8 {
			t.Fatalf("%s: floor %d", target, got)
		}
	}
	tr, run, claims, loads := recoveryAdmissionFixture()
	got, err := tr.addRecoverySpareClaims(run, claims, loads)
	if err != nil {
		t.Fatal(err)
	}
	for _, claim := range got {
		if claim.RecoveryPlacement == nil || claim.RecoveryPlacement.PodName == "" {
			t.Fatal("admission did not freeze pod name")
		}
	}
}
