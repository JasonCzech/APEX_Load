package api

import (
	"context"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/kubeops"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

func capacityReleaseIdentityFixture() (*storage.PodCapacityRecoveryPlacement, GeneratorInventoryEntry) {
	proof := &storage.PodCapacityRecoveryPlacement{Namespace: "ns", WorkloadUID: "workload", PodUID: "pod-uid", PodName: "generator-1", NodeName: "worker-1"}
	entry := GeneratorInventoryEntry{Target: "10.244.1.4:9090", Status: "connected", PodCapacity: &protocol.CapacitySnapshot{Incarnation: "new-process"}, PodPlacement: &GeneratorPodPlacement{Namespace: "ns", UID: "pod-uid", Name: "generator-1", NodeName: "worker-1", Ready: true, scaleLimit: kubeops.StatefulSetAutoscalingLimit{Namespace: "ns", Workload: "generator", WorkloadUID: "workload", HPAUID: "hpa", ScaledObjectUID: "keda", MaximumReplicas: 4, ObservedAt: time.Now()}}}
	return proof, entry
}

func TestCapacityReleaseObservationRequiresUniqueCurrentIdentity(t *testing.T) {
	for _, mode := range []string{"current", "legacy name", "legacy claim", "reused IP", "missing", "namespace", "limit namespace", "workload", "name", "node", "unready", "terminating", "stale", "disconnected", "no capacity", "no incarnation", "duplicate", "conflicting address evidence", "conflicting UID evidence", "empty proof"} {
		t.Run(mode, func(t *testing.T) {
			proof, entry := capacityReleaseIdentityFixture()
			claim := storage.PodCapacityReservation{Target: "10.244.1.32:9090", Incarnation: "old-process", RecoveryPlacement: proof}
			entries := []GeneratorInventoryEntry{entry}
			switch mode {
			case "legacy name":
				proof.PodName = ""
			case "legacy claim":
				claim.RecoveryPlacement = nil
				claim.Target = entry.Target
			case "reused IP":
				entries[0].Target = claim.Target
				entries[0].PodPlacement.UID = "different-pod"
			case "missing":
				entries = nil
			case "namespace":
				entry.PodPlacement.Namespace = "other"
			case "limit namespace":
				entry.PodPlacement.scaleLimit.Namespace = "other"
			case "workload":
				entry.PodPlacement.scaleLimit.WorkloadUID = "other"
			case "name":
				entry.PodPlacement.Name = "generator-2"
			case "node":
				entry.PodPlacement.NodeName = "worker-2"
			case "unready":
				entry.PodPlacement.Ready = false
			case "terminating":
				entry.PodPlacement.Terminating = true
			case "stale":
				entry.PodPlacement.scaleLimit.ObservedAt = time.Now().Add(-time.Hour)
			case "disconnected":
				entries[0].Status = "disconnected"
			case "no capacity":
				entries[0].PodCapacity = nil
			case "no incarnation":
				entry.PodCapacity.Incarnation = ""
			case "duplicate":
				entries = append(entries, entry)
			case "conflicting address evidence":
				copy := entry
				placement := *entry.PodPlacement
				placement.UID = "other-pod"
				copy.PodPlacement = &placement
				entries = append(entries, copy)
			case "conflicting UID evidence":
				copy := entry
				placement := *entry.PodPlacement
				placement.NodeName = "other"
				copy.PodPlacement = &placement
				entries = append(entries, copy)
			case "empty proof":
				proof.PodUID = ""
			}
			got, ok := newCapacityReleaseInventory(entries).observation(claim)
			want := mode == "current" || mode == "legacy name" || mode == "legacy claim"
			if ok != want {
				t.Fatalf("accepted=%v want=%v", ok, want)
			}
			if ok && got.Target != entry.Target {
				t.Fatal("did not resolve the current address")
			}
		})
	}
}

func TestReconcilePodCapacityFollowsPodIdentityWithoutReleasingLiveClaims(t *testing.T) {
	for _, mode := range []string{"restarted", "same process new address", "wrong acknowledgement", "active run", "replaced pod", "stale", "duplicate"} {
		t.Run(mode, func(t *testing.T) {
			proof, entry := capacityReleaseIdentityFixture()
			tr, store, claim, _, _ := seedCapacityAckClaim(t, proof)
			status := "COMPLETE"
			if mode == "active run" {
				status = "RUNNING"
			}
			now := time.Now()
			if err := store.SaveTest(&storage.TestRecord{ID: claim.TestID, Status: status, StartTime: now, EndTime: &now}); err != nil {
				t.Fatal(err)
			}
			if mode == "same process new address" || mode == "wrong acknowledgement" {
				entry.PodCapacity.Incarnation = claim.Incarnation
			}
			if mode == "replaced pod" {
				entry.PodPlacement.UID = "replacement"
			}
			if mode == "stale" {
				entry.PodPlacement.scaleLimit.ObservedAt = time.Now().Add(-time.Hour)
			}
			entries := []GeneratorInventoryEntry{entry}
			if mode == "duplicate" {
				entries = append(entries, entry)
			}
			calls := 0
			client := &capacityAdoptionClient{adoptionFakeClient: &adoptionFakeClient{}, confirm: func(req *pb.PodCapacityClaim) (*pb.PodCapacityAck, error) {
				calls++
				if !req.Release || req.ReservationId != claim.ReservationID || req.Incarnation != claim.Incarnation || req.ExecutionGeneration != claim.Generation {
					t.Fatal("release identity changed")
				}
				if mode == "wrong acknowledgement" {
					return capacityAckForMode("wrong identity"), nil
				}
				return capacityAckForMode("accepted"), nil
			}}
			tr.execution = newDistributedExecutionBackend([]string{entry.Target}, time.Second, func(_ context.Context, target string) (generatorClient, error) {
				if target != entry.Target {
					t.Fatalf("dialed retired address %q", target)
				}
				return client, nil
			})
			tr.reconcilePodCapacity(context.Background(), entries)
			rows, err := store.ListPodCapacityReservations()
			if err != nil {
				t.Fatal(err)
			}
			want := 1
			if mode == "restarted" || mode == "same process new address" {
				want = 0
			}
			if len(rows) != want {
				t.Fatalf("retained %d claims, want %d", len(rows), want)
			}
			wantCalls := 0
			if mode == "same process new address" || mode == "wrong acknowledgement" {
				wantCalls = 1
			}
			if calls != wantCalls {
				t.Fatalf("release RPCs %d, want %d", calls, wantCalls)
			}
		})
	}
}

func TestPendingQueueMetadataPreservesPeakCapacityFailure(t *testing.T) {
	for _, reason := range []string{"waiting for peak pod capacity: no compatible spare", "pending test promotion requires fresh identity", "waiting for capacity", ""} {
		t.Run(reason, func(t *testing.T) {
			run := &ManagedTest{ID: "pending", ProjectID: "p", Status: "PENDING", CreatedAt: time.Now(), TotalVUsers: 1, PendingReason: reason}
			tr := &TestRunner{settings: defaultSystemSettings(4), tests: map[string]*ManagedTest{run.ID: run}}
			tr.refreshPendingQueueMetadataWithTimeslotsLocked(time.Now(), nil, nil, map[*ManagedTest]projectQuotaEvidence{run: {projectID: "p", loaded: true}})
			expected := reason
			if reason == "waiting for capacity" || reason == "" {
				expected = "ready to start when the queue advances"
			}
			if run.PendingReason != expected {
				t.Fatalf("reason %q, want %q", run.PendingReason, expected)
			}
			if run.QueuePosition != 1 {
				t.Fatal("queue position not updated")
			}
		})
	}
}
