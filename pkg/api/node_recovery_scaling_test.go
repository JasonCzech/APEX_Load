package api

import (
	"testing"
	"time"
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
