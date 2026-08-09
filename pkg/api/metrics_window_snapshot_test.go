package api

import (
	"testing"
	"time"
)

func TestGetTestMetricsWindowSelectsBeforeClone(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	base := time.Now().UTC().Add(-time.Minute)
	history := make([]*LiveMetrics, 32)
	for index := range history {
		history[index] = &LiveMetrics{
			Timestamp:      base.Add(time.Duration(index) * time.Second),
			ErrorBreakdown: map[string]int64{"shared": int64(index)},
		}
	}
	runner.mu.Lock()
	runner.tests["windowed"] = &ManagedTest{ID: "windowed", Status: "RUNNING", History: history, LiveMetrics: history[len(history)-1]}
	runner.mu.Unlock()

	snapshot, ok := runner.GetTestMetricsWindow("windowed", nil, 3)
	if !ok {
		t.Fatal("GetTestMetricsWindow did not find the test")
	}
	if got := len(snapshot.History); got != 3 {
		t.Fatalf("history length = %d, want 3", got)
	}
	if !snapshot.History[0].Timestamp.Equal(history[29].Timestamp) {
		t.Fatalf("first timestamp = %s, want %s", snapshot.History[0].Timestamp, history[29].Timestamp)
	}
	snapshot.History[0].ErrorBreakdown["shared"] = 999
	if history[29].ErrorBreakdown["shared"] == 999 {
		t.Fatal("windowed snapshot aliases canonical metrics")
	}
}

func TestGetTestSummaryOmitsHistoryForStreamAdmission(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	runner.mu.Lock()
	runner.tests["stream"] = &ManagedTest{
		ID:        "stream",
		ProjectID: "project-a",
		Status:    "RUNNING",
		History:   []*LiveMetrics{{Timestamp: time.Now().UTC()}},
	}
	runner.mu.Unlock()

	snapshot, ok := runner.GetTestSummary("stream")
	if !ok {
		t.Fatal("GetTestSummary did not find the test")
	}
	if snapshot.ProjectID != "project-a" {
		t.Fatalf("project id = %q, want project-a", snapshot.ProjectID)
	}
	if snapshot.History != nil {
		t.Fatalf("summary retained history: %#v", snapshot.History)
	}
}
