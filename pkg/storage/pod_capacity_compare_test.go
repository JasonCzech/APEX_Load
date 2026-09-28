package storage

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestPodCapacityCompareAndReserve(t *testing.T) {
	checkPodCapacityCompareAndReserve(t, newAtomicBindingStore(t))
}

func checkPodCapacityCompareAndReserve(t *testing.T, s *SQLiteStore) {
	t.Helper()
	modes := []string{"activate", "restore standby", "changed state", "released", "changed revision", "wrong incarnation", "stale owner", "unexpected existing", "duplicate expected", "missing proposal", "atomic rollback", "concurrent"}
	for index, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			name := fmt.Sprintf("capacity-compare-%d", index)
			ok, lease, err := s.TryAcquireSchedulerLeaseTerm(name, "owner", time.Now(), time.Minute)
			if err != nil || !ok {
				t.Fatalf("lease: %v", err)
			}
			budget := protocol.CapacityVector{CPUCores: 4, MemoryMB: 1000, NetworkMbps: 100, VUsers: 100, Protocols: map[string]float64{"http": 100}}
			old := PodCapacityReservation{ReservationID: "attempt", Revision: 1, Target: name + "-spare", TestID: name, Incarnation: name + "-pod", Generation: lease.Generation, State: "committed", ModelVersion: protocol.CapacityReservationModel, Budget: budget, Demand: budget, Dedicated: true}
			if mode == "restore standby" {
				old.GroupPeaks = map[string]int32{"g": 10}
				old.GroupDemands = map[string]protocol.CapacityVector{"g": budget.Scale(.1)}
			}
			if err := s.ReservePodCapacity([]PodCapacityReservation{old}, name, "owner", lease.Generation); err != nil {
				t.Fatal(err)
			}
			if err := s.TransitionPodCapacity(old.Target, old.TestID, old.Incarnation, old.ReservationID, "committed", name, "owner", lease.Generation); err != nil {
				t.Fatal(err)
			}
			next := old
			next.Revision++
			next.State = "pending"
			next.GroupPeaks = map[string]int32{"g": 10}
			next.GroupDemands = map[string]protocol.CapacityVector{"g": budget.Scale(.1)}
			extra := old
			extra.Target = name + "-extra"
			extra.Incarnation = name + "-extra-pod"
			extra.State = "pending"
			claims := []PodCapacityReservation{next, extra}
			expected := []PodCapacityReservation{old}
			owner := "owner"
			switch mode {
			case "restore standby":
				claims[0].GroupPeaks = nil
				claims[0].GroupDemands = nil
			case "changed state":
				if err := s.TransitionPodCapacity(old.Target, old.TestID, old.Incarnation, old.ReservationID, "releasing", name, "owner", lease.Generation); err != nil {
					t.Fatal(err)
				}
			case "released":
				if err := s.TransitionPodCapacity(old.Target, old.TestID, old.Incarnation, old.ReservationID, "released", name, "owner", lease.Generation); err != nil {
					t.Fatal(err)
				}
			case "changed revision":
				expected[0].Revision++
			case "wrong incarnation":
				expected[0].Incarnation = "replacement"
			case "stale owner":
				owner = "stale"
			case "unexpected existing":
				expected = nil
			case "duplicate expected":
				expected = append(expected, old)
			case "missing proposal":
				claims = []PodCapacityReservation{extra}
			case "atomic rollback":
				claims[1].Demand = budget.Scale(2)
			case "concurrent":
				claims = claims[:1]
			}
			before, err := s.ListPodCapacityReservations()
			if err != nil {
				t.Fatal(err)
			}
			if mode == "concurrent" {
				start := make(chan struct{})
				results := make(chan error, 2)
				var wg sync.WaitGroup
				for i := range 2 {
					proposal := next
					proposal.GroupPeaks = map[string]int32{fmt.Sprintf("g%d", i): 10}
					proposal.GroupDemands = map[string]protocol.CapacityVector{fmt.Sprintf("g%d", i): budget.Scale(.1)}
					wg.Add(1)
					go func() {
						defer wg.Done()
						<-start
						results <- s.ReservePodCapacityIfUnchanged([]PodCapacityReservation{proposal}, expected, name, owner, lease.Generation)
					}()
				}
				close(start)
				wg.Wait()
				close(results)
				accepted := 0
				for err := range results {
					if err == nil {
						accepted++
					}
				}
				if accepted != 1 {
					t.Fatalf("concurrent replacements accepted=%d, want one", accepted)
				}
				rows, err := s.ListPodCapacityReservations()
				if err != nil {
					t.Fatal(err)
				}
				for _, r := range rows {
					if r.TestID == name && (r.State != "pending" || r.Revision != 2 || !reflect.DeepEqual(r.Demand, budget)) {
						t.Fatalf("incorrect winner: %+v", r)
					}
				}
				return
			}
			err = s.ReservePodCapacityIfUnchanged(claims, expected, name, owner, lease.Generation)
			accepted := mode == "activate" || mode == "restore standby"
			if (err == nil) != accepted {
				t.Fatalf("replacement accepted=%v, error=%v", accepted, err)
			}
			after, readErr := s.ListPodCapacityReservations()
			if readErr != nil {
				t.Fatal(readErr)
			}
			if !accepted {
				if !reflect.DeepEqual(before, after) {
					t.Fatal("failed compare or capacity validation partially changed the placement")
				}
				return
			}
			found := 0
			for _, r := range after {
				if r.TestID != name {
					continue
				}
				found++
				if r.State != "pending" || !r.Dedicated || !reflect.DeepEqual(r.Demand, budget) {
					t.Fatalf("replacement lost the full frozen budget: %+v", r)
				}
				if r.Target == old.Target && (r.Revision != 2 || !reflect.DeepEqual(r.GroupPeaks, claims[0].GroupPeaks)) {
					t.Fatalf("incorrect activation: %+v", r)
				}
			}
			if found != 2 {
				t.Fatalf("replacement placement count=%d", found)
			}
		})
	}
}
