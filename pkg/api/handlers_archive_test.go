package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonczech/stormrunner/pkg/dsl"
	"github.com/jasonczech/stormrunner/pkg/protocol"
	"github.com/jasonczech/stormrunner/pkg/storage"
)

func TestArchiveQueryEndpoints(t *testing.T) {
	archiveDir := t.TempDir()
	t.Setenv("STORMRUNNER_ARCHIVE_DIR", archiveDir)

	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "stormrunner.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	server := NewServer(ServerConfig{Store: store})
	protocolName := "archive-api-stub-" + strings.ReplaceAll(strings.ToLower(t.Name()), "/", "-")
	protocol.Register(protocolName, func() protocol.Protocol {
		return &archiveStubProtocol{}
	})

	server.runner.StoreScript(&dsl.Script{
		ID:       "script-archive-api",
		Name:     "Archive API Script",
		Protocol: protocolName,
		Version:  "1.0.0",
		Actions:  []dsl.Action{{Name: "step-1", Type: "request", Target: "/"}},
	})

	testRun, err := server.runner.CreateTest(CreateTestRequest{
		Name:     "Archive API Run",
		Duration: "2200ms",
		ScriptID: "script-archive-api",
		VUsers:   1,
	})
	if err != nil {
		t.Fatalf("CreateTest returned error: %v", err)
	}
	if err := server.runner.StartTest(testRun.ID); err != nil {
		t.Fatalf("StartTest returned error: %v", err)
	}

	completed := waitForTestStatus(t, server.runner, testRun.ID, "COMPLETE")
	if completed.Archive == nil {
		t.Fatal("expected archive metadata after completion")
	}

	t.Run("manifest", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+testRun.ID+"/archive/manifest", nil)
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)

		if resp.Code != http.StatusOK {
			t.Fatalf("manifest status = %d, want %d", resp.Code, http.StatusOK)
		}
		var payload RunArchiveManifest
		if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
			t.Fatalf("unmarshal manifest: %v", err)
		}
		if payload.TestID != testRun.ID {
			t.Fatalf("manifest test_id = %q, want %q", payload.TestID, testRun.ID)
		}
		if payload.TransactionsParquetFile == "" || payload.ResourceSamplesParquetFile == "" || payload.IntervalSummariesParquetFile == "" {
			t.Fatalf("expected manifest parquet file fields, got %#v", payload)
		}
	})

	t.Run("artifact download", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+testRun.ID+"/archive/files/transactions.parquet", nil)
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)

		if resp.Code != http.StatusOK {
			t.Fatalf("artifact status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
		}
		if contentDisposition := resp.Header().Get("Content-Disposition"); !strings.Contains(contentDisposition, "transactions.parquet") {
			t.Fatalf("expected content disposition to include parquet filename, got %q", contentDisposition)
		}
		if resp.Body.Len() == 0 {
			t.Fatal("expected parquet artifact download to return content")
		}
	})

	t.Run("transactions", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+testRun.ID+"/archive/transactions?limit=5&offset=0&action=step-1&success=true", nil)
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)

		if resp.Code != http.StatusOK {
			t.Fatalf("transactions status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
		}
		var payload struct {
			Transactions []TransactionEvent `json:"transactions"`
			Total        int                `json:"total"`
		}
		if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
			t.Fatalf("unmarshal transactions: %v", err)
		}
		if payload.Total == 0 || len(payload.Transactions) == 0 {
			t.Fatal("expected at least one transaction event")
		}
		if payload.Transactions[0].ActionName != "step-1" {
			t.Fatalf("transaction action_name = %q, want step-1", payload.Transactions[0].ActionName)
		}
		if !payload.Transactions[0].Success {
			t.Fatal("expected filtered transaction to be successful")
		}
	})

	t.Run("summaries", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+testRun.ID+"/archive/summaries?limit=5", nil)
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)

		if resp.Code != http.StatusOK {
			t.Fatalf("summaries status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
		}
		var payload struct {
			Summaries []IntervalSummaryRecord `json:"summaries"`
			Total     int                     `json:"total"`
		}
		if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
			t.Fatalf("unmarshal summaries: %v", err)
		}
		if payload.Total == 0 || len(payload.Summaries) == 0 {
			t.Fatal("expected at least one interval summary")
		}
	})

	t.Run("resources", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+testRun.ID+"/archive/resources?limit=5", nil)
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)

		if resp.Code != http.StatusOK {
			t.Fatalf("resources status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
		}
		var payload struct {
			Resources []ResourceSample `json:"resources"`
			Total     int              `json:"total"`
		}
		if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
			t.Fatalf("unmarshal resources: %v", err)
		}
		if payload.Total == 0 || len(payload.Resources) == 0 {
			t.Fatal("expected at least one resource sample")
		}
	})

	t.Run("report", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+testRun.ID+"/archive/report", nil)
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)

		if resp.Code != http.StatusOK {
			t.Fatalf("report status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
		}
		var payload RunArchiveReport
		if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
			t.Fatalf("unmarshal report: %v", err)
		}
		if payload.Manifest == nil || payload.Manifest.TestID != testRun.ID {
			t.Fatalf("expected report manifest for test %q, got %#v", testRun.ID, payload.Manifest)
		}
		if payload.Overview.Transactions == 0 {
			t.Fatal("expected report to include transaction totals")
		}
		stats, ok := payload.ByAction["step-1"]
		if !ok {
			t.Fatal("expected action breakdown for step-1")
		}
		if stats.Transactions == 0 {
			t.Fatal("expected action breakdown to include transactions")
		}
	})
}