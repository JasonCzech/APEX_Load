package api

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

type standbyAdoptionClient struct {
	*capacityAdoptionClient
	epoch         bool
	epochRejected bool
	closed        int
}

func (c *standbyAdoptionClient) ClaimExecutionEpochWithAdoptions(_ context.Context, generation uint64, _ string, _ time.Duration, adoptions []generatorrpc.RunAdoption) (generatorrpc.ExecutionEpochResult, error) {
	if len(adoptions) != 0 {
		return generatorrpc.ExecutionEpochResult{}, errors.New("standby received executable adoption")
	}
	if c.epochRejected {
		return generatorrpc.ExecutionEpochResult{}, errors.New("epoch refused")
	}
	c.epoch = true
	return generatorrpc.ExecutionEpochResult{Accepted: true, CurrentGeneration: generation, AdoptionSupported: true}, nil
}
func (c *standbyAdoptionClient) ClaimExecutionEpoch(ctx context.Context, generation uint64, owner string, ttl time.Duration) (generatorrpc.ExecutionEpochResult, error) {
	return c.ClaimExecutionEpochWithAdoptions(ctx, generation, owner, ttl, nil)
}
func (c *standbyAdoptionClient) Close() error { c.closed++; return nil }

func TestPhaseATransfersStandbyCapacityWithoutAddingParticipant(t *testing.T) {
	for _, mode := range []string{"accepted", "completed", "unreachable", "epoch rejected", "legacy capacity", "changed incarnation", "nil ack", "changed ledger"} {
		t.Run(mode, func(t *testing.T) {
			runner, store, oldLeader := newAdoptionTestRunner(t)
			oldOwner, oldGeneration := oldLeader.executionLeaseTerm()
			budget := protocol.CapacityVector{CPUCores: 1, MemoryMB: 100, NetworkMbps: 100, VUsers: 10}
			standby := storage.PodCapacityReservation{ReservationID: "attempt", Revision: 1, Target: "gen-spare:9091", TestID: "orphan", Incarnation: "standby-pod", Generation: oldGeneration, State: "committed", ModelVersion: protocol.CapacityReservationModel, Budget: budget, Demand: budget, Dedicated: true}
			if err := store.ReservePodCapacity([]storage.PodCapacityReservation{standby}, executionLeaseName, oldOwner, oldGeneration); err != nil {
				t.Fatal(err)
			}
			if err := store.TransitionPodCapacity(standby.Target, standby.TestID, standby.Incarnation, standby.ReservationID, "committed", executionLeaseName, oldOwner, oldGeneration); err != nil {
				t.Fatal(err)
			}
			if err := store.ReleaseSchedulerLease(executionLeaseName, oldOwner); err != nil {
				t.Fatal(err)
			}
			leader := newExecutionLeadership(store, "standby-successor")
			leader.tick()
			runner.leadership = leader
			owner, generation := leader.executionLeaseTerm()
			live := &adoptionFakeClient{outcome: pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ADOPTED}
			if mode == "completed" {
				live.outcome = pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ALREADY_COMPLETED
			}
			spare := &standbyAdoptionClient{capacityAdoptionClient: &capacityAdoptionClient{adoptionFakeClient: &adoptionFakeClient{}}, epochRejected: mode == "epoch rejected"}
			confirmations := 0
			spare.confirm = func(request *pb.PodCapacityClaim) (*pb.PodCapacityAck, error) {
				confirmations++
				expected := standby
				expected.Generation = generation
				if !spare.epoch || !reflect.DeepEqual(request, podClaim(expected, false)) {
					t.Error("standby transfer lacked epoch or changed frozen capacity")
				}
				ack := &pb.PodCapacityAck{Accepted: true, Snapshot: &pb.PodCapacitySnapshot{Incarnation: standby.Incarnation}}
				switch mode {
				case "legacy capacity":
					ack.Accepted = false
				case "changed incarnation":
					ack.Snapshot.Incarnation = "replacement"
				case "nil ack":
					return nil, nil
				case "changed ledger":
					if err := store.TransitionPodCapacity(standby.Target, standby.TestID, standby.Incarnation, standby.ReservationID, "releasing", executionLeaseName, owner, generation); err != nil {
						t.Fatal(err)
					}
				}
				return ack, nil
			}
			runner.execution = newDistributedExecutionBackend([]string{"gen-a:9091", standby.Target}, time.Second, func(_ context.Context, target string) (generatorClient, error) {
				if target == standby.Target {
					if mode == "unreachable" {
						return nil, errors.New("standby unavailable")
					}
					return spare, nil
				}
				return live, nil
			})
			test := storedContinueRun(t, runner, store, "orphan", "continue")
			runner.tests[test.ID] = test
			if err := runner.adoptOrphanedRuns(); err != nil {
				t.Fatal(err)
			}
			rows, err := store.ListPodCapacityReservations()
			if err != nil || len(rows) != 1 {
				t.Fatalf("standby lost: %+v %v", rows, err)
			}
			if mode == "accepted" || mode == "completed" {
				expected := standby
				expected.Generation = generation
				if !reflect.DeepEqual(rows[0], expected) || confirmations != 1 || test.adoptedResume == nil || test.adoptedResume.finishStatus != "" {
					t.Fatalf("standby was not adopted: %+v %+v", rows, test.adoptedResume)
				}
				wantParticipants := 1
				if mode == "completed" {
					wantParticipants = 0
				}
				if len(test.adoptedResume.adopted) != wantParticipants || test.adoptedResume.adopted[standby.Target] != nil || test.remoteClientsByTarget[standby.Target] != nil {
					t.Fatal("idle standby became executable participant")
				}
			} else if rows[0].Generation != oldGeneration || test.adoptedResume == nil || test.adoptedResume.finishStatus != "FAILED" {
				t.Fatalf("standby problem did not fail closed: %+v %+v", rows, test.adoptedResume)
			}
			if mode != "unreachable" && spare.closed != 1 {
				t.Fatalf("standby transport close count=%d", spare.closed)
			}
		})
	}
}

func TestStandbyEpochBatchPreservesOtherRunAdoptions(t *testing.T) {
	runner, store, leader := newAdoptionTestRunner(t)
	owner, generation := leader.executionLeaseTerm()
	budget := protocol.CapacityVector{CPUCores: 1, MemoryMB: 100, NetworkMbps: 100, VUsers: 10}
	claim := storage.PodCapacityReservation{ReservationID: "attempt", Revision: 1, Target: "shared-target", TestID: "standby-owner", Incarnation: "pod", Generation: generation, ModelVersion: protocol.CapacityReservationModel, Budget: budget, Demand: budget, Dedicated: true}
	if err := store.ReservePodCapacity([]storage.PodCapacityReservation{claim}, executionLeaseName, owner, generation); err != nil {
		t.Fatal(err)
	}
	if err := store.TransitionPodCapacity(claim.Target, claim.TestID, claim.Incarnation, claim.ReservationID, "committed", executionLeaseName, owner, generation); err != nil {
		t.Fatal(err)
	}
	existing := []generatorrpc.RunAdoption{{TestID: "another-run", PriorGeneration: generation}}
	batch := map[string][]generatorrpc.RunAdoption{claim.Target: existing}
	candidate := &runAdoptionCandidate{test: &ManagedTest{ID: claim.TestID}}
	if err := runner.addStandbyCapacityAdoptionTargets([]*runAdoptionCandidate{candidate}, batch); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(batch[claim.Target], existing) || !reflect.DeepEqual(candidate.standbyTargets, []string{claim.Target}) {
		t.Fatal("standby replaced another run's adoption batch")
	}
	// A later storage snapshot can see a newly inserted standby that the first
	// epoch batch never included. Do not silently resume without its confirmation.
	if err := runner.adoptPodCapacity(context.Background(), claim.TestID, nil, generation+1, owner); err == nil {
		t.Fatal("unclaimed standby ignored")
	}
}

type unavailableAdoptionCapacityStore struct{ *storage.SQLiteStore }

func (s *unavailableAdoptionCapacityStore) ListPodCapacityReservations() ([]storage.PodCapacityReservation, error) {
	return nil, errors.New("capacity snapshot unavailable")
}

func TestAdoptionDoesNotClaimEpochWithoutStandbySnapshot(t *testing.T) {
	runner, store, old := newAdoptionTestRunner(t)
	owner, _ := old.executionLeaseTerm()
	if err := store.ReleaseSchedulerLease(executionLeaseName, owner); err != nil {
		t.Fatal(err)
	}
	leader := newExecutionLeadership(store, "snapshot-successor")
	leader.tick()
	runner.leadership = leader
	test := storedContinueRun(t, runner, store, "snapshot-run", "continue")
	runner.tests[test.ID] = test
	runner.testStore = &unavailableAdoptionCapacityStore{store}
	runner.execution = newDistributedExecutionBackend([]string{"gen-a:9091"}, time.Second, func(context.Context, string) (generatorClient, error) {
		t.Error("claimed a generator before the complete standby snapshot")
		return nil, errors.New("unexpected dial")
	})
	if err := runner.adoptOrphanedRuns(); err == nil {
		t.Fatal("missing capacity snapshot was ignored")
	}
	if test.adoptedResume != nil {
		t.Fatal("run resumed without capacity snapshot")
	}
}
