package api

import (
	"context"
	"fmt"
	"testing"
	"time"
)

type inventoryPromotionBackend struct {
	executionBackendSpy
	entries []GeneratorInventoryEntry
}

func (b *inventoryPromotionBackend) SystemInventory(context.Context) []GeneratorInventoryEntry {
	return cloneGeneratorInventoryEntries(b.entries)
}

func waitForInventoryPromotionIdle(t *testing.T, runner *TestRunner) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		runner.mu.RLock()
		active := runner.pendingPromotionRetryActive
		runner.mu.RUnlock()
		if !active {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("inventory promotion worker did not finish")
}

func TestInventoryRefreshPromotesCapacityWaiterWhenFleetGrows(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2})
	runner := server.runner
	storePreflightScript(runner, "inventory-wakeup-script", "waiting-project")
	pending, err := runner.CreateTest(CreateTestRequest{Name: "capacity waiter", ProjectID: "waiting-project", ScriptID: "inventory-wakeup-script", VUsers: 1, Duration: "1m"})
	if err != nil {
		t.Fatal(err)
	}
	backend := &inventoryPromotionBackend{executionBackendSpy: executionBackendSpy{called: make(chan string, 2)}}
	for i := 0; i < 2; i++ {
		backend.entries = append(backend.entries, GeneratorInventoryEntry{Target: fmt.Sprintf("gen-%d:9090", i), GeneratorID: fmt.Sprintf("gen-%d", i), Status: "connected", DrainState: "active", CompatibilityState: "compatible", AvailableCapacity: 100, MaxVUsers: 100})
	}
	both := backend.entries
	backend.entries = both[:1]
	runner.setExecutionBackend(backend)
	runner.mu.Lock()
	runner.settings.CapacityPlanning.Availability.Source = capacityAvailabilityConfiguredPlusLive
	runner.settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators = 2
	runner.settings.CapacityPlanning.Reservations = []CapacityReservation{{ProjectID: "reserved-project", ReservedGenerators: 1}}
	run := runner.tests[pending.ID]
	run.Status = "PENDING"
	since := time.Now().UTC()
	run.PendingSince = &since
	runner.mu.Unlock()
	server.refreshGeneratorInventory(context.Background())
	waitForInventoryPromotionIdle(t, runner)
	select {
	case <-backend.called:
		t.Fatal("admitted without shared capacity")
	default:
	}
	backend.entries = both
	// A read-only inventory request must not launch work. The background refresh
	// is the queue wake-up boundary.
	server.systemInventory(context.Background())
	select {
	case <-backend.called:
		t.Fatal("inventory read launched work")
	case <-time.After(100 * time.Millisecond):
	}
	server.refreshGeneratorInventory(context.Background())
	select {
	case id := <-backend.called:
		if id != pending.ID {
			t.Fatalf("started %q", id)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("new fleet capacity did not wake the pending run")
	}
	waitForInventoryPromotionIdle(t, runner)
	server.refreshGeneratorInventory(context.Background())
	waitForInventoryPromotionIdle(t, runner)
	select {
	case <-backend.called:
		t.Fatal("refresh launched the same run twice")
	default:
	}
}

func TestInventoryRefreshDoesNotPromoteUnrequestedOrUnownedWork(t *testing.T) {
	for _, mode := range []string{"queued", "follower", "canceled probe", "shutdown"} {
		t.Run(mode, func(t *testing.T) {
			server := NewServer(ServerConfig{MaxConcurrent: 2})
			runner := server.runner
			storePreflightScript(runner, "inventory-gate-script", "waiting-project")
			pending, err := runner.CreateTest(CreateTestRequest{Name: "gated waiter", ProjectID: "waiting-project", ScriptID: "inventory-gate-script", VUsers: 1, Duration: "1m"})
			if err != nil {
				t.Fatal(err)
			}
			backend := &inventoryPromotionBackend{executionBackendSpy: executionBackendSpy{called: make(chan string, 1)}}
			runner.setExecutionBackend(backend)
			runner.mu.Lock()
			run := runner.tests[pending.ID]
			run.Status = "PENDING"
			since := time.Now().UTC()
			run.PendingSince = &since
			if mode == "queued" {
				run.Status = "QUEUED"
				run.PendingSince = nil
			}
			if mode == "follower" {
				runner.leadership = newExecutionLeadership(&fakeLeaseStore{dfltOK: false}, "follower")
			}
			if mode == "shutdown" {
				runner.shuttingDown = true
			}
			want := run.Status
			runner.mu.Unlock()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "canceled probe" {
				cancel()
			}
			server.refreshGeneratorInventory(ctx)
			waitForInventoryPromotionIdle(t, runner)
			select {
			case <-backend.called:
				t.Fatal("inventory refresh started gated work")
			default:
			}
			runner.mu.RLock()
			status := runner.tests[pending.ID].Status
			active := runner.pendingPromotionRetryActive
			runner.mu.RUnlock()
			if status != want || active {
				t.Fatalf("status=%s retry active=%v", status, active)
			}
		})
	}
}
