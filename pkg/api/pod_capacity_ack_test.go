package api

import (
	"context"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

func capacityAckForMode(mode string) *pb.PodCapacityAck {
	if mode == "nil" {
		return nil
	}
	ack := &pb.PodCapacityAck{Accepted: true, Snapshot: &pb.PodCapacitySnapshot{Incarnation: "pod"}}
	switch mode {
	case "rejected":
		ack.Accepted = false
	case "missing identity":
		ack.Snapshot = nil
	case "wrong identity":
		ack.Snapshot.Incarnation = "replacement"
	}
	return ack
}

func seedCapacityAckClaim(t *testing.T, proofs ...*storage.PodCapacityRecoveryPlacement) (*TestRunner, *storage.SQLiteStore, storage.PodCapacityReservation, string, uint64) {
	t.Helper()
	tr, store, leader := newAdoptionTestRunner(t)
	owner, generation := leader.executionLeaseTerm()
	budget := protocol.CapacityVector{CPUCores: 64, MemoryMB: 64000, NetworkMbps: 10000, VUsers: 1000, Protocols: map[string]float64{"http": 1000}}
	demand := budget.Scale(.01)
	claim := storage.PodCapacityReservation{ReservationID: "attempt", Revision: 1, Target: "gen", TestID: "run", Incarnation: "pod", Generation: generation, State: "committed", ModelVersion: protocol.CapacityReservationModel, Budget: budget, Demand: demand, GroupPeaks: map[string]int32{"g": 10}, GroupDemands: map[string]protocol.CapacityVector{"g": demand}}
	if len(proofs) > 0 {
		claim.RecoveryPlacement = proofs[0]
	}
	if err := store.ReservePodCapacity([]storage.PodCapacityReservation{claim}, executionLeaseName, owner, generation); err != nil {
		t.Fatal(err)
	}
	if err := store.TransitionPodCapacity(claim.Target, claim.TestID, claim.Incarnation, claim.ReservationID, "committed", executionLeaseName, owner, generation); err != nil {
		t.Fatal(err)
	}
	return tr, store, claim, owner, generation
}

func TestPodCapacityPlacementRequiresIncarnationAcknowledgment(t *testing.T) {
	for _, mode := range []string{"accepted", "nil", "rejected", "missing identity", "wrong identity"} {
		t.Run(mode, func(t *testing.T) {
			tr, store, leader := newAdoptionTestRunner(t)
			owner, generation := leader.executionLeaseTerm()
			budget := protocol.CapacityVector{CPUCores: 64, MemoryMB: 64000, NetworkMbps: 10000, VUsers: 1000, Protocols: map[string]float64{"http": 1000}}
			capacity := protocol.NewCapacitySnapshot(budget, protocol.CapacityVector{}, 0)
			capacity.Incarnation = "pod"
			client := &capacityAdoptionClient{adoptionFakeClient: &adoptionFakeClient{}, confirm: func(*pb.PodCapacityClaim) (*pb.PodCapacityAck, error) { return capacityAckForMode(mode), nil }}
			test := &ManagedTest{ID: "run", Groups: []TestGroup{{Name: "g", Protocol: "http", VUsers: 10}}}
			claims, err := tr.reservePodPlacement(context.Background(), test, map[string]generatorLoadSnapshot{"gen": {PodCapacity: &capacity}}, map[string][]GeneratorShardSummary{"g": {{Target: "gen", VUsers: 10}}}, map[string]generatorClient{"gen": client}, generation, owner)
			if (err == nil) != (mode == "accepted") || len(claims) != 1 {
				t.Fatalf("claims %d, error %v", len(claims), err)
			}
			rows, err := store.ListPodCapacityReservations()
			if err != nil || len(rows) != 1 || rows[0].State != "pending" {
				t.Fatalf("ambiguous reservation was lost or committed: %+v %v", rows, err)
			}
		})
	}
}

func TestPodCapacityReleaseRetainsUnconfirmedReservation(t *testing.T) {
	for _, path := range []string{"release", "reconcile"} {
		for _, mode := range []string{"accepted", "nil", "rejected", "missing identity", "wrong identity"} {
			t.Run(path+"/"+mode, func(t *testing.T) {
				tr, store, claim, owner, generation := seedCapacityAckClaim(t)
				client := &capacityAdoptionClient{adoptionFakeClient: &adoptionFakeClient{}, confirm: func(*pb.PodCapacityClaim) (*pb.PodCapacityAck, error) { return capacityAckForMode(mode), nil }}
				if path == "release" {
					tr.releasePodClaims([]storage.PodCapacityReservation{claim}, map[string]generatorClient{claim.Target: client}, owner, generation)
				} else {
					now := time.Now()
					if err := store.SaveTest(&storage.TestRecord{ID: claim.TestID, Status: "COMPLETE", StartTime: now, EndTime: &now}); err != nil {
						t.Fatal(err)
					}
					tr.execution = newDistributedExecutionBackend([]string{claim.Target}, time.Second, func(context.Context, string) (generatorClient, error) { return client, nil })
					tr.reconcilePodCapacity(context.Background(), []GeneratorInventoryEntry{{Target: claim.Target, Status: "connected", PodCapacity: &protocol.CapacitySnapshot{Incarnation: claim.Incarnation}}})
				}
				rows, err := store.ListPodCapacityReservations()
				if err != nil {
					t.Fatal(err)
				}
				want := 1
				if mode == "accepted" {
					want = 0
				}
				if len(rows) != want {
					t.Fatalf("release result=%+v; want %d retained claims", rows, want)
				}
			})
		}
	}
}

func TestPodCapacityResizeRetainsBudgetUntilRollbackConfirmed(t *testing.T) {
	for _, mode := range []string{"accepted", "nil", "rejected", "missing identity", "wrong identity", "rollback wrong identity"} {
		t.Run(mode, func(t *testing.T) {
			tr, store, claim, _, _ := seedCapacityAckClaim(t)
			calls := 0
			client := &capacityAdoptionClient{adoptionFakeClient: &adoptionFakeClient{}, confirm: func(*pb.PodCapacityClaim) (*pb.PodCapacityAck, error) {
				calls++
				if mode == "rollback wrong identity" {
					if calls == 1 {
						return capacityAckForMode("accepted"), nil
					}
					return capacityAckForMode("wrong identity"), nil
				}
				return capacityAckForMode(mode), nil
			}}
			rollback, err := tr.expandPodScale(context.Background(), claim.TestID, "g", []GeneratorShardSummary{{Target: claim.Target, VUsers: 20}}, map[string]generatorClient{claim.Target: client})
			if (err == nil) != (mode == "accepted" || mode == "rollback wrong identity") {
				t.Fatalf("resize error=%v", err)
			}
			rollback()
			rows, err := store.ListPodCapacityReservations()
			if err != nil || len(rows) != 1 {
				t.Fatalf("lost reserved budget: %+v %v", rows, err)
			}
			want := int32(20)
			if mode == "accepted" {
				want = 10
			}
			if rows[0].GroupPeaks["g"] != want {
				t.Fatalf("peak %d, want %d until confirmation", rows[0].GroupPeaks["g"], want)
			}
		})
	}
}

func TestPodCapacityAckRejectsEmptyExpectedIncarnation(t *testing.T) {
	ack := &pb.PodCapacityAck{Accepted: true, Snapshot: &pb.PodCapacitySnapshot{}}
	if podCapacityAckConfirms(ack, "") {
		t.Fatal("empty identities cannot confirm a reservation")
	}
}
