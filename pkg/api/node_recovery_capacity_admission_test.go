package api

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/kubeops"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

func recoveryAdmissionFixture() (*TestRunner, *ManagedTest, []storage.PodCapacityReservation, map[string]generatorLoadSnapshot) {
	tr, test, placements := nodeRecoveryPreviewFixture()
	tr.durableExecutionFence = true
	test.ID = "run"
	test.Assignment.RecoverySpareVUsers = 200
	loads := map[string]generatorLoadSnapshot{}
	for i := range tr.liveInventory {
		e := &tr.liveInventory[i]
		budget := protocol.CapacityVector{CPUCores: 64, MemoryMB: 64000, NetworkMbps: 10000, VUsers: 100, Protocols: map[string]float64{"http": 100}}
		snapshot := protocol.NewCapacitySnapshot(budget, protocol.CapacityVector{}, 0)
		snapshot.Incarnation = e.PodCapacity.Incarnation
		e.PodCapacity = &snapshot
		loads[e.Target] = generatorLoadSnapshot{Target: e.Target, HeartbeatOK: true, MaxVUsers: 100, AvailableCapacity: 100, PodCapacity: &snapshot, SupportedProtocols: e.SupportedProtocols, CompatibleFeatures: e.CompatibleFeatures}
	}
	var claims []storage.PodCapacityReservation
	for _, p := range placements {
		c := loads[p.Target].PodCapacity
		claims = append(claims, storage.PodCapacityReservation{ReservationID: "attempt", Revision: 1, Target: p.Target, TestID: test.ID, Incarnation: c.Incarnation, Generation: 1, State: "pending", ModelVersion: protocol.CapacityReservationModel, Budget: c.Schedulable, Demand: p.Proposed, HeadroomPct: c.HeadroomPct, GroupPeaks: map[string]int32{"http": 100}, GroupDemands: map[string]protocol.CapacityVector{"http": p.Proposed}})
	}
	return tr, test, claims, loads
}

func TestRecoverySpareAdmissionFreezesDistinctWorkerPlan(t *testing.T) {
	tr, test, claims, loads := recoveryAdmissionFixture()
	got, err := tr.addRecoverySpareClaims(test, claims, loads)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("claims=%+v", got)
	}
	destinations := map[string]string{}
	for _, claim := range got {
		proof := claim.RecoveryPlacement
		if proof == nil || proof.WorkloadUID != "workload" || proof.PodUID != "pod-"+claim.Target {
			t.Fatalf("missing identity: %+v", claim)
		}
		if claim.Target == "a" || claim.Target == "b" {
			if len(proof.ReplacementSources) != 0 {
				t.Fatal("participant became standby")
			}
			continue
		}
		if !claim.Dedicated || !sameCapacityVector(claim.Budget, claim.Demand) || len(claim.GroupPeaks) != 0 || proof.NodeName != "worker-2" || len(proof.ReplacementSources) != 1 {
			t.Fatalf("invalid standby=%+v", claim)
		}
		source := proof.ReplacementSources[0]
		if destinations[source] != "" {
			t.Fatal("duplicate source match")
		}
		destinations[source] = claim.Target
	}
	if len(destinations) != 2 || destinations["a"] == destinations["b"] {
		t.Fatalf("replacement map=%v", destinations)
	}
	for _, claim := range claims {
		if claim.RecoveryPlacement != nil {
			t.Fatal("mutated input claims")
		}
	}
}

func TestRecoverySpareAdmissionRejectsUnprovenCapacity(t *testing.T) {
	for _, mode := range []string{"off", "quota envelope", "stale", "missing scale limit", "missing source name", "missing spare name", "replica maximum", "same worker", "occupied", "hard maximum", "legacy", "replaced pod", "ambiguous", "weighted", "non-http", "iterations", "no fence"} {
		t.Run(mode, func(t *testing.T) {
			tr, test, claims, loads := recoveryAdmissionFixture()
			switch mode {
			case "off":
				test.Assignment.RecoverySpareVUsers = 0
			case "quota envelope":
				test.Assignment.RecoverySpareVUsers = 199
			case "stale":
				tr.liveInventoryAt = time.Now().Add(-time.Hour)
			case "missing scale limit":
				tr.liveInventory[0].PodPlacement.scaleLimit = kubeops.StatefulSetAutoscalingLimit{}
			case "missing source name":
				tr.liveInventory[0].PodPlacement.Name = ""
			case "missing spare name":
				tr.liveInventory[2].PodPlacement.Name = ""
			case "replica maximum":
				for i := range tr.liveInventory {
					tr.liveInventory[i].PodPlacement.scaleLimit.MaximumReplicas = 3
				}
			case "same worker":
				for i := range tr.liveInventory {
					tr.liveInventory[i].PodPlacement.NodeName = "worker-1"
				}
			case "occupied":
				loads["c"].PodCapacity.ReportedTestIDs = []string{"other"}
			case "hard maximum":
				load := loads["c"]
				load.MaxVUsers = 99
				loads["c"] = load
			case "legacy":
				load := loads["c"]
				load.CompatibleFeatures = []string{generatorrpc.FeaturePodCapacityV1}
				loads["c"] = load
			case "replaced pod":
				c := *tr.liveInventory[2].PodCapacity
				c.Incarnation = "new"
				tr.liveInventory[2].PodCapacity = &c
			case "ambiguous":
				tr.liveInventory = append(tr.liveInventory, tr.liveInventory[0])
			case "weighted":
				test.Assignment.Mode = generatorAssignmentModeRegionWeighted
			case "non-http":
				test.Groups[0].Protocol = "database"
			case "iterations":
				test.TerminationMode = "iterations"
			case "no fence":
				tr.durableExecutionFence = false
			}
			got, err := tr.addRecoverySpareClaims(test, claims, loads)
			if mode == "off" {
				if err != nil || !reflect.DeepEqual(got, claims) {
					t.Fatalf("default changed: %v", err)
				}
			} else if err == nil {
				t.Fatalf("accepted %s: %+v", mode, got)
			}
		})
	}
}

func TestRecoverySparePlacementIsAtomicBeforeAcknowledgment(t *testing.T) {
	for _, occupied := range []bool{false, true} {
		t.Run(map[bool]string{false: "available", true: "conflicting run"}[occupied], func(t *testing.T) {
			tr, store, leader := newAdoptionTestRunner(t)
			fixture, test, _, loads := recoveryAdmissionFixture()
			tr.settings = fixture.settings
			tr.liveInventory = fixture.liveInventory
			tr.liveInventoryAt = fixture.liveInventoryAt
			tr.execution = fixture.execution
			owner, generation := leader.executionLeaseTerm()
			if occupied {
				c := loads["c"].PodCapacity
				other := storage.PodCapacityReservation{ReservationID: "other", Revision: 1, Target: "c", TestID: "other", Incarnation: c.Incarnation, Generation: generation, State: "pending", ModelVersion: protocol.CapacityReservationModel, Budget: c.Schedulable, Demand: c.Schedulable, Dedicated: true, HeadroomPct: c.HeadroomPct}
				if err := store.ReservePodCapacity([]storage.PodCapacityReservation{other}, executionLeaseName, owner, generation); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := store.ListPodCapacityReservations()
			calls := 0
			clients := map[string]generatorClient{}
			for target := range loads {
				clients[target] = &capacityAdoptionClient{adoptionFakeClient: &adoptionFakeClient{}, confirm: func(c *pb.PodCapacityClaim) (*pb.PodCapacityAck, error) {
					calls++
					rows, err := store.ListPodCapacityReservations()
					if err != nil || len(rows) != 4 {
						t.Fatalf("RPC preceded complete reservation: %d %v", len(rows), err)
					}
					return &pb.PodCapacityAck{Accepted: true, Snapshot: &pb.PodCapacitySnapshot{Incarnation: c.Incarnation}}, nil
				}}
			}
			claims, err := tr.reservePodPlacement(context.Background(), test, loads, map[string][]GeneratorShardSummary{"http": {{Target: "a", VUsers: 100}, {Target: "b", VUsers: 100}}}, clients, generation, owner)
			if occupied {
				after, _ := store.ListPodCapacityReservations()
				if err == nil || calls != 0 || !reflect.DeepEqual(before, after) {
					t.Fatalf("partial placement: err=%v calls=%d", err, calls)
				}
				return
			}
			if err != nil || len(claims) != 4 || calls != 4 {
				t.Fatalf("placement: %v claims=%d calls=%d", err, len(claims), calls)
			}
			rows, _ := store.ListPodCapacityReservations()
			for _, row := range rows {
				if row.State != "committed" || row.RecoveryPlacement == nil {
					t.Fatalf("work could stage before confirmation: %+v", row)
				}
			}
		})
	}
}

func TestRecoverySpareQuotaAndDemandIncludeEnvelope(t *testing.T) {
	tr := NewTestRunner(10, nil, nil)
	current := &ManagedTest{ID: "current", ProjectID: "p", Status: "RUNNING", TotalVUsers: 50, Assignment: &GeneratorAssignmentSettings{PoolID: "pool", TolerateGeneratorLoss: true, RecoverySpareVUsers: 100}}
	next := &ManagedTest{ID: "next", ProjectID: "p", Status: "QUEUED", TotalVUsers: 20, Assignment: &GeneratorAssignmentSettings{PoolID: "pool", TolerateGeneratorLoss: true, RecoverySpareVUsers: 100}}
	tr.tests[current.ID] = current
	if got := tr.countActiveVUsersForProjectLocked("p"); got != 150 {
		t.Fatalf("project charge=%d", got)
	}
	if blocked, _ := tr.evaluatePoolQuotaBlockWithQuotaLocked(next, "", storage.PoolProjectQuota{MaxVUsers: 269}, true); !blocked {
		t.Fatal("spares escaped pool quota")
	}
	if blocked, reason := tr.evaluatePoolQuotaBlockWithQuotaLocked(next, "", storage.PoolProjectQuota{MaxVUsers: 270}, true); blocked {
		t.Fatal(reason)
	}
	stats := scalingSignalStats([]*ManagedTest{next})
	if stats.QueuedVUsers != 120 || stats.QueuedProtocolVUsers["http"] != 100 {
		t.Fatalf("missing queued spare demand: %+v", stats)
	}
}

func TestRecoverySpareAssignmentValidation(t *testing.T) {
	for _, value := range []int{-1, 0, 100, maxManagedTestVUsers + 1} {
		normalized, err := normalizeGeneratorAssignmentSettings(&GeneratorAssignmentSettings{TolerateGeneratorLoss: true, RecoverySpareVUsers: value})
		if (err == nil) != (value >= 0 && value <= maxManagedTestVUsers) {
			t.Fatalf("value=%d error=%v", value, err)
		}
		if err == nil && normalized.RecoverySpareVUsers != value {
			t.Fatal("lost envelope")
		}
	}
	if _, err := normalizeGeneratorAssignmentSettings(&GeneratorAssignmentSettings{RecoverySpareVUsers: 100}); err == nil {
		t.Fatal("reserved without loss tolerance")
	}
	if _, err := normalizeGroupGeneratorAssignmentSettings(&GeneratorAssignmentSettings{TolerateGeneratorLoss: true, RecoverySpareVUsers: 100}); err == nil || !strings.Contains(err.Error(), "test-scoped") {
		t.Fatalf("group envelope=%v", err)
	}
}

func TestRecoverySparePreviewIncludesReservedFootprint(t *testing.T) {
	tr, test, _, _ := recoveryAdmissionFixture()
	placements, warnings := tr.previewPodPlacementLocked(test)
	if len(warnings) != 0 || len(placements) != 4 {
		t.Fatalf("preview=%+v warnings=%v", placements, warnings)
	}
	dedicated := 0
	for _, p := range placements {
		if p.After.Dedicated {
			dedicated++
			if p.Proposed.VUsers != 100 {
				t.Fatal("partial dedicated spare")
			}
		}
	}
	if dedicated != 2 {
		t.Fatalf("dedicated spare count=%d", dedicated)
	}
	test.Assignment.RecoverySpareVUsers = 199
	if _, warnings = tr.previewPodPlacementLocked(test); len(warnings) == 0 {
		t.Fatal("preview did not report insufficient spare envelope")
	}
	test.Assignment.RecoverySpareVUsers = 200
	tr.settings.CapacityPlanning.PodReservationsEnabled = false
	if _, warnings = tr.previewPodPlacementLocked(test); len(warnings) == 0 {
		t.Fatal("preview ignored disabled reservations")
	}
}

func TestRecoveryStandbyIdentityRequiresCurrentKubernetesProof(t *testing.T) {
	for _, mode := range []string{"valid", "legacy proof", "pod", "pod name", "node", "workload", "stale", "terminating", "ambiguous"} {
		t.Run(mode, func(t *testing.T) {
			tr, test, claims, loads := recoveryAdmissionFixture()
			got, err := tr.addRecoverySpareClaims(test, claims, loads)
			if err != nil {
				t.Fatal(err)
			}
			spare := got[2]
			switch mode {
			case "legacy proof":
				spare.RecoveryPlacement.PodName = ""
			case "pod":
				tr.liveInventory[2].PodPlacement.UID = "new"
			case "pod name":
				tr.liveInventory[2].PodPlacement.Name = "changed-3"
			case "node":
				tr.liveInventory[2].PodPlacement.NodeName = "new"
			case "workload":
				tr.liveInventory[2].PodPlacement.scaleLimit.WorkloadUID = "new"
			case "stale":
				tr.liveInventoryAt = time.Now().Add(-time.Hour)
			case "terminating":
				tr.liveInventory[2].PodPlacement.Terminating = true
			case "ambiguous":
				tr.liveInventory = append(tr.liveInventory, tr.liveInventory[2])
			}
			if tr.recoveryStandbyIdentityCurrent(spare) != (mode == "valid" || mode == "legacy proof") {
				t.Fatal("accepted changed identity or rejected current proof")
			}
		})
	}
}

func TestRecoveryBoundPeakCannotExpandWithoutNewAdmission(t *testing.T) {
	tr, store, claim, owner, generation := seedCapacityAckClaim(t)
	claim.Target = "bound"
	claim.Incarnation = "bound-incarnation"
	claim.RecoveryPlacement = &storage.PodCapacityRecoveryPlacement{Namespace: "ns", WorkloadUID: "workload", PodUID: "bound-pod", NodeName: "worker"}
	if err := store.ReservePodCapacity([]storage.PodCapacityReservation{claim}, executionLeaseName, owner, generation); err != nil {
		t.Fatal(err)
	}
	before, _ := store.ListPodCapacityReservations()
	if _, err := tr.expandPodScale(context.Background(), claim.TestID, "g", []GeneratorShardSummary{{Target: claim.Target, VUsers: 10}}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.expandPodScale(context.Background(), claim.TestID, "g", []GeneratorShardSummary{{Target: claim.Target, VUsers: 20}}, nil); err == nil {
		t.Fatal("expanded outside frozen recovery guarantee")
	}
	after, _ := store.ListPodCapacityReservations()
	if !reflect.DeepEqual(before, after) {
		t.Fatal("rejected expansion changed ledger")
	}
}

func TestRecoveryBoundEvacuationRequiresFrozenReplacement(t *testing.T) {
	for _, mode := range []string{"activate", "different source", "same worker", "different workload", "replaced pod", "stale inventory"} {
		t.Run(mode, func(t *testing.T) {
			tr, store, source, owner, generation := seedCapacityAckClaim(t)
			settings := tr.GetSystemSettings()
			settings.CapacityPlanning.PodReservationsEnabled = true
			tr.settings = settings
			source.Target = "bound-source"
			source.Incarnation = "bound-source-incarnation"
			source.RecoveryPlacement = &storage.PodCapacityRecoveryPlacement{Namespace: "ns", WorkloadUID: "workload", PodUID: "source-pod", NodeName: "worker-1"}
			standby, _ := standbyEvacuationFixture()
			standby.Generation = generation
			standby.RecoveryPlacement = &storage.PodCapacityRecoveryPlacement{Namespace: "ns", WorkloadUID: "workload", PodUID: "spare-pod", NodeName: "worker-2", ReplacementSources: []string{source.Target}}
			fixture, _, _, _ := recoveryAdmissionFixture()
			entry := fixture.liveInventory[2]
			entry.Target = standby.Target
			entry.PodCapacity.Incarnation = standby.Incarnation
			entry.PodPlacement.UID = "spare-pod"
			entry.PodPlacement.Namespace = "ns"
			entry.PodPlacement.scaleLimit.Namespace = "ns"
			tr.liveInventory, tr.liveInventoryAt = []GeneratorInventoryEntry{entry}, time.Now()
			switch mode {
			case "different source":
				standby.RecoveryPlacement.ReplacementSources = []string{"other-source"}
			case "same worker":
				standby.RecoveryPlacement.NodeName = source.RecoveryPlacement.NodeName
			case "different workload":
				standby.RecoveryPlacement.WorkloadUID = "other-workload"
			case "replaced pod":
				entry.PodPlacement.UID = "new-pod"
			case "stale inventory":
				tr.liveInventoryAt = time.Now().Add(-time.Hour)
			}
			if err := store.ReservePodCapacity([]storage.PodCapacityReservation{source, standby}, executionLeaseName, owner, generation); err != nil {
				t.Fatal(err)
			}
			for _, claim := range []storage.PodCapacityReservation{source, standby} {
				if err := store.TransitionPodCapacity(claim.Target, claim.TestID, claim.Incarnation, claim.ReservationID, "committed", executionLeaseName, owner, generation); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := store.ListPodCapacityReservations()
			client := standbyCapacityWireClient(t, standby)
			b := newDistributedExecutionBackend([]string{standby.Target}, time.Second, func(context.Context, string) (generatorClient, error) { return client, nil })
			claims, err := tr.reservePodEvacuation(context.Background(), b, source.TestID, source.Target, map[string][]GeneratorShardSummary{"g": {{Target: standby.Target, VUsers: 10}}}, map[string]generatorClient{standby.Target: client}, false, map[string]storage.PodCapacityReservation{standby.Target: standby})
			if (err == nil) != (mode == "activate") {
				t.Fatalf("claims=%+v err=%v", claims, err)
			}
			if err != nil {
				after, _ := store.ListPodCapacityReservations()
				if !reflect.DeepEqual(before, after) {
					t.Fatal("rejected topology changed durable reservations")
				}
			} else if len(claims) != 1 || !reflect.DeepEqual(claims[0].RecoveryPlacement, standby.RecoveryPlacement) {
				t.Fatal("activation lost frozen proof")
			}
		})
	}
}
