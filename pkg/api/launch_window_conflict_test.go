package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestLaunchWindowConflictError(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  *LaunchWindowConflictError
		want string
	}{
		{"nil", nil, "launch window conflict"},
		{"empty", &LaunchWindowConflictError{}, "launch window conflict"},
		{"details", &LaunchWindowConflictError{Conflicts: []LaunchConflict{{Message: "reservation overlap"}, {Message: "maintenance"}}}, "launch window conflict: reservation overlap"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.err.Error(); got != tc.want {
				t.Fatalf("Error = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLaunchWindowConflictDistinguishesUnavailableEvidence(t *testing.T) {
	runner := NewTestRunner(10, NewHub(), nil)
	run := &ManagedTest{Duration: time.Minute, TotalVUsers: 1}
	now := time.Now().UTC()
	window := launchWindowForTest(run, now)
	for _, tc := range []struct {
		name   string
		change func(*launchWindowEvidence)
	}{
		{"stale", func(e *launchWindowEvidence) { e.window = LaunchWindow{} }},
		{"occurrences", func(e *launchWindowEvidence) { e.occurrenceErr = errors.New("storage unavailable") }},
		{"reservations", func(e *launchWindowEvidence) { e.reservationErr = errors.New("storage unavailable") }},
		{"quota", func(e *launchWindowEvidence) { e.projectQuota.err = errors.New("storage unavailable") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			evidence := runner.loadLaunchWindowEvidenceForProject(window, run.ProjectID, "")
			tc.change(&evidence)
			err := runner.launchStartWindowErrorWithEvidenceLocked(run, now, "", "", evidence)
			var conflict *LaunchWindowConflictError
			if !errors.Is(err, errStartAdmissionEvidenceStale) || errors.As(err, &conflict) {
				t.Fatalf("error = %v, want retryable evidence error, not conflict", err)
			}
		})
	}
	evidence := runner.loadLaunchWindowEvidenceForProject(window, run.ProjectID, "")
	if err := runner.launchStartWindowErrorWithEvidenceLocked(run, now, "", "", evidence); err != nil {
		t.Fatalf("available nonconflicting window: %v", err)
	}
	if err := runner.launchStartWindowErrorWithEvidenceLocked(nil, now, "", "", launchWindowEvidence{}); err != nil {
		t.Fatalf("nil run: %v", err)
	}
}

func TestHandleStartTestPreservesLaunchWindowConflict(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 10, Addr: ":8080"})
	backend := &scheduleExecutionBackend{started: make(chan string, 1)}
	server.runner.setExecutionBackend(backend)
	storePreflightScript(server.runner, "window-http", "")
	settings := server.runner.GetSystemSettings()
	settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators = 1
	settings.CapacityPlanning.Generator.MaxVUsers = 10000
	settings.CapacityPlanning.HeadroomPct = 0
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatal(err)
	}
	run, err := server.runner.CreateTest(CreateTestRequest{
		Name: "HTTP launch", ScriptID: "window-http", VUsers: 1, Duration: "2h",
	})
	if err != nil {
		t.Fatal(err)
	}
	reservedAt := time.Now().UTC().Add(time.Hour).Truncate(15 * time.Minute)
	reservation, err := server.runner.CreateTest(CreateTestRequest{
		Name: "Future reservation", ScriptID: "window-http", VUsers: 1, Duration: "30m",
		ReserveLaunchWindow: true, ScheduledStartAt: &reservedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	err = server.runner.StartTest(run.ID)
	var windowErr *LaunchWindowConflictError
	if !errors.As(fmt.Errorf("wrapped: %w", err), &windowErr) || len(windowErr.Conflicts) != 1 {
		t.Fatalf("start = %v, want typed window conflict", err)
	}
	for _, override := range []bool{false, true} {
		body, _ := json.Marshal(StartTestOptions{OverrideCapacityShortfall: override})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/tests/"+run.ID+"/start", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		if resp.Code != http.StatusConflict {
			t.Fatalf("override=%v status=%d body=%s", override, resp.Code, resp.Body)
		}
		var payload struct {
			Code              string           `json:"code"`
			TestID            string           `json:"test_id"`
			Error             string           `json:"error"`
			OverrideAvailable *bool            `json:"override_available"`
			Conflicts         []LaunchConflict `json:"blocking_conflicts"`
		}
		if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Code != "launch_window_conflict" || payload.TestID != run.ID || payload.Error != windowErr.Error() || payload.OverrideAvailable == nil || *payload.OverrideAvailable || !reflect.DeepEqual(payload.Conflicts, windowErr.Conflicts) {
			t.Fatalf("lost conflict response: %+v", payload)
		}
		conflict := payload.Conflicts[0]
		if conflict.Code != "generator_capacity_window" || conflict.Source != "controller" || conflict.Severity != "blocking" || conflict.StartsAt == nil || !conflict.StartsAt.Equal(reservedAt) || conflict.RequiredGenerators != 2 || conflict.AvailableGenerators != 1 {
			t.Fatalf("unexpected window detail: %+v", conflict)
		}
	}
	current, ok := server.runner.GetTest(run.ID)
	persisted, err := store.GetTest(run.ID)
	if err != nil || !ok || current == nil || persisted == nil {
		t.Fatalf("load rejected run: %v", err)
	}
	if current.Status != run.Status || current.StartedAt != nil || current.EndedAt != nil || current.Error != "" || persisted.Status != run.Status || persisted.StartedAt != nil || persisted.EndTime != nil || persisted.Error != "" {
		t.Fatalf("rejection changed run: memory=%+v stored=%+v", current, persisted)
	}
	select {
	case id := <-backend.started:
		t.Fatalf("blocked run dispatched: %s", id)
	default:
	}
	events, err := store.ListAuditEvents(storage.AuditEventFilter{Action: "capacity.launch_window.reject", Limit: 10})
	if err != nil || len(events) != 2 {
		t.Fatalf("audit events=%+v err=%v", events, err)
	}
	for _, event := range events {
		if event.Status != "blocked" || event.ResourceID != run.ID || event.Details["reason"] != "launch_window_conflict" || event.Details["blocking_conflicts"] == nil {
			t.Fatalf("audit=%+v", event)
		}
	}
	// Removing the reservation lets the same unstarted run be retried normally.
	if err := server.runner.StopTest(reservation.ID); err != nil {
		t.Fatal(err)
	}
	if err := server.runner.StartTest(run.ID); err != nil {
		t.Fatalf("retry after reservation release: %v", err)
	}
	select {
	case id := <-backend.started:
		if id != run.ID {
			t.Fatalf("started %q, want %q", id, run.ID)
		}
	case <-time.After(time.Second):
		t.Fatal("retry did not dispatch")
	}
	if err := server.runner.StopTest(run.ID); err != nil {
		t.Fatal(err)
	}
}

func TestHandleStartTestKeepsLaunchEvidenceOutageRetryable(t *testing.T) {
	db, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := &admissionEvidenceStore{Store: db, occurrenceErr: errors.New("occurrence store unavailable")}
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 10, Addr: ":8080"})
	storePreflightScript(server.runner, "window-evidence-http", "")
	run, err := server.runner.CreateTest(CreateTestRequest{
		Name: "Evidence outage", ScriptID: "window-evidence-http", VUsers: 1, Duration: "1m",
	})
	if err != nil {
		t.Fatal(err)
	}
	store.occurrenceFailures.Store(-1)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, httptest.NewRequest(http.MethodPost, "/api/v1/tests/"+run.ID+"/start", nil))
	if resp.Code != http.StatusServiceUnavailable || resp.Header().Get("Retry-After") != "1" {
		t.Fatalf("status=%d retry-after=%q body=%s", resp.Code, resp.Header().Get("Retry-After"), resp.Body)
	}
	var payload map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["code"] != "start_admission_temporarily_unavailable" || payload["test_id"] != run.ID || payload["blocking_conflicts"] != nil {
		t.Fatalf("outage misclassified: %+v", payload)
	}
	current, ok := server.runner.GetTest(run.ID)
	if !ok || current.Status != run.Status || current.StartedAt != nil || current.EndedAt != nil {
		t.Fatalf("outage changed lifecycle: %+v", current)
	}
}
