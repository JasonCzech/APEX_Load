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

func TestEnforceTestQuotaPerTestCaps(t *testing.T) {
	runner := newQuotaTestRunner(t, storage.ProjectSettings{MaxVUsersPerTest: 50, MaxTestDurationMinutes: 10})

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
			err := runner.enforceTestQuotaLocked("project-1", tc.vusers, tc.dur, tc.status)
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
	if err := runner.enforceTestQuotaLocked("project-1", 100000, 100*time.Hour, "QUEUED"); err != nil {
		t.Fatalf("unlimited project should not reject, got %v", err)
	}
	// Unknown project -> no settings -> no rejection.
	if err := runner.enforceTestQuotaLocked("nope", 100000, 100*time.Hour, "QUEUED"); err != nil {
		t.Fatalf("unknown project should not reject, got %v", err)
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
