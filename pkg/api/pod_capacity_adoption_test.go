package api

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

type capacityAdoptionClient struct {
	*adoptionFakeClient
	confirm func(*pb.PodCapacityClaim) (*pb.PodCapacityAck, error)
}

func (c *capacityAdoptionClient) ReservePodCapacity(_ context.Context, claim *pb.PodCapacityClaim) (*pb.PodCapacityAck, error) {
	return c.confirm(claim)
}

func TestPhaseATransfersCapacityOrFailsClosed(t *testing.T) {
	for _, mode := range []string{"accepted", "legacy generator", "nil ack", "wrong incarnation", "changed ledger", "completed"} {
		t.Run(mode, func(t *testing.T) {
			runner, store, oldLeadership := newAdoptionTestRunner(t)
			oldOwner, oldGeneration := oldLeadership.executionLeaseTerm()
			claim := storage.PodCapacityReservation{ReservationID: "attempt", Revision: 1, Target: "gen-a:9091", TestID: "orphan", Incarnation: "pod", Generation: oldGeneration, State: "committed", ModelVersion: protocol.CapacityReservationModel, Budget: protocol.CapacityVector{CPUCores: 1, MemoryMB: 100, NetworkMbps: 100, VUsers: 10}, Demand: protocol.CapacityVector{CPUCores: .5, MemoryMB: 50, NetworkMbps: 50, VUsers: 5}, GroupPeaks: map[string]int32{"ga": 5}}
			if err := store.ReservePodCapacity([]storage.PodCapacityReservation{claim}, executionLeaseName, oldOwner, oldGeneration); err != nil {
				t.Fatal(err)
			}
			if err := store.TransitionPodCapacity(claim.Target, claim.TestID, claim.Incarnation, claim.ReservationID, "committed", executionLeaseName, oldOwner, oldGeneration); err != nil {
				t.Fatal(err)
			}
			if err := store.ReleaseSchedulerLease(executionLeaseName, oldOwner); err != nil {
				t.Fatal(err)
			}
			leader := newExecutionLeadership(store, "capacity-successor")
			leader.tick()
			runner.leadership = leader
			owner, generation := leader.executionLeaseTerm()
			if generation <= oldGeneration {
				t.Fatal("term did not advance")
			}
			calls := 0
			outcome := pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ADOPTED
			if mode == "completed" {
				outcome = pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ALREADY_COMPLETED
			}
			client := &capacityAdoptionClient{adoptionFakeClient: &adoptionFakeClient{outcome: outcome}}
			client.confirm = func(request *pb.PodCapacityClaim) (*pb.PodCapacityAck, error) {
				calls++
				expected := claim
				expected.Generation = generation
				if !reflect.DeepEqual(request, podClaim(expected, false)) {
					t.Fatalf("changed frozen claim: %+v", request)
				}
				ack := &pb.PodCapacityAck{Accepted: true, Snapshot: &pb.PodCapacitySnapshot{Incarnation: claim.Incarnation}}
				switch mode {
				case "legacy generator":
					ack.Accepted = false
				case "nil ack":
					return nil, nil
				case "wrong incarnation":
					ack.Snapshot.Incarnation = "other"
				case "changed ledger":
					if err := store.TransitionPodCapacity(claim.Target, claim.TestID, claim.Incarnation, claim.ReservationID, "releasing", executionLeaseName, owner, generation); err != nil {
						t.Fatal(err)
					}
				}
				return ack, nil
			}
			runner.execution = newDistributedExecutionBackend([]string{claim.Target}, time.Second, func(context.Context, string) (generatorClient, error) { return client, nil })
			test := storedContinueRun(t, runner, store, claim.TestID, "continue")
			runner.tests[test.ID] = test
			if err := runner.adoptOrphanedRuns(); err != nil {
				t.Fatal(err)
			}
			resume := test.adoptedResume
			rows, err := store.ListPodCapacityReservations()
			if err != nil || len(rows) != 1 {
				t.Fatalf("capacity was lost: %+v %v", rows, err)
			}
			if mode == "accepted" {
				if resume == nil || resume.finishStatus != "" || rows[0].Generation != generation || calls != 1 {
					t.Fatalf("not adopted: %+v %+v calls=%d", resume, rows, calls)
				}
				expected := claim
				expected.Generation = generation
				if !reflect.DeepEqual(rows[0], expected) {
					t.Fatalf("claim changed: %+v", rows[0])
				}
			} else if mode == "completed" {
				if resume == nil || resume.finishStatus != "" || calls != 0 || rows[0].Generation != oldGeneration {
					t.Fatalf("completed claim was transferred: %+v %+v", resume, rows)
				}
			} else {
				if resume == nil || resume.finishStatus != "FAILED" || !strings.Contains(resume.finishMessage, "capacity ownership transfer") || rows[0].Generation != oldGeneration {
					t.Fatalf("failure did not retain capacity and stop run: %+v %+v", resume, rows)
				}
			}
		})
	}
}
