package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestStandaloneCapacityBindRefreshesDurableAndRunnerState(t *testing.T) {
	server, store := newTimeslotServer(t)
	now := time.Now().UTC()
	if err := store.SaveCapacityReservation(&storage.CapacityReservation{
		ID: "slot-refresh-runner", ProjectID: "proj-a",
		StartAt: now.Add(-time.Hour), EndAt: now.Add(time.Hour), ApprovalState: "approved",
	}); err != nil {
		t.Fatalf("SaveCapacityReservation: %v", err)
	}
	if err := store.SaveTest(&storage.TestRecord{
		ID: "test-refresh-runner", Name: "refresh runner", Status: "QUEUED", ProjectID: "proj-a",
		StartTime: now, Duration: int64(10 * time.Minute), Groups: []any{},
	}); err != nil {
		t.Fatalf("SaveTest: %v", err)
	}

	response := timeslotRequest(t, server, http.MethodPost, "/api/v1/capacity/timeslots/slot-refresh-runner/bind", map[string]any{
		"test_id": "test-refresh-runner",
	})
	if response.Code != http.StatusOK {
		t.Fatalf("bind status = %d body=%s", response.Code, response.Body.String())
	}
	durable, err := store.GetTest("test-refresh-runner")
	if err != nil || durable.TimeslotID == nil || *durable.TimeslotID != "slot-refresh-runner" {
		t.Fatalf("durable test binding = (%#v, %v)", durable, err)
	}
	server.runner.mu.RLock()
	inMemory := server.runner.tests["test-refresh-runner"]
	server.runner.mu.RUnlock()
	if inMemory == nil || inMemory.TimeslotID == nil || *inMemory.TimeslotID != "slot-refresh-runner" {
		t.Fatalf("runner binding was not refreshed: %#v", inMemory)
	}
}

func TestTimeslotInvalidationClearsLiveAndDurableBinding(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		invalidate func(*testing.T, *Server, string, time.Time) *httptest.ResponseRecorder
		after      string
	}{
		{
			name: "invalidating edit",
			invalidate: func(t *testing.T, server *Server, slotID string, start time.Time) *httptest.ResponseRecorder {
				return timeslotRequest(t, server, http.MethodPut, "/api/v1/capacity/timeslots/"+slotID, map[string]any{
					"start_at": start, "end_at": start.Add(2 * time.Hour),
				})
			},
			after: "start",
		},
		{
			name: "reject",
			invalidate: func(t *testing.T, server *Server, slotID string, _ time.Time) *httptest.ResponseRecorder {
				return timeslotRequest(t, server, http.MethodPost, "/api/v1/capacity/timeslots/"+slotID+"/reject", nil)
			},
			after: "rebind",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			server, store := newTimeslotServer(t)
			now := time.Now().UTC()
			start := now.Add(-time.Minute)
			testID := "queued-live-clear"
			slotID := "slot-live-clear"
			status := "QUEUED"
			windowEnd := now.Add(10 * time.Minute)
			if err := store.SaveCapacityReservation(&storage.CapacityReservation{
				ID: slotID, ProjectID: "proj-a", StartAt: start, EndAt: now.Add(time.Hour),
				ApprovalState: "approved", BoundTestID: &testID, BoundTestStatus: &status,
			}); err != nil {
				t.Fatalf("SaveCapacityReservation: %v", err)
			}
			if err := store.SaveTest(&storage.TestRecord{
				ID: testID, Name: "Queued bound run", Status: status, ProjectID: "proj-a", StartTime: now,
				Duration: int64(10 * time.Minute), TimeslotID: &slotID,
				CapacityWindowStartAt: &now, CapacityWindowEndAt: &windowEnd, Groups: []any{},
			}); err != nil {
				t.Fatalf("SaveTest: %v", err)
			}
			server.runner.mu.Lock()
			server.runner.tests[testID] = &ManagedTest{
				ID: testID, Name: "Queued bound run", Status: status, ProjectID: "proj-a", CreatedAt: now,
				Duration: 10 * time.Minute, DurationStr: "10m", TimeslotID: &slotID,
				CapacityWindowStartAt: &now, CapacityWindowEndAt: &windowEnd,
			}
			server.runner.mu.Unlock()

			response := testCase.invalidate(t, server, slotID, start)
			if response.Code != http.StatusOK {
				t.Fatalf("invalidation status = %d body=%s", response.Code, response.Body.String())
			}
			durable, err := store.GetTest(testID)
			if err != nil || durable.TimeslotID != nil || durable.CapacityWindowStartAt != nil || durable.CapacityWindowEndAt != nil {
				t.Fatalf("durable binding after invalidation = (%#v, %v)", durable, err)
			}
			server.runner.mu.RLock()
			live := server.runner.tests[testID]
			liveSlot := strings.TrimSpace(trimPtr(live.TimeslotID))
			liveWindow := live.CapacityWindowStartAt != nil || live.CapacityWindowEndAt != nil
			server.runner.mu.RUnlock()
			if liveSlot != "" || liveWindow {
				t.Fatalf("live binding survived invalidation: slot=%q window=%v", liveSlot, liveWindow)
			}

			switch testCase.after {
			case "start":
				started := make(chan string, 1)
				server.runner.setExecutionBackend(&scheduleExecutionBackend{started: started})
				if err := server.runner.StartTest(testID); err != nil {
					t.Fatalf("StartTest after unbind: %v", err)
				}
				select {
				case got := <-started:
					if got != testID {
						t.Fatalf("started test = %q, want %q", got, testID)
					}
				case <-time.After(2 * time.Second):
					t.Fatal("unbound queued test did not start")
				}
			case "rebind":
				newSlotID := "slot-live-rebind"
				if err := store.SaveCapacityReservation(&storage.CapacityReservation{
					ID: newSlotID, ProjectID: "proj-a", StartAt: start, EndAt: now.Add(time.Hour), ApprovalState: "approved",
				}); err != nil {
					t.Fatalf("SaveCapacityReservation(rebind): %v", err)
				}
				if _, result, err := server.runner.BindCapacityTimeslot(newSlotID, testID); err != nil || result != storage.CapacityBindingBound {
					t.Fatalf("BindCapacityTimeslot = (%q, %v), want bound", result, err)
				}
				durable, err = store.GetTest(testID)
				if err != nil || strings.TrimSpace(trimPtr(durable.TimeslotID)) != newSlotID {
					t.Fatalf("durable rebind = (%#v, %v)", durable, err)
				}
				server.runner.mu.RLock()
				liveSlot = strings.TrimSpace(trimPtr(server.runner.tests[testID].TimeslotID))
				server.runner.mu.RUnlock()
				if liveSlot != newSlotID {
					t.Fatalf("live rebind = %q, want %q", liveSlot, newSlotID)
				}
			}
		})
	}
}

func TestCrossReplicaTimeslotInvalidationRefreshesAtStartBoundary(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "cross-replica-timeslot.db")
	leaderStore, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(leader): %v", err)
	}
	defer leaderStore.Close()
	followerStore, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(follower): %v", err)
	}
	defer followerStore.Close()
	now := time.Now().UTC()
	if err := leaderStore.SaveProject(&storage.Project{ID: "proj-ha-binding", Name: "HA binding", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	testID := "cross-replica-start"
	slotID := "cross-replica-slot"
	status := "QUEUED"
	windowEnd := now.Add(10 * time.Minute)
	if err := leaderStore.SaveCapacityReservation(&storage.CapacityReservation{
		ID: slotID, ProjectID: "proj-ha-binding", StartAt: now.Add(-time.Minute), EndAt: now.Add(time.Hour),
		ApprovalState: "approved", BoundTestID: &testID, BoundTestStatus: &status,
	}); err != nil {
		t.Fatalf("SaveCapacityReservation: %v", err)
	}
	if err := leaderStore.SaveTest(&storage.TestRecord{
		ID: testID, Name: "Cross-replica start", Status: status, ProjectID: "proj-ha-binding", StartTime: now,
		Duration: int64(10 * time.Minute), TimeslotID: &slotID,
		CapacityWindowStartAt: &now, CapacityWindowEndAt: &windowEnd, Groups: []any{},
	}); err != nil {
		t.Fatalf("SaveTest: %v", err)
	}
	leader := NewTestRunner(2, NewHub(), leaderStore)
	leader.mu.Lock()
	leader.tests[testID] = &ManagedTest{
		ID: testID, Name: "Cross-replica start", Status: status, ProjectID: "proj-ha-binding", CreatedAt: now,
		Duration: 10 * time.Minute, DurationStr: "10m", TimeslotID: &slotID,
		CapacityWindowStartAt: &now, CapacityWindowEndAt: &windowEnd,
	}
	leader.mu.Unlock()
	follower := NewServer(ServerConfig{Store: followerStore, MaxConcurrent: 2})
	response := timeslotRequest(t, follower, http.MethodPost, "/api/v1/capacity/timeslots/"+slotID+"/reject", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("follower rejection = %d body=%s", response.Code, response.Body.String())
	}
	leader.mu.RLock()
	staleBeforeStart := strings.TrimSpace(trimPtr(leader.tests[testID].TimeslotID))
	leader.mu.RUnlock()
	if staleBeforeStart != slotID {
		t.Fatalf("leader was unexpectedly synchronized before start: %q", staleBeforeStart)
	}

	started := make(chan string, 1)
	leader.setExecutionBackend(&scheduleExecutionBackend{started: started})
	if err := leader.StartTest(testID); err != nil {
		t.Fatalf("StartTest after cross-replica invalidation: %v", err)
	}
	select {
	case got := <-started:
		if got != testID {
			t.Fatalf("started test = %q, want %q", got, testID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cross-replica unbound test did not start")
	}
	leader.mu.RLock()
	refreshed := leader.tests[testID]
	refreshedSlot := strings.TrimSpace(trimPtr(refreshed.TimeslotID))
	refreshedWindow := refreshed.CapacityWindowStartAt != nil || refreshed.CapacityWindowEndAt != nil
	leader.mu.RUnlock()
	if refreshedSlot != "" || refreshedWindow {
		t.Fatalf("leader retained stale binding after start refresh: slot=%q window=%v", refreshedSlot, refreshedWindow)
	}
}

func TestHATestReadRefreshesCapacityBindingInsideThrottleWindow(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "ha-read-binding.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	now := time.Now().UTC()
	testID := "ha-read-binding"
	slotID := "ha-read-slot"
	status := "QUEUED"
	if err := store.SaveCapacityReservation(&storage.CapacityReservation{
		ID: slotID, ProjectID: storage.DefaultProjectID, StartAt: now.Add(-time.Minute), EndAt: now.Add(time.Hour),
		ApprovalState: "approved", BoundTestID: &testID, BoundTestStatus: &status,
	}); err != nil {
		t.Fatalf("SaveCapacityReservation: %v", err)
	}
	if err := store.SaveTest(&storage.TestRecord{
		ID: testID, Name: "HA read binding", Status: status, ProjectID: storage.DefaultProjectID,
		StartTime: now, Duration: int64(10 * time.Minute), TimeslotID: &slotID, Groups: []any{},
	}); err != nil {
		t.Fatalf("SaveTest: %v", err)
	}
	leadership := newExecutionLeadership(store, "ha-read-follower")
	runner := NewTestRunner(2, NewHub(), store, WithExecutionLeadership(leadership))
	first, ok := runner.GetTest(testID)
	if !ok || strings.TrimSpace(trimPtr(first.TimeslotID)) != slotID {
		t.Fatalf("initial HA read = %#v, want bound", first)
	}
	// Suppress the full-map follower refresh. The single-object durable read must
	// still reconcile the reciprocal binding immediately.
	runner.haTestRefresh.markFresh(time.Now())
	if changed, err := store.UpdateCapacityReservationApprovalState(slotID, []string{"approved"}, "rejected", time.Now().UTC()); err != nil || !changed {
		t.Fatalf("reject capacity reservation = (%v, %v)", changed, err)
	}
	second, ok := runner.GetTest(testID)
	if !ok || second.TimeslotID != nil || second.CapacityWindowStartAt != nil || second.CapacityWindowEndAt != nil {
		t.Fatalf("throttled HA read retained stale binding: %#v", second)
	}
}

func TestHAListReadsRefreshCrossReplicaCapacityBinding(t *testing.T) {
	readPaths := []struct {
		name string
		read func(*TestRunner) []*ManagedTest
	}{
		{
			name: "full list",
			read: func(runner *TestRunner) []*ManagedTest { return runner.ListTests() },
		},
		{
			name: "bounded summary page",
			read: func(runner *TestRunner) []*ManagedTest {
				tests, _ := runner.listTestSummaryPage(testSummaryListQuery{limit: defaultTestListLimit})
				return tests
			},
		},
	}
	for _, role := range []string{"leader", "throttled follower"} {
		for _, readPath := range readPaths {
			t.Run(role+"/"+readPath.name, func(t *testing.T) {
				dbPath := filepath.Join(t.TempDir(), "ha-list-binding.db")
				localStore, err := storage.NewSQLiteStore(dbPath)
				if err != nil {
					t.Fatalf("NewSQLiteStore(local): %v", err)
				}
				defer localStore.Close()
				remoteStore, err := storage.NewSQLiteStore(dbPath)
				if err != nil {
					t.Fatalf("NewSQLiteStore(remote): %v", err)
				}
				defer remoteStore.Close()

				now := time.Now().UTC().Truncate(time.Microsecond)
				testID := "ha-list-bound-test"
				slotID := "ha-list-slot"
				status := "QUEUED"
				windowEnd := now.Add(10 * time.Minute)
				if err := localStore.SaveCapacityReservation(&storage.CapacityReservation{
					ID: slotID, ProjectID: storage.DefaultProjectID, StartAt: now.Add(-time.Minute), EndAt: now.Add(time.Hour),
					ApprovalState: "approved", BoundTestID: &testID, BoundTestStatus: &status,
				}); err != nil {
					t.Fatalf("SaveCapacityReservation: %v", err)
				}
				if err := localStore.SaveTest(&storage.TestRecord{
					ID: testID, Name: "HA list bound test", Status: status, ProjectID: storage.DefaultProjectID,
					StartTime: now, Duration: int64(10 * time.Minute), TimeslotID: &slotID,
					CapacityWindowStartAt: &now, CapacityWindowEndAt: &windowEnd, Groups: []any{},
				}); err != nil {
					t.Fatalf("SaveTest: %v", err)
				}

				leadership := newExecutionLeadership(localStore, "ha-list-"+strings.ReplaceAll(role, " ", "-"))
				if role == "leader" {
					leadership.tick()
					if !leadership.IsLeader() {
						t.Fatal("failed to establish leader fixture")
					}
				}
				runner := NewTestRunner(2, NewHub(), localStore, WithExecutionLeadership(leadership))
				runner.mu.RLock()
				local := runner.tests[testID]
				boundBefore := local != nil && strings.TrimSpace(trimPtr(local.TimeslotID)) == slotID
				runner.mu.RUnlock()
				if !boundBefore {
					t.Fatal("runner did not hydrate the initial capacity binding")
				}
				// A follower full-map refresh is deliberately suppressed here; a
				// leader would skip it regardless. The list projection itself must
				// reconcile the durable binding written by the other replica.
				runner.haTestRefresh.markFresh(time.Now())
				if changed, err := remoteStore.UpdateCapacityReservationApprovalState(slotID, []string{"approved"}, "rejected", time.Now().UTC()); err != nil || !changed {
					t.Fatalf("remote rejection = (%v, %v)", changed, err)
				}

				listed := readPath.read(runner)
				if len(listed) != 1 {
					t.Fatalf("listed tests = %#v, want one", listed)
				}
				if listed[0].TimeslotID != nil || listed[0].CapacityWindowStartAt != nil || listed[0].CapacityWindowEndAt != nil {
					t.Fatalf("list returned stale capacity binding: %#v", listed[0])
				}
			})
		}
	}
}

func TestStandaloneCapacityBindIsFencedAfterDatabaseTakeover(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "shared-ha-capacity-bind.db")
	oldStore, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(old): %v", err)
	}
	defer oldStore.Close()
	newStore, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(new): %v", err)
	}
	defer newStore.Close()
	server := NewServer(ServerConfig{Store: oldStore, MaxConcurrent: 1, HAExecutionLeadership: true})
	server.leadership.tick()
	deadline := time.Now().Add(2 * time.Second)
	for !server.leadership.IsAdmitted() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !server.leadership.IsAdmitted() {
		t.Fatal("old server did not finish leadership recovery")
	}
	defer server.leadership.relinquish()

	now := time.Now().UTC()
	if err := oldStore.SaveCapacityReservation(&storage.CapacityReservation{
		ID: "slot-stale-handler", ProjectID: storage.DefaultProjectID,
		StartAt: now.Add(-time.Hour), EndAt: now.Add(time.Hour), ApprovalState: "approved",
	}); err != nil {
		t.Fatalf("SaveCapacityReservation: %v", err)
	}
	if err := oldStore.SaveTest(&storage.TestRecord{
		ID: "test-stale-handler", Name: "stale handler", Status: "QUEUED", ProjectID: storage.DefaultProjectID,
		StartTime: now, Duration: int64(10 * time.Minute), Groups: []any{},
	}); err != nil {
		t.Fatalf("SaveTest: %v", err)
	}
	oldToken := server.leadership.executionFenceToken()
	if err := oldStore.ReleaseSchedulerLease(executionLeaseName, oldToken); err != nil {
		t.Fatalf("release old lease: %v", err)
	}
	newLeadership := newExecutionLeadership(newStore, "capacity-bind-successor")
	newLeadership.tick()
	defer newLeadership.relinquish()
	if newLeadership.executionFenceToken() == "" {
		t.Fatal("successor did not acquire execution lease")
	}

	response := timeslotRequest(t, server, http.MethodPost, "/api/v1/capacity/timeslots/slot-stale-handler/bind", map[string]any{
		"test_id": "test-stale-handler",
	})
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("stale bind status = %d, want 503 body=%s", response.Code, response.Body.String())
	}
	reservation, err := newStore.GetCapacityReservation("slot-stale-handler")
	if err != nil || reservation.BoundTestID != nil {
		t.Fatalf("stale handler changed reservation = (%#v, %v)", reservation, err)
	}
	test, err := newStore.GetTest("test-stale-handler")
	if err != nil || test.TimeslotID != nil {
		t.Fatalf("stale handler changed test = (%#v, %v)", test, err)
	}
}

func TestTerminalCapacityReleaseClearsBothDurableSides(t *testing.T) {
	server, store := newTimeslotServer(t)
	now := time.Now().UTC()
	testID := "test-terminal-two-sided"
	slotID := "slot-terminal-two-sided"
	status := "RUNNING"
	if err := store.SaveCapacityReservation(&storage.CapacityReservation{
		ID: slotID, ProjectID: "proj-a", StartAt: now.Add(-time.Hour), EndAt: now.Add(time.Hour),
		ApprovalState: "approved", BoundTestID: &testID, BoundTestStatus: &status,
	}); err != nil {
		t.Fatalf("SaveCapacityReservation: %v", err)
	}
	if err := store.SaveTest(&storage.TestRecord{
		ID: testID, Name: "terminal release", Status: status, ProjectID: "proj-a", StartTime: now,
		Duration: int64(10 * time.Minute), TimeslotID: &slotID, Groups: []any{},
	}); err != nil {
		t.Fatalf("SaveTest: %v", err)
	}
	server.runner.mu.Lock()
	server.runner.tests[testID] = &ManagedTest{ID: testID, Status: status, TimeslotID: &slotID}
	server.runner.mu.Unlock()
	endedAt := now.Add(5 * time.Minute)
	if err := server.runner.updateStoredTestStatus(testID, "COMPLETE", &endedAt, ""); err != nil {
		t.Fatalf("updateStoredTestStatus: %v", err)
	}
	reservation, err := store.GetCapacityReservation(slotID)
	if err != nil || reservation.BoundTestID != nil || reservation.BoundTestStatus != nil {
		t.Fatalf("reservation release = (%#v, %v)", reservation, err)
	}
	durable, err := store.GetTest(testID)
	if err != nil || durable.TimeslotID != nil {
		t.Fatalf("test release = (%#v, %v)", durable, err)
	}
	server.runner.mu.RLock()
	inMemory := server.runner.tests[testID]
	server.runner.mu.RUnlock()
	if inMemory == nil || inMemory.TimeslotID != nil {
		t.Fatalf("runner terminal release was not refreshed: %#v", inMemory)
	}
}
