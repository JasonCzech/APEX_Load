package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

// seedFleetPool creates a fleet pool with n ready members (envelope = n).
func seedFleetPool(t *testing.T, store storage.Store, poolID string, n int) {
	t.Helper()
	if err := store.SaveFleetPool(&storage.FleetPool{ID: poolID, Name: poolID, Status: "active"}); err != nil {
		t.Fatalf("SaveFleetPool(%s): %v", poolID, err)
	}
	members := make([]*storage.FleetPoolMember, 0, n)
	for i := 0; i < n; i++ {
		target := fmt.Sprintf("%s-gen-%d:9090", poolID, i+1)
		members = append(members, &storage.FleetPoolMember{
			ID:          "m-" + target,
			PoolID:      poolID,
			GeneratorID: fmt.Sprintf("%s-gen-%d", poolID, i+1),
			Target:      target,
			Status:      "ready",
		})
	}
	if err := store.SaveFleetPoolMembers(poolID, members); err != nil {
		t.Fatalf("SaveFleetPoolMembers(%s): %v", poolID, err)
	}
}

// M1: the approval-time overbooking guard must bound a pool-scoped reservation by
// its fleet pool's member envelope (not the global pool) and must not cross-count
// pool vs global reservations.
func TestReservationOverbookingGuardPoolScoped(t *testing.T) {
	server, store := newTimeslotServer(t)
	// Global pool = 8 generators; fleet pool-x envelope = 2 members.
	settings := server.runner.GetSystemSettings()
	settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators = 8
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings: %v", err)
	}
	seedFleetPool(t, store, "pool-x", 2)

	start := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	end := start.Add(2 * time.Hour)
	create := func(name string, gens int, poolID string) string {
		rc := map[string]any{"generators": gens}
		if poolID != "" {
			rc["pool_id"] = poolID
		}
		rec := timeslotRequest(t, server, http.MethodPost, "/api/v1/capacity/timeslots", map[string]any{
			"project_id": "proj-a", "start_at": start, "end_at": end, "timezone": "UTC",
			"requested_capacity": rc,
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %s = %d body=%s", name, rec.Code, rec.Body.String())
		}
		var got storage.CapacityReservation
		_ = json.Unmarshal(rec.Body.Bytes(), &got)
		return got.ID
	}
	approve := func(id string) *httptest.ResponseRecorder {
		result := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			result <- timeslotRequest(t, server, http.MethodPost, "/api/v1/capacity/timeslots/"+id+"/approve", nil)
		}()
		select {
		case rec := <-result:
			return rec
		case <-time.After(2 * time.Second):
			t.Fatal("timeslot approval deadlocked while resolving the pool envelope")
			return nil
		}
	}

	p1 := create("p1", 2, "pool-x") // fills the 2-member envelope exactly
	p2 := create("p2", 2, "pool-x") // would overbook the pool
	g1 := create("g1", 8, "")       // global, fills the global pool exactly

	if rec := approve(p1); rec.Code != http.StatusOK {
		t.Fatalf("approve p1 = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	// 2 + 2 > 2-member envelope -> blocked. (Pre-fix: 4 <= global 8 -> wrongly approved.)
	if rec := approve(p2); rec.Code != http.StatusConflict {
		t.Fatalf("approve overbooking pool slot = %d body=%s, want 409", rec.Code, rec.Body.String())
	}
	// The global slot must not be cross-counted against the approved pool slot:
	// 8 <= global 8 -> approved. (Pre-fix: summed 8 + 2 > 8 -> wrongly blocked.)
	if rec := approve(g1); rec.Code != http.StatusOK {
		t.Fatalf("approve global slot = %d body=%s, want 200 (pool overlap must not count globally)", rec.Code, rec.Body.String())
	}
}

// M2: a timeslot referencing a non-existent fleet pool is rejected at create, so a
// pool-scoped reservation can't be silently inert.
func TestCreateCapacityReservationRejectsUnknownPool(t *testing.T) {
	server, store := newTimeslotServer(t)
	start := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	body := func(poolID string) map[string]any {
		return map[string]any{
			"project_id": "proj-a", "start_at": start, "end_at": end, "timezone": "UTC",
			"requested_capacity": map[string]any{"generators": 1, "pool_id": poolID},
		}
	}
	if rec := timeslotRequest(t, server, http.MethodPost, "/api/v1/capacity/timeslots", body("ghost")); rec.Code != http.StatusBadRequest {
		t.Fatalf("create with unknown pool = %d body=%s, want 400", rec.Code, rec.Body.String())
	}
	seedFleetPool(t, store, "pool-real", 1)
	if rec := timeslotRequest(t, server, http.MethodPost, "/api/v1/capacity/timeslots", body("pool-real")); rec.Code != http.StatusCreated {
		t.Fatalf("create with real pool = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
}

// M2: a system-settings reservation referencing a non-existent fleet pool is
// rejected at write time (but a global reservation and a real-pool one are fine).
func TestUpdateSystemSettingsRejectsUnknownReservationPool(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 50, Addr: ":8080"})
	tr := server.runner

	settings := tr.GetSystemSettings()
	settings.CapacityPlanning.Reservations = []CapacityReservation{{ProjectID: "proj-a", ReservedGenerators: 1, PoolID: "ghost"}}
	if _, err := tr.UpdateSystemSettings(settings); err == nil {
		t.Fatalf("UpdateSystemSettings with unknown pool reservation should error")
	} else if !strings.Contains(err.Error(), "unknown fleet pool") {
		t.Fatalf("error = %q, want it to mention an unknown fleet pool", err.Error())
	}

	seedFleetPool(t, store, "pool-real", 2)
	settings = tr.GetSystemSettings()
	settings.CapacityPlanning.Reservations = []CapacityReservation{{ProjectID: "proj-a", ReservedGenerators: 1, PoolID: "pool-real"}}
	if _, err := tr.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings with a real pool reservation should succeed, got %v", err)
	}
}

// F5: per-pool quota values mirror the project-wide caps — negatives and blank
// pool keys are rejected (rather than silently disabling the cap, fail-open).
func TestValidateProjectSettingsPoolQuotas(t *testing.T) {
	cases := []struct {
		name    string
		quotas  map[string]storage.PoolProjectQuota
		wantErr bool
	}{
		{"valid", map[string]storage.PoolProjectQuota{"pool-x": {MaxVUsers: 500, MaxConcurrentTests: 2}}, false},
		{"zero is unlimited", map[string]storage.PoolProjectQuota{"pool-x": {}}, false},
		{"negative vusers", map[string]storage.PoolProjectQuota{"pool-x": {MaxVUsers: -5}}, true},
		{"negative concurrent", map[string]storage.PoolProjectQuota{"pool-x": {MaxConcurrentTests: -1}}, true},
		{"blank key", map[string]storage.PoolProjectQuota{" ": {MaxVUsers: 10}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateProjectSettings(storage.ProjectSettings{PoolQuotas: tc.quotas})
			if tc.wantErr && err == nil {
				t.Fatalf("validateProjectSettings(%v) = nil, want error", tc.quotas)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("validateProjectSettings(%v) = %v, want nil", tc.quotas, err)
			}
		})
	}
}

// Test-gap coverage: a pool-scoped, bound, approved timeslot round-trips its PoolID
// through storage and protects pool capacity at admission (excluded from global,
// counted in the pool envelope).
func TestPoolScopedBoundTimeslotProtectsPoolAtAdmission(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 50, Addr: ":8080"})
	tr := server.runner
	seedCapacityProjects(t, store, "proj-a", "proj-b")
	seedFleetPool(t, store, "pool-z", 2) // envelope = 2

	gens := 2
	boundTest := "bound-test-1"
	now := time.Now()
	res := &storage.CapacityReservation{
		ID:                "ts-pool-z",
		ProjectID:         "proj-a",
		StartAt:           now.Add(-time.Hour),
		EndAt:             now.Add(time.Hour),
		Timezone:          "UTC",
		ApprovalState:     "approved",
		BoundTestID:       &boundTest,
		RequestedCapacity: &storage.CapacityReservationRequest{Generators: &gens, PoolID: "pool-z"},
	}
	if err := store.SaveCapacityReservation(res); err != nil {
		t.Fatalf("SaveCapacityReservation: %v", err)
	}
	// PoolID survives the requested_capacity_json round-trip.
	got, err := store.GetCapacityReservation("ts-pool-z")
	if err != nil {
		t.Fatalf("GetCapacityReservation: %v", err)
	}
	if got.RequestedCapacity == nil || strings.TrimSpace(got.RequestedCapacity.PoolID) != "pool-z" {
		t.Fatalf("pool_id not preserved through storage: %+v", got.RequestedCapacity)
	}

	poolRun := func(id, project, poolID string, recGens int) *ManagedTest {
		return &ManagedTest{
			ID: id, ProjectID: project, Status: "QUEUED", TotalVUsers: recGens * 10,
			Assignment:       &GeneratorAssignmentSettings{Mode: "specific_hosts", PoolID: poolID},
			CapacityAdvisory: &CapacityAdvisory{RecommendedGenerators: recGens},
		}
	}
	eval := func(test *ManagedTest) (bool, string) {
		return evaluateQuotaBlockWithLoadedEvidenceForTest(t, tr, test, "")
	}

	// proj-a reserved the pool's whole 2-generator envelope via the bound timeslot,
	// so a different project's pool run gets nothing from the shared remainder.
	if blocked, reason := eval(poolRun("b1", "proj-b", "pool-z", 1)); !blocked || !strings.Contains(reason, "shared generator capacity on pool") {
		t.Fatalf("proj-b pool run = (%v, %q), want blocked by the bound pool timeslot", blocked, reason)
	}
	// The reserving project can use its own reservation.
	if blocked, reason := eval(poolRun("a2", "proj-a", "pool-z", 2)); blocked {
		t.Fatalf("proj-a should consume its own pool reservation, got %q", reason)
	}
	// A global (non-pool) run is not gated by the pool-scoped timeslot.
	global := &ManagedTest{ID: "g1", ProjectID: "proj-b", Status: "QUEUED", TotalVUsers: 10, CapacityAdvisory: &CapacityAdvisory{RecommendedGenerators: 1}}
	if blocked, reason := eval(global); blocked {
		t.Fatalf("global run must be excluded from the pool timeslot envelope, got %q", reason)
	}
}

// Test-gap coverage: a pool-scoped reservation on an existing pool whose member
// envelope is empty (drained after creation) currently fails OPEN at admission —
// it gates nothing rather than blocking. This pins that intentional contract (the
// admission gate doesn't enforce a reservation it can't size); the write-time
// PoolID validation only guards against a non-existent pool, not a later-drained one.
func TestPoolReservedCapacityEmptyEnvelopeFailsOpen(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 50, Addr: ":8080"})
	tr := server.runner
	seedCapacityProjects(t, store, "proj-a", "proj-b")

	// Pool exists but has no members (envelope = 0).
	if err := store.SaveFleetPool(&storage.FleetPool{ID: "pool-empty", Name: "Drained", Status: "active"}); err != nil {
		t.Fatalf("SaveFleetPool: %v", err)
	}
	settings := tr.GetSystemSettings()
	settings.CapacityPlanning.Reservations = []CapacityReservation{{ProjectID: "proj-a", ReservedGenerators: 3, PoolID: "pool-empty"}}
	if _, err := tr.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings (existing empty pool should pass write validation): %v", err)
	}

	other := &ManagedTest{
		ID: "b1", ProjectID: "proj-b", Status: "QUEUED", TotalVUsers: 10,
		Assignment:       &GeneratorAssignmentSettings{Mode: "specific_hosts", PoolID: "pool-empty"},
		CapacityAdvisory: &CapacityAdvisory{RecommendedGenerators: 1},
	}
	blocked, reason := evaluateQuotaBlockWithLoadedEvidenceForTest(t, tr, other, "")
	if blocked {
		t.Fatalf("empty-envelope pool reservation should fail open (gate nothing), got blocked: %q", reason)
	}
}

// M2 (PUT path): the timeslot UPDATE route must also reject an unknown fleet pool,
// and repointing an approved reservation's pool must re-trigger approval (the pool
// selects the protected envelope, so it is an admission-relevant term).
func TestUpdateCapacityReservationRejectsUnknownPoolAndRepointsApproval(t *testing.T) {
	server, store := newTimeslotServer(t)
	settings := server.runner.GetSystemSettings()
	settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators = 20
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings: %v", err)
	}
	seedFleetPool(t, store, "pool-a", 2)
	seedFleetPool(t, store, "pool-b", 2)

	start := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	end := start.Add(2 * time.Hour)
	rec := timeslotRequest(t, server, http.MethodPost, "/api/v1/capacity/timeslots", map[string]any{
		"project_id": "proj-a", "start_at": start, "end_at": end, "timezone": "UTC",
		"requested_capacity": map[string]any{"generators": 1, "pool_id": "pool-a"},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d body=%s", rec.Code, rec.Body.String())
	}
	var created storage.CapacityReservation
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	id := created.ID

	if rec := timeslotRequest(t, server, http.MethodPost, "/api/v1/capacity/timeslots/"+id+"/approve", nil); rec.Code != http.StatusOK {
		t.Fatalf("approve = %d body=%s", rec.Code, rec.Body.String())
	}

	put := func(poolID string) *httptest.ResponseRecorder {
		return timeslotRequest(t, server, http.MethodPut, "/api/v1/capacity/timeslots/"+id, map[string]any{
			"start_at": start, "end_at": end, "timezone": "UTC",
			"requested_capacity": map[string]any{"generators": 1, "pool_id": poolID},
		})
	}

	// PUT repointing to a non-existent pool is rejected (leaves the slot unchanged).
	if rec := put("ghost"); rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT unknown pool = %d body=%s, want 400", rec.Code, rec.Body.String())
	}
	// PUT repointing to a different real pool changes the protected envelope, so the
	// approved reservation drops back to pending.
	rec2 := put("pool-b")
	if rec2.Code != http.StatusOK {
		t.Fatalf("PUT pool-b = %d body=%s, want 200", rec2.Code, rec2.Body.String())
	}
	var updated storage.CapacityReservation
	_ = json.Unmarshal(rec2.Body.Bytes(), &updated)
	if updated.ApprovalState != "pending" {
		t.Fatalf("repointing the pool should re-pend approval, got %q", updated.ApprovalState)
	}
}

// Low-fix regression guard: the global fleet slot-accounting view excludes
// pool-scoped reservations (they belong to a separate pool envelope).
func TestFleetProjectSlotAccountingExcludesPoolReservations(t *testing.T) {
	settings := SystemSettings{}
	settings.CapacityPlanning.Generator.MaxVUsers = 1000
	settings.CapacityPlanning.Reservations = []CapacityReservation{
		{ProjectID: "proj-a", ReservedGenerators: 5},
		{ProjectID: "proj-a", ReservedGenerators: 9, PoolID: "pool-x"},
	}
	accounting := buildFleetProjectSlotAccounting(settings, nil)
	var entry *FleetProjectSlotAccounting
	for i := range accounting {
		if accounting[i].ProjectID == "proj-a" {
			entry = &accounting[i]
		}
	}
	if entry == nil {
		t.Fatalf("no proj-a accounting entry, got %+v", accounting)
	}
	if entry.ReservedGenerators != 5 {
		t.Fatalf("global ReservedGenerators = %d, want 5 (pool-scoped 9 excluded)", entry.ReservedGenerators)
	}
}

// Low-fix regression guard: the run-calendar capacity overlay excludes pool-scoped
// reservations from both the reserved-generator figure and the reservation count.
func TestRunCalendarCapacityExcludesPoolReservations(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 50, Addr: ":8080"})
	tr := server.runner
	seedFleetPool(t, store, "pool-x", 4)

	winStart := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	winEnd := time.Date(2026, 7, 1, 17, 0, 0, 0, time.UTC)
	settings := tr.GetSystemSettings()
	settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators = 50
	settings.CapacityPlanning.Reservations = []CapacityReservation{
		{ProjectID: "proj-a", ReservedGenerators: 5, StartsAt: &winStart, EndsAt: &winEnd},
		{ProjectID: "proj-a", ReservedGenerators: 9, PoolID: "pool-x", StartsAt: &winStart, EndsAt: &winEnd},
	}
	if _, err := tr.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings: %v", err)
	}

	capacity := tr.buildRunCalendarCapacity(
		time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC),
		"", "UTC",
	)
	var day *RunCalendarCapacityDay
	for i := range capacity.Days {
		if capacity.Days[i].Date == "2026-07-01" {
			day = &capacity.Days[i]
		}
	}
	if day == nil {
		t.Fatalf("no 2026-07-01 day in capacity overlay, got %+v", capacity.Days)
	}
	if day.PeakReservedGenerators != 5 {
		t.Fatalf("PeakReservedGenerators = %d, want 5 (pool-scoped 9 excluded)", day.PeakReservedGenerators)
	}
	if day.ReservationCount != 1 {
		t.Fatalf("ReservationCount = %d, want 1 (pool-scoped reservation excluded)", day.ReservationCount)
	}
}
