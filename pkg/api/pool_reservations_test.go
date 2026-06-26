package api

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

// A pool-scoped reservation protects capacity within a fleet pool's member
// envelope: a non-reserving project cannot consume the slice reserved for another
// project on that pool, while the reserving project can.
func TestPoolReservedCapacityProtectsPool(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 50, Addr: ":8080"})
	tr := server.runner

	// Fleet pool with a 4-generator envelope.
	if err := store.SaveFleetPool(&storage.FleetPool{ID: "pool-r", Name: "Reserved Pool", Status: "active"}); err != nil {
		t.Fatalf("SaveFleetPool returned error: %v", err)
	}
	members := []*storage.FleetPoolMember{}
	for i, target := range []string{"gen-1:9090", "gen-2:9090", "gen-3:9090", "gen-4:9090"} {
		members = append(members, &storage.FleetPoolMember{ID: "m-" + target, PoolID: "pool-r", GeneratorID: fmt.Sprintf("gen-%d", i+1), Target: target, Status: "ready"})
	}
	if err := store.SaveFleetPoolMembers("pool-r", members); err != nil {
		t.Fatalf("SaveFleetPoolMembers returned error: %v", err)
	}

	// Project A reserves 3 of the pool's 4 generators (shared remainder = 1).
	settings := tr.GetSystemSettings()
	settings.CapacityPlanning.Reservations = []CapacityReservation{{ProjectID: "proj-a", ReservedGenerators: 3, PoolID: "pool-r"}}
	if _, err := tr.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings returned error: %v", err)
	}

	poolTest := func(id, project string, gens int) *ManagedTest {
		return &ManagedTest{
			ID: id, ProjectID: project, Status: "QUEUED", TotalVUsers: gens * 10,
			Assignment:       &GeneratorAssignmentSettings{Mode: "specific_hosts", PoolID: "pool-r"},
			CapacityAdvisory: &CapacityAdvisory{RecommendedGenerators: gens},
		}
	}
	eval := func(test *ManagedTest) (bool, string) {
		tr.mu.RLock()
		defer tr.mu.RUnlock()
		return tr.evaluateQuotaBlockLocked(test, "")
	}

	// Non-reserving project B asking for 2 generators would eat into A's reserved 3
	// (only 1 shared remains) -> blocked.
	if blocked, reason := eval(poolTest("b2", "proj-b", 2)); !blocked || !strings.Contains(reason, "shared generator capacity on pool") {
		t.Fatalf("B/2gen = (%v, %q), want blocked on pool shared capacity", blocked, reason)
	}
	// B asking for just 1 generator fits the shared remainder -> admitted.
	if blocked, reason := eval(poolTest("b1", "proj-b", 1)); blocked {
		t.Fatalf("B/1gen should fit the shared remainder, got %q", reason)
	}
	// Reserving project A can use its full reservation (3) -> admitted.
	if blocked, reason := eval(poolTest("a3", "proj-a", 3)); blocked {
		t.Fatalf("A/3gen should be covered by its reservation, got %q", reason)
	}
	// A asking for 5 (> reservation 3 + shared 1 = 4 envelope) -> blocked.
	if blocked, _ := eval(poolTest("a5", "proj-a", 5)); !blocked {
		t.Fatalf("A/5gen exceeds the pool envelope, want blocked")
	}
	// A run on a different pool with no reservation is unaffected.
	other := poolTest("o1", "proj-b", 2)
	other.Assignment.PoolID = "pool-other"
	if blocked, reason := eval(other); blocked {
		t.Fatalf("run on an unreserved pool should not be gated, got %q", reason)
	}
	// A non-pool run is unaffected by pool reservations.
	global := &ManagedTest{ID: "g1", ProjectID: "proj-b", Status: "QUEUED", TotalVUsers: 20, CapacityAdvisory: &CapacityAdvisory{RecommendedGenerators: 2}}
	if blocked, reason := eval(global); blocked {
		t.Fatalf("non-pool run should not be gated by pool reservations, got %q", reason)
	}
}
