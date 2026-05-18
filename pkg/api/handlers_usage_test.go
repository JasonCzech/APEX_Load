package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/stormrunner/pkg/storage"
)

func TestHandleGetUsageAccountingSummarizesProjectProtocolRegionAndArchives(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	archiveDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(archiveDir, "transactions.jsonl"), []byte("1234567890"), 0o644); err != nil {
		t.Fatalf("write archive transaction file: %v", err)
	}
	if err := os.Mkdir(filepath.Join(archiveDir, "nested"), 0o755); err != nil {
		t.Fatalf("create nested archive dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(archiveDir, "nested", "manifest.json"), []byte("12345"), 0o644); err != nil {
		t.Fatalf("write archive manifest file: %v", err)
	}

	server.runner.mu.Lock()
	server.runner.tests["test-a"] = &ManagedTest{
		ID:          "test-a",
		Name:        "Checkout",
		Status:      "COMPLETE",
		ProjectID:   "project-a",
		TotalVUsers: 15,
		Duration:    2 * time.Minute,
		Groups: []TestGroup{
			{Name: "buyers", Protocol: "http", VUsers: 10},
			{Name: "checkout-api", Protocol: "grpc", VUsers: 5},
		},
		Assignment: &GeneratorAssignmentSettings{RegionWeights: map[string]int{"us-east": 100}},
		Archive:    &storage.RunArchiveRecord{ArchivePath: archiveDir},
	}
	server.runner.tests["test-b"] = &ManagedTest{
		ID:          "test-b",
		Name:        "Search",
		Status:      "FAILED",
		ProjectID:   "project-b",
		TotalVUsers: 20,
		Duration:    time.Minute,
		Groups:      []TestGroup{{Name: "search", Protocol: "http", VUsers: 20}},
	}
	server.runner.mu.Unlock()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/usage?project_id=project-a", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var payload UsageAccountingResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.ProjectID != "project-a" {
		t.Fatalf("project_id = %q, want project-a", payload.ProjectID)
	}
	if payload.Totals.RunCount != 1 || payload.Totals.TotalVUsers != 15 || payload.Totals.DurationSeconds != 120 || payload.Totals.VUserSeconds != 1800 {
		t.Fatalf("totals = %#v, want one 15-vuser 120-second run", payload.Totals)
	}
	if payload.Totals.ArchiveBytes != 15 {
		t.Fatalf("archive bytes = %d, want 15", payload.Totals.ArchiveBytes)
	}
	httpBucket := findUsageBucket(t, payload.ByProtocol, "http")
	if httpBucket.GroupCount != 1 || httpBucket.TotalVUsers != 10 || httpBucket.VUserSeconds != 1200 {
		t.Fatalf("http bucket = %#v, want one 10-vuser group", httpBucket)
	}
	grpcBucket := findUsageBucket(t, payload.ByProtocol, "grpc")
	if grpcBucket.GroupCount != 1 || grpcBucket.TotalVUsers != 5 || grpcBucket.VUserSeconds != 600 {
		t.Fatalf("grpc bucket = %#v, want one 5-vuser group", grpcBucket)
	}
	regionBucket := findUsageBucket(t, payload.ByRegion, "us-east")
	if regionBucket.RunCount != 1 || regionBucket.VUserSeconds != 1800 {
		t.Fatalf("region bucket = %#v, want us-east run usage", regionBucket)
	}
	if len(payload.ByProject) != 0 {
		t.Fatalf("by_project = %#v, want omitted when project_id is filtered", payload.ByProject)
	}
}

func findUsageBucket(t *testing.T, buckets []UsageAccountingBucket, key string) UsageAccountingBucket {
	t.Helper()
	for _, bucket := range buckets {
		if bucket.Key == key {
			return bucket
		}
	}
	t.Fatalf("missing usage bucket %q in %#v", key, buckets)
	return UsageAccountingBucket{}
}
