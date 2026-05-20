package api

import (
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
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

func TestHandleExportUsageAccountingEmitsCSV(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
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

	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/usage/export?project_id=project-a", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	if got := resp.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/csv") {
		t.Fatalf("content type = %q, want text/csv", got)
	}
	if got := resp.Header().Get("Content-Disposition"); !strings.Contains(got, "apexload-usage-accounting.csv") {
		t.Fatalf("content disposition = %q, want usage filename", got)
	}

	rows, err := csv.NewReader(strings.NewReader(resp.Body.String())).ReadAll()
	if err != nil {
		t.Fatalf("read csv: %v", err)
	}
	if len(rows) != 5 {
		t.Fatalf("rows = %#v, want header plus totals, two protocols, and region", rows)
	}
	header := []string{"generated_at", "project_id", "scope", "key", "run_count", "group_count", "total_vusers", "duration_seconds", "vuser_seconds", "archive_bytes", "status_counts"}
	if strings.Join(rows[0], "|") != strings.Join(header, "|") {
		t.Fatalf("header = %#v, want %#v", rows[0], header)
	}
	assertUsageCSVRow(t, rows, "totals", "", "1", "0", "15", "120", "1800", "COMPLETE=1")
	assertUsageCSVRow(t, rows, "protocol", "grpc", "1", "1", "5", "120", "600", "COMPLETE=1")
	assertUsageCSVRow(t, rows, "protocol", "http", "1", "1", "10", "120", "1200", "COMPLETE=1")
	assertUsageCSVRow(t, rows, "region", "us-east", "1", "0", "15", "120", "1800", "COMPLETE=1")
}

func TestHandleUsageAccountingSnapshotsCapturesAndListsPersistedSnapshots(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	server.runner.mu.Lock()
	server.runner.tests["test-a"] = &ManagedTest{
		ID:          "test-a",
		Name:        "Checkout",
		Status:      "COMPLETE",
		ProjectID:   "project-a",
		TotalVUsers: 7,
		Duration:    time.Minute,
		Groups:      []TestGroup{{Name: "buyers", Protocol: "http", VUsers: 7}},
	}
	server.runner.mu.Unlock()

	captureReq := httptest.NewRequest(http.MethodPost, "/api/v1/system/usage/snapshots?project_id=project-a", nil)
	captureResp := httptest.NewRecorder()
	server.mux.ServeHTTP(captureResp, captureReq)

	if captureResp.Code != http.StatusCreated {
		t.Fatalf("capture status = %d, want %d body=%s", captureResp.Code, http.StatusCreated, captureResp.Body.String())
	}
	var captured UsageAccountingSnapshotView
	if err := json.Unmarshal(captureResp.Body.Bytes(), &captured); err != nil {
		t.Fatalf("unmarshal captured snapshot: %v", err)
	}
	if captured.ID == "" || captured.ProjectID != "project-a" || captured.Usage.Totals.RunCount != 1 || captured.Usage.Totals.VUserSeconds != 420 {
		t.Fatalf("captured snapshot = %#v, want project-a usage snapshot", captured)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/system/usage/snapshots?project_id=project-a", nil)
	listResp := httptest.NewRecorder()
	server.mux.ServeHTTP(listResp, listReq)

	if listResp.Code != http.StatusOK {
		t.Fatalf("list status = %d, want %d body=%s", listResp.Code, http.StatusOK, listResp.Body.String())
	}
	var listed UsageAccountingSnapshotsResponse
	if err := json.Unmarshal(listResp.Body.Bytes(), &listed); err != nil {
		t.Fatalf("unmarshal listed snapshots: %v", err)
	}
	if listed.Count != 1 || len(listed.Snapshots) != 1 {
		t.Fatalf("listed snapshots = %#v, want one snapshot", listed)
	}
	if listed.Snapshots[0].ID != captured.ID || listed.Snapshots[0].Usage.Totals.TotalVUsers != 7 {
		t.Fatalf("listed snapshot = %#v, want captured usage", listed.Snapshots[0])
	}
	events, err := store.ListAuditEvents(storage.AuditEventFilter{Action: "usage.snapshot.capture", Limit: 10})
	if err != nil {
		t.Fatalf("ListAuditEvents returned error: %v", err)
	}
	if len(events) != 1 || events[0].ResourceID != captured.ID || events[0].ProjectID != "project-a" {
		t.Fatalf("audit events = %#v, want capture audit for snapshot", events)
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

func assertUsageCSVRow(t *testing.T, rows [][]string, scope string, key string, runCount string, groupCount string, totalVUsers string, durationSeconds string, vuserSeconds string, statusCounts string) {
	t.Helper()
	for _, row := range rows[1:] {
		if row[2] != scope || row[3] != key {
			continue
		}
		if row[1] != "project-a" || row[4] != runCount || row[5] != groupCount || row[6] != totalVUsers || row[7] != durationSeconds || row[8] != vuserSeconds || row[10] != statusCounts {
			t.Fatalf("row for %s/%s = %#v", scope, key, row)
		}
		return
	}
	t.Fatalf("missing row for %s/%s in %#v", scope, key, rows)
}
