package api

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

// countingReleaseStore records how many times a scenario listing resolves each
// release. releaseRefLocked is a store read taken while tr.mu is held, so its
// call count is the honest proxy for the enrichment pass's cost.
type countingReleaseStore struct {
	mu    sync.Mutex
	calls map[string]int
	total int
}

func (s *countingReleaseStore) GetRelease(id string) (*storage.Release, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.calls == nil {
		s.calls = map[string]int{}
	}
	s.calls[id]++
	s.total++
	return &storage.Release{ID: id, Name: "release " + id, ApplicationID: "app-1"}, nil
}

func (s *countingReleaseStore) snapshot() (map[string]int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]int, len(s.calls))
	for id, count := range s.calls {
		out[id] = count
	}
	return out, s.total
}

func newScenarioEnrichmentRunner(t *testing.T, scenarioCount, testsPerScenario int) (*TestRunner, *countingReleaseStore) {
	t.Helper()
	runner := NewTestRunner(1, NewHub(), nil)
	releases := &countingReleaseStore{}
	runner.releaseStore = releases
	base := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	for scenarioIndex := 0; scenarioIndex < scenarioCount; scenarioIndex++ {
		scenarioID := fmt.Sprintf("scenario-%d", scenarioIndex)
		runner.scenarios[scenarioID] = &StoredScenario{
			ID:        scenarioID,
			Name:      scenarioID,
			ProjectID: "project-a",
		}
		for testIndex := 0; testIndex < testsPerScenario; testIndex++ {
			testID := fmt.Sprintf("%s-test-%d", scenarioID, testIndex)
			startedAt := base.Add(time.Duration(testIndex) * time.Minute)
			runner.tests[testID] = &ManagedTest{
				ID:         testID,
				ScenarioID: scenarioID,
				ReleaseID:  "release-shared",
				Status:     "COMPLETE",
				CreatedAt:  startedAt,
				StartedAt:  &startedAt,
			}
		}
	}
	return runner, releases
}

// TestListScenariosResolvesEachReleaseOnce pins the O5 remediation: the listing
// pass walks tr.tests once for the whole batch and resolves each distinct
// release exactly once. Before the change, releaseRefLocked was invoked once per
// test carrying a release (4 scenarios x 5 tests = 20 store reads for a single
// shared release).
func TestListScenariosResolvesEachReleaseOnce(t *testing.T) {
	runner, releases := newScenarioEnrichmentRunner(t, 4, 5)

	listed := runner.ListScenarios()
	if len(listed) != 4 {
		t.Fatalf("ListScenarios returned %d scenarios, want 4", len(listed))
	}

	calls, total := releases.snapshot()
	if total != 1 {
		t.Fatalf("GetRelease called %d times for one distinct release across the listing, want 1 (calls=%v)", total, calls)
	}
	if calls["release-shared"] != 1 {
		t.Fatalf("GetRelease(release-shared) called %d times, want 1", calls["release-shared"])
	}
}

// TestListScenariosFilteredSkipsEnrichmentForOtherProjects pins that the
// project filter runs before enrichment, so scenarios in other projects cost
// nothing. Enrichment never changes ProjectID, so this is behaviour-preserving.
func TestListScenariosFilteredSkipsEnrichmentForOtherProjects(t *testing.T) {
	runner, releases := newScenarioEnrichmentRunner(t, 3, 2)
	otherStarted := time.Date(2026, 8, 7, 9, 0, 0, 0, time.UTC)
	runner.scenarios["scenario-other"] = &StoredScenario{ID: "scenario-other", Name: "other", ProjectID: "project-b"}
	runner.tests["scenario-other-test-0"] = &ManagedTest{
		ID:         "scenario-other-test-0",
		ScenarioID: "scenario-other",
		ReleaseID:  "release-other",
		Status:     "COMPLETE",
		CreatedAt:  otherStarted,
		StartedAt:  &otherStarted,
	}

	listed := runner.ListScenariosFiltered("project-a")
	if len(listed) != 3 {
		t.Fatalf("ListScenariosFiltered returned %d scenarios, want 3", len(listed))
	}
	for _, scenario := range listed {
		if scenario.ProjectID != "project-a" {
			t.Fatalf("scenario %s leaked from project %q", scenario.ID, scenario.ProjectID)
		}
	}

	calls, _ := releases.snapshot()
	if calls["release-other"] != 0 {
		t.Fatalf("GetRelease(release-other) called %d times for an out-of-project scenario, want 0", calls["release-other"])
	}
	if calls["release-shared"] != 1 {
		t.Fatalf("GetRelease(release-shared) called %d times, want 1", calls["release-shared"])
	}
}

// TestScenarioListEnrichmentMatchesSingleScenarioEnrichment guards the
// refactor's equivalence: the batched rollup must produce the same
// result_count / last-run / associated-release projection as enriching each
// scenario on its own.
func TestScenarioListEnrichmentMatchesSingleScenarioEnrichment(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	runner.releaseStore = &countingReleaseStore{}
	base := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	runner.scenarios["s1"] = &StoredScenario{ID: "s1", Name: "s1", ProjectID: "project-a"}
	runner.scenarios["s2"] = &StoredScenario{ID: "s2", Name: "s2", ProjectID: "project-a"}
	runner.scenarios["s3"] = &StoredScenario{ID: "s3", Name: "s3", ProjectID: "project-a"}

	early := base
	late := base.Add(2 * time.Hour)
	ended := late.Add(30 * time.Minute)
	runner.tests["t1"] = &ManagedTest{ID: "t1", ScenarioID: "s1", Status: "COMPLETE", ReleaseID: "r1", CreatedAt: early, StartedAt: &early}
	runner.tests["t2"] = &ManagedTest{ID: "t2", ScenarioID: "s1", Status: "RUNNING", ReleaseID: "r2", CreatedAt: late, StartedAt: &late, EndedAt: &ended}
	runner.tests["t3"] = &ManagedTest{ID: "t3", ScenarioID: "s2", Status: "ABORTED", CreatedAt: early}

	listed := runner.ListScenarios()
	byID := map[string]*StoredScenario{}
	for _, scenario := range listed {
		byID[scenario.ID] = scenario
	}

	for id := range runner.scenarios {
		runner.mu.RLock()
		want := runner.enrichScenarioSummaryLocked(runner.scenarios[id])
		runner.mu.RUnlock()
		got := byID[id]
		if got == nil {
			t.Fatalf("scenario %s missing from listing", id)
		}
		if got.ResultCount != want.ResultCount {
			t.Fatalf("scenario %s ResultCount = %d, want %d", id, got.ResultCount, want.ResultCount)
		}
		if got.LastRunTestID != want.LastRunTestID {
			t.Fatalf("scenario %s LastRunTestID = %q, want %q", id, got.LastRunTestID, want.LastRunTestID)
		}
		if (got.LastRunAt == nil) != (want.LastRunAt == nil) {
			t.Fatalf("scenario %s LastRunAt nil-ness = %v, want %v", id, got.LastRunAt == nil, want.LastRunAt == nil)
		}
		if got.LastRunAt != nil && !got.LastRunAt.Equal(*want.LastRunAt) {
			t.Fatalf("scenario %s LastRunAt = %v, want %v", id, got.LastRunAt, want.LastRunAt)
		}
		if (got.LastRunEndedAt == nil) != (want.LastRunEndedAt == nil) {
			t.Fatalf("scenario %s LastRunEndedAt nil-ness = %v, want %v", id, got.LastRunEndedAt == nil, want.LastRunEndedAt == nil)
		}
		if got.LastRunEndedAt != nil && !got.LastRunEndedAt.Equal(*want.LastRunEndedAt) {
			t.Fatalf("scenario %s LastRunEndedAt = %v, want %v", id, got.LastRunEndedAt, want.LastRunEndedAt)
		}
		if got.AssociatedReleases == nil {
			t.Fatalf("scenario %s AssociatedReleases is nil, want a non-nil slice", id)
		}
		if len(got.AssociatedReleases) != len(want.AssociatedReleases) {
			t.Fatalf("scenario %s AssociatedReleases = %v, want %v", id, got.AssociatedReleases, want.AssociatedReleases)
		}
		gotIDs := map[string]struct{}{}
		for _, ref := range got.AssociatedReleases {
			gotIDs[ref.ID] = struct{}{}
		}
		for _, ref := range want.AssociatedReleases {
			if _, ok := gotIDs[ref.ID]; !ok {
				t.Fatalf("scenario %s missing release %q in %v", id, ref.ID, got.AssociatedReleases)
			}
		}
	}
}
