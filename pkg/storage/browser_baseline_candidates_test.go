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
