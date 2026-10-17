package recordbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func assertReplayCapacityResponse(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	ref, _ := body["ref"].(string)
	// Capacity keeps the existing generic 5xx response plus a diagnostic ref;
	// the specific project/global cause is retained in the server log.
	if response.Code != http.StatusServiceUnavailable || len(body) != 2 || body["error"] != "internal server error" || ref == "" {
		t.Fatalf("capacity response: %d %s", response.Code, response.Body.String())
	}
}

func TestBridgeReplayReservationsPreserveProjectAdmissionAndWorkerRelease(t *testing.T) {
	bridge := newBrowserRecorderBridgeTestServer(t, nil)
	manager := bridge.service.Replay
	script := portReplayScript(t)
	script.ProjectID = "project-a"
	ids := make([]string, 0, defaultReplayReservationLimitPerProject)
	for range defaultReplayReservationLimitPerProject {
		session, err := manager.Start("script", script, BrowserReplayStartRequest{StartPaused: true})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, session.ID)
	}
	if _, err := manager.Start("script", script, BrowserReplayStartRequest{StartPaused: true}); !errors.Is(err, errLocalReplayProjectCapacity) {
		t.Fatalf("101st in-flight project replay: %v", err)
	}
	// Exercise the same shared HTTP adapter used by the paired bridge, with a
	// valid stopped recording. Admission must fail before launching a browser.
	_, recording := recordingConversionFixture()
	bridge.service.Recording = &BrowserRecordingManager{sessions: map[string]*BrowserRecordingSession{recording.ID: recording}}
	r := httptest.NewRequest(http.MethodPost, "/verify", nil)
	r.SetPathValue("id", recording.ID)
	w := httptest.NewRecorder()
	bridge.service.HandleVerifyBrowserRecording(w, r)
	assertReplayCapacityResponse(t, w)
	if _, err := manager.Start("script", script, BrowserReplayStartRequest{ReplaySessionID: ids[0], StartPaused: true}); !errors.Is(err, errLocalReplayIDConflict) {
		t.Fatalf("duplicate ID must precede capacity refusal: %v", err)
	} else {
		conflict := httptest.NewRecorder()
		bridge.service.writeReplayStartError(conflict, err)
		if conflict.Code != http.StatusConflict || !strings.Contains(conflict.Body.String(), `"error":"replay_session_id is already in use"`) {
			t.Fatalf("conflict response: %d %s", conflict.Code, conflict.Body.String())
		}
	}
	other := *script
	other.ProjectID = "project-b"
	if _, err := manager.Start("other", &other, BrowserReplayStartRequest{StartPaused: true}); err != nil {
		t.Fatalf("independent project rejected: %v", err)
	}
	if _, found, changed := manager.Resume(ids[0]); !found || !changed {
		t.Fatal("could not resume the first replay")
	}
	wait := func(id, status string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if found, err := manager.Wait(ctx, id); err != nil || !found {
			t.Fatalf("wait %q: found=%t err=%v", id, found, err)
		}
		if session, found := manager.Get(id); !found || session.Status != status {
			t.Fatalf("terminal replay %q: %#v found=%t", id, session, found)
		}
	}
	wait(ids[0], BrowserReplayCompleted)
	failed := *script
	failed.Protocol = "recordbridge-reservation-missing-protocol"
	session, err := manager.Start("failed", &failed, BrowserReplayStartRequest{})
	if err != nil {
		t.Fatalf("completion did not release project slot: %v", err)
	}
	wait(session.ID, BrowserReplayFailed)
	if _, err := manager.Start("replacement", script, BrowserReplayStartRequest{StartPaused: true}); err != nil {
		t.Fatalf("failed worker did not release project slot: %v", err)
	}
}

func TestBridgeReplayReservationsPreserveConfiguredReserver(t *testing.T) {
	want := errors.New("configured reservation refusal")
	calls := 0
	bridge := NewBrowserRecorderBridgeServer(BrowserRecorderBridgeConfig{Replay: ReplayManagerOptions{ReserveID: func(_ context.Context, id, project string) (string, func(), SessionLease, error) {
		calls++
		if id != "requested" || project != "project-port" {
			t.Errorf("configured reservation inputs: %q %q", id, project)
		}
		return "", nil, nil, want
	}}})
	t.Cleanup(func() { _ = bridge.Shutdown(context.Background()) })
	_, err := bridge.service.Replay.Start("script", portReplayScript(t), BrowserReplayStartRequest{ReplaySessionID: "requested"})
	if !errors.Is(err, want) || calls != 1 {
		t.Fatalf("configured reserver replaced: calls=%d err=%v", calls, err)
	}
}

func TestLocalReplayReservationsGlobalLimitAndConcurrentProjectAdmission(t *testing.T) {
	owner := newLocalReplayReservations()
	for index := range defaultReplayReservationLimit {
		if _, _, _, err := owner.reserve(context.Background(), fmt.Sprintf("id-%d", index), fmt.Sprintf("project-%d", index/defaultReplayReservationLimitPerProject)); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, _, err := owner.reserve(context.Background(), "overflow", "another-project"); !errors.Is(err, errLocalReplayCapacity) {
		t.Fatalf("global limit: %v", err)
	} else {
		w := httptest.NewRecorder()
		(&Service{}).writeReplayStartError(w, err)
		assertReplayCapacityResponse(t, w)
	}
	owner = newLocalReplayReservations()
	results := make(chan error, 120)
	var workers sync.WaitGroup
	for range 120 {
		workers.Go(func() {
			_, _, _, err := owner.reserve(context.Background(), "", " project-a ")
			results <- err
		})
	}
	workers.Wait()
	close(results)
	accepted := 0
	for err := range results {
		if err == nil {
			accepted++
		} else if !errors.Is(err, errLocalReplayProjectCapacity) {
			t.Fatal(err)
		}
	}
	if accepted != defaultReplayReservationLimitPerProject {
		t.Fatalf("concurrent accepted=%d", accepted)
	}
}

func TestLocalReplayReservationsRetainedOwnershipAndLateRelease(t *testing.T) {
	owner := newLocalReplayReservations()
	now := time.Now()
	owner.now = func() time.Time { return now }
	_, release, lease, err := owner.reserve(context.Background(), "retained", "p")
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.RetainForTerminal(time.Minute); err != nil {
		t.Fatal(err)
	}
	release()
	if _, _, _, err := owner.reserve(context.Background(), "retained", "p"); !errors.Is(err, errLocalReplayIDConflict) {
		t.Fatalf("worker release discarded retained ID: %v", err)
	}
	for range defaultReplayReservationLimitPerProject {
		if _, _, _, err := owner.reserve(context.Background(), "", "p"); err != nil {
			t.Fatalf("terminal record consumes active capacity: %v", err)
		}
	}
	now = now.Add(time.Minute)
	_, _, successor, err := owner.reserve(context.Background(), "retained", "other")
	if err != nil {
		t.Fatalf("expired ID was not reusable: %v", err)
	}
	if !errors.Is(lease.FailureOrExpired(now), errLocalReplayReservationLost) {
		t.Fatal("expired owner still publishes")
	}
	select {
	case <-lease.Done():
	default:
		t.Fatal("expired lease did not signal worker cancellation")
	}
	lease.ReleaseRetainedID()
	lease.ReleaseAll()
	release()
	if _, _, _, err := owner.reserve(context.Background(), "retained", "other"); !errors.Is(err, errLocalReplayIDConflict) {
		t.Fatalf("late predecessor release removed successor: %v", err)
	}
	successor.ReleaseAll()
	if _, _, _, err := owner.reserve(context.Background(), "retained", "other"); err != nil {
		t.Fatalf("current owner did not release ID: %v", err)
	}
}

func TestLocalReplayReservationsReleaseFailedManagerAdmission(t *testing.T) {
	owner := newLocalReplayReservations()
	manager := NewBrowserReplayManager(nil, ReplayManagerOptions{ReserveID: owner.reserve})
	manager.Close()
	_, err := manager.Start("script", portReplayScript(t), BrowserReplayStartRequest{})
	if !errors.Is(err, ErrBrowserReplayManagerClosed) {
		t.Fatalf("closed admission: %v", err)
	}
	if len(owner.sessions) != 0 {
		t.Fatal("failed admission leaked its reservation")
	}
}

func TestLocalReplayReservationsRejectRepeatedRetentionAtExpiry(t *testing.T) {
	owner := newLocalReplayReservations()
	now := time.Now()
	owner.now = func() time.Time { return now }
	_, _, lease, err := owner.reserve(context.Background(), "expired", "p")
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.RetainForTerminal(time.Minute); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	// No intervening prune or read may be necessary to invalidate the fence.
	if err := lease.RetainForTerminal(time.Hour); !errors.Is(err, errLocalReplayReservationLost) {
		t.Fatalf("repeated expired retention: %v", err)
	}
	select {
	case <-lease.Done():
	default:
		t.Fatal("expired retention did not notify the worker")
	}
	if !owner.sessions["expired"].expiresAt.Equal(now) {
		t.Fatal("repeated retention extended the expired fence")
	}
}
