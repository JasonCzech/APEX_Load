package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

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
