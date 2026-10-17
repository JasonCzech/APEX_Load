package generatorrpc

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"google.golang.org/protobuf/proto"
)

func standbyCapacityFixture(t *testing.T) (*Service, *pb.PodCapacityClaim) {
	t.Helper()
	s := newIsolatedService(t, 10000, WithExecutionEpochStatePath(filepath.Join(t.TempDir(), "epoch.json")))
	s.podResources = podResourceCapacity{CPULimitCores: 8, MemoryLimitBytes: 16000 * 1024 * 1024, MaxVUsers: 10000}
	if ack := claimEpoch(t, s, 1, time.Minute); !ack.Accepted {
		t.Fatalf("epoch: %+v", ack)
	}
	claim := podClaimFixture(s, "standby", 1)
	claim.Dedicated, claim.GroupPeaks, claim.ExecutionGeneration = true, nil, 1
	return s, claim
}

func requireCapacityAccepted(t *testing.T, s *Service, claim *pb.PodCapacityClaim) *pb.PodCapacityAck {
	t.Helper()
	ack, err := s.ReservePodCapacity(context.Background(), claim)
	if err != nil || !ack.GetAccepted() {
		t.Fatalf("capacity rejected: %v %+v", err, ack)
	}
	return ack
}

func TestPodCapacityStandbyActivationAndRelease(t *testing.T) {
	s, claim := standbyCapacityFixture(t)
	ack := requireCapacityAccepted(t, s, claim)
	if got := PodCapacityFromProto(ack.Snapshot); got.ReservedPct != 100 || got.RemainingPct != 0 || !got.Dedicated {
		t.Fatalf("standby capacity: %+v", got)
	}
	requireCapacityAccepted(t, s, claim)
	other := podClaimFixture(s, "other", .1)
	other.ExecutionGeneration = 1
	if ack, err := s.ReservePodCapacity(context.Background(), other); err != nil || ack.Accepted {
		t.Fatalf("shared standby: %+v %v", ack, err)
	}
	for _, n := range []int32{0, 1} {
		s.mu.Lock()
		err := s.validatePodCapacityAssignmentLocked(claim.TestId, "soap", n)
		s.mu.Unlock()
		if err == nil {
			t.Fatalf("standby executed %d VUsers", n)
		}
	}
	active := proto.Clone(claim).(*pb.PodCapacityClaim)
	active.GroupPeaks = map[string]int32{"soap": 20}
	if ack, _ := s.ReservePodCapacity(context.Background(), active); ack.Accepted {
		t.Fatal("activation without revision accepted")
	}
	active.Revision++
	requireCapacityAccepted(t, s, active)
	for _, tc := range []struct {
		group    string
		n        int32
		accepted bool
	}{{"soap", 20, true}, {"soap", 21, false}, {"unknown", 0, false}} {
		s.mu.Lock()
		err := s.validatePodCapacityAssignmentLocked(claim.TestId, tc.group, tc.n)
		s.mu.Unlock()
		if (err == nil) != tc.accepted {
			t.Fatalf("assignment %+v: %v", tc, err)
		}
	}
	s.mu.Lock()
	s.assignments[claim.TestId] = assignedTest{groups: map[string]int32{"soap": 20}}
	s.mu.Unlock()
	release := proto.Clone(active).(*pb.PodCapacityClaim)
	release.Release = true
	ack, err := s.ReservePodCapacity(context.Background(), release)
	if err != nil || ack.Accepted {
		t.Fatalf("released active standby: %+v %v", ack, err)
	}
	s.mu.Lock()
	delete(s.assignments, claim.TestId)
	s.mu.Unlock()
	requireCapacityAccepted(t, s, release)
	requireCapacityAccepted(t, s, release)
}

func TestPodCapacityStandbyRequiresIdleUnsharedFullBudget(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*Service, *pb.PodCapacityClaim)
	}{
		{"shared", func(s *Service, c *pb.PodCapacityClaim) { c.Dedicated = false }},
		{"partial budget", func(s *Service, c *pb.PodCapacityClaim) { c.Demand.Vusers-- }},
		{"assignment", func(s *Service, c *pb.PodCapacityClaim) { s.assignments["legacy"] = assignedTest{} }},
		{"staging", func(s *Service, c *pb.PodCapacityClaim) {
			s.lifecycleCoordinator.pendingStages["legacy"] = &pendingAssignmentStage{}
		}},
		{"control", func(s *Service, c *pb.PodCapacityClaim) {
			s.lifecycleCoordinator.pendingControls["legacy"] = &pendingAssignmentControl{}
		}},
		{"stopping", func(s *Service, c *pb.PodCapacityClaim) { s.lifecycleCoordinator.stoppingTests["legacy"] = struct{}{} }},
		{"execution", func(s *Service, c *pb.PodCapacityClaim) {
			s.executions["legacy"] = map[string]*groupExecution{"group": {}}
		}},
		{"draining", func(s *Service, c *pb.PodCapacityClaim) { s.draining = true }},
		{"other reservation", func(s *Service, c *pb.PodCapacityClaim) {
			s.podCapacityClaims = map[string]*pb.PodCapacityClaim{"other": podClaimFixture(s, "other", .1)}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, claim := standbyCapacityFixture(t)
			s.mu.Lock()
			tc.setup(s, claim)
			s.mu.Unlock()
			ack, err := s.ReservePodCapacity(context.Background(), claim)
			// Remove synthetic lifecycle records before service cleanup.
			s.mu.Lock()
			s.assignments = make(map[string]assignedTest)
			s.lifecycleCoordinator.pendingStages = make(map[string]*pendingAssignmentStage)
			s.lifecycleCoordinator.pendingControls = make(map[string]*pendingAssignmentControl)
			s.lifecycleCoordinator.stoppingTests = make(map[string]struct{})
			s.executions = make(map[string]map[string]*groupExecution)
			_, stored := s.podCapacityClaims[claim.TestId]
			s.mu.Unlock()
			if err != nil || ack.Accepted || stored {
				t.Fatalf("unsafe standby accepted: %+v %v", ack, err)
			}
		})
	}
}

func TestPodCapacityStandbyOwnershipTransferIsFrozen(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*pb.PodCapacityClaim)
	}{
		{"unchanged", func(c *pb.PodCapacityClaim) {}},
		{"revision", func(c *pb.PodCapacityClaim) { c.Revision++ }},
		{"budget", func(c *pb.PodCapacityClaim) { c.Schedulable.NetworkMbps--; c.Demand.NetworkMbps-- }},
		{"demand", func(c *pb.PodCapacityClaim) { c.Demand.Vusers-- }},
		{"attempt", func(c *pb.PodCapacityClaim) { c.ReservationId += "-new" }},
		{"incarnation", func(c *pb.PodCapacityClaim) { c.Incarnation += "-new" }},
		{"activate", func(c *pb.PodCapacityClaim) { c.GroupPeaks = map[string]int32{"soap": 20}; c.Revision++ }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, claim := standbyCapacityFixture(t)
			requireCapacityAccepted(t, s, claim)
			next := proto.Clone(claim).(*pb.PodCapacityClaim)
			next.ExecutionGeneration = 2
			if ack, err := s.ReservePodCapacity(context.Background(), next); err == nil || ack.GetAccepted() {
				t.Fatal("transferred without authoritative epoch")
			}
			if ack := claimEpoch(t, s, 2, time.Minute); !ack.Accepted {
				t.Fatalf("epoch: %+v", ack)
			}
			tc.mutate(next)
			ack, err := s.ReservePodCapacity(context.Background(), next)
			if err != nil || ack.Accepted != (tc.name == "unchanged") {
				t.Fatalf("transfer: %+v %v", ack, err)
			}
			want := claim
			if tc.name == "unchanged" {
				want = next
			}
			s.mu.Lock()
			same := proto.Equal(s.podCapacityClaims[claim.TestId], want)
			s.mu.Unlock()
			if !same {
				t.Fatal("transfer changed frozen reservation")
			}
			if ack, err := s.ReservePodCapacity(context.Background(), claim); err == nil || ack.GetAccepted() {
				t.Fatal("stale owner accepted")
			}
			if tc.name == "unchanged" {
				requireCapacityAccepted(t, s, next)
				next.Revision++
				next.GroupPeaks = map[string]int32{"soap": 20}
				requireCapacityAccepted(t, s, next)
			}
		})
	}
}

func TestPodCapacityStandbyFencesConcurrentOldOwnerActivation(t *testing.T) {
	s, claim := standbyCapacityFixture(t)
	requireCapacityAccepted(t, s, claim)
	if ack := claimEpoch(t, s, 2, time.Minute); !ack.Accepted {
		t.Fatalf("epoch: %+v", ack)
	}
	old := proto.Clone(claim).(*pb.PodCapacityClaim)
	old.Revision++
	old.GroupPeaks = map[string]int32{"soap": 20}
	next := proto.Clone(claim).(*pb.PodCapacityClaim)
	next.ExecutionGeneration = 2
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			ack, err := s.ReservePodCapacity(context.Background(), old)
			if err == nil || ack.GetAccepted() {
				t.Error("stale activation accepted")
			}
		}()
		go func() {
			defer wg.Done()
			ack, err := s.ReservePodCapacity(context.Background(), next)
			if err != nil || !ack.GetAccepted() {
				t.Errorf("standby confirmation: %+v %v", ack, err)
			}
		}()
	}
	wg.Wait()
	s.mu.Lock()
	same := proto.Equal(s.podCapacityClaims[claim.TestId], next)
	s.mu.Unlock()
	if !same {
		t.Fatal("concurrent activation mutated standby")
	}
}

func TestPodCapacityStandbyRechecksIdleOnTransferAndActivation(t *testing.T) {
	for _, operation := range []string{"confirm", "transfer", "activate"} {
		for _, work := range []string{"assignment", "staging", "control", "stopping", "execution"} {
			t.Run(operation+"/"+work, func(t *testing.T) {
				s, claim := standbyCapacityFixture(t)
				requireCapacityAccepted(t, s, claim)
				next := proto.Clone(claim).(*pb.PodCapacityClaim)
				if operation == "transfer" {
					if ack := claimEpoch(t, s, 2, time.Minute); !ack.Accepted {
						t.Fatalf("epoch: %+v", ack)
					}
					next.ExecutionGeneration = 2
				}
				if operation == "activate" {
					next.Revision++
					next.GroupPeaks = map[string]int32{"soap": 20}
				}
				s.mu.Lock()
				switch work {
				case "assignment":
					s.assignments[claim.TestId] = assignedTest{}
				case "staging":
					s.lifecycleCoordinator.pendingStages[claim.TestId] = &pendingAssignmentStage{}
				case "control":
					s.lifecycleCoordinator.pendingControls[claim.TestId] = &pendingAssignmentControl{}
				case "stopping":
					s.lifecycleCoordinator.stoppingTests[claim.TestId] = struct{}{}
				case "execution":
					s.executions[claim.TestId] = map[string]*groupExecution{"soap": {}}
				}
				s.mu.Unlock()
				ack, err := s.ReservePodCapacity(context.Background(), next)
				release := proto.Clone(next).(*pb.PodCapacityClaim)
				release.Release = true
				released, releaseErr := s.ReservePodCapacity(context.Background(), release)
				s.mu.Lock()
				delete(s.assignments, claim.TestId)
				delete(s.lifecycleCoordinator.pendingStages, claim.TestId)
				delete(s.lifecycleCoordinator.pendingControls, claim.TestId)
				delete(s.lifecycleCoordinator.stoppingTests, claim.TestId)
				delete(s.executions, claim.TestId)
				same := proto.Equal(s.podCapacityClaims[claim.TestId], claim)
				s.mu.Unlock()
				if err != nil || ack.Accepted || releaseErr != nil || released.Accepted || !same {
					t.Fatalf("unsafe mutation: update=%+v %v release=%+v %v unchanged=%v", ack, err, released, releaseErr, same)
				}
			})
		}
	}
}
