package storage

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestGrafanaImportSessionSurvivesStoreReplicaAndClaimsOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "grafana-session.db")
	first, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	now := time.Now().UTC()
	if err := first.SaveProject(&Project{ID: "project-a", Name: "A", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	session := &GrafanaImportSession{ID: "session-a", ProjectID: "project-a", Name: "upload", Content: `{}`, SourceSHA256: "abc", CreatedAt: now, ExpiresAt: now.Add(time.Minute)}
	if err := first.SaveGrafanaImportSession(session, 5, 1<<20); err != nil {
		t.Fatal(err)
	}
	loaded, err := second.GetGrafanaImportSession(session.ID, session.ProjectID, now)
	if err != nil || loaded.Content != session.Content {
		t.Fatalf("replica load = %#v, %v", loaded, err)
	}
	claimed, ok, err := second.ClaimGrafanaImportSession(session.ID, session.ProjectID, "claim-1", now, time.Minute)
	if err != nil || !ok || claimed.ClaimToken != "claim-1" {
		t.Fatalf("first claim = %#v, %v, %v", claimed, ok, err)
	}
	if _, ok, err := first.ClaimGrafanaImportSession(session.ID, session.ProjectID, "claim-2", now, time.Minute); err != nil || ok {
		t.Fatalf("concurrent claim = %v, %v; want false, nil", ok, err)
	}
	if err := second.ReleaseGrafanaImportSession(session.ID, session.ProjectID, "claim-1"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := first.ClaimGrafanaImportSession(session.ID, session.ProjectID, "claim-3", now, time.Minute); err != nil || !ok {
		t.Fatalf("retry claim = %v, %v; want true, nil", ok, err)
	}
}

func TestGrafanaImportSessionExpiresAndIsProjectScoped(t *testing.T) {
	store := newGrafanaDashboardTestStore(t)
	now := time.Now().UTC()
	session := &GrafanaImportSession{ID: "session-a", ProjectID: "project-a", Content: `{}`, SourceSHA256: "abc", CreatedAt: now, ExpiresAt: now.Add(time.Minute)}
	if err := store.SaveGrafanaImportSession(session, 5, 1<<20); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		project string
		at      time.Time
	}{{"project-b", now}, {"project-a", now.Add(2 * time.Minute)}} {
		if _, err := store.GetGrafanaImportSession(session.ID, tc.project, tc.at); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("GetGrafanaImportSession(%q, %v) error = %v, want sql.ErrNoRows", tc.project, tc.at, err)
		}
	}
}

func TestGrafanaImportSessionStaleClaimCanBeRecovered(t *testing.T) {
	store := newGrafanaDashboardTestStore(t)
	now := time.Now().UTC()
	session := &GrafanaImportSession{ID: "session-stale", ProjectID: "project-a", Content: `{}`, SourceSHA256: "abc", CreatedAt: now, ExpiresAt: now.Add(10 * time.Minute)}
	if err := store.SaveGrafanaImportSession(session, 5, 1<<20); err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := store.ClaimGrafanaImportSession(session.ID, session.ProjectID, "first", now, time.Minute); err != nil || !claimed {
		t.Fatalf("initial claim = %v, %v; want true, nil", claimed, err)
	}
	if _, claimed, err := store.ClaimGrafanaImportSession(session.ID, session.ProjectID, "too-soon", now.Add(30*time.Second), time.Minute); err != nil || claimed {
		t.Fatalf("fresh claim replacement = %v, %v; want false, nil", claimed, err)
	}
	got, claimed, err := store.ClaimGrafanaImportSession(session.ID, session.ProjectID, "recovered", now.Add(2*time.Minute), time.Minute)
	if err != nil || !claimed || got.ClaimToken != "recovered" {
		t.Fatalf("stale claim recovery = %#v, %v, %v", got, claimed, err)
	}
}

func TestGrafanaImportCommitFencesStaleClaimAndConsumesSession(t *testing.T) {
	store := newGrafanaDashboardTestStore(t)
	now := time.Now().UTC()
	session := &GrafanaImportSession{ID: "session-fenced", ProjectID: "project-a", Content: `{}`, SourceSHA256: "abc", CreatedAt: now, ExpiresAt: now.Add(10 * time.Minute)}
	if err := store.SaveGrafanaImportSession(session, 5, 1<<20); err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := store.ClaimGrafanaImportSession(session.ID, session.ProjectID, "old", now, time.Minute); err != nil || !claimed {
		t.Fatalf("old claim = %v, %v", claimed, err)
	}
	if _, claimed, err := store.ClaimGrafanaImportSession(session.ID, session.ProjectID, "new", now.Add(2*time.Minute), time.Minute); err != nil || !claimed {
		t.Fatalf("replacement claim = %v, %v", claimed, err)
	}
	staleDashboard := sampleDashboard("dash-stale", "project-a")
	staleDashboard.ImportSessionID = session.ID
	staleDashboard.ImportClaimToken = "old"
	if err := store.SaveGrafanaDashboard(staleDashboard); !errors.Is(err, ErrGrafanaImportClaimLost) {
		t.Fatalf("stale commit error = %v, want ErrGrafanaImportClaimLost", err)
	}

	winner := sampleDashboard("dash-winner", "project-a")
	winner.ImportSessionID = session.ID
	winner.ImportClaimToken = "new"
	if err := store.SaveGrafanaDashboard(winner); err != nil {
		t.Fatalf("winning commit: %v", err)
	}
	if _, err := store.GetGrafanaImportSession(session.ID, session.ProjectID, now.Add(2*time.Minute)); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("committed session still exists: %v", err)
	}
	if _, err := store.GetGrafanaDashboard(staleDashboard.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("stale dashboard was persisted: %v", err)
	}
}

func TestGrafanaImportCommitRejectsExpiredClaim(t *testing.T) {
	store := newGrafanaDashboardTestStore(t)
	now := time.Now().UTC()
	session := &GrafanaImportSession{ID: "session-expired-claim", ProjectID: "project-a", Content: `{}`, SourceSHA256: "abc", CreatedAt: now, ExpiresAt: now.Add(time.Minute)}
	if err := store.SaveGrafanaImportSession(session, 5, 1<<20); err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := store.ClaimGrafanaImportSession(session.ID, session.ProjectID, "claim", now, time.Minute); err != nil || !claimed {
		t.Fatalf("claim = %v, %v", claimed, err)
	}
	if _, err := store.db.Exec(`UPDATE grafana_import_sessions SET expires_at = ? WHERE id = ?`, now.Add(-time.Minute), session.ID); err != nil {
		t.Fatal(err)
	}
	dashboard := sampleDashboard("dash-expired-claim", "project-a")
	dashboard.ImportSessionID = session.ID
	dashboard.ImportClaimToken = "claim"
	if err := store.SaveGrafanaDashboard(dashboard); !errors.Is(err, ErrGrafanaImportClaimLost) {
		t.Fatalf("expired claim commit error = %v, want ErrGrafanaImportClaimLost", err)
	}
}

func TestGrafanaImportSessionGlobalLimit(t *testing.T) {
	store := newGrafanaDashboardTestStore(t)
	now := time.Now().UTC()
	first := &GrafanaImportSession{ID: "session-a", ProjectID: "project-a", Content: `{}`, SourceSHA256: "abc", CreatedAt: now, ExpiresAt: now.Add(time.Minute)}
	if err := store.SaveGrafanaImportSession(first, 1, 1<<20); err != nil {
		t.Fatal(err)
	}
	second := &GrafanaImportSession{ID: "session-b", ProjectID: "project-b", Content: `{}`, SourceSHA256: "def", CreatedAt: now, ExpiresAt: now.Add(time.Minute)}
	if err := store.SaveGrafanaImportSession(second, 1, 1<<20); !errors.Is(err, ErrGrafanaImportSessionLimit) {
		t.Fatalf("second session error = %v, want ErrGrafanaImportSessionLimit", err)
	}
}

func TestGrafanaImportSessionGlobalByteLimit(t *testing.T) {
	store := newGrafanaDashboardTestStore(t)
	now := time.Now().UTC()
	first := &GrafanaImportSession{ID: "session-a", ProjectID: "project-a", Content: `1234`, SourceSHA256: "abc", CreatedAt: now, ExpiresAt: now.Add(time.Minute)}
	if err := store.SaveGrafanaImportSession(first, 5, 7); err != nil {
		t.Fatal(err)
	}
	second := &GrafanaImportSession{ID: "session-b", ProjectID: "project-b", Content: `5678`, SourceSHA256: "def", CreatedAt: now, ExpiresAt: now.Add(time.Minute)}
	if err := store.SaveGrafanaImportSession(second, 5, 7); !errors.Is(err, ErrGrafanaImportSessionLimit) {
		t.Fatalf("second session error = %v, want ErrGrafanaImportSessionLimit", err)
	}
}

func TestGrafanaImportSessionByteLimitCountsUTF8Bytes(t *testing.T) {
	store := newGrafanaDashboardTestStore(t)
	now := time.Now().UTC()
	first := &GrafanaImportSession{ID: "session-utf8-a", ProjectID: "project-a", Content: "😀", SourceSHA256: "abc", CreatedAt: now, ExpiresAt: now.Add(time.Minute)}
	if err := store.SaveGrafanaImportSession(first, 5, 7); err != nil {
		t.Fatal(err)
	}
	second := &GrafanaImportSession{ID: "session-utf8-b", ProjectID: "project-b", Content: "😀", SourceSHA256: "def", CreatedAt: now, ExpiresAt: now.Add(time.Minute)}
	if err := store.SaveGrafanaImportSession(second, 5, 7); !errors.Is(err, ErrGrafanaImportSessionLimit) {
		t.Fatalf("second UTF-8 session error = %v, want byte quota rejection", err)
	}
}

func TestGrafanaDashboardSnapshotRoundTripAndDelete(t *testing.T) {
	store := newGrafanaDashboardTestStore(t)
	now := time.Now().UTC()
	if err := store.SaveTest(&TestRecord{ID: "run-a", Name: "Run", Status: "COMPLETE", ProjectID: "project-a", StartTime: now}); err != nil {
		t.Fatal(err)
	}
	want := &GrafanaDashboardSnapshot{TestID: "run-a", ProjectID: "project-a", Payload: []byte(`{"dashboards":[]}`), CapturedAt: now}
	if err := store.SaveGrafanaDashboardSnapshot(want); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveGrafanaDashboardSnapshot(&GrafanaDashboardSnapshot{
		TestID: "run-a", ProjectID: "project-a", Payload: []byte(`{"dashboards":[{"id":"replacement"}]}`), CapturedAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetGrafanaDashboardSnapshot("run-a", "project-a")
	if err != nil || string(got.Payload) != string(want.Payload) {
		t.Fatalf("snapshot = %#v, %v; first capture must remain immutable", got, err)
	}
	if !got.CapturedAt.Equal(want.CapturedAt) {
		t.Fatalf("captured_at = %v, want original %v", got.CapturedAt, want.CapturedAt)
	}
	if _, err := store.GetGrafanaDashboardSnapshot("run-a", "project-b"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cross-project snapshot error = %v, want sql.ErrNoRows", err)
	}
	if err := store.DeleteTest("run-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetGrafanaDashboardSnapshot("run-a", "project-a"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("snapshot survived test deletion: %v", err)
	}
}

func TestGrafanaDashboardSnapshotSizeValidation(t *testing.T) {
	if err := validateGrafanaDashboardSnapshotSize([]byte("12345"), 4); err == nil {
		t.Fatal("expected oversized snapshot payload to be rejected")
	}
	if err := validateGrafanaDashboardSnapshotSize([]byte("1234"), 4); err != nil {
		t.Fatalf("payload at limit rejected: %v", err)
	}
}
