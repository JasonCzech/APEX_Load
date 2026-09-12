package protocol

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestPodCapacitySnapshotDoesNotDiscloseTestIDs(t *testing.T) {
	snapshot := CapacitySnapshot{ReportedTestIDs: []string{"other-project-test"}}
	copy := snapshot.Clone()
	copy.ReportedTestIDs[0] = "changed"
	if snapshot.ReportedTestIDs[0] != "other-project-test" {
		t.Fatal("clone shared internal test identities")
	}
	raw, err := json.Marshal(snapshot)
	if err != nil || strings.Contains(string(raw), "other-project-test") {
		t.Fatalf("aggregate snapshot disclosed test identity: %s %v", raw, err)
	}
}

func TestCapacityHeadroomAndIndependentDimensions(t *testing.T) {
	raw := CapacityVector{CPUCores: 8, MemoryMB: 16000, NetworkMbps: 1000, VUsers: 10000, Protocols: map[string]float64{"soap": 5000}}
	budget := raw.Scale(.8)
	used := raw.Scale(.4)
	s := NewCapacitySnapshot(budget, used, 20)
	if s.ReservedPct != 50 || s.RemainingPct != 50 {
		t.Fatalf("snapshot = %+v", s)
	}
	if err := budget.Fits(used.Add(used)); err != nil {
		t.Fatal(err)
	}
	for _, d := range []CapacityVector{{MemoryMB: 12801}, {Protocols: map[string]float64{"soap": 4001}}, {Protocols: map[string]float64{"browser": 1}}, {CPUCores: math.NaN()}, {VUsers: -1}} {
		if budget.Fits(d) == nil {
			t.Fatalf("accepted invalid/excess demand: %+v", d)
		}
	}
	if raw.Protocols["soap"] != 5000 {
		t.Fatal("mutated source vector")
	}
}
