package generatorrpc

import (
	"context"
	"math"
	"sync"
	"sync/atomic"
	"testing"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestPodCapacityRequiresKnownRuntimeLimits(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cpu    float64
		memory int64
	}{
		{"missing CPU", 0, 16000 * 1024 * 1024},
		{"missing memory", 8, 0},
		{"invalid CPU", math.NaN(), 16000 * 1024 * 1024},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newIsolatedService(t, 10000)
			s.podResources = podResourceCapacity{CPULimitCores: tc.cpu, MemoryLimitBytes: tc.memory, MaxVUsers: 10000}
			ack, err := s.ReservePodCapacity(context.Background(), podClaimFixture(s, "missing", .5))
			if err != nil || ack.Accepted || len(s.podCapacityClaims) != 0 {
				t.Fatalf("unknown runtime capacity admitted: %+v %v", ack, err)
			}
		})
	}
}

func TestPodCapacityToleratesShardRounding(t *testing.T) {
	s := newIsolatedService(t, 10000)
	s.podResources = podResourceCapacity{CPULimitCores: 8, MemoryLimitBytes: 16000 * 1024 * 1024, MaxVUsers: 10000}
	claim := podClaimFixture(s, "rounding", .5)
	claim.Demand.Vusers = math.Nextafter(2000, 0)
	ack, err := s.ReservePodCapacity(context.Background(), claim)
	if err != nil || !ack.Accepted {
		t.Fatalf("rounding rejected a valid peak: %+v %v", ack, err)
	}
}

func TestPodCapacityStaleReleaseCannotRemoveNewAttempt(t *testing.T) {
	s := newIsolatedService(t, 10000)
	s.podResources = podResourceCapacity{CPULimitCores: 8, MemoryLimitBytes: 16000 * 1024 * 1024, MaxVUsers: 10000}
	old := podClaimFixture(s, "retry", .5)
	ack, err := s.ReservePodCapacity(context.Background(), old)
	if err != nil || !ack.Accepted {
		t.Fatalf("initial claim: %+v %v", ack, err)
	}
	old.Release = true
	ack, err = s.ReservePodCapacity(context.Background(), old)
	if err != nil || !ack.Accepted {
		t.Fatalf("release: %+v %v", ack, err)
	}
	next := podClaimFixture(s, "retry", .5)
	next.ReservationId = "second-attempt"
	ack, err = s.ReservePodCapacity(context.Background(), next)
	if err != nil || !ack.Accepted {
		t.Fatalf("retry claim: %+v %v", ack, err)
	}
	ack, err = s.ReservePodCapacity(context.Background(), old)
	if err != nil || ack.Accepted || PodCapacityFromProto(ack.Snapshot).ReservedPct != 50 {
		t.Fatalf("stale release affected new attempt: %+v %v", ack, err)
	}
}

func podClaimFixture(s *Service, id string, fraction float64) *pb.PodCapacityClaim {
	budget := protocol.CapacityVector{CPUCores: 6.4, MemoryMB: 12800, NetworkMbps: 800, VUsers: 8000, Protocols: map[string]float64{"soap": 4000}}
	return &pb.PodCapacityClaim{ReservationId: id + "-attempt", TestId: id, Incarnation: s.podCapacityIncarnation, ModelVersion: protocol.CapacityReservationModel, HeadroomPct: 20, Schedulable: CapacityVectorToProto(budget), Demand: CapacityVectorToProto(budget.Scale(fraction)), GroupPeaks: map[string]int32{"soap": int32(4000 * fraction)}}
}

func TestPodCapacityConcurrentReservationsAndRelease(t *testing.T) {
	s := NewService("capacity", 10000)
	s.podResources = podResourceCapacity{CPULimitCores: 8, MemoryLimitBytes: 16000 * 1024 * 1024, MaxVUsers: 10000}
	var accepted atomic.Int32
	var wg sync.WaitGroup
	for _, id := range []string{"one", "two", "three"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			ack, err := s.ReservePodCapacity(context.Background(), podClaimFixture(s, id, .5))
			if err != nil {
				t.Error(err)
			} else if ack.Accepted {
				accepted.Add(1)
			}
		}(id)
	}
	wg.Wait()
	if accepted.Load() != 2 {
		t.Fatalf("accepted %d, want 2", accepted.Load())
	}
	s.mu.Lock()
	var id string
	for id = range s.podCapacityClaims {
		break
	}
	s.mu.Unlock()
	claim := podClaimFixture(s, id, .5)
	ack, err := s.ReservePodCapacity(context.Background(), claim)
	if err != nil || !ack.Accepted {
		t.Fatalf("retry: %v %+v", err, ack)
	}
	s.mu.Lock()
	s.assignments[id] = assignedTest{}
	s.mu.Unlock()
	claim.Release = true
	ack, err = s.ReservePodCapacity(context.Background(), claim)
	if err != nil || ack.Accepted {
		t.Fatalf("released live reservation: %v %+v", err, ack)
	}
	s.mu.Lock()
	delete(s.assignments, id)
	s.mu.Unlock()
	ack, err = s.ReservePodCapacity(context.Background(), claim)
	if err != nil || !ack.Accepted {
		t.Fatalf("release: %v %+v", err, ack)
	}
	snapshot := PodCapacityFromProto(ack.Snapshot)
	if snapshot.ReservedPct != 50 || snapshot.RemainingPct != 50 {
		t.Fatalf("remaining snapshot: %+v", snapshot)
	}
}

func TestPodCapacityDedicatedAndPeakFence(t *testing.T) {
	s := NewService("capacity", 10000)
	s.podResources = podResourceCapacity{CPULimitCores: 8, MemoryLimitBytes: 16000 * 1024 * 1024, MaxVUsers: 10000}
	claim := podClaimFixture(s, "dedicated", 1)
	claim.Dedicated = true
	ack, err := s.ReservePodCapacity(context.Background(), claim)
	if err != nil || !ack.Accepted {
		t.Fatalf("claim: %v %+v", err, ack)
	}
	ack, _ = s.ReservePodCapacity(context.Background(), podClaimFixture(s, "other", .1))
	if ack.Accepted {
		t.Fatal("shared claim entered dedicated pod")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.validatePodCapacityAssignmentLocked("dedicated", "soap", 4001) == nil {
		t.Fatal("accepted peak increase")
	}
	if s.validatePodCapacityAssignmentLocked("other", "soap", 1) == nil {
		t.Fatal("accepted unreserved test")
	}
	if err := s.validatePodCapacityAssignmentLocked("dedicated", "soap", 20); err != nil {
		t.Fatal(err)
	}
}

func TestPodCapacityResizeRevisionAndLiveRollbackFence(t *testing.T) {
	s := newIsolatedService(t, 10000)
	s.podResources = podResourceCapacity{CPULimitCores: 8, MemoryLimitBytes: 16000 * 1024 * 1024, MaxVUsers: 10000}
	claim := podClaimFixture(s, "resize", .5)
	ack, err := s.ReservePodCapacity(context.Background(), claim)
	if err != nil || !ack.Accepted {
		t.Fatalf("initial: %v %+v", err, ack)
	}
	grown := podClaimFixture(s, "resize", .75)
	grown.Revision = 1
	ack, err = s.ReservePodCapacity(context.Background(), grown)
	if err != nil || !ack.Accepted {
		t.Fatalf("grow: %v %+v", err, ack)
	}
	ack, _ = s.ReservePodCapacity(context.Background(), claim)
	if ack.Accepted {
		t.Fatal("stale revision replaced grown claim")
	}
	s.mu.Lock()
	s.assignments[claim.TestId] = assignedTest{groups: map[string]int32{"soap": 2500}}
	s.mu.Unlock()
	restore := podClaimFixture(s, "resize", .5)
	restore.Revision = 2
	ack, _ = s.ReservePodCapacity(context.Background(), restore)
	if ack.Accepted {
		t.Fatal("rollback released live capacity")
	}
	s.mu.Lock()
	s.assignments[claim.TestId] = assignedTest{groups: map[string]int32{"soap": 1000}}
	s.mu.Unlock()
	ack, err = s.ReservePodCapacity(context.Background(), restore)
	if err != nil || !ack.Accepted {
		t.Fatalf("confirmed rollback: %v %+v", err, ack)
	}
	if got := PodCapacityFromProto(ack.Snapshot).ReservedPct; got != 50 {
		t.Fatalf("reserved %v, want 50", got)
	}
}
