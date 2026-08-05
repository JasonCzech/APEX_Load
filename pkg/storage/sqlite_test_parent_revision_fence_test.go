package storage

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func seedTestParentRevisionFixture(t *testing.T, name string) (*SQLiteStore, *Project, *Application, *Release) {
	t.Helper()
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), name+".db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	project := &Project{ID: name + "-project", Name: name, CreatedAt: now, UpdatedAt: now}
	if err := store.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	application := &Application{ID: name + "-application", ProjectID: project.ID, Name: name, CreatedAt: now, UpdatedAt: now}
	if err := store.SaveApplication(application); err != nil {
		t.Fatal(err)
	}
	release := &Release{ID: name + "-release", ApplicationID: application.ID, ProjectID: project.ID, Name: name, CreatedAt: now, UpdatedAt: now}
	if err := store.SaveRelease(release); err != nil {
		t.Fatal(err)
	}
	project, err = store.GetProject(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	application, err = store.GetApplication(application.ID)
	if err != nil {
		t.Fatal(err)
	}
	release, err = store.GetRelease(release.ID)
	if err != nil {
		t.Fatal(err)
	}
	return store, project, application, release
}

func parentRevisionTestRecord(id string, now time.Time, project *Project, application *Application, release *Release) *TestRecord {
	return &TestRecord{
		ID: id, Name: id, Status: "QUEUED", ProjectID: project.ID,
		ApplicationID: application.ID, ReleaseID: release.ID,
		TotalVUsers: 1, Duration: int64(time.Minute), StartTime: now, Groups: []any{},
		ExpectedProjectUpdatedAt: project.UpdatedAt, ExpectedApplicationUpdatedAt: application.UpdatedAt,
		ExpectedReleaseUpdatedAt: release.UpdatedAt,
	}
}

func TestCreateTestIfAbsentRejectsChangedParentRevisions(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*SQLiteStore, *Project, *Application, *Release) error
		wantErr string
	}{
		{
			name: "project",
			mutate: func(store *SQLiteStore, project *Project, _ *Application, _ *Release) error {
				project.Settings.MaxVUsersPerTest = 1
				return store.SaveProject(project)
			},
			wantErr: "test project",
		},
		{
			name: "application",
			mutate: func(store *SQLiteStore, _ *Project, application *Application, _ *Release) error {
				application.Name += " changed"
				return store.SaveApplication(application)
			},
			wantErr: "test application",
		},
		{
			name: "release",
			mutate: func(store *SQLiteStore, _ *Project, _ *Application, release *Release) error {
				release.Name += " changed"
				return store.SaveRelease(release)
			},
			wantErr: "test release",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			store, project, application, release := seedTestParentRevisionFixture(t, "parent-revision-"+testCase.name)
			record := parentRevisionTestRecord("parent-revision-test-"+testCase.name, time.Now().UTC(), project, application, release)
			if err := testCase.mutate(store, project, application, release); err != nil {
				t.Fatalf("mutate parent: %v", err)
			}
			created, err := store.CreateTestIfAbsent(record)
			if err == nil || created || !strings.Contains(err.Error(), testCase.wantErr) || !strings.Contains(err.Error(), "changed while the run was being prepared") {
				t.Fatalf("CreateTestIfAbsent = (%t, %v), want %s revision rejection", created, err, testCase.name)
			}
			if _, err := store.GetTest(record.ID); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("rejected create persisted test: %v", err)
			}
		})
	}
}

func TestAtomicTimeslotCreateRejectsChangedProjectRevisionAndRollsBack(t *testing.T) {
	store, project, application, release := seedTestParentRevisionFixture(t, "timeslot-parent-revision")
	now := time.Now().UTC()
	const slotID = "timeslot-parent-revision-slot"
	capacity := 10
	if err := store.SaveCapacityReservation(&CapacityReservation{
		ID: slotID, ProjectID: project.ID, StartAt: now.Add(-time.Minute), EndAt: now.Add(time.Hour),
		ApprovalState: "approved", RequestedCapacity: &CapacityReservationRequest{VUsers: &capacity},
	}); err != nil {
		t.Fatal(err)
	}
	record := parentRevisionTestRecord("timeslot-parent-revision-test", now, project, application, release)
	project.Settings.MaxVUsersPerTest = 1
	if err := store.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	result, err := store.CreateTestAndBindCapacityReservation(record, slotID, now)
	if err == nil || result != CapacityBindingFenced || !strings.Contains(err.Error(), "test project") {
		t.Fatalf("atomic create = (%q, %v), want parent-revision fence", result, err)
	}
	if _, err := store.GetTest(record.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("fenced atomic create persisted test: %v", err)
	}
	reservation, err := store.GetCapacityReservation(slotID)
	if err != nil {
		t.Fatal(err)
	}
	if reservation.BoundTestID != nil {
		t.Fatalf("fenced atomic create partially bound reservation: %v", reservation.BoundTestID)
	}
}

func TestStartTransitionRejectsChangedProjectRevision(t *testing.T) {
	store, project, application, release := seedTestParentRevisionFixture(t, "start-parent-revision")
	now := time.Now().UTC()
	record := parentRevisionTestRecord("start-parent-revision-test", now, project, application, release)
	if err := store.SaveTest(record); err != nil {
		t.Fatalf("SaveTest: %v", err)
	}

	start, err := store.GetTest(record.ID)
	if err != nil {
		t.Fatalf("GetTest: %v", err)
	}
	start.Status = "INITIALIZING"
	start.ExpectedProjectUpdatedAt = project.UpdatedAt
	project.Settings.MaxVUsersPerTest = 1
	if err := store.SaveProject(project); err != nil {
		t.Fatalf("mutate project policy/quota revision: %v", err)
	}

	if err := store.SaveTestStartIfNoDeploymentLease(start, DefaultDeploymentLeaseName, "", ""); !errors.Is(err, ErrTestProjectRevisionChanged) {
		t.Fatalf("start error = %v, want ErrTestProjectRevisionChanged", err)
	}
	stored, err := store.GetTest(record.ID)
	if err != nil {
		t.Fatalf("GetTest after fenced start: %v", err)
	}
	if stored.Status != "QUEUED" {
		t.Fatalf("fenced start persisted status %q, want QUEUED", stored.Status)
	}
}

func TestParentRevisionQueriesRewriteForPostgres(t *testing.T) {
	for name, query := range map[string]string{
		"project": testProjectRevisionSelect, "application": testApplicationRevisionSelect, "release": testReleaseRevisionSelect,
	} {
		rewritten := rewriteSQL(query, storeDialectPostgres)
		if strings.Contains(rewritten, "?") || !strings.Contains(rewritten, "$1") || !strings.Contains(rewritten, "updated_at") {
			t.Fatalf("Postgres %s parent-revision query = %s", name, rewritten)
		}
	}
}
