package api

import (
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestScheduleWindowContainsAnchorsOvernightTailToStartDay(t *testing.T) {
	window := storage.ScenarioScheduleWindow{
		Name:       "monday overnight",
		DaysOfWeek: []int{int(time.Monday)},
		StartTime:  "22:00",
		EndTime:    "02:00",
		Timezone:   "UTC",
	}
	tests := []struct {
		name      string
		candidate time.Time
		want      bool
	}{
		{name: "before monday start", candidate: time.Date(2026, time.July, 20, 21, 59, 0, 0, time.UTC), want: false},
		{name: "monday start day", candidate: time.Date(2026, time.July, 20, 23, 0, 0, 0, time.UTC), want: true},
		{name: "tuesday tail", candidate: time.Date(2026, time.July, 21, 1, 0, 0, 0, time.UTC), want: true},
		{name: "tuesday end exclusive", candidate: time.Date(2026, time.July, 21, 2, 0, 0, 0, time.UTC), want: false},
		{name: "unrelated tuesday night", candidate: time.Date(2026, time.July, 21, 23, 0, 0, 0, time.UTC), want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := scheduleWindowContains(window, "UTC", tc.candidate)
			if err != nil {
				t.Fatalf("scheduleWindowContains: %v", err)
			}
			if got != tc.want {
				t.Fatalf("scheduleWindowContains(%s) = %v, want %v", tc.candidate, got, tc.want)
			}
		})
	}
}

func TestOvernightWeekdayWindowsProtectLaunchAndAvailability(t *testing.T) {
	tuesdayTail := time.Date(2026, time.July, 21, 1, 0, 0, 0, time.UTC)
	window := storage.ScenarioScheduleWindow{
		Name:       "monday overnight",
		DaysOfWeek: []int{int(time.Monday)},
		StartTime:  "22:00",
		EndTime:    "02:00",
		Timezone:   "UTC",
	}

	blackout := &storage.ScenarioSchedule{Timezone: "UTC", BlackoutWindows: []storage.ScenarioScheduleWindow{window}}
	allowed, err := scheduleAllowsCandidate(blackout, tuesdayTail)
	if err != nil {
		t.Fatalf("scheduleAllowsCandidate: %v", err)
	}
	if allowed {
		t.Fatal("overnight Monday blackout allowed a launch during its Tuesday tail")
	}

	availability := &storage.ScenarioSchedule{Timezone: "UTC", AvailabilityWindows: []storage.ScenarioScheduleWindow{window}}
	if !launchScheduleAvailabilityAllows(availability, tuesdayTail, tuesdayTail.Add(30*time.Minute)) {
		t.Fatal("overnight Monday availability rejected a launch during its Tuesday tail")
	}
}
