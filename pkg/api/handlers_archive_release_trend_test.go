package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

type releaseTrendArchiveCASStore struct {
	record  *storage.RunArchiveRecord
	updates int
}

func (s *releaseTrendArchiveCASStore) SaveRunArchive(record *storage.RunArchiveRecord) error {
	s.record = cloneRunArchiveRecord(record)
	return nil
}

func (s *releaseTrendArchiveCASStore) GetRunArchive(testID string) (*storage.RunArchiveRecord, error) {
	if s.record == nil || s.record.TestID != testID {
		return nil, sql.ErrNoRows
	}
	return cloneRunArchiveRecord(s.record), nil
}

func (s *releaseTrendArchiveCASStore) UpdateRunArchiveTrendPointIfUnchanged(expected *storage.RunArchiveRecord, trendPointJSON string) (bool, error) {
	if !sameRunArchiveRecordVersion(s.record, expected, nil) || s.record.TrendPointJSON != expected.TrendPointJSON {
		return false, nil
	}
	s.record.TrendPointJSON = trendPointJSON
	s.updates++
	return true, nil
}

// registerReleaseTrendRun wires an archived run into the runner so the archive-report
// handlers can build it and discover its release siblings.
func registerReleaseTrendRun(t *testing.T, server *Server, releaseID, id, name string, started, completed time.Time, slaBreached bool) {
	t.Helper()
	archive := writeReleaseTrendArchiveFixture(t, releaseID, id, name, started, completed, slaBreached)
	server.runner.mu.Lock()
	server.runner.tests[id] = &ManagedTest{
		ID:        id,
		Name:      name,
		Status:    "COMPLETE",
		ReleaseID: releaseID,
		CreatedAt: started,
		StartedAt: ptrTime(started),
		EndedAt:   ptrTime(completed),
		Archive:   archive,
	}
	server.runner.archives[id] = archive
	server.runner.mu.Unlock()
}

func getArchiveReportJSON(t *testing.T, server *Server, id string) RunArchiveReport {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+id+"/archive/report", nil)
	rr := httptest.NewRecorder()
	server.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("archive report status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var report RunArchiveReport
	if err := json.NewDecoder(rr.Body).Decode(&report); err != nil {
		t.Fatalf("decode archive report: %v", err)
	}
	return report
}

func TestArchiveReportAttachesReleaseTrend(t *testing.T) {
	server := NewServer(ServerConfig{})
	releaseID := "release-trend-attach"
	started := time.Date(2026, time.May, 18, 12, 0, 0, 0, time.UTC)
	registerReleaseTrendRun(t, server, releaseID, "run-early", "Early Run", started, started.Add(3*time.Minute), true)
	registerReleaseTrendRun(t, server, releaseID, "run-late", "Late Run", started.Add(5*time.Minute), started.Add(8*time.Minute), false)

	t.Run("json carries the baseline comparison", func(t *testing.T) {
		report := getArchiveReportJSON(t, server, "run-late")
		if report.ReleaseTrend == nil {
			t.Fatal("expected release_trend to be attached at the handler layer")
		}
		trend := report.ReleaseTrend
		if trend.ReleaseID != releaseID || trend.RunCount != 2 {
			t.Fatalf("trend release/run-count = %q/%d, want %q/2", trend.ReleaseID, trend.RunCount, releaseID)
		}
		if !trend.HasBaseline || trend.Baseline == nil || trend.Baseline.RunID != "run-early" {
			t.Fatalf("expected baseline run-early, got %#v", trend.Baseline)
		}
		if trend.Current.RunID != "run-late" {
			t.Fatalf("current run = %q, want run-late", trend.Current.RunID)
		}
		if trend.Deltas == nil {
			t.Fatal("baseline comparison should carry deltas")
		}
	})

	t.Run("executive html renders the trend section", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tests/run-late/report?profile=executive", nil)
		rr := httptest.NewRecorder()
		server.mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("html report status = %d, body = %s", rr.Code, rr.Body.String())
		}
		body := rr.Body.String()
		if !strings.Contains(body, "Release trend") {
			t.Fatalf("executive html missing Release Trend heading:\n%s", body)
		}
		if !strings.Contains(body, "Early Run") {
			t.Fatalf("executive html should name the baseline run:\n%s", body)
		}
	})

	t.Run("first run in a release degrades to a no-baseline note", func(t *testing.T) {
		soloRelease := "release-trend-solo"
		registerReleaseTrendRun(t, server, soloRelease, "run-solo", "Solo Run", started, started.Add(2*time.Minute), false)
		report := getArchiveReportJSON(t, server, "run-solo")
		if report.ReleaseTrend == nil {
			t.Fatal("expected release_trend for a single-run release")
		}
		if report.ReleaseTrend.HasBaseline {
			t.Fatalf("single-run release should have no baseline, got %#v", report.ReleaseTrend)
		}
		if report.ReleaseTrend.RunCount != 1 || report.ReleaseTrend.Note == "" {
			t.Fatalf("expected run_count 1 + explanatory note, got %#v", report.ReleaseTrend)
		}
	})
}

func TestArchiveReportOmitsReleaseTrendForUnassociatedRun(t *testing.T) {
	server := NewServer(ServerConfig{})
	started := time.Date(2026, time.May, 18, 12, 0, 0, 0, time.UTC)
	// Empty release id => no release context, so no trend is attached.
	archive := writeReleaseTrendArchiveFixture(t, "", "run-orphan", "Orphan Run", started, started.Add(2*time.Minute), false)
	server.runner.mu.Lock()
	server.runner.tests["run-orphan"] = &ManagedTest{
		ID:        "run-orphan",
		Name:      "Orphan Run",
		Status:    "COMPLETE",
		CreatedAt: started,
		EndedAt:   ptrTime(started.Add(2 * time.Minute)),
		Archive:   archive,
	}
	server.runner.archives["run-orphan"] = archive
	server.runner.mu.Unlock()

	report := getArchiveReportJSON(t, server, "run-orphan")
	if report.ReleaseTrend != nil {
		t.Fatalf("run with no release association should have nil release_trend, got %#v", report.ReleaseTrend)
	}
}

func TestReleaseTrendRepairsInvalidCompactProjectionAndLiveCache(t *testing.T) {
	for _, testCase := range []struct {
		name string
		raw  string
	}{
		{name: "malformed", raw: `{"run_id":`},
		{name: "mismatched run", raw: `{"run_id":"another-run"}`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			server := NewServer(ServerConfig{})
			const releaseID = "release-trend-repair"
			const runID = "run-repair"
			started := time.Date(2026, time.May, 18, 12, 0, 0, 0, time.UTC)
			record := writeReleaseTrendArchiveFixture(t, releaseID, runID, "Repair Run", started, started.Add(3*time.Minute), false)
			record.TrendPointJSON = testCase.raw
			store := &releaseTrendArchiveCASStore{record: cloneRunArchiveRecord(record)}
			server.runner.archiveStore = store
			server.runner.mu.Lock()
			server.runner.tests[runID] = &ManagedTest{
				ID: runID, Name: "Repair Run", Status: "COMPLETE", ReleaseID: releaseID,
				CreatedAt: started, StartedAt: ptrTime(started), EndedAt: ptrTime(started.Add(3 * time.Minute)),
				Archive: cloneRunArchiveRecord(record),
			}
			server.runner.archives[runID] = cloneRunArchiveRecord(record)
			server.runner.mu.Unlock()

			runs, err := server.collectReleaseTrendRuns(releaseID, false)
			if err != nil {
				t.Fatal(err)
			}
			if len(runs) != 1 || runs[0].Report == nil {
				t.Fatalf("repaired trend runs = %#v", runs)
			}
			if store.updates != 1 {
				t.Fatalf("targeted trend updates = %d, want 1", store.updates)
			}
			var persisted ReleaseTrendPoint
			if err := json.Unmarshal([]byte(store.record.TrendPointJSON), &persisted); err != nil || persisted.RunID != runID {
				t.Fatalf("persisted trend point = %q (%+v, %v)", store.record.TrendPointJSON, persisted, err)
			}
			cached, ok := server.runner.GetRunArchive(runID)
			if !ok || cached.TrendPointJSON != store.record.TrendPointJSON {
				t.Fatalf("runner archive cache was not repaired: ok=%v cached=%+v stored=%+v", ok, cached, store.record)
			}
			server.runner.mu.RLock()
			attached := cloneRunArchiveRecord(server.runner.tests[runID].Archive)
			server.runner.mu.RUnlock()
			if attached == nil || attached.TrendPointJSON != store.record.TrendPointJSON {
				t.Fatalf("managed test archive was not repaired: %+v", attached)
			}
		})
	}
}

func TestArchiveReportCanOmitReleaseTrendForInteractiveSummary(t *testing.T) {
	server := NewServer(ServerConfig{})
	start := time.Date(2026, time.May, 18, 12, 0, 0, 0, time.UTC)
	registerReleaseTrendRun(t, server, "release", "run-summary", "Summary", start, start.Add(time.Minute), false)
	rr := httptest.NewRecorder()
	server.mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/tests/run-summary/archive/report?include_release_trend=false", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	var report RunArchiveReport
	if err := json.Unmarshal(rr.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.ReleaseTrend != nil {
		t.Fatal("interactive report should omit release trend")
	}
	if report.Verdict == nil || report.Verdict.Status == "" {
		t.Fatal("interactive report must preserve computed acceptance")
	}
}

// The dashboard's report names the run's release rather than showing its ID.
func TestArchiveReportJSONAttachesIdentityNames(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "identity.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	server := NewServer(ServerConfig{Store: store})
	started := time.Date(2026, time.May, 18, 12, 0, 0, 0, time.UTC)
	// The store seeds the legacy-imported release.
	release, err := store.GetRelease("legacy-imported")
	if err != nil || release == nil || release.Name == "" {
		t.Fatalf("seeded release = %#v, %v", release, err)
	}
	registerReleaseTrendRun(t, server, "legacy-imported", "run-named", "Named Run", started, started.Add(3*time.Minute), false)
	report := getArchiveReportJSON(t, server, "run-named")
	if report.IdentityNames.Release != release.Name {
		t.Fatalf("identity_names.release = %q, want %q", report.IdentityNames.Release, release.Name)
	}
}
