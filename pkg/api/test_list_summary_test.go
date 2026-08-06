package api

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
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

func TestCloneManagedTestSummaryUsesPreservedTerminalSparkline(t *testing.T) {
	test := &ManagedTest{
		ID:          "run-terminal",
		Status:      "COMPLETE",
		LiveMetrics: &LiveMetrics{Timestamp: time.Unix(3, 0), TPS: 13},
		History:     []*LiveMetrics{{Timestamp: time.Unix(3, 0), TPS: 13}},
		Sparkline: []TestSparklinePoint{
			{Timestamp: time.Unix(1, 0), TPS: 5},
			{Timestamp: time.Unix(2, 0), TPS: 9},
			{Timestamp: time.Unix(3, 0), TPS: 13},
		},
	}

	cloned := cloneManagedTestSummaryForReadLocked(test)
	if cloned.History != nil || len(cloned.Sparkline) != 3 || cloned.Sparkline[0].TPS != 5 {
		t.Fatalf("terminal summary = history:%#v sparkline:%#v", cloned.History, cloned.Sparkline)
	}
	cloned.Sparkline[0].TPS = 99
	if test.Sparkline[0].TPS != 5 {
		t.Fatal("terminal summary sparkline aliases canonical state")
	}
}

func TestOperationalTestSnapshotsOmitRetainedHistory(t *testing.T) {
	scheduledAt := time.Date(2026, time.August, 5, 12, 0, 0, 0, time.UTC)
	source := &ManagedTest{
		ID:               "run-operational",
		Status:           "SCHEDULED",
		Groups:           []TestGroup{{Name: "group", Protocol: "http", VUsers: 2, script: &dsl.Script{ID: "script"}}},
		Duration:         10 * time.Minute,
		ScheduledStartAt: &scheduledAt,
		ResourceBaseline: true,
		StartDelay:       "45s",
		LiveMetrics: &LiveMetrics{
			TPS:          12,
			Transactions: map[string]*TxMetrics{"checkout": {Count: 3}},
		},
		History:   []*LiveMetrics{{Timestamp: time.Unix(1, 0), TPS: 5}},
		Sparkline: []TestSparklinePoint{{Timestamp: time.Unix(1, 0), TPS: 5}},
	}
	runner := &TestRunner{tests: map[string]*ManagedTest{source.ID: source}}

	snapshots := runner.listOperationalTestSnapshots()
	if len(snapshots) != 1 {
		t.Fatalf("snapshot count = %d, want 1", len(snapshots))
	}
	snapshot := snapshots[0]
	if snapshot.History != nil || snapshot.Sparkline != nil {
		t.Fatalf("operational snapshot retained history-derived data: history=%d sparkline=%d", len(snapshot.History), len(snapshot.Sparkline))
	}
	if snapshot.LiveMetrics == source.LiveMetrics || snapshot.LiveMetrics.TPS != 12 {
		t.Fatal("operational snapshot did not retain independent scalar live metrics")
	}
	if snapshot.LiveMetrics.Transactions != nil {
		t.Fatalf("operational snapshot retained transaction map: %#v", snapshot.LiveMetrics.Transactions)
	}
	if !snapshot.ResourceBaseline || snapshot.StartDelay != "45s" {
		t.Fatalf("operational snapshot launch timing = baseline %v, delay %q", snapshot.ResourceBaseline, snapshot.StartDelay)
	}
	wantEnd := launchWindowForTest(source, scheduledAt).EndAt
	event := runCalendarEventFromTest(snapshot, nil, SystemInventoryResponse{}, nil, scheduledAt)
	if event.EndAt == nil || !event.EndAt.Equal(wantEnd) {
		t.Fatalf("calendar end = %v, want launch window end %v", event.EndAt, wantEnd)
	}
	if snapshot.Groups[0].script != nil {
		t.Fatal("operational snapshot retained an executable script graph")
	}
	snapshot.Groups[0].VUsers = 99
	if source.Groups[0].VUsers != 2 || source.LiveMetrics.Transactions["checkout"].Count != 3 {
		t.Fatal("mutating operational snapshot changed the live runner state")
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

func TestParseTestListOptionsAppliesBoundedDefault(t *testing.T) {
	request := httptest.NewRequest("GET", "/api/v1/tests", nil)
	recorder := httptest.NewRecorder()
	summary, activeOnly, limit, offset, ok := parseTestListOptions(recorder, request)
	if !ok || summary || activeOnly || limit != defaultTestListLimit || offset != 0 {
		t.Fatalf("unexpected defaults: summary=%v activeOnly=%v limit=%d offset=%d ok=%v", summary, activeOnly, limit, offset, ok)
	}
}

func TestIsActiveTestListStatus(t *testing.T) {
	tests := []struct {
		status string
		want   bool
	}{
		{status: "RUNNING", want: true},
		{status: "RESERVED", want: true},
		{status: "QUEUED", want: true},
		{status: "PENDING", want: true},
		{status: "INITIALIZING", want: true},
		{status: "STARTING", want: true},
		{status: "shakeout", want: true},
		{status: "SHAKEOUT_REVIEW", want: true},
		{status: "PAUSED", want: true},
		{status: "STOPPING", want: true},
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

func TestSortTestListPrefersActiveTestsFirst(t *testing.T) {
	olderActive := &ManagedTest{ID: "active", Status: "RUNNING", CreatedAt: time.Unix(1, 0)}
	newerComplete := &ManagedTest{ID: "complete", Status: "COMPLETE", CreatedAt: time.Unix(2, 0)}
	sameTimeLast := &ManagedTest{ID: "same-z", Status: "COMPLETE", CreatedAt: time.Unix(3, 0)}
	sameTimeFirst := &ManagedTest{ID: "same-a", Status: "COMPLETE", CreatedAt: time.Unix(3, 0)}
	tests := []*ManagedTest{olderActive, sameTimeLast, newerComplete, sameTimeFirst}

	sortTestList(tests)

	want := []string{"active", "same-a", "same-z", "complete"}
	for index, id := range want {
		if tests[index].ID != id {
			t.Fatalf("tests[%d] = %q, want %q (order=%v)", index, tests[index].ID, id, []string{tests[0].ID, tests[1].ID, tests[2].ID, tests[3].ID})
		}
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
		t.Fatalf("page = %#v, want the active run to be prioritized first", page)
	}
	if page[0].History != nil {
		t.Fatal("summary page retained full history")
	}
	if len(page[0].Sparkline) == 0 {
		t.Fatal("summary page omitted the bounded sparkline")
	}
}

func TestHandleListTestsCombinesProjectAndContextFilters(t *testing.T) {
	runner := &TestRunner{tests: map[string]*ManagedTest{
		"matching": {
			ID: "matching", ProjectID: "project-a", ApplicationID: "application-a", ReleaseID: "release-a", Status: "COMPLETE",
		},
		"other-project": {
			ID: "other-project", ProjectID: "project-b", ApplicationID: "application-a", ReleaseID: "release-a", Status: "COMPLETE",
		},
		"other-application": {
			ID: "other-application", ProjectID: "project-a", ApplicationID: "application-b", ReleaseID: "release-a", Status: "COMPLETE",
		},
		"other-release": {
			ID: "other-release", ProjectID: "project-a", ApplicationID: "application-a", ReleaseID: "release-b", Status: "COMPLETE",
		},
	}}
	server := &Server{runner: runner}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/api/v1/tests?project_id=project-a&application_id=application-a&release_id=release-a&limit=10", nil)

	server.handleListTests(recorder, request)

	var response struct {
		Tests []*ManagedTest `json:"tests"`
		Total int            `json:"total"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != 200 || response.Total != 1 || len(response.Tests) != 1 || response.Tests[0].ID != "matching" {
		t.Fatalf("combined project/application/release filters returned status=%d response=%+v", recorder.Code, response)
	}
}

func TestHandleListTestsUsesBoundedSummarySnapshotsByDefault(t *testing.T) {
	history := make([]*LiveMetrics, 32)
	for index := range history {
		history[index] = &LiveMetrics{Timestamp: time.Unix(int64(index+1), 0), TPS: float64(index)}
	}
	runner := &TestRunner{tests: make(map[string]*ManagedTest, defaultTestListLimit+1)}
	for index := 0; index <= defaultTestListLimit; index++ {
		id := fmt.Sprintf("run-%03d", index)
		runner.tests[id] = &ManagedTest{ID: id, Status: "COMPLETE", CreatedAt: time.Unix(int64(index+1), 0), History: history}
	}
	server := &Server{runner: runner}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/api/v1/tests", nil)

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
	if recorder.Code != 200 || response.Count != defaultTestListLimit || response.Total != defaultTestListLimit+1 || response.Limit != defaultTestListLimit || response.Summary {
		t.Fatalf("unexpected default list metadata: status=%d response=%+v", recorder.Code, response)
	}
	if len(response.Tests) != defaultTestListLimit || response.Tests[0].ID != "run-200" || response.Tests[len(response.Tests)-1].ID != "run-001" {
		t.Fatalf("default bounded page changed newest-first ordering: first=%q last=%q", response.Tests[0].ID, response.Tests[len(response.Tests)-1].ID)
	}
	for _, test := range response.Tests {
		if test.History != nil {
			t.Fatalf("default list exposed history for %s", test.ID)
		}
	}
}

func TestHandleListTestsReturnsBoundedSummaryMetadata(t *testing.T) {
	runner := &TestRunner{tests: map[string]*ManagedTest{
		"run-1": {ID: "run-1", Status: "RUNNING", CreatedAt: time.Unix(1, 0), History: []*LiveMetrics{{TPS: 1}}},
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
	// run-1 is older but RUNNING, so the active-first tiebreak in sortTestList
	// puts it ahead of the newer terminal run-2 inside the bounded window.
	if len(response.Tests) != 1 || response.Tests[0].ID != "run-1" || response.Tests[0].History != nil {
		t.Fatalf("unexpected summary page: %#v", response.Tests)
	}
}

func TestHandleScenarioResultsUsesBoundedSummaryPage(t *testing.T) {
	runner := &TestRunner{tests: make(map[string]*ManagedTest, defaultTestListLimit+2)}
	sharedHistory := []*LiveMetrics{{Timestamp: time.Unix(1, 0), TPS: 1}}
	for index := 0; index <= defaultTestListLimit; index++ {
		id := fmt.Sprintf("scenario-run-%03d", index)
		runner.tests[id] = &ManagedTest{
			ID: id, ScenarioID: "scenario-1", Status: "COMPLETE",
			CreatedAt: time.Unix(int64(index+1), 0), History: sharedHistory,
		}
	}
	runner.tests["other-scenario"] = &ManagedTest{ID: "other-scenario", ScenarioID: "scenario-2", Status: "COMPLETE"}
	server := &Server{runner: runner}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/api/v1/scenarios/scenario-1/results", nil)
	request.SetPathValue("id", "scenario-1")

	server.handleScenarioResults(recorder, request)

	var response struct {
		Tests  []*ManagedTest `json:"tests"`
		Count  int            `json:"count"`
		Total  int            `json:"total"`
		Limit  int            `json:"limit"`
		Offset int            `json:"offset"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != 200 || response.Count != defaultTestListLimit || response.Total != defaultTestListLimit+1 || response.Limit != defaultTestListLimit || response.Offset != 0 {
		t.Fatalf("unexpected scenario results metadata: status=%d response=%+v", recorder.Code, response)
	}
	for _, test := range response.Tests {
		if test.ScenarioID != "scenario-1" || test.History != nil {
			t.Fatalf("scenario result was unscoped or retained rich history: %#v", test)
		}
	}
}
