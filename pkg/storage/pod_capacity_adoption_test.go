package storage

import (
	"reflect"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestAdoptPodCapacity(t *testing.T) { checkAdoptPodCapacity(t, newAtomicBindingStore(t)) }

func checkAdoptPodCapacity(t *testing.T, s *SQLiteStore) {
	t.Helper()
	const name = "capacity-adoption"
	ok, old, err := s.TryAcquireSchedulerLeaseTerm(name, "old", time.Now(), time.Minute)
	if err != nil || !ok {
		t.Fatalf("old lease: %v", err)
	}
	budget := protocol.CapacityVector{CPUCores: 4, MemoryMB: 1000, NetworkMbps: 100, VUsers: 100}
	claims := []PodCapacityReservation{}
	for _, target := range []string{"a", "b"} {
		claims = append(claims, PodCapacityReservation{ReservationID: "attempt", Revision: 1, Target: target, TestID: "run", Incarnation: target + "-instance", Generation: old.Generation, State: "committed", ModelVersion: protocol.CapacityReservationModel, Budget: budget, Demand: budget.Scale(.5), GroupPeaks: map[string]int32{"g": 50}, GroupDemands: map[string]protocol.CapacityVector{"g": budget.Scale(.5)}})
	}
	if err := s.ReservePodCapacity(claims, name, "old", old.Generation); err != nil {
		t.Fatal(err)
	}
	for _, c := range claims {
		if err := s.TransitionPodCapacity(c.Target, c.TestID, c.Incarnation, c.ReservationID, "committed", name, "old", old.Generation); err != nil {
			t.Fatal(err)
		}
	}
	// Adoption compares the durable snapshot returned to the controller.
	claims, err = s.ListPodCapacityReservations()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReleaseSchedulerLease(name, "old"); err != nil {
		t.Fatal(err)
	}
	ok, next, err := s.TryAcquireSchedulerLeaseTerm(name, "new", time.Now(), time.Minute)
	if err != nil || !ok || next.Generation <= old.Generation {
		t.Fatalf("new lease: %+v %v", next, err)
	}
	for _, tc := range []struct {
		name   string
		change func([]PodCapacityReservation)
	}{
		{"changed second revision", func(c []PodCapacityReservation) { c[1].Revision++ }},
		{"changed second demand", func(c []PodCapacityReservation) { c[1].Demand = c[1].Demand.Scale(2) }},
		{"missing second row", func(c []PodCapacityReservation) { c[1].Target = "missing" }},
		{"pending", func(c []PodCapacityReservation) { c[1].State = "pending" }},
		{"releasing", func(c []PodCapacityReservation) { c[1].State = "releasing" }},
		{"future generation", func(c []PodCapacityReservation) { c[1].Generation = next.Generation + 1 }},
		{"duplicate", func(c []PodCapacityReservation) { c[1] = c[0] }},
		{"different run", func(c []PodCapacityReservation) { c[1].TestID = "other" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			invalid := append([]PodCapacityReservation(nil), claims...)
			tc.change(invalid)
			if err := s.AdoptPodCapacity(invalid, name, "new", next.Generation); err == nil {
				t.Fatal("invalid adoption accepted")
			}
			rows, err := s.ListPodCapacityReservations()
			if err != nil || !reflect.DeepEqual(rows, claims) {
				t.Fatalf("partial transfer: %+v %v", rows, err)
			}
		})
	}
	if err := s.AdoptPodCapacity(claims, name, "old", next.Generation); err == nil {
		t.Fatal("stale owner accepted")
	}
	if err := s.AdoptPodCapacity(nil, name, "new", next.Generation); err == nil {
		t.Fatal("empty adoption accepted")
	}
	for i := 0; i < 2; i++ {
		if err := s.AdoptPodCapacity(claims, name, "new", next.Generation); err != nil {
			t.Fatalf("adoption/retry: %v", err)
		}
	}
	expected := append([]PodCapacityReservation(nil), claims...)
	for i := range expected {
		expected[i].Generation = next.Generation
	}
	rows, err := s.ListPodCapacityReservations()
	if err != nil || !reflect.DeepEqual(rows, expected) {
		t.Fatalf("transfer changed frozen claim: %+v %v", rows, err)
	}
	// A later control can revise the adopted claim, but a retry of adoption
	// cannot overwrite that revision or accept a stale controller term.
	resized := expected[0]
	resized.Revision++
	resized.Demand = budget.Scale(.75)
	if err := s.ReservePodCapacity([]PodCapacityReservation{resized}, name, "new", next.Generation); err != nil {
		t.Fatalf("resize after adoption: %v", err)
	}
	if err := s.AdoptPodCapacity(claims, name, "new", next.Generation); err == nil {
		t.Fatal("adoption overwrote a later resize")
	}
	if err := s.ReservePodCapacity(claims, name, "old", old.Generation); err == nil {
		t.Fatal("old owner changed adopted capacity")
	}
}
