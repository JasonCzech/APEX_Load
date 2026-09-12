package storage

import (
	"fmt"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestPodCapacityAtomicPlacementAndFencing(t *testing.T) {
	s := newAtomicBindingStore(t)
	checkPodCapacityAtomicPlacementAndFencing(t, s)
}

func checkPodCapacityAtomicPlacementAndFencing(t *testing.T, s *SQLiteStore) {
	t.Helper()
	const leaseName = "pod-capacity-test"
	ok, lease, err := s.TryAcquireSchedulerLeaseTerm(leaseName, "owner", time.Now(), time.Minute)
	if err != nil || !ok {
		t.Fatalf("lease: %v", err)
	}
	budget := protocol.CapacityVector{CPUCores: 8, MemoryMB: 16000, NetworkMbps: 800, VUsers: 8000, Protocols: map[string]float64{"soap": 4000}}
	claim := PodCapacityReservation{ReservationID: "attempt-one", Target: "pod-a", TestID: "one", Incarnation: "instance-a", Generation: lease.Generation, ModelVersion: protocol.CapacityReservationModel, Budget: budget, Demand: budget.Scale(.5), GroupPeaks: map[string]int32{"soap": 2000}}
	if err := s.ReservePodCapacity([]PodCapacityReservation{claim}, leaseName, "owner", lease.Generation); err != nil {
		t.Fatal(err)
	}
	if err := s.ReservePodCapacity([]PodCapacityReservation{claim}, leaseName, "owner", lease.Generation); err != nil {
		t.Fatalf("retry: %v", err)
	}
	second := claim
	second.Target = "pod-b"
	second.TestID = "two"
	tooLarge := claim
	tooLarge.TestID = "two"
	tooLarge.Demand = budget
	if err := s.ReservePodCapacity([]PodCapacityReservation{second, tooLarge}, leaseName, "owner", lease.Generation); err == nil {
		t.Fatal("overbooked")
	}
	rows, err := s.ListPodCapacityReservations()
	if err != nil || len(rows) != 1 {
		t.Fatalf("partial placement leaked: %+v %v", rows, err)
	}
	if err := s.TransitionPodCapacity(claim.Target, claim.TestID, claim.Incarnation, claim.ReservationID, "released", leaseName, "stale", lease.Generation); err == nil {
		t.Fatal("stale owner released capacity")
	}
	if err := s.TransitionPodCapacity(claim.Target, claim.TestID, claim.Incarnation, claim.ReservationID, "committed", leaseName, "owner", lease.Generation); err != nil {
		t.Fatal(err)
	}
	rows, _ = s.ListPodCapacityReservations()
	if rows[0].State != "committed" {
		t.Fatal("state did not persist")
	}
	now := time.Now().UTC()
	if err := s.SaveProject(&Project{ID: "pod-project", Name: "Pod project", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveTest(&TestRecord{ID: claim.TestID, Name: "Pod test", Status: "COMPLETE", ProjectID: "pod-project", StartTime: now}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteTest(claim.TestID); err != nil {
		t.Fatal(err)
	}
	rows, err = s.ListPodCapacityReservations()
	if err != nil || len(rows) != 1 {
		t.Fatalf("test deletion released unconfirmed capacity: %+v %v", rows, err)
	}
	validate := func(string, []*CapacityReservation) error { return nil }
	if _, err := s.CompareAndSwapSettingWithCapacityFence("system_settings", "", `{"capacity_planning":{"pod_reservations_enabled":false}}`, time.Now(), validate); err == nil {
		t.Fatal("disabled enforcement while capacity was held")
	}
	claim.Revision = 1
	claim.Demand = budget.Scale(.75)
	if err := s.ReservePodCapacity([]PodCapacityReservation{claim}, leaseName, "owner", lease.Generation); err != nil {
		t.Fatal(err)
	}
	rows, _ = s.ListPodCapacityReservations()
	if rows[0].Demand.VUsers != 6000 || rows[0].State != "committed" {
		t.Fatalf("resize changed lifecycle or demand: %+v", rows[0])
	}
	stale := claim
	stale.Revision = 0
	if err := s.ReservePodCapacity([]PodCapacityReservation{stale}, leaseName, "owner", lease.Generation); err == nil {
		t.Fatal("stale resize accepted")
	}
	if err := s.TransitionPodCapacity(claim.Target, claim.TestID, claim.Incarnation, claim.ReservationID, "released", leaseName, "owner", lease.Generation); err != nil {
		t.Fatal(err)
	}
	replacement := claim
	replacement.ReservationID = "attempt-two"
	if err := s.ReservePodCapacity([]PodCapacityReservation{replacement}, leaseName, "owner", lease.Generation); err != nil {
		t.Fatal(err)
	}
	if err := s.TransitionPodCapacity(claim.Target, claim.TestID, claim.Incarnation, claim.ReservationID, "released", leaseName, "owner", lease.Generation); err == nil {
		t.Fatal("stale cleanup released a later launch attempt")
	}
	rows, err = s.ListPodCapacityReservations()
	if err != nil || len(rows) != 1 || rows[0].ReservationID != replacement.ReservationID {
		t.Fatalf("replacement reservation lost: %+v %v", rows, err)
	}
	if err := s.TransitionPodCapacity(replacement.Target, replacement.TestID, replacement.Incarnation, replacement.ReservationID, "released", leaseName, "owner", lease.Generation); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 3)
	for i := 0; i < 3; i++ {
		candidate := claim
		candidate.TestID = fmt.Sprintf("concurrent-%d", i)
		candidate.Demand = budget.Scale(.5)
		go func() {
			<-start
			results <- s.ReservePodCapacity([]PodCapacityReservation{candidate}, leaseName, "owner", lease.Generation)
		}()
	}
	close(start)
	accepted := 0
	for i := 0; i < 3; i++ {
		if err := <-results; err == nil {
			accepted++
		}
	}
	if accepted != 2 {
		t.Fatalf("concurrent half-capacity admissions: got %d, want 2", accepted)
	}
	rows, err = s.ListPodCapacityReservations()
	if err != nil || len(rows) != 2 {
		t.Fatalf("concurrent ledger: %+v %v", rows, err)
	}
	for _, row := range rows {
		if err := s.TransitionPodCapacity(row.Target, row.TestID, row.Incarnation, row.ReservationID, "released", leaseName, "owner", lease.Generation); err != nil {
			t.Fatal(err)
		}
	}
	if updated, err := s.CompareAndSwapSettingWithCapacityFence("system_settings", "", `{"capacity_planning":{"pod_reservations_enabled":false}}`, time.Now(), validate); err != nil || !updated {
		t.Fatalf("disable after release: %v %v", updated, err)
	}
	if err := s.ReservePodCapacity([]PodCapacityReservation{claim}, leaseName, "owner", lease.Generation); err == nil {
		t.Fatal("stale launch reserved after enforcement was disabled")
	}
}
