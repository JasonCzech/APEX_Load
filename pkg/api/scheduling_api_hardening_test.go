package api

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestDecodeOptionalJSONBodyRejectsMalformedUnknownAndTrailingInput(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{name: "empty", body: "", want: true},
		{name: "valid", body: `{"reason":"ok"}`, want: true},
		{name: "malformed", body: `{"reason":`, want: false},
		{name: "unknown", body: `{"unexpected":true}`, want: false},
		{name: "trailing", body: `{"reason":"ok"} {}`, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(test.body))
			response := httptest.NewRecorder()
			var payload struct {
				Reason string `json:"reason"`
			}
			if got := decodeOptionalJSONBody(response, req, &payload); got != test.want {
				t.Fatalf("decodeOptionalJSONBody = %v, want %v body=%s", got, test.want, response.Body.String())
			}
			if !test.want && response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", response.Code)
			}
		})
	}
}

func TestScheduleExpectedRevisionSupportsIfMatchAndRejectsDisagreement(t *testing.T) {
	bodyRevision := 4
	req := httptest.NewRequest(http.MethodPut, "/", nil)
	req.Header.Set("If-Match", `W/"4"`)
	got, err := scheduleExpectedRevision(req, &bodyRevision)
	if err != nil || got == nil || *got != 4 {
		t.Fatalf("scheduleExpectedRevision = %v, %v", got, err)
	}
	req.Header.Set("If-Match", `"5"`)
	if _, err := scheduleExpectedRevision(req, &bodyRevision); err == nil {
		t.Fatal("mismatched If-Match and expected_revision was accepted")
	}
	if _, err := scheduleExpectedRevision(httptest.NewRequest(http.MethodPut, "/", nil), nil); !errors.Is(err, errSchedulePreconditionRequired) {
		t.Fatalf("missing precondition error = %v, want %v", err, errSchedulePreconditionRequired)
	}
	invalidRevision := 0
	if _, err := scheduleExpectedRevision(httptest.NewRequest(http.MethodPut, "/", nil), &invalidRevision); err == nil {
		t.Fatal("non-positive expected_revision was accepted")
	}
}

func TestScheduleUpdateHandlersRequireRevisionPrecondition(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 1})
	requests := []string{
		"/api/v1/scenarios/scenario-1/schedules/schedule-1",
		"/api/v1/execution-plans/plan-1/schedules/schedule-1",
	}
	for _, path := range requests {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPut, path, strings.NewReader(`{"name":"Nightly","cron_expr":"0 2 * * *","enabled":true}`))
			req.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			server.mux.ServeHTTP(response, req)
			if response.Code != http.StatusPreconditionRequired {
				t.Fatalf("status = %d, want %d body=%s", response.Code, http.StatusPreconditionRequired, response.Body.String())
			}
		})
	}
}

func TestOneTimeScheduleSelectionUsesDueTimeAndRotatingCursor(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	scheduler := newScenarioScheduler(runner)
	now := time.Now().UTC()
	runner.mu.Lock()
	for index := 0; index < maxOneTimeScheduledStartsPerSweep+2; index++ {
		id := strings.Repeat("x", 1) + string(rune('a'+index%26)) + "-" + time.Unix(int64(index), 0).Format("150405")
		at := now.Add(-time.Duration(maxOneTimeScheduledStartsPerSweep+2-index) * time.Minute)
		runner.tests[id] = &ManagedTest{ID: id, Status: "SCHEDULED", ScheduledStartAt: &at}
	}
	first := scheduler.dueOneTimeScheduledTestIDsLocked(now, maxOneTimeScheduledStartsPerSweep)
	second := scheduler.dueOneTimeScheduledTestIDsLocked(now, maxOneTimeScheduledStartsPerSweep)
	runner.mu.Unlock()
	if len(first) != maxOneTimeScheduledStartsPerSweep || len(second) != maxOneTimeScheduledStartsPerSweep {
		t.Fatalf("selection sizes = %d, %d", len(first), len(second))
	}
	seen := make(map[string]struct{}, len(first))
	for _, id := range first {
		seen[id] = struct{}{}
	}
	foundLater := false
	for _, id := range second {
		if _, ok := seen[id]; !ok {
			foundLater = true
			break
		}
	}
	if !foundLater {
		t.Fatal("rotating cursor did not admit any job beyond the first bounded page")
	}
}

func TestStopCancelsScheduledTestBeforeStart(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	start := time.Now().Add(time.Hour).UTC()
	runner.mu.Lock()
	runner.tests["scheduled-cancel"] = &ManagedTest{ID: "scheduled-cancel", Name: "Scheduled", Status: "SCHEDULED", ScheduledStartAt: &start, CreatedAt: time.Now().UTC()}
	runner.mu.Unlock()
	if err := runner.StopTest("scheduled-cancel"); err != nil {
		t.Fatalf("StopTest: %v", err)
	}
	test, ok := runner.GetTest("scheduled-cancel")
	if !ok || test.Status != "ABORTED" || test.EndedAt == nil {
		t.Fatalf("cancelled scheduled test = %#v, found=%v", test, ok)
	}
}

func TestExecutionPlanOperatorDecisionIsDurableUntilResolution(t *testing.T) {
	store, _, schedule, now := newExecutionPlanScheduleRecoveryFixture(t, "parallel")
	schedule.MissedRunPolicy = "require_operator_decision"
	schedule.LastError = "operator decision required"
	schedule.NextRequiredAction = "operator_decision_required"
	if changed, err := store.UpdateExecutionPlanScheduleIfRevision(schedule, schedule.Revision); err != nil || !changed {
		t.Fatalf("update schedule = %v, %v", changed, err)
	}
	occurrence := &storage.ExecutionPlanScheduleOccurrence{
		ID: "operator-decision-occurrence", ScheduleID: schedule.ID, PlannedAt: schedule.NextRunAt.UTC(), Status: "blocked",
		ReasonCode: "missed_run_requires_decision", ConflictSummary: "operator decision required", CreatedAt: now, UpdatedAt: now,
	}
	if claimed, err := store.ClaimExecutionPlanScheduleOccurrenceIfCurrent(occurrence); err != nil || !claimed {
		t.Fatalf("claim occurrence = %v, %v", claimed, err)
	}
	runner := NewTestRunner(1, NewHub(), store)
	before := schedule.NextRunAt.UTC()
	if err := runner.runDueExecutionPlanScheduleOnce(schedule, now); err != nil {
		t.Fatalf("blocked sweep: %v", err)
	}
	stillBlocked, err := store.GetExecutionPlanSchedule(schedule.ID)
	if err != nil || stillBlocked.NextRunAt == nil || !stillBlocked.NextRunAt.Equal(before) || stillBlocked.NextRequiredAction != "operator_decision_required" {
		t.Fatalf("blocked schedule advanced without a decision: %#v, %v", stillBlocked, err)
	}
	resolvedOccurrence, resolvedSchedule, found, err := runner.ResolveExecutionPlanScheduleOccurrence(schedule.PlanID, schedule.ID, occurrence.ID, ScheduleOccurrenceResolutionRequest{Action: "catch_up"})
	if err != nil || !found {
		t.Fatalf("resolve = occurrence %#v schedule %#v found=%v err=%v", resolvedOccurrence, resolvedSchedule, found, err)
	}
	if resolvedOccurrence.Status != "materializing" || resolvedOccurrence.RunID == "" || resolvedSchedule.NextRequiredAction != "" {
		t.Fatalf("resolved state = occurrence %#v schedule %#v", resolvedOccurrence, resolvedSchedule)
	}
	// Repeating the same operator command is fenced by occurrence revision/state.
	_, _, found, err = runner.ResolveExecutionPlanScheduleOccurrence(schedule.PlanID, schedule.ID, occurrence.ID, ScheduleOccurrenceResolutionRequest{Action: "catch_up"})
	if !found || !errorsIsAny(err, errScheduleOccurrenceNotResolvable, errScheduleConcurrentChange) {
		t.Fatalf("duplicate resolution = found=%v err=%v", found, err)
	}
}

func errorsIsAny(err error, targets ...error) bool {
	for _, target := range targets {
		if errors.Is(err, target) {
			return true
		}
	}
	return false
}

func TestMalformedScenarioApprovalBodyCannotMutate(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir() + "/apexload.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 1})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scenarios/missing/schedules/missing/approvals/missing/approve", bytes.NewBufferString(`{"reason":`))
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, req)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("malformed approval status = %d, body=%s", response.Code, response.Body.String())
	}
}
