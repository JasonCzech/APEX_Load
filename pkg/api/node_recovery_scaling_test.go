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
