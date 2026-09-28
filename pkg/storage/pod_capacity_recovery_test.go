package storage

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestPodCapacityRecoveryPlacementIsFrozen(t *testing.T) {
	checkPodCapacityRecoveryPlacementIsFrozen(t, newAtomicBindingStore(t))
}

func checkPodCapacityRecoveryPlacementIsFrozen(t *testing.T, s *SQLiteStore) {
	t.Helper()
	for index, mode := range []string{"activation", "drop proof", "namespace", "workload", "pod", "pod name", "node", "sources"} {
		t.Run(mode, func(t *testing.T) {
			name := fmt.Sprintf("recovery-proof-%d", index)
			ok, lease, err := s.TryAcquireSchedulerLeaseTerm(name, "controller", time.Now(), time.Minute)
			if !ok || err != nil {
				t.Fatal(err)
			}
			budget := protocol.CapacityVector{CPUCores: 4, MemoryMB: 1000, NetworkMbps: 100, VUsers: 100}
			claim := PodCapacityReservation{ReservationID: "attempt", Revision: 1, Target: name + "-spare", TestID: name, Incarnation: name + "-incarnation", Generation: lease.Generation, State: "pending", ModelVersion: protocol.CapacityReservationModel, Budget: budget, Demand: budget, Dedicated: true, RecoveryPlacement: &PodCapacityRecoveryPlacement{Namespace: "test", WorkloadUID: "workload", PodUID: "pod", PodName: "generator-2", NodeName: "worker2", ReplacementSources: []string{"source"}}}
			if err := s.ReservePodCapacity([]PodCapacityReservation{claim}, name, "controller", lease.Generation); err != nil {
				t.Fatal(err)
			}
			before, err := s.ListPodCapacityReservations()
			if err != nil {
				t.Fatal(err)
			}
			next := claim
			proof := *claim.RecoveryPlacement
			next.RecoveryPlacement = &proof
			next.Revision++
			next.GroupPeaks = map[string]int32{"g": 10}
			next.GroupDemands = map[string]protocol.CapacityVector{"g": budget.Scale(.1)}
			switch mode {
			case "drop proof":
				next.RecoveryPlacement = nil
			case "namespace":
				proof.Namespace = "other"
			case "workload":
				proof.WorkloadUID = "other"
			case "pod":
				proof.PodUID = "other"
			case "pod name":
				proof.PodName = "generator-3"
			case "node":
				proof.NodeName = "other"
			case "sources":
				proof.ReplacementSources = []string{"other"}
			}
			err = s.ReservePodCapacityIfUnchanged([]PodCapacityReservation{next}, []PodCapacityReservation{claim}, name, "controller", lease.Generation)
			if (err == nil) != (mode == "activation") {
				t.Fatalf("replacement err=%v", err)
			}
			after, err := s.ListPodCapacityReservations()
			if err != nil {
				t.Fatal(err)
			}
			if mode != "activation" && !reflect.DeepEqual(before, after) {
				t.Fatal("failed replacement changed proof")
			}
			if mode == "activation" {
				found := false
				for _, row := range after {
					if row.TestID == name {
						found = true
						if !reflect.DeepEqual(row.RecoveryPlacement, claim.RecoveryPlacement) {
							t.Fatal("activation lost identity proof")
						}
					}
				}
				if !found {
					t.Fatal("activated row missing")
				}
			}
		})
	}
}
func TestPodCapacityRecoveryPlacementValidation(t *testing.T) {
	for _, mode := range []string{"valid", "legacy", "empty", "space", "control", "too long", "duplicate", "self", "empty source", "too many", "shared spare"} {
		t.Run(mode, func(t *testing.T) {
			c := PodCapacityReservation{Target: "spare", Dedicated: true, RecoveryPlacement: &PodCapacityRecoveryPlacement{Namespace: "ns", WorkloadUID: "workload", PodUID: "pod", NodeName: "node", ReplacementSources: []string{"source"}}}
			switch mode {
			case "legacy":
				c.RecoveryPlacement = nil
			case "empty":
				c.RecoveryPlacement.PodUID = ""
			case "space":
				c.RecoveryPlacement.NodeName = "node "
			case "control":
				c.RecoveryPlacement.Namespace = "ns\nother"
			case "too long":
				c.RecoveryPlacement.WorkloadUID = string(make([]byte, 513))
			case "duplicate":
				c.RecoveryPlacement.ReplacementSources = []string{"source", "source"}
			case "self":
				c.RecoveryPlacement.ReplacementSources = []string{c.Target}
			case "empty source":
				c.RecoveryPlacement.ReplacementSources = []string{""}
			case "too many":
				c.RecoveryPlacement.ReplacementSources = make([]string, 257)
			case "shared spare":
				c.Dedicated = false
			}
			err := validatePodCapacityRecoveryPlacement(c)
			if (err == nil) != (mode == "valid" || mode == "legacy") {
				t.Fatalf("validation=%v", err)
			}
		})
	}
}

func TestRecoveryPodNameValidationAndLegacyCompatibility(t *testing.T) {
	for _, name := range []string{"", "generator-2", "bad/name", "bad\\name", "host:9090", "has space", "tab\tname", strings.Repeat("a", 254)} {
		c := PodCapacityReservation{Target: "spare", RecoveryPlacement: &PodCapacityRecoveryPlacement{Namespace: "ns", WorkloadUID: "workload", PodUID: "pod", NodeName: "node", PodName: name}}
		err := validatePodCapacityRecoveryPlacement(c)
		if (err == nil) != (name == "" || name == "generator-2") {
			t.Fatalf("name %q: %v", name, err)
		}
	}
}
