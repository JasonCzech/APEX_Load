package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

type stubArchiveService struct {
	test              *ManagedTest
	testFound         bool
	archive           *storage.RunArchiveRecord
	archiveFound      bool
	manifest          *RunArchiveManifest
	testManifest      *TestRunManifest
	retentionSettings ArchiveRetentionSettings
	retentionTests    []*ManagedTest
	getTestCalls      int
	retentionCalls    int
}

func (service *stubArchiveService) GetTest(string) (*ManagedTest, bool) {
	service.getTestCalls++
	return service.test, service.testFound
}

func (service *stubArchiveService) GetTestSummary(id string) (*ManagedTest, bool) {
	return service.GetTest(id)
}

func (service *stubArchiveService) GetTestManifest(string) (*TestRunManifest, error) {
	return service.testManifest, nil
}

func (service *stubArchiveService) GetRunArchive(string) (*storage.RunArchiveRecord, bool) {
	return service.archive, service.archiveFound
}

func (service *stubArchiveService) GetRunArchiveContext(context.Context, string) (*storage.RunArchiveRecord, bool) {
	return service.archive, service.archiveFound
}

func (service *stubArchiveService) GetRunArchiveManifest(string) (*RunArchiveManifest, error) {
	return service.manifest, nil
}

func (service *stubArchiveService) RetentionInputs() (ArchiveRetentionSettings, []*ManagedTest) {
	service.retentionCalls++
	return service.retentionSettings, service.retentionTests
}

func (service *stubArchiveService) GetRetentionArchive(string) (*storage.RunArchiveRecord, bool, error) {
	return service.archive, service.archiveFound, nil
}

func TestArchiveHandlerUsesArchiveServiceBoundary(t *testing.T) {
	service := &stubArchiveService{}
	server := &Server{archiveService: service}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/tests/missing/archive/verify", nil)
	req.SetPathValue("id", "missing")
	recorder := httptest.NewRecorder()

	server.handleVerifyRunArchive(recorder, req)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNotFound)
	}
	var payload map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if payload["error"] != "test not found" {
		t.Fatalf("error = %q, want %q", payload["error"], "test not found")
	}
	if service.getTestCalls != 1 {
		t.Fatalf("archive service GetTest calls = %d, want 1", service.getTestCalls)
	}
}

func TestArchiveRetentionPlanUsesArchiveServiceInputs(t *testing.T) {
	now := time.Now().UTC()
	service := &stubArchiveService{
		retentionSettings: ArchiveRetentionSettings{
			Enabled:              true,
			DefaultRetentionDays: 1,
			HARArtifactDir:       t.TempDir(),
		},
		retentionTests: []*ManagedTest{{
			ID:        "expired-run",
			ProjectID: "project-a",
			Status:    "COMPLETE",
			Archive: &storage.RunArchiveRecord{
				TestID:      "expired-run",
				ArchivePath: "/archive/expired-run",
				CompletedAt: now.Add(-48 * time.Hour),
			},
		}},
	}
	server := &Server{archiveService: service}
	recorder := httptest.NewRecorder()

	server.handlePlanArchiveRetention(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/archives/retention/plan", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	var payload ArchiveRetentionPlanResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !payload.DryRun || len(payload.Candidates) != 1 || payload.Candidates[0].TestID != "expired-run" {
		t.Fatalf("retention plan = %+v, want one dry-run candidate for expired-run", payload)
	}
	if payload.Summary.CandidateCount != 1 {
		t.Fatalf("candidate count = %d, want 1", payload.Summary.CandidateCount)
	}
	if service.retentionCalls != 1 {
		t.Fatalf("archive service RetentionInputs calls = %d, want 1", service.retentionCalls)
	}
}

func TestTestRunnerArchiveServicePreservesSnapshotReads(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	runner.mu.Lock()
	runner.tests["run-1"] = &ManagedTest{ID: "run-1", Name: "Original"}
	runner.archives["run-1"] = &storage.RunArchiveRecord{TestID: "run-1", ArchivePath: "/original"}
	runner.mu.Unlock()
	service := newTestRunnerArchiveService(runner, nil)

	test, ok := service.GetTest("run-1")
	if !ok || test == nil {
		t.Fatal("adapter did not return the TestRunner test snapshot")
	}
	test.Name = "Mutated"
	testAgain, ok := service.GetTest("run-1")
	if !ok || testAgain.Name != "Original" {
		t.Fatalf("second test snapshot = %+v, want unmodified Original", testAgain)
	}

	archive, ok := service.GetRunArchive("run-1")
	if !ok || archive == nil {
		t.Fatal("adapter did not return the TestRunner archive snapshot")
	}
	archive.ArchivePath = "/mutated"
	archiveAgain, ok := service.GetRunArchive("run-1")
	if !ok || archiveAgain.ArchivePath != "/original" {
		t.Fatalf("second archive snapshot = %+v, want unmodified /original", archiveAgain)
	}
}

var _ archiveService = (*stubArchiveService)(nil)
