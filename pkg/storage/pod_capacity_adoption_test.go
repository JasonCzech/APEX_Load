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

func TestAdoptPodCapacityRequiresEveryStandby(t *testing.T) {
	checkAdoptStandbyPodCapacity(t, newAtomicBindingStore(t))
}

func checkAdoptStandbyPodCapacity(t *testing.T, s *SQLiteStore) {
	t.Helper()
	const name = "standby-capacity-adoption"
	ok, old, err := s.TryAcquireSchedulerLeaseTerm(name, "standby-old", time.Now(), time.Minute)
	if err != nil || !ok {
		t.Fatalf("old lease: %v", err)
	}
	budget := protocol.CapacityVector{CPUCores: 4, MemoryMB: 1000, NetworkMbps: 100, VUsers: 100}
	claims := []PodCapacityReservation{
		{ReservationID: "standby-attempt", Revision: 1, Target: "standby-active", TestID: "standby-run", Incarnation: "active-pod", Generation: old.Generation, ModelVersion: protocol.CapacityReservationModel, Budget: budget, Demand: budget.Scale(.5), GroupPeaks: map[string]int32{"g": 50}},
		{ReservationID: "standby-attempt", Revision: 1, Target: "standby-idle", TestID: "standby-run", Incarnation: "idle-pod", Generation: old.Generation, ModelVersion: protocol.CapacityReservationModel, Budget: budget, Demand: budget, Dedicated: true},
	}
	if err := s.ReservePodCapacity(claims, name, "standby-old", old.Generation); err != nil {
		t.Fatal(err)
	}
	for _, claim := range claims {
		if err := s.TransitionPodCapacity(claim.Target, claim.TestID, claim.Incarnation, claim.ReservationID, "committed", name, "standby-old", old.Generation); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := func() []PodCapacityReservation {
		all, err := s.ListPodCapacityReservations()
		if err != nil {
			t.Fatal(err)
		}
		result := []PodCapacityReservation{}
		for _, claim := range all {
			if claim.TestID == "standby-run" {
				result = append(result, claim)
			}
		}
		return result
	}
	claims = snapshot()
	if len(claims) != 2 {
		t.Fatal("missing fixture reservations")
	}
	if err := s.ReleaseSchedulerLease(name, "standby-old"); err != nil {
		t.Fatal(err)
	}
	ok, next, err := s.TryAcquireSchedulerLeaseTerm(name, "standby-new", time.Now(), time.Minute)
	if err != nil || !ok {
		t.Fatalf("new lease: %v", err)
	}
	// Model a standby appearing after the caller's capacity snapshot. Even a
	// correctly confirmed live claim must roll back when coverage is incomplete.
	if err := s.AdoptPodCapacity(claims[:1], name, "standby-new", next.Generation); err == nil {
		t.Fatal("unconfirmed standby was ignored")
	}
	if got := snapshot(); !reflect.DeepEqual(got, claims) {
		t.Fatalf("partial transfer: %+v", got)
	}
	for i := 0; i < 2; i++ {
		if err := s.AdoptPodCapacity(claims, name, "standby-new", next.Generation); err != nil {
			t.Fatalf("complete adoption/retry: %v", err)
		}
	}
	expected := append([]PodCapacityReservation(nil), claims...)
	for i := range expected {
		expected[i].Generation = next.Generation
	}
	if got := snapshot(); !reflect.DeepEqual(got, expected) {
		t.Fatalf("frozen standby changed: %+v", got)
	}
}

func TestAdoptPodCapacityAcceptsCurrentTermStandby(t *testing.T) {
	checkAdoptCurrentTermStandbyPodCapacity(t, newAtomicBindingStore(t))
}

// An evacuation rollback re-reserves an idle standby under the successor's own
// term before adoption. That row has no old owner, so adopting the run's live
// claim must not require it, while an old-term standby still must be included.
func checkAdoptCurrentTermStandbyPodCapacity(t *testing.T, s *SQLiteStore) {
	t.Helper()
	const name = "current-term-standby-adoption"
	ok, old, err := s.TryAcquireSchedulerLeaseTerm(name, "rollback-old", time.Now(), time.Minute)
	if err != nil || !ok {
		t.Fatalf("old lease: %v", err)
	}
	budget := protocol.CapacityVector{CPUCores: 4, MemoryMB: 1000, NetworkMbps: 100, VUsers: 100}
	live := PodCapacityReservation{ReservationID: "rollback-attempt", Revision: 1, Target: "rollback-active", TestID: "rollback-run", Incarnation: "active-pod", Generation: old.Generation, ModelVersion: protocol.CapacityReservationModel, Budget: budget, Demand: budget.Scale(.5), GroupPeaks: map[string]int32{"g": 50}}
	if err := s.ReservePodCapacity([]PodCapacityReservation{live}, name, "rollback-old", old.Generation); err != nil {
		t.Fatal(err)
	}
	if err := s.TransitionPodCapacity(live.Target, live.TestID, live.Incarnation, live.ReservationID, "committed", name, "rollback-old", old.Generation); err != nil {
		t.Fatal(err)
	}
	stored, err := s.ListPodCapacityReservations()
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range stored {
		if row.TestID == live.TestID && row.Target == live.Target {
			live = row
		}
	}
	if err := s.ReleaseSchedulerLease(name, "rollback-old"); err != nil {
		t.Fatal(err)
	}
	ok, next, err := s.TryAcquireSchedulerLeaseTerm(name, "rollback-new", time.Now(), time.Minute)
	if err != nil || !ok {
		t.Fatalf("new lease: %v", err)
	}
	standby := PodCapacityReservation{ReservationID: "spare-attempt", Revision: 3, Target: "rollback-spare", TestID: "rollback-run", Incarnation: "spare-pod", Generation: next.Generation, ModelVersion: protocol.CapacityReservationModel, Budget: budget, Demand: budget, Dedicated: true}
	if err := s.ReservePodCapacity([]PodCapacityReservation{standby}, name, "rollback-new", next.Generation); err != nil {
		t.Fatal(err)
	}
	// Until the generator confirms it, a pending standby still blocks adoption.
	if err := s.AdoptPodCapacity([]PodCapacityReservation{live}, name, "rollback-new", next.Generation); err == nil {
		t.Fatal("a pending current-term standby was accepted")
	}
	if err := s.TransitionPodCapacity(standby.Target, standby.TestID, standby.Incarnation, standby.ReservationID, "committed", name, "rollback-new", next.Generation); err != nil {
		t.Fatal(err)
	}
	if err := s.AdoptPodCapacity([]PodCapacityReservation{live}, name, "rollback-new", next.Generation); err != nil {
		t.Fatalf("current-term standby blocked adoption: %v", err)
	}
	rows, err := s.ListPodCapacityReservations()
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.TestID == "rollback-run" && (row.Generation != next.Generation || row.State != "committed") {
			t.Fatalf("reservation after adoption: %+v", row)
		}
	}
}
