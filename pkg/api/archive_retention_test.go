package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestPruneRunArchiveRejectsSupersededPublicationBeforeDeletingFiles(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now().UTC().Truncate(time.Microsecond)
	projectID := "retention-race-project"
	testID := "retention-race-run"
	if err := store.SaveProject(&storage.Project{ID: projectID, Name: projectID, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTest(&storage.TestRecord{ID: testID, Name: testID, Status: "COMPLETE", ProjectID: projectID, StartTime: now}); err != nil {
		t.Fatal(err)
	}
	oldRoot := filepath.Join(t.TempDir(), "old")
	newRoot := filepath.Join(t.TempDir(), "new")
	for _, root := range []string{oldRoot, newRoot} {
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, runArchiveManifestFileName), []byte(`{"test_id":"`+testID+`"}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := &storage.RunArchiveRecord{
		TestID: testID, ArchiveBackend: archiveBackendFilesystem, ArchivePath: oldRoot,
		ManifestPath: filepath.Join(oldRoot, runArchiveManifestFileName), ManifestSHA256: "old-sha",
		CreatedAt: now.Add(-time.Hour), CompletedAt: now.Add(-time.Hour),
	}
	if err := store.SaveRunArchive(old); err != nil {
		t.Fatal(err)
	}
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 1})
	defer func() { _ = server.Shutdown(context.Background()) }()
	newer := *old
	newer.ArchivePath = newRoot
	newer.ManifestPath = filepath.Join(newRoot, runArchiveManifestFileName)
	newer.ManifestSHA256 = "new-sha"
	newer.CompletedAt = now
	if err := store.SaveRunArchive(&newer); err != nil {
		t.Fatal(err)
	}

	err = server.pruneRunArchive(context.Background(), old)
	if !errors.Is(err, errArchivePublicationChanged) {
		t.Fatalf("prune error = %v, want errArchivePublicationChanged", err)
	}
	for _, root := range []string{oldRoot, newRoot} {
		if _, err := os.Stat(root); err != nil {
			t.Fatalf("archive root %q was removed by stale prune: %v", root, err)
		}
	}
	current, err := store.GetRunArchive(testID)
	if err != nil {
		t.Fatal(err)
	}
	if current.ManifestSHA256 != newer.ManifestSHA256 || current.ArchivePath != newer.ArchivePath {
		t.Fatalf("current archive = %+v, want newer publication %+v", current, newer)
	}
	if _, err := store.GetArchiveCleanupJob(testID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cleanup job lookup err = %v, want sql.ErrNoRows", err)
	}
}

func TestGetRunArchiveContextEvictsHACacheWhenDurablePointerWasDeleted(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now().UTC().Truncate(time.Microsecond)
	projectID := "archive-cache-project"
	testID := "archive-cache-run"
	if err := store.SaveProject(&storage.Project{ID: projectID, Name: projectID, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTest(&storage.TestRecord{ID: testID, Name: testID, Status: "COMPLETE", ProjectID: projectID, StartTime: now}); err != nil {
		t.Fatal(err)
	}
	record := &storage.RunArchiveRecord{
		TestID: testID, ArchiveBackend: archiveBackendFilesystem, ArchivePath: "/archives/stale",
		ManifestPath: "/archives/stale/manifest.json", ManifestSHA256: "stale-sha",
		CreatedAt: now, CompletedAt: now,
	}
	if err := store.SaveRunArchive(record); err != nil {
		t.Fatal(err)
	}
	deleted, err := store.DeleteRunArchiveIfUnchanged(record)
	if err != nil || !deleted {
		t.Fatalf("DeleteRunArchiveIfUnchanged = %v, %v", deleted, err)
	}
	runner := &TestRunner{
		tests:        map[string]*ManagedTest{testID: {ID: testID, ProjectID: projectID, Archive: cloneRunArchiveRecord(record)}},
		archives:     map[string]*storage.RunArchiveRecord{testID: cloneRunArchiveRecord(record)},
		archiveStore: store,
		leadership:   &executionLeadership{},
	}
	if got, ok := runner.GetRunArchiveContext(context.Background(), testID); ok || got != nil {
		t.Fatalf("GetRunArchiveContext = %+v, %v; want no durable archive", got, ok)
	}
	runner.mu.RLock()
	defer runner.mu.RUnlock()
	if _, ok := runner.archives[testID]; ok {
		t.Fatal("stale archive remained in HA archive index")
	}
	if runner.tests[testID].Archive != nil {
		t.Fatalf("stale archive remained attached to test: %+v", runner.tests[testID].Archive)
	}
}

func TestPlanArchiveRetentionPruneAppliesSpecificPolicies(t *testing.T) {
	now := time.Date(2026, time.May, 18, 12, 0, 0, 0, time.UTC)
	oldCompletedAt := now.Add(-10 * 24 * time.Hour)
	recentCompletedAt := now.Add(-2 * 24 * time.Hour)
	settings := ArchiveRetentionSettings{
		Enabled:              true,
		DefaultRetentionDays: 30,
		Policies: []ArchiveRetentionPolicy{{
			Name:            "failed-checkout",
			ProjectID:       "checkout",
			RunStatus:       "FAILED",
			ArtifactType:    "bundle",
			ComplianceClass: "regulated",
			Reason:          "failed_run_short_retention",
			RetentionDays:   7,
		}},
	}
	tests := []*ManagedTest{
		{
			ID:        "old-failed",
			Status:    "FAILED",
			ProjectID: "checkout",
			Archive:   &storage.RunArchiveRecord{TestID: "old-failed", ArchivePath: "/archives/old", CompletedAt: oldCompletedAt},
		},
		{
			ID:        "recent-failed",
			Status:    "FAILED",
			ProjectID: "checkout",
			Archive:   &storage.RunArchiveRecord{TestID: "recent-failed", ArchivePath: "/archives/recent", CompletedAt: recentCompletedAt},
		},
		{
			ID:        "old-complete-default",
			Status:    "COMPLETE",
			ProjectID: "checkout",
			Archive:   &storage.RunArchiveRecord{TestID: "old-complete-default", ArchivePath: "/archives/default", CompletedAt: oldCompletedAt},
		},
	}

	candidates := planArchiveRetentionPrune(now, settings, tests)
	if len(candidates) != 1 {
		t.Fatalf("candidates = %#v, want one expired failed checkout archive", candidates)
	}
	candidate := candidates[0]
	if candidate.TestID != "old-failed" || candidate.PolicyName != "failed-checkout" || candidate.RetentionDays != 7 {
		t.Fatalf("candidate = %#v", candidate)
	}
	if candidate.ComplianceClass != "regulated" || candidate.Reason != "failed_run_short_retention" {
		t.Fatalf("candidate governance = %#v, want compliance class and reason", candidate)
	}
	if !candidate.ExpiredAt.Equal(oldCompletedAt.Add(7 * 24 * time.Hour)) {
		t.Fatalf("expired at = %s, want completion + retention", candidate.ExpiredAt)
	}
}

func TestPlanArchiveRetentionPruneHonorsDisabledSetting(t *testing.T) {
	now := time.Date(2026, time.May, 18, 12, 0, 0, 0, time.UTC)
	candidates := planArchiveRetentionPrune(now, ArchiveRetentionSettings{Enabled: false, DefaultRetentionDays: 1}, []*ManagedTest{{
		ID:      "old",
		Status:  "COMPLETE",
		Archive: &storage.RunArchiveRecord{TestID: "old", ArchivePath: "/archives/old", CompletedAt: now.Add(-365 * 24 * time.Hour)},
	}})
	if len(candidates) != 0 {
		t.Fatalf("candidates = %#v, want none while retention disabled", candidates)
	}
}

func TestArchiveRetentionPlanAndApplyHandlersPruneExpiredArchives(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()
	if err := store.SaveProject(&storage.Project{ID: "project-a", Name: "Project A", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	settings := server.runner.GetSystemSettings()
	settings.ArchiveRetention = ArchiveRetentionSettings{
		Enabled:              true,
		DefaultRetentionDays: 1,
	}
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings returned error: %v", err)
	}

	archiveRoot := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", archiveRoot)
	archiveDir := filepath.Join(archiveRoot, "expired-archive")
	if err := os.MkdirAll(archiveDir, 0o755); err != nil {
		t.Fatalf("create archive dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(archiveDir, "manifest.json"), []byte(`{"test_id":"expired"}`), 0o644); err != nil {
		t.Fatalf("write archive file: %v", err)
	}
	completedAt := time.Now().UTC().Add(-48 * time.Hour)
	archiveRecord := &storage.RunArchiveRecord{
		TestID: "expired", ArchiveBackend: archiveBackendFilesystem, ArchivePath: archiveDir,
		ManifestPath: filepath.Join(archiveDir, "manifest.json"), CompletedAt: completedAt,
	}
	if err := store.SaveProject(&storage.Project{ID: "project-a", Name: "Project A", CreatedAt: completedAt, UpdatedAt: completedAt}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTest(&storage.TestRecord{ID: "expired", Name: "Expired", Status: "COMPLETE", ProjectID: "project-a", StartTime: completedAt}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRunArchive(archiveRecord); err != nil {
		t.Fatal(err)
	}
	server.runner.mu.Lock()
	server.runner.tests["expired"] = &ManagedTest{
		ID:        "expired",
		Name:      "Expired",
		Status:    "COMPLETE",
		ProjectID: "project-a",
		Archive:   archiveRecord,
	}
	server.runner.mu.Unlock()

	planReq := httptest.NewRequest(http.MethodPost, "/api/v1/archives/retention/plan", nil)
	planResp := httptest.NewRecorder()
	server.mux.ServeHTTP(planResp, planReq)

	if planResp.Code != http.StatusOK {
		t.Fatalf("plan status = %d, want %d body=%s", planResp.Code, http.StatusOK, planResp.Body.String())
	}
	var plan ArchiveRetentionPlanResponse
	if err := json.Unmarshal(planResp.Body.Bytes(), &plan); err != nil {
		t.Fatalf("unmarshal plan: %v", err)
	}
	if !plan.DryRun || plan.Summary.CandidateCount != 1 || len(plan.Candidates) != 1 || plan.Candidates[0].ProjectID != "project-a" {
		t.Fatalf("plan = %#v, want one dry-run candidate", plan)
	}
	if plan.Candidates[0].ComplianceClass != "standard" || plan.Candidates[0].Reason != "expired_by_policy" {
		t.Fatalf("plan candidate governance = %#v, want default compliance evidence", plan.Candidates[0])
	}
	if _, err := os.Stat(archiveDir); err != nil {
		t.Fatalf("archive should still exist after plan: %v", err)
	}

	applyReq := httptest.NewRequest(http.MethodPost, "/api/v1/archives/retention/apply", nil)
	applyResp := httptest.NewRecorder()
	server.mux.ServeHTTP(applyResp, applyReq)

	if applyResp.Code != http.StatusOK {
		t.Fatalf("apply status = %d, want %d body=%s", applyResp.Code, http.StatusOK, applyResp.Body.String())
	}
	var applied ArchiveRetentionPlanResponse
	if err := json.Unmarshal(applyResp.Body.Bytes(), &applied); err != nil {
		t.Fatalf("unmarshal apply: %v", err)
	}
	if applied.DryRun || applied.Summary.PrunedCount != 1 || applied.Summary.FailedCount != 0 || len(applied.Results) != 1 || applied.Results[0].Status != "pruned" {
		t.Fatalf("applied = %#v, want one pruned result", applied)
	}
	if applied.Results[0].ComplianceClass != "standard" || applied.Results[0].Reason != "expired_by_policy" {
		t.Fatalf("applied result governance = %#v, want retention reason evidence", applied.Results[0])
	}
	if _, err := os.Stat(archiveDir); !os.IsNotExist(err) {
		t.Fatalf("archive dir stat err = %v, want removed", err)
	}
	if _, err := store.GetRunArchive("expired"); err != sql.ErrNoRows {
		t.Fatalf("archive metadata lookup err = %v, want sql.ErrNoRows", err)
	}
	events, err := store.ListAuditEvents(storage.AuditEventFilter{Action: "archive.retention.prune", Limit: 10})
	if err != nil {
		t.Fatalf("ListAuditEvents returned error: %v", err)
	}
	if len(events) != 1 || events[0].ProjectID != "project-a" || events[0].ResourceID != "expired" || events[0].Status != "success" {
		t.Fatalf("audit events = %#v, want prune audit event", events)
	}
	if events[0].Details["compliance_class"] != "standard" || events[0].Details["reason"] != "expired_by_policy" {
		t.Fatalf("audit details = %#v, want compliance class and reason", events[0].Details)
	}
}
