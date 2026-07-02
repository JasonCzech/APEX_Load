package api

import (
	"context"
	"testing"
	"time"
)

// reservationRunner builds a runner with a fixed generator inventory and
// reservations, plus any pre-existing active tests.
func reservationRunner(t *testing.T, totalGenerators int, reservations []CapacityReservation, active map[string]*ManagedTest) *TestRunner {
	t.Helper()
	runner := NewTestRunner(100, NewHub(), nil)
	runner.settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators = totalGenerators
	runner.settings.CapacityPlanning.Reservations = reservations
	for id, test := range active {
		test.ID = id
		runner.tests[id] = test
	}
	return runner
}

func activeTest(projectID string, generators int) *ManagedTest {
	return &ManagedTest{
		ProjectID:        projectID,
		Status:           "RUNNING",
		CapacityAdvisory: &CapacityAdvisory{RecommendedGenerators: generators},
	}
}

func TestReservedCapacityBlocksNonReservingProjectFromSharedOverflow(t *testing.T) {
	// 10 generators, 4 reserved for project-a -> shared pool = 6.
	runner := reservationRunner(t, 10, []CapacityReservation{{ProjectID: "project-a", ReservedGenerators: 4}}, map[string]*ManagedTest{
		"b-running": activeTest("project-b", 6), // project-b already fills the shared pool
	})

	// project-b wants one more generator -> would push shared to 7 > 6 -> blocked.
	blocked, reason := runner.evaluateReservedCapacityBlockLocked(activeTest("project-b", 1), "")
	if !blocked {
		t.Fatalf("expected block when shared pool is full, got allowed")
	}
	if reason == "" {
		t.Fatalf("expected a reason")
	}
}

func TestReservedCapacityProtectedForOwner(t *testing.T) {
	// Shared pool full (project-b uses all 6), but project-a's 4 reserved are
	// still available to project-a.
	runner := reservationRunner(t, 10, []CapacityReservation{{ProjectID: "project-a", ReservedGenerators: 4}}, map[string]*ManagedTest{
		"b-running": activeTest("project-b", 6),
	})
	blocked, _ := runner.evaluateReservedCapacityBlockLocked(activeTest("project-a", 4), "")
	if blocked {
		t.Fatalf("project-a must be able to use its reserved capacity even when shared is full")
	}
}

func TestReservedCapacityOwnerBeyondReservationDrawsShared(t *testing.T) {
	// project-a already uses its 4 reserved; a 5th generator must come from the
	// shared pool, which project-b has exhausted -> blocked.
	runner := reservationRunner(t, 10, []CapacityReservation{{ProjectID: "project-a", ReservedGenerators: 4}}, map[string]*ManagedTest{
		"a-running": activeTest("project-a", 4),
		"b-running": activeTest("project-b", 6),
	})
	blocked, _ := runner.evaluateReservedCapacityBlockLocked(activeTest("project-a", 2), "")
	if !blocked {
		t.Fatalf("owner exceeding its reservation should be bounded by the shared pool")
	}
}

func TestPoolReservationCreditsGlobalAdmissionForOwner(t *testing.T) {
	runner := reservationRunner(t, 10, []CapacityReservation{
		{ProjectID: "project-c", ReservedGenerators: 6},
		{ProjectID: "project-a", PoolID: "pool-a", ReservedGenerators: 2},
	}, map[string]*ManagedTest{
		"b-running": activeTest("project-b", 4),
	})

	blocked, reason := runner.evaluateReservedCapacityBlockLocked(activeTest("project-a", 2), "")
	if blocked {
		t.Fatalf("project-a should be admitted against its pool reservation global credit; blocked: %s", reason)
	}
}

func TestReservedCapacityNoReservationsNeverBlocks(t *testing.T) {
	runner := reservationRunner(t, 10, nil, map[string]*ManagedTest{
		"b-running": activeTest("project-b", 50),
	})
	if blocked, _ := runner.evaluateReservedCapacityBlockLocked(activeTest("project-b", 50), ""); blocked {
		t.Fatalf("with no reservations configured, admission must not block on reservations")
	}
}

func TestSharedGeneratorUsageExcludesOwnReservationAndTest(t *testing.T) {
	runner := reservationRunner(t, 10, []CapacityReservation{{ProjectID: "project-a", ReservedGenerators: 4}}, map[string]*ManagedTest{
		"a-running": activeTest("project-a", 5), // 5 active, 4 reserved -> 1 shared
		"b-running": activeTest("project-b", 3), // 3 active, 0 reserved -> 3 shared
	})
	if got := runner.sharedGeneratorUsageLocked("", time.Now(), nil); got != 4 {
		t.Fatalf("shared usage = %d, want 4 (1 from a above reservation + 3 from b)", got)
	}
	// Excluding b-running drops its 3 shared.
	if got := runner.sharedGeneratorUsageLocked("b-running", time.Now(), nil); got != 1 {
		t.Fatalf("shared usage excluding b = %d, want 1", got)
	}
}

type countingInventoryBackend struct {
	calls int
}

func (b *countingInventoryBackend) Run(*TestRunner, *ManagedTest) {}

func (b *countingInventoryBackend) SystemInventory(context.Context) []GeneratorInventoryEntry {
	b.calls++
	return []GeneratorInventoryEntry{{
		Target:            "live-should-not-be-called:9090",
		Status:            "connected",
		DrainState:        "active",
		AvailableCapacity: 100,
	}}
}

func TestReservedCapacityUsesCachedLiveInventoryForAdmission(t *testing.T) {
	runner := reservationRunner(t, 0, []CapacityReservation{{ProjectID: "project-a", ReservedGenerators: 1}}, nil)
	runner.settings.CapacityPlanning.Availability.Source = capacityAvailabilityConfiguredPlusLive
	backend := &countingInventoryBackend{}
	runner.setExecutionBackend(backend)
	runner.setCachedGeneratorInventory([]GeneratorInventoryEntry{{
		Target:            "gen-a:9090",
		Status:            "connected",
		DrainState:        "active",
		AvailableCapacity: 100,
	}}, time.Now())

	blocked, reason := runner.evaluateReservedCapacityBlockLocked(activeTest("project-a", 1), "")
	if blocked {
		t.Fatalf("owner should be admitted using cached live inventory; blocked: %s", reason)
	}
	if backend.calls != 0 {
		t.Fatalf("SystemInventory called %d time(s); admission must use cached snapshot", backend.calls)
	}
}
