package storage

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestBrowserBaselineCandidatePages(t *testing.T) {
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "candidates.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	exerciseBrowserBaselineCandidatePages(t, s)
}

func exerciseBrowserBaselineCandidatePages(t *testing.T, s *SQLiteStore) {
	t.Helper()
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	if err := s.SaveProject(&Project{ID: "foreign", Name: "Foreign"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveApplication(&Application{ID: "app", ProjectID: DefaultProjectID, Name: "App"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveRelease(&Release{ID: "app-release", ProjectID: DefaultProjectID, ApplicationID: "app", Name: "Release"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveScenario(&Scenario{ID: "scenario", Name: "Scenario", Duration: "1m"}, nil); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		id, project, app, scenario, status string
		ago                                time.Duration
	}{
		{"z", DefaultProjectID, "", "", "COMPLETE", time.Minute},
		{"a", DefaultProjectID, "", "", "COMPLETE", time.Minute},
		{"older", DefaultProjectID, "", "", "COMPLETE", 2 * time.Minute},
		{"self", DefaultProjectID, "", "", "COMPLETE", time.Second},
		{"future", DefaultProjectID, "", "", "COMPLETE", -time.Minute},
		{"boundary", DefaultProjectID, "", "", "COMPLETE", 0},
		{"failed", DefaultProjectID, "", "", "FAILED", time.Second},
		{"foreign", "foreign", "", "", "COMPLETE", time.Second},
		{"app-run", DefaultProjectID, "app", "", "COMPLETE", time.Second},
		{"scenario-run", DefaultProjectID, "", "scenario", "COMPLETE", time.Second},
	} {
		end := now.Add(-row.ago)
		release := ""
		if row.app == "app" {
			release = "app-release"
		}
		if err := s.SaveTest(&TestRecord{ID: row.id, Name: row.id, ProjectID: row.project, ApplicationID: row.app, ReleaseID: release, ScenarioID: row.scenario, Status: row.status, StartTime: end.Add(-time.Minute), EndTime: &end}); err != nil {
			t.Fatal(err)
		}
	}
	q := BrowserBaselineCandidateQuery{ProjectID: DefaultProjectID, CurrentTestID: "self", ApplicationID: LegacyApplicationID, Limit: 1}
	var got []string
	for {
		page, more, err := s.ListBrowserBaselineCandidates(context.Background(), q)
		if err != nil || len(page) != 1 {
			t.Fatalf("page %+v %v", page, err)
		}
		got = append(got, page[0].TestID)
		if !more {
			break
		}
		q.AfterID = page[0].TestID
	}
	if !reflect.DeepEqual(got, []string{"a", "boundary", "future", "older", "z"}) {
		t.Fatalf("ordered IDs: %v", got)
	}
	q.AfterID = "older"
	q.Limit = MaxBrowserBaselineCandidatePage
	page, more, err := s.ListBrowserBaselineCandidates(context.Background(), q)
	if err != nil || more || len(page) != 1 || page[0].TestID != "z" {
		t.Fatalf("stable cursor: %+v %v %v", page, more, err)
	}
	q.AfterID = ""
	q.ApplicationID = "app"
	page, more, err = s.ListBrowserBaselineCandidates(context.Background(), q)
	if err != nil || more || len(page) != 1 || page[0].TestID != "app-run" {
		t.Fatalf("application scope: %+v %v", page, err)
	}
	q.ApplicationID = LegacyApplicationID
	q.ScenarioID = "scenario"
	page, _, err = s.ListBrowserBaselineCandidates(context.Background(), q)
	if err != nil || len(page) != 1 || page[0].TestID != "scenario-run" {
		t.Fatalf("scenario scope: %+v %v", page, err)
	}
	// Preserve the two historical default-project representations. The literal
	// project "default" is a separate tenant and must not enter this inventory.
	if err := s.SaveProject(&Project{ID: "default", Name: "Not the default project"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO projects(id,name,created_at,updated_at) VALUES ('','Legacy empty project',?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		id      string
		project any
	}{
		{"legacy-null", nil}, {"legacy-empty", ""}, {"wrong-default", "default"},
	} {
		end := now.Add(-time.Minute)
		if err := s.SaveTest(&TestRecord{ID: item.id, Name: item.id, ProjectID: DefaultProjectID, Status: "COMPLETE", StartTime: end.Add(-time.Minute), EndTime: &end}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(`UPDATE tests SET project_id=? WHERE id=?`, item.project, item.id); err != nil {
			t.Fatal(err)
		}
	}
	legacyQuery := BrowserBaselineCandidateQuery{ProjectID: DefaultProjectID, CurrentTestID: "self", ApplicationID: LegacyApplicationID, Limit: MaxBrowserBaselineCandidatePage}
	legacyPage, more, err := s.ListBrowserBaselineCandidates(context.Background(), legacyQuery)
	if err != nil || more {
		t.Fatalf("legacy inventory: %v %v", more, err)
	}
	ids := map[string]bool{}
	for _, candidate := range legacyPage {
		ids[candidate.TestID] = true
	}
	if !ids["legacy-null"] || !ids["legacy-empty"] || ids["wrong-default"] {
		t.Fatalf("legacy scope: %v", ids)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err = s.ListBrowserBaselineCandidates(ctx, q); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	for _, limit := range []int{0, -1, MaxBrowserBaselineCandidatePage + 1} {
		bad := q
		bad.Limit = limit
		if _, _, err = s.ListBrowserBaselineCandidates(context.Background(), bad); err == nil {
			t.Fatal("invalid limit accepted")
		}
	}
}

func TestBrowserBaselineIdentityReadsRespectPoolWaitDeadline(t *testing.T) {
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "pool.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.db.db.SetMaxOpenConns(1)
	conn, err := s.db.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, tc := range []struct {
		name string
		read func(context.Context) error
	}{
		{"test", func(ctx context.Context) error { _, err := s.GetTestContext(ctx, "run"); return err }},
		{"archive", func(ctx context.Context) error { _, err := s.GetRunArchiveContext(ctx, "run"); return err }},
		{"finalization", func(ctx context.Context) error { _, err := s.GetArchiveFinalizationJobContext(ctx, "run"); return err }},
		{"refresh", func(ctx context.Context) error { _, err := s.GetArchiveRefreshJobContext(ctx, "run"); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- tc.read(ctx) }()
			select {
			case err := <-done:
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("deadline: %v", err)
				}
			case <-time.After(time.Second):
				conn.Close()
				<-done
				t.Fatal("identity read ignored pool-wait deadline")
			}
		})
	}
}
