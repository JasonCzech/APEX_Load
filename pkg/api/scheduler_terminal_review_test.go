package api

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestCronFixedValueStepUsesFixedValueAsRangeStart(t *testing.T) {
	for _, minute := range []int{5, 15, 25, 35, 45, 55} {
		if !cronPartMatches("5/10", minute, 0, 59) {
			t.Fatalf("5/10 did not match minute %d", minute)
		}
	}
	for _, minute := range []int{0, 6, 14, 56} {
		if cronPartMatches("5/10", minute, 0, 59) {
			t.Fatalf("5/10 unexpectedly matched minute %d", minute)
		}
	}
}

func TestValidateCronRejectsOversizeAndSpecialTokenSteps(t *testing.T) {
	for _, expression := range []string{
		strings.Repeat("0", maxCronExpressionBytes+1),
		"0 9 L/2 * *",
		"0 9 * * 5L/2",
	} {
		if err := validateCronExpr(expression); err == nil {
			t.Fatalf("validateCronExpr(%q) succeeded, want rejection", expression)
		}
	}
}

func TestNextCronRunBoundsRejectedEligibleCandidates(t *testing.T) {
	from := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	_, err := nextCronRun("* * * * *", "UTC", from, func(time.Time) (bool, error) {
		return false, nil
	})
	if err == nil || !strings.Contains(err.Error(), "eligibility filters rejected") {
		t.Fatalf("nextCronRun error = %v, want bounded eligibility error", err)
	}
}

func TestPreviewEveryMinuteReturnsConsecutiveOccurrences(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	runner.mu.Lock()
	runner.scenarios["scenario-preview-minute"] = &StoredScenario{
		ID:       "scenario-preview-minute",
		Name:     "Minute Preview",
		Duration: "1m",
	}
	runner.mu.Unlock()

	preview, err := runner.PreviewScenarioScheduleRuns("scenario-preview-minute", CreateScenarioScheduleRequest{
		CronExpr: "* * * * *",
		Timezone: "UTC",
	}, 4)
	if err != nil {
		t.Fatalf("PreviewScenarioScheduleRuns returned error: %v", err)
	}
	if len(preview.Occurrences) != 4 {
		t.Fatalf("preview occurrences = %d, want 4", len(preview.Occurrences))
	}
	for index := 1; index < len(preview.Occurrences); index++ {
		if delta := preview.Occurrences[index].Sub(preview.Occurrences[index-1]); delta != time.Minute {
			t.Fatalf("occurrence %d delta = %s, want 1m", index, delta)
		}
	}
}

func TestScenarioScheduleInputValidationBoundsWindowsAndTimezone(t *testing.T) {
	if err := validateScheduleTimezone("Not/ARealZone"); err == nil {
		t.Fatal("invalid schedule timezone was accepted")
	}
	windows := make([]storage.ScenarioScheduleWindow, maxScenarioScheduleWindows+1)
	for index := range windows {
		windows[index] = storage.ScenarioScheduleWindow{StartTime: "00:00", EndTime: "01:00"}
	}
	if err := validateScenarioScheduleWindowSets(windows, nil, nil, "UTC"); err == nil {
		t.Fatal("oversized schedule window set was accepted")
	}
	if err := validateScenarioScheduleWindowSets([]storage.ScenarioScheduleWindow{{
		StartTime:  "00:00",
		EndTime:    "01:00",
		DaysOfWeek: []int{7},
	}}, nil, nil, "UTC"); err == nil {
		t.Fatal("out-of-range schedule weekday was accepted")
	}
}

func TestDueOneTimeScheduledStartsAreBoundedPerSweep(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	now := time.Now().UTC()
	runner.mu.Lock()
	for index := 0; index < maxOneTimeScheduledStartsPerSweep+20; index++ {
		id := fmt.Sprintf("scheduled-%04d", index)
		start := now.Add(-time.Minute)
		runner.tests[id] = &ManagedTest{ID: id, Status: "SCHEDULED", ScheduledStartAt: &start}
	}
	ids := runner.dueOneTimeScheduledTestIDsLocked(now, maxOneTimeScheduledStartsPerSweep)
	runner.mu.Unlock()
	if len(ids) != maxOneTimeScheduledStartsPerSweep {
		t.Fatalf("due IDs = %d, want bounded %d", len(ids), maxOneTimeScheduledStartsPerSweep)
	}
	if ids[0] != "scheduled-0000" || ids[len(ids)-1] != fmt.Sprintf("scheduled-%04d", maxOneTimeScheduledStartsPerSweep-1) {
		t.Fatalf("bounded due IDs are not deterministic: first=%q last=%q", ids[0], ids[len(ids)-1])
	}
}
