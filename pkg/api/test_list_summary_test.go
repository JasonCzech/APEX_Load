package api

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCloneManagedTestSummaryForReadLockedOmitsHistory(t *testing.T) {
	test := &ManagedTest{
		ID:          "run-1",
		Status:      "RUNNING",
		LiveMetrics: &LiveMetrics{TPS: 12},
		History: []*LiveMetrics{
			{Timestamp: time.Unix(1, 0), TPS: 5},
			{Timestamp: time.Unix(2, 0), TPS: 9},
		},
	}

	cloned := cloneManagedTestSummaryForReadLocked(test)
	if cloned == nil {
		t.Fatal("expected a summary clone")
	}
	if cloned.History != nil {
		t.Fatalf("summary retained %d history records", len(cloned.History))
	}
	if len(cloned.Sparkline) == 0 {
		t.Fatal("expected a bounded sparkline in the summary clone")
	}
	if len(test.History) != 2 {
		t.Fatal("summary cloning mutated the source history")
	}
}

func TestParseTestListOptions(t *testing.T) {
	request := httptest.NewRequest("GET", "/api/v1/tests?summary=true&active_only=true&limit=250&offset=10", nil)
	recorder := httptest.NewRecorder()
	summary, activeOnly, limit, offset, ok := parseTestListOptions(recorder, request)
	if !ok || !summary || !activeOnly || limit != 250 || offset != 10 {
		t.Fatalf("unexpected options: summary=%v activeOnly=%v limit=%d offset=%d ok=%v", summary, activeOnly, limit, offset, ok)
	}
}

func TestIsActiveTestListStatus(t *testing.T) {
	tests := []struct {
		status string
		want   bool
	}{
		{status: "RUNNING", want: true},
		{status: "shakeout", want: true},
		{status: "PAUSED", want: true},
		{status: "COMPLETE", want: false},
		{status: "FAILED", want: false},
	}
	for _, test := range tests {
		t.Run(test.status, func(t *testing.T) {
			if got := isActiveTestListStatus(test.status); got != test.want {
				t.Fatalf("isActiveTestListStatus(%q) = %v, want %v", test.status, got, test.want)
			}
		})
	}
}

func TestSortTestListKeepsActiveRunsInsideBoundedResults(t *testing.T) {
	olderActive := &ManagedTest{ID: "active", Status: "RUNNING", CreatedAt: time.Unix(1, 0)}
	newerComplete := &ManagedTest{ID: "complete", Status: "COMPLETE", CreatedAt: time.Unix(2, 0)}
	tests := []*ManagedTest{newerComplete, olderActive}

	sortTestList(tests, true)

	if tests[0] != olderActive {
		t.Fatalf("bounded list placed %q before active run", tests[0].ID)
	}
}

func TestListTestSummaryPageBuildsOnlyTheBoundedWindow(t *testing.T) {
	runner := &TestRunner{tests: map[string]*ManagedTest{
		"active": {
			ID:        "active",
			Status:    "RUNNING",
			ReleaseID: "release-1",
			CreatedAt: time.Unix(1, 0),
			History: []*LiveMetrics{
				{Timestamp: time.Unix(1, 0), TPS: 5},
				{Timestamp: time.Unix(2, 0), TPS: 9},
			},
		},
		"complete": {
			ID:        "complete",
			Status:    "COMPLETE",
			ReleaseID: "release-1",
			CreatedAt: time.Unix(2, 0),
			History:   []*LiveMetrics{{Timestamp: time.Unix(2, 0), TPS: 12}},
		},
		"other-release": {
			ID:        "other-release",
			Status:    "COMPLETE",
			ReleaseID: "release-2",
			CreatedAt: time.Unix(3, 0),
		},
	}}

	page, total := runner.listTestSummaryPage(testSummaryListQuery{releaseID: "release-1", limit: 1})

	if total != 2 {
		t.Fatalf("total = %d, want 2", total)
	}
	if len(page) != 1 || page[0].ID != "active" {
		t.Fatalf("page = %#v, want the active run", page)
	}
	if page[0].History != nil {
		t.Fatal("summary page retained full history")
	}
	if len(page[0].Sparkline) == 0 {
		t.Fatal("summary page omitted the bounded sparkline")
	}
}

func TestHandleListTestsReturnsBoundedSummaryMetadata(t *testing.T) {
	runner := &TestRunner{tests: map[string]*ManagedTest{
		"run-1": {ID: "run-1", Status: "COMPLETE", CreatedAt: time.Unix(1, 0), History: []*LiveMetrics{{TPS: 1}}},
		"run-2": {ID: "run-2", Status: "COMPLETE", CreatedAt: time.Unix(2, 0), History: []*LiveMetrics{{TPS: 2}}},
	}}
	server := &Server{runner: runner}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/api/v1/tests?summary=true&limit=1", nil)

	server.handleListTests(recorder, request)

	var response struct {
		Tests   []*ManagedTest `json:"tests"`
		Count   int            `json:"count"`
		Total   int            `json:"total"`
		Limit   int            `json:"limit"`
		Summary bool           `json:"summary"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != 200 || response.Count != 1 || response.Total != 2 || response.Limit != 1 || !response.Summary {
		t.Fatalf("unexpected response metadata: status=%d response=%+v", recorder.Code, response)
	}
	if len(response.Tests) != 1 || response.Tests[0].ID != "run-2" || response.Tests[0].History != nil {
		t.Fatalf("unexpected summary page: %#v", response.Tests)
	}
}
