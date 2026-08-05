package api

import (
	"testing"
	"time"
)

func TestGroupVUsersSnapshotTracksRuntimeScale(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	test := &ManagedTest{Groups: []TestGroup{{Name: "buyers", VUsers: 10}, {Name: "search", VUsers: 5}}}

	first := runner.groupVUsersSnapshot(test)
	if first["buyers"] != 10 || first["search"] != 5 {
		t.Fatalf("initial snapshot = %#v", first)
	}
	runner.mu.Lock()
	test.Groups[0].VUsers = 25
	runner.mu.Unlock()
	second := runner.groupVUsersSnapshot(test)
	if second["buyers"] != 25 || second["search"] != 5 {
		t.Fatalf("post-scale snapshot = %#v, want buyers=25 search=5", second)
	}
	if first["buyers"] != 10 {
		t.Fatalf("prior snapshot was mutated: %#v", first)
	}
}

func TestGoalControllerDoesNotOverwriteUnscaledConcurrentGroup(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	test := &ManagedTest{
		ID: "goal-run",
		Groups: []TestGroup{
			{Name: "buyers", VUsers: 50},
			{Name: "canary", Role: GroupRoleObserver, VUsers: 50},
		},
		goalControl: &goalControllerState{},
	}
	live := &LiveMetrics{Timestamp: time.Now().UTC()}
	groupVUsers := map[string]int{"buyers": 50, "canary": 50}
	applyGoalControllerUpdateWithManagedScaler(runner, test, live, groupVUsers, goalControllerUpdate{
		ShouldScale: true,
		TargetTotal: 110,
		Status:      &GoalStatus{},
	}, func(groupName string, target int) error {
		if groupName != "buyers" || target != 60 {
			t.Fatalf("unexpected goal scale %s=%d", groupName, target)
		}
		// Simulate an operator scale landing after the goal controller's snapshot
		// but before its post-scale bookkeeping lock.
		runner.mu.Lock()
		test.Groups[1].VUsers = 80
		runner.mu.Unlock()
		return nil
	})
	if got := test.Groups[1].VUsers; got != 80 {
		t.Fatalf("unscaled concurrent group = %d, want operator value 80", got)
	}
	if got := groupVUsers["canary"]; got != 80 {
		t.Fatalf("goal status group snapshot = %d, want refreshed value 80", got)
	}
}

func TestVUserRampTargetsBoundPersistenceStepsAndKeepExactEndpoint(t *testing.T) {
	tests := []struct {
		name          string
		current       int
		target        int
		want          []int
		wantMaxLength int
	}{
		{name: "small increase preserves one-vuser steps", current: 1, target: 4, want: []int{2, 3, 4}},
		{name: "small decrease preserves one-vuser steps", current: 4, target: 1, want: []int{3, 2, 1}},
		{name: "large increase is bounded", current: 1, target: 10_001, wantMaxLength: maxVUserRampSteps},
		{name: "large decrease is bounded", current: 10_001, target: 1, wantMaxLength: maxVUserRampSteps},
		{name: "unchanged has no steps", current: 5, target: 5, want: []int{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := vuserRampTargets(tc.current, tc.target)
			if tc.want != nil && !intSlicesEqual(got, tc.want) {
				t.Fatalf("targets = %v, want %v", got, tc.want)
			}
			if tc.wantMaxLength > 0 && len(got) > tc.wantMaxLength {
				t.Fatalf("target count = %d, want <= %d", len(got), tc.wantMaxLength)
			}
			if tc.current != tc.target && (len(got) == 0 || got[len(got)-1] != tc.target) {
				t.Fatalf("targets = %v, want exact final target %d", got, tc.target)
			}
			for index := 1; index < len(got); index++ {
				if tc.target > tc.current && got[index] <= got[index-1] {
					t.Fatalf("increase is not monotonic: %v", got)
				}
				if tc.target < tc.current && got[index] >= got[index-1] {
					t.Fatalf("decrease is not monotonic: %v", got)
				}
			}
		})
	}
}

func TestUsageActiveDurationSecondsUsesObservedActiveRuntime(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	started := now.Add(-20 * time.Minute)
	endedEarly := started.Add(4*time.Minute + 30*time.Second)
	pausedAt := now.Add(-3 * time.Minute)
	tests := []struct {
		name string
		test *ManagedTest
		want int64
	}{
		{name: "never started", test: &ManagedTest{Duration: 8 * time.Hour}, want: 0},
		{name: "ended early", test: &ManagedTest{StartedAt: &started, EndedAt: &endedEarly, Duration: 8 * time.Hour}, want: 270},
		{name: "completed pauses removed", test: &ManagedTest{StartedAt: &started, CumulativePause: 2 * time.Minute}, want: 18 * 60},
		{name: "current pause removed", test: &ManagedTest{StartedAt: &started, CumulativePause: time.Minute, PausedAt: &pausedAt}, want: 16 * 60},
		{name: "negative clamps to zero", test: &ManagedTest{StartedAt: &started, CumulativePause: time.Hour}, want: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := usageActiveDurationSeconds(tc.test, now); got != tc.want {
				t.Fatalf("duration seconds = %d, want %d", got, tc.want)
			}
		})
	}

	queued := &ManagedTest{Status: "QUEUED", TotalVUsers: 1_000, Duration: 8 * time.Hour}
	completed := &ManagedTest{Status: "COMPLETE", TotalVUsers: 10, StartedAt: &started, EndedAt: &endedEarly}
	usage := usageAccountingFromTestsAt("", []*ManagedTest{queued, completed}, now)
	if usage.Totals.RunCount != 2 || usage.Totals.DurationSeconds != 270 || usage.Totals.VUserSeconds != 2_700 {
		t.Fatalf("usage totals = %#v, want statuses for both runs but execution usage only for completed run", usage.Totals)
	}
}
