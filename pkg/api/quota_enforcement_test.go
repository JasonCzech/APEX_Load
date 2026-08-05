package api

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func newQuotaTestRunner(t *testing.T, settings storage.ProjectSettings) *TestRunner {
	t.Helper()
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	project := &storage.Project{ID: "project-1", Name: "Payments", Settings: settings, CreatedAt: time.Now()}
	if err := store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}

	runner := NewTestRunner(10, NewHub(), store)
	runner.StoreScript(&dsl.Script{
		ID:        "script-1",
		Name:      "Checkout",
		Protocol:  "http",
		Version:   "1.0.0",
		ProjectID: "project-1",
		Config: dsl.ScriptConfig{
			BaseURL:  "https://example.com",
			RunLogic: dsl.RunLogicConfig{Iterations: 1},
		},
		Actions: []dsl.Action{{Name: "Home", Type: "GET", Target: "/"}},
	})
	return runner
}

// These helpers deliberately load all durable admission evidence before taking
// the runner lock. Keeping tests on the same contract as production prevents a
// convenient test-only wrapper from reintroducing storage I/O under tr.mu.
func projectQuotaForTest(t *testing.T, runner *TestRunner, test *ManagedTest) projectQuotaSnapshot {
	t.Helper()
	evidence := loadProjectQuotaEvidence(runner.projectStore, test.ProjectID, testAssignedPoolID(test))
	quota, ok := evidence.quotaFor(test)
	if !ok {
		t.Fatalf("project quota evidence unavailable: %v", evidence.err)
	}
	return quota
}

func evaluateQuotaBlockWithLoadedEvidenceForTest(t *testing.T, runner *TestRunner, test *ManagedTest, excludeTestID string) (bool, string) {
	t.Helper()
	now := time.Now().UTC()
	activeSlots, reservationErr := runner.loadBoundTimeslotsActiveAt(now)
	quota := projectQuotaForTest(t, runner, test)
	runner.mu.RLock()
	defer runner.mu.RUnlock()
	return runner.evaluateQuotaBlockWithTimeslotsAndProjectQuotaLocked(test, excludeTestID, now, activeSlots, reservationErr, quota)
}

func evaluateReservedCapacityBlockWithLoadedEvidenceForTest(t *testing.T, runner *TestRunner, test *ManagedTest, excludeTestID string) (bool, string) {
	t.Helper()
	now := time.Now().UTC()
	activeSlots, reservationErr := runner.loadBoundTimeslotsActiveAt(now)
	runner.mu.RLock()
	defer runner.mu.RUnlock()
	return runner.evaluateReservedCapacityBlockWithTimeslotsLocked(test, excludeTestID, now, activeSlots, reservationErr)
}

func launchTimeslotRevalidationWithLoadedEvidenceForTest(t *testing.T, runner *TestRunner, test *ManagedTest, now time.Time) error {
	t.Helper()
	if test == nil || test.TimeslotID == nil || runner.reservationStore == nil {
		return nil
	}
	launchStart := now.UTC()
	if test.StartedAt != nil && !test.StartedAt.IsZero() && (test.Status == "RUNNING" || test.Status == "PAUSED" || test.Status == "STOPPING") {
		launchStart = test.StartedAt.UTC()
	}
	evidence := runner.loadLaunchWindowEvidenceWithBoundTimeslot(launchWindowForTest(test, launchStart), *test.TimeslotID)
	runner.mu.RLock()
	defer runner.mu.RUnlock()
	return runner.launchTimeslotRevalidationErrorWithEvidenceLocked(test, now, evidence)
}

func TestEnforceTestQuotaPerTestCaps(t *testing.T) {
	runner := newQuotaTestRunner(t, storage.ProjectSettings{MaxVUsersPerTest: 50, MaxTestDurationMinutes: 10})
	quota := projectQuotaForTest(t, runner, &ManagedTest{ProjectID: "project-1"})

	cases := []struct {
		name    string
		vusers  int
		dur     time.Duration
		status  string
		wantErr string
	}{
		{"vuser cap exceeded", 100, 5 * time.Minute, "QUEUED", "per-test VUser cap"},
		{"duration cap exceeded", 30, 20 * time.Minute, "QUEUED", "max duration"},
		{"within caps", 30, 5 * time.Minute, "QUEUED", ""},
		{"draft bypasses caps", 100, 20 * time.Minute, "DRAFT", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := enforceTestQuotaWithProjectQuota(quota, tc.vusers, tc.dur, tc.status)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestEnforceTestQuotaNoProjectStoreOrLimits(t *testing.T) {
	// No project limits configured -> never rejects.
	runner := newQuotaTestRunner(t, storage.ProjectSettings{})
	quota := projectQuotaForTest(t, runner, &ManagedTest{ProjectID: "project-1"})
	if err := enforceTestQuotaWithProjectQuota(quota, 100000, 100*time.Hour, "QUEUED"); err != nil {
		t.Fatalf("unlimited project should not reject, got %v", err)
	}
	// An unknown project is NOT "no settings, therefore no limits". Once a project
	// store is configured, admission evidence must resolve the project or it is
	// unavailable, and callers fail closed rather than admitting a run on stale or
	// absent quota authority (projectQuotaEvidence.quotaFor in capacity.go requires
	// projectResolved). Assert the evidence is withheld rather than silently
	// degrading to "unlimited".
	unknownEvidence := loadProjectQuotaEvidence(runner.projectStore, "nope", "")
	if _, ok := unknownEvidence.quotaFor(&ManagedTest{ProjectID: "nope"}); ok {
		t.Fatal("unknown project yielded usable quota evidence; admission must fail closed instead")
	}
}

func TestCreateTestRejectedByPerTestVUserCap(t *testing.T) {
	runner := newQuotaTestRunner(t, storage.ProjectSettings{MaxVUsersPerTest: 50})

	_, err := runner.CreateTest(CreateTestRequest{
		Name:      "Oversized",
		Duration:  "30s",
		ProjectID: "project-1",
		Groups:    []CreateGroupEntry{{Name: "G", ScriptID: "script-1", VUsers: 100, RampUp: "5s"}},
	})
	if err == nil || !strings.Contains(err.Error(), "per-test VUser cap") {
		t.Fatalf("expected per-test VUser cap rejection, got %v", err)
	}

	// Within the cap succeeds.
	tr, err := runner.CreateTest(CreateTestRequest{
		Name:      "Right-sized",
		Duration:  "30s",
		ProjectID: "project-1",
		Groups:    []CreateGroupEntry{{Name: "G", ScriptID: "script-1", VUsers: 40, RampUp: "5s"}},
	})
	if err != nil {
		t.Fatalf("within-cap test should succeed, got %v", err)
	}
	if tr.TotalVUsers != 40 {
		t.Fatalf("total vusers = %d, want 40", tr.TotalVUsers)
	}
}
