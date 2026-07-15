package api

import (
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestSystemSettingsUpdateRejectsApprovedTimeslotOverbooking(t *testing.T) {
	t.Run("global envelope", func(t *testing.T) {
		server, _ := newTimeslotServer(t)
		start := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
		end := start.Add(2 * time.Hour)
		settings := server.runner.GetSystemSettings()
		settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators = 10
		if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
			t.Fatalf("save base settings: %v", err)
		}
		id := createCapacityTimeslotForApproval(t, server, start, end, 6, "")
		if rec := approveCapacityTimeslot(t, server, id); rec.Code != 200 {
			t.Fatalf("approve timeslot = %d body=%s", rec.Code, rec.Body.String())
		}

		settings = server.runner.GetSystemSettings()
		settings.CapacityPlanning.Reservations = []CapacityReservation{{
			ProjectID: "static-global", ReservedGenerators: 5,
			StartsAt: ptr(start), EndsAt: ptr(end),
		}}
		if _, err := server.runner.UpdateSystemSettings(settings); err == nil || !strings.Contains(err.Error(), "capacity_planning update rejected") {
			t.Fatalf("overbooking settings update error = %v, want capacity rejection", err)
		}
	})

	t.Run("pool envelope and unrelated pool", func(t *testing.T) {
		server, store := newTimeslotServer(t)
		seedFleetPool(t, store, "pool-target", 10)
		seedFleetPool(t, store, "pool-other", 1)
		start := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
		end := start.Add(2 * time.Hour)
		id := createCapacityTimeslotForApproval(t, server, start, end, 6, "pool-target")
		if rec := approveCapacityTimeslot(t, server, id); rec.Code != 200 {
			t.Fatalf("approve pool timeslot = %d body=%s", rec.Code, rec.Body.String())
		}

		settings := server.runner.GetSystemSettings()
		settings.CapacityPlanning.Reservations = []CapacityReservation{{
			ProjectID: "static-target", ReservedGenerators: 5, PoolID: "pool-target",
			StartsAt: ptr(start), EndsAt: ptr(end),
		}}
		if _, err := server.runner.UpdateSystemSettings(settings); err == nil || !strings.Contains(err.Error(), `fleet pool "pool-target"`) {
			t.Fatalf("target-pool overbooking error = %v, want pool capacity rejection", err)
		}

		settings = server.runner.GetSystemSettings()
		settings.CapacityPlanning.Reservations = []CapacityReservation{{
			ProjectID: "static-other", ReservedGenerators: 1, PoolID: "pool-other",
			StartsAt: ptr(start), EndsAt: ptr(end),
		}}
		if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
			t.Fatalf("unrelated pool reservation should remain isolated: %v", err)
		}
	})
}

func TestCapacityApprovalRefreshesStaleHASettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "capacity-ha.db")
	writerStore, err := storage.NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("open writer store: %v", err)
	}
	defer writerStore.Close()
	writer := NewServer(ServerConfig{Store: writerStore, MaxConcurrent: 2})
	base := writer.runner.GetSystemSettings()
	base.CapacityPlanning.Availability.ConfiguredAvailableGenerators = 10
	if _, err := writer.runner.UpdateSystemSettings(base); err != nil {
		t.Fatalf("save base settings: %v", err)
	}

	staleStore, err := storage.NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("open stale store: %v", err)
	}
	defer staleStore.Close()
	stale := NewServer(ServerConfig{Store: staleStore, MaxConcurrent: 2})
	// Warm the read throttle while the replica still has the base snapshot.
	if got := stale.runner.GetSystemSettings(); len(got.CapacityPlanning.Reservations) != 0 {
		t.Fatalf("stale replica unexpectedly started with reservations: %+v", got.CapacityPlanning.Reservations)
	}

	start := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)
	end := start.Add(2 * time.Hour)
	gens := 3
	if err := staleStore.SaveCapacityReservation(&storage.CapacityReservation{
		ID: "stale-approval", ProjectID: "proj-a", StartAt: start, EndAt: end,
		ApprovalState: "pending", RequestedCapacity: &storage.CapacityReservationRequest{Generators: &gens},
	}); err != nil {
		t.Fatalf("save pending timeslot: %v", err)
	}
	updated := writer.runner.GetSystemSettings()
	updated.CapacityPlanning.Reservations = []CapacityReservation{{
		ProjectID: "writer-static", ReservedGenerators: 8, StartsAt: ptr(start), EndsAt: ptr(end),
	}}
	if _, err := writer.runner.UpdateSystemSettings(updated); err != nil {
		t.Fatalf("writer settings update: %v", err)
	}

	stale.runner.mu.RLock()
	cachedReservations := len(stale.runner.settings.CapacityPlanning.Reservations)
	stale.runner.mu.RUnlock()
	if cachedReservations != 0 {
		t.Fatalf("test requires a stale cache before approval, got %d reservations", cachedReservations)
	}
	if rec := approveCapacityTimeslot(t, stale, "stale-approval"); rec.Code != 409 {
		t.Fatalf("stale-replica approval = %d body=%s, want 409", rec.Code, rec.Body.String())
	}
	stale.runner.mu.RLock()
	refreshedReservations := len(stale.runner.settings.CapacityPlanning.Reservations)
	stale.runner.mu.RUnlock()
	if refreshedReservations != 1 {
		t.Fatalf("approval did not force-refresh stale settings; cached reservations = %d", refreshedReservations)
	}
}

func TestConcurrentSettingsAndTimeslotApprovalCannotBothCommit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "capacity-concurrent.db")
	approvalStore, err := storage.NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("open approval store: %v", err)
	}
	defer approvalStore.Close()
	approvalServer := NewServer(ServerConfig{Store: approvalStore, MaxConcurrent: 2})
	base := approvalServer.runner.GetSystemSettings()
	base.CapacityPlanning.Availability.ConfiguredAvailableGenerators = 10
	if _, err := approvalServer.runner.UpdateSystemSettings(base); err != nil {
		t.Fatalf("save base settings: %v", err)
	}

	settingsStore, err := storage.NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("open settings store: %v", err)
	}
	defer settingsStore.Close()
	settingsServer := NewServer(ServerConfig{Store: settingsStore, MaxConcurrent: 2})
	start := time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)
	end := start.Add(2 * time.Hour)
	gens := 6
	if err := approvalStore.SaveCapacityReservation(&storage.CapacityReservation{
		ID: "concurrent-approval", ProjectID: "proj-a", StartAt: start, EndAt: end,
		ApprovalState: "pending", RequestedCapacity: &storage.CapacityReservationRequest{Generators: &gens},
	}); err != nil {
		t.Fatalf("save pending timeslot: %v", err)
	}
	next := settingsServer.runner.GetSystemSettings()
	next.CapacityPlanning.Reservations = []CapacityReservation{{
		ProjectID: "concurrent-static", ReservedGenerators: 6, StartsAt: ptr(start), EndsAt: ptr(end),
	}}

	startWrites := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	approvalResult := make(chan int, 1)
	settingsResult := make(chan error, 1)
	go func() {
		defer wg.Done()
		<-startWrites
		approvalResult <- approveCapacityTimeslot(t, approvalServer, "concurrent-approval").Code
	}()
	go func() {
		defer wg.Done()
		<-startWrites
		_, err := settingsServer.runner.UpdateSystemSettings(next)
		settingsResult <- err
	}()
	close(startWrites)
	wg.Wait()
	approvalCode := <-approvalResult
	settingsErr := <-settingsResult
	approvalCommitted := approvalCode == 200
	settingsCommitted := settingsErr == nil
	if approvalCommitted == settingsCommitted {
		t.Fatalf("approval code=%d settings err=%v; exactly one conflicting write must commit", approvalCode, settingsErr)
	}
	if !approvalCommitted && approvalCode != 409 {
		t.Fatalf("losing approval code = %d, want 409", approvalCode)
	}
	if !settingsCommitted && !strings.Contains(settingsErr.Error(), "capacity_planning update rejected") {
		t.Fatalf("losing settings error = %v, want capacity rejection", settingsErr)
	}

	storedSlot, err := approvalStore.GetCapacityReservation("concurrent-approval")
	if err != nil {
		t.Fatalf("get durable timeslot: %v", err)
	}
	raw, err := approvalStore.GetSetting(systemSettingsStoreKey)
	if err != nil {
		t.Fatalf("get durable settings: %v", err)
	}
	durable, err := systemSettingsFromStoredValue(raw, base)
	if err != nil {
		t.Fatalf("decode durable settings: %v", err)
	}
	if storedSlot.ApprovalState == "approved" && len(durable.CapacityPlanning.Reservations) != 0 {
		t.Fatalf("conflicting promises both persisted: slot=%s reservations=%+v", storedSlot.ApprovalState, durable.CapacityPlanning.Reservations)
	}
}
