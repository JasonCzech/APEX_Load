package api

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"reflect"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

func standbyEvacuationFixture() (storage.PodCapacityReservation, generatorLoadSnapshot) {
	budget := protocol.CapacityVector{CPUCores: 4, MemoryMB: 1000, NetworkMbps: 100, VUsers: 100, Protocols: map[string]float64{"http": 100}}
	claim := storage.PodCapacityReservation{ReservationID: "attempt", Revision: 1, Target: "spare", TestID: "run", Incarnation: "spare-pod", Generation: 1, State: "committed", ModelVersion: protocol.CapacityReservationModel, Budget: budget, Demand: budget, Dedicated: true}
	capacity := protocol.NewCapacitySnapshot(budget, budget, 0)
	capacity.Incarnation = claim.Incarnation
	capacity.Dedicated = true
	capacity.ReportedTestIDs = []string{claim.TestID}
	capacity.ObservedAtUnixMS = time.Now().UnixMilli()
	return claim, generatorLoadSnapshot{Target: claim.Target, PodCapacity: &capacity, HeartbeatOK: true, MaxVUsers: 100, CompatibleFeatures: []string{generatorrpc.FeaturePodCapacityV1, generatorrpc.FeaturePodCapacityStandbyV1}}
}

func TestStandbyEvacuationLoadRequiresExclusiveFreshProof(t *testing.T) {
	for _, mode := range []string{"valid", "ordinary", "wrong run", "wrong target", "wrong generation", "pending", "active groups", "empty reservation", "empty incarnation", "zero revision", "overflow", "failed heartbeat", "active users", "no max", "legacy", "missing capacity", "unready", "shared", "replaced pod", "headroom", "other run", "extra run", "invalid budget", "model", "demand", "schedulable", "reserved", "missing timestamp", "future", "stale"} {
		t.Run(mode, func(t *testing.T) {
			claim, load := standbyEvacuationFixture()
			load.AvailableCapacity = 17
			switch mode {
			case "ordinary":
				claim = storage.PodCapacityReservation{}
			case "wrong run":
				claim.TestID = "other"
			case "wrong target":
				claim.Target = "other"
			case "wrong generation":
				claim.Generation++
			case "pending":
				claim.State = "pending"
			case "active groups":
				claim.GroupPeaks = map[string]int32{"g": 1}
			case "empty reservation":
				claim.ReservationID = ""
			case "empty incarnation":
				claim.Incarnation = ""
			case "zero revision":
				claim.Revision = 0
			case "overflow":
				claim.Revision = math.MaxUint64 - 1
			case "failed heartbeat":
				load.HeartbeatOK = false
			case "active users":
				load.Stats.ActiveVUsers = 1
			case "no max":
				load.MaxVUsers = 0
			case "legacy":
				load.CompatibleFeatures = []string{generatorrpc.FeaturePodCapacityV1}
			case "missing capacity":
				load.PodCapacity = nil
			case "unready":
				load.PodCapacity.State = "unreconciled"
			case "shared":
				load.PodCapacity.Dedicated = false
			case "replaced pod":
				load.PodCapacity.Incarnation = "new"
			case "headroom":
				load.PodCapacity.HeadroomPct++
			case "other run":
				load.PodCapacity.ReportedTestIDs = []string{"other"}
			case "extra run":
				load.PodCapacity.ReportedTestIDs = append(load.PodCapacity.ReportedTestIDs, "other")
			case "invalid budget":
				claim.Budget.CPUCores = math.NaN()
			case "model":
				claim.ModelVersion = "new"
			case "demand":
				claim.Demand = claim.Demand.Scale(.5)
			case "schedulable":
				load.PodCapacity.Schedulable = claim.Budget.Scale(2)
			case "reserved":
				load.PodCapacity.Reserved = claim.Budget.Scale(.5)
			case "missing timestamp":
				load.PodCapacity.ObservedAtUnixMS = 0
			case "future":
				load.PodCapacity.ObservedAtUnixMS = time.Now().Add(time.Minute).UnixMilli()
			case "stale":
				load.PodCapacity.ObservedAtUnixMS = time.Now().Add(-2 * defaultGeneratorSnapshotTTL).UnixMilli()
			}
			got := standbyEvacuationLoad(load, claim, "run", 1)
			want := int32(0)
			if mode == "valid" {
				want = 100
			}
			if mode == "ordinary" {
				want = 17
			}
			if got.AvailableCapacity != want {
				t.Fatalf("capacity=%d want=%d", got.AvailableCapacity, want)
			}
			if load.AvailableCapacity != 17 {
				t.Fatal("changed public admission snapshot")
			}
		})
	}
}

// A scripted external heartbeat exercises the actual protobuf transport. The
// capacity ledger and placement implementation under test are real.
type standbyCapacityHeartbeatServer struct {
	pb.UnimplementedGeneratorControlServer
	claim storage.PodCapacityReservation
}

func (s *standbyCapacityHeartbeatServer) Heartbeat(stream pb.GeneratorControl_HeartbeatServer) error {
	if _, err := stream.Recv(); err != nil {
		return err
	}
	c := s.claim
	return stream.Send(&pb.HeartbeatPong{GeneratorId: c.Target, MaxVusers: 100, CompatibleFeatures: []string{generatorrpc.FeaturePodCapacityV1, generatorrpc.FeaturePodCapacityStandbyV1, generatorrpc.FeatureEvacuationAssignmentStateV1}, PodCapacity: &pb.PodCapacitySnapshot{Incarnation: c.Incarnation, State: "ready", Dedicated: true, Schedulable: generatorrpc.CapacityVectorToProto(c.Budget), Reserved: generatorrpc.CapacityVectorToProto(c.Demand), ReservedTestIds: []string{c.TestID}}})
}
func (s *standbyCapacityHeartbeatServer) ReservePodCapacity(ctx context.Context, _ *pb.PodCapacityClaim) (*pb.PodCapacityAck, error) {
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 10*time.Second {
		return nil, errors.New("activation or rollback has no bounded remote deadline")
	}
	return &pb.PodCapacityAck{Accepted: true, Snapshot: &pb.PodCapacitySnapshot{Incarnation: s.claim.Incarnation}}, nil
}
func standbyCapacityWireClient(t *testing.T, claim storage.PodCapacityReservation) *generatorrpc.Client {
	t.Helper()
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	pb.RegisterGeneratorControlServer(server, &standbyCapacityHeartbeatServer{claim: claim})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	client, err := generatorrpc.Dial(ctx, claim.Target, grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestStandbyEvacuationReservesFrozenPeakAndRollsBack(t *testing.T) {
	for _, mode := range []string{"activate", "dry run", "changed ledger", "missing source", "insufficient budget", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			tr, store, source, owner, generation := seedCapacityAckClaim(t)
			standby, _ := standbyEvacuationFixture()
			standby.Generation = generation
			if err := store.ReservePodCapacity([]storage.PodCapacityReservation{standby}, executionLeaseName, owner, generation); err != nil {
				t.Fatal(err)
			}
			if err := store.TransitionPodCapacity(standby.Target, standby.TestID, standby.Incarnation, standby.ReservationID, "committed", executionLeaseName, owner, generation); err != nil {
				t.Fatal(err)
			}
			settings := tr.GetSystemSettings()
			settings.CapacityPlanning.PodReservationsEnabled = true
			tr.settings = settings
			standbys, err := tr.standbyEvacuationReservations("run", generation)
			if err != nil {
				t.Fatal(err)
			}
			client := standbyCapacityWireClient(t, standby)
			b := newDistributedExecutionBackend([]string{standby.Target}, time.Second, func(context.Context, string) (generatorClient, error) { return client, nil })
			switch mode {
			case "changed ledger":
				if err := store.TransitionPodCapacity(standby.Target, standby.TestID, standby.Incarnation, standby.ReservationID, "releasing", executionLeaseName, owner, generation); err != nil {
					t.Fatal(err)
				}
			case "missing source":
				if err := store.TransitionPodCapacity(source.Target, source.TestID, source.Incarnation, source.ReservationID, "released", executionLeaseName, owner, generation); err != nil {
					t.Fatal(err)
				}
			case "insufficient budget":
				source.Revision++
				source.GroupDemands["g"] = source.Budget
				source.Demand = source.Budget
				if err := store.ReservePodCapacity([]storage.PodCapacityReservation{source}, executionLeaseName, owner, generation); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := store.ListPodCapacityReservations()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "canceled" {
				cancel()
			}
			claims, err := tr.reservePodEvacuation(ctx, b, "run", source.Target, map[string][]GeneratorShardSummary{"g": {{Target: standby.Target, VUsers: 10}}}, map[string]generatorClient{standby.Target: client}, mode == "dry run", standbys)
			accepted := mode == "activate" || mode == "dry run"
			if (err == nil) != accepted {
				t.Fatalf("mode=%s claims=%+v err=%v", mode, claims, err)
			}
			if mode != "activate" {
				after, _ := store.ListPodCapacityReservations()
				if !reflect.DeepEqual(before, after) {
					t.Fatal("rejected/dry placement mutated ledger")
				}
				return
			}
			if len(claims) != 1 || claims[0].Revision != 2 || claims[0].GroupPeaks["g"] != 10 || !sameCapacityVector(claims[0].Demand, standby.Budget) {
				t.Fatalf("activation=%+v", claims)
			}
			if err := tr.rollbackEvacuationPodClaims(b, claims, standbys); err != nil {
				t.Fatal(err)
			}
			rows, _ := store.ListPodCapacityReservations()
			for _, row := range rows {
				if row.Target == standby.Target && (row.State != "committed" || row.Revision != 3 || len(row.GroupPeaks) != 0 || !sameCapacityVector(row.Demand, standby.Budget)) {
					t.Fatalf("rollback lost standby: %+v", row)
				}
			}
		})
	}
}

func TestStandbyRollbackRetainsUncertainCapacity(t *testing.T) {
	for _, mode := range []string{"success", "activation rejected", "idle rejected", "wrong incarnation", "dial failed", "changed ledger"} {
		t.Run(mode, func(t *testing.T) {
			tr, store, _, owner, generation := seedCapacityAckClaim(t)
			standby, _ := standbyEvacuationFixture()
			standby.Generation = generation
			active := standby
			active.Revision = 2
			active.State = "pending"
			active.GroupPeaks = map[string]int32{"g": 10}
			active.GroupDemands = map[string]protocol.CapacityVector{"g": standby.Budget.Scale(.1)}
			if err := store.ReservePodCapacity([]storage.PodCapacityReservation{active}, executionLeaseName, owner, generation); err != nil {
				t.Fatal(err)
			}
			if mode == "changed ledger" {
				if err := store.TransitionPodCapacity(active.Target, active.TestID, active.Incarnation, active.ReservationID, "releasing", executionLeaseName, owner, generation); err != nil {
					t.Fatal(err)
				}
			}
			calls := 0
			client := &capacityAdoptionClient{adoptionFakeClient: &adoptionFakeClient{}, confirm: func(claim *pb.PodCapacityClaim) (*pb.PodCapacityAck, error) {
				calls++
				if claim.Release {
					t.Fatal("rollback released the promised standby")
				}
				ack := &pb.PodCapacityAck{Accepted: true, Snapshot: &pb.PodCapacitySnapshot{Incarnation: active.Incarnation}}
				if mode == "activation rejected" || mode == "idle rejected" && calls == 2 {
					ack.Accepted = false
				}
				if mode == "wrong incarnation" {
					ack.Snapshot.Incarnation = "replacement"
				}
				return ack, nil
			}}
			b := newDistributedExecutionBackend([]string{active.Target}, time.Second, func(context.Context, string) (generatorClient, error) {
				if mode == "dial failed" {
					return nil, errors.New("unreachable")
				}
				return client, nil
			})
			err := tr.rollbackEvacuationPodClaims(b, []storage.PodCapacityReservation{active}, map[string]storage.PodCapacityReservation{standby.Target: standby})
			if (err == nil) != (mode == "success") {
				t.Fatalf("rollback err=%v", err)
			}
			rows, _ := store.ListPodCapacityReservations()
			found := false
			for _, row := range rows {
				if row.Target != standby.Target {
					continue
				}
				found = true
				if !row.Dedicated || !sameCapacityVector(row.Demand, standby.Budget) {
					t.Fatal("lost full reserved budget")
				}
				if mode == "success" {
					if len(row.GroupPeaks) != 0 || row.Revision != 3 || row.State != "committed" {
						t.Fatalf("idle claim=%+v", row)
					}
				} else if row.Revision != 2 || row.GroupPeaks["g"] != 10 {
					t.Fatalf("uncertain claim changed: %+v", row)
				}
			}
			if !found {
				t.Fatal("lost standby reservation")
			}
		})
	}
}

func (s *standbyCapacityHeartbeatServer) ClaimExecutionEpoch(_ context.Context, c *pb.ExecutionEpochClaim) (*pb.ExecutionEpochAck, error) {
	return &pb.ExecutionEpochAck{Accepted: true, CurrentGeneration: c.ExecutionGeneration, ExpiresAtUnixMs: time.Now().Add(time.Duration(c.LeaseTtlMilliseconds) * time.Millisecond).UnixMilli()}, nil
}
func TestConnectEvacuationCandidatesIncludesOnlyOwnedStandby(t *testing.T) {
	for _, owned := range []bool{false, true} {
		t.Run(fmt.Sprint(owned), func(t *testing.T) {
			claim, _ := standbyEvacuationFixture()
			client := standbyCapacityWireClient(t, claim)
			b := newDistributedExecutionBackend([]string{claim.Target}, time.Second, func(context.Context, string) (generatorClient, error) { return client, nil })
			standbys := map[string]storage.PodCapacityReservation{}
			if owned {
				standbys[claim.Target] = claim
			}
			clients := map[string]generatorClient{}
			got := b.connectEvacuationCandidates(context.Background(), &ManagedTest{ID: claim.TestID}, "source", clients, nil, claim.Generation, "owner", time.Now().Add(time.Minute), standbys)
			if (len(got) == 1) != owned {
				t.Fatalf("owned=%v candidates=%v", owned, got)
			}
			if len(clients) != len(got) {
				t.Fatal("client set differs from accepted candidates")
			}
		})
	}
}

func TestStandbyEvacuationReservationsRejectUnconfirmedOwner(t *testing.T) {
	for _, mode := range []string{"committed", "pending", "releasing", "different generation", "other run"} {
		t.Run(mode, func(t *testing.T) {
			tr, store, _, owner, generation := seedCapacityAckClaim(t)
			claim, _ := standbyEvacuationFixture()
			claim.Generation = generation
			if mode == "other run" {
				claim.TestID = "other"
			}
			if err := store.ReservePodCapacity([]storage.PodCapacityReservation{claim}, executionLeaseName, owner, generation); err != nil {
				t.Fatal(err)
			}
			state := "committed"
			if mode == "releasing" {
				state = "releasing"
			}
			if mode != "pending" {
				if err := store.TransitionPodCapacity(claim.Target, claim.TestID, claim.Incarnation, claim.ReservationID, state, executionLeaseName, owner, generation); err != nil {
					t.Fatal(err)
				}
			}
			requested := generation
			if mode == "different generation" {
				requested++
			}
			claims, err := tr.standbyEvacuationReservations("run", requested)
			accepted := mode == "committed" || mode == "other run"
			if (err == nil) != accepted {
				t.Fatalf("claims=%+v err=%v", claims, err)
			}
			if mode == "committed" && len(claims) != 1 {
				t.Fatal("missing committed standby")
			}
			if mode == "other run" && len(claims) != 0 {
				t.Fatal("selected another run's standby")
			}
		})
	}
}

func TestStandbyActivationRequiresDurableLedger(t *testing.T) {
	claim, _ := standbyEvacuationFixture()
	runner := NewTestRunner(1, nil, nil)
	if _, err := runner.reservePodEvacuation(context.Background(), nil, "run", "source", nil, nil, false, map[string]storage.PodCapacityReservation{claim.Target: claim}); err == nil {
		t.Fatal("standby activation proceeded without a durable ledger")
	}
}
