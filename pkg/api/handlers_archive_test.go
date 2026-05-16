package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/stormrunner/pkg/dsl"
	"github.com/jasonczech/stormrunner/pkg/engine"
	"github.com/jasonczech/stormrunner/pkg/protocol"
	"github.com/jasonczech/stormrunner/pkg/storage"
)

type archiveRedactionProtocol struct{}

func (p *archiveRedactionProtocol) Name() string                                   { return "archive-redaction" }
func (p *archiveRedactionProtocol) Init(_ context.Context, _ map[string]any) error { return nil }
func (p *archiveRedactionProtocol) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	return &protocol.Result{
		ActionName:       action.Name,
		StartTime:        time.Now(),
		Duration:         5 * time.Millisecond,
		BytesSent:        128,
		BytesReceived:    512,
		StatusCode:       200,
		Success:          true,
		CorrelationID:    "corr-archive-redaction-1",
		DependencyType:   "http",
		DependencyTarget: "archive.example.test",
		Snapshot: &protocol.ErrorSnapshot{
			RequestURL:      "https://archive.example.test/customers/customer-1234",
			RequestHeaders:  map[string]string{"X-Customer": "customer-1234"},
			RequestBody:     "customer=customer-1234",
			ResponseHeaders: map[string]string{"X-Customer": "customer-1234"},
			ResponseBody:    `{"customer":"customer-1234"}`,
		},
	}, nil
}
func (p *archiveRedactionProtocol) Teardown(_ context.Context) error    { return nil }
func (p *archiveRedactionProtocol) HealthCheck(_ context.Context) error { return nil }

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

	t.Run("browser diagnostics download", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+testRun.ID+"/archive/browser-diagnostics", nil)
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)

		if resp.Code != http.StatusOK {
			t.Fatalf("browser diagnostics status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
		}
		if got := resp.Header().Get("Content-Type"); !strings.Contains(got, "application/zip") {
			t.Fatalf("browser diagnostics content type = %q, want application/zip", got)
		}
		if got := resp.Header().Get("Content-Disposition"); !strings.Contains(got, "browser-diagnostics.zip") {
			t.Fatalf("browser diagnostics content disposition = %q, want browser-diagnostics.zip", got)
		}
		if resp.Body.Len() == 0 {
			t.Fatal("expected browser diagnostics download to return zip content")
		}
	})

	t.Run("transactions", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+testRun.ID+"/archive/transactions?limit=5&offset=0&action=step-1&success=true&correlation_id=corr-archive-1&dependency_type=http&dependency_target=archive.example.test&soap_operation=CreateOrder", nil)
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
		if payload.Transactions[0].CorrelationID != "corr-archive-1" {
			t.Fatalf("transaction correlation_id = %q, want corr-archive-1", payload.Transactions[0].CorrelationID)
		}
		if payload.Transactions[0].DependencyType != "http" || payload.Transactions[0].DependencyTarget != "archive.example.test" {
			t.Fatalf("unexpected transaction dependency metadata: %#v", payload.Transactions[0])
		}
		if payload.Transactions[0].Extractions["soap_operation"] != "CreateOrder" {
			t.Fatalf("transaction soap_operation = %q, want CreateOrder", payload.Transactions[0].Extractions["soap_operation"])
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
		if payload.BySOAPOperation["CreateOrder"].Transactions == 0 {
			t.Fatalf("expected SOAP operation breakdown, got %#v", payload.BySOAPOperation)
		}
		if len(payload.SummaryTimeline) == 0 {
			t.Fatal("expected report to include summary timeline")
		}
	})

	t.Run("html report", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+testRun.ID+"/report", nil)
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)

		if resp.Code != http.StatusOK {
			t.Fatalf("html report status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
		}
		if got := resp.Header().Get("Content-Type"); !strings.Contains(got, "text/html") {
			t.Fatalf("html report content type = %q, want text/html", got)
		}
		body := resp.Body.String()
		if !strings.Contains(body, "<!DOCTYPE html>") {
			t.Fatalf("expected HTML document, got %q", body)
		}
		if !strings.Contains(body, testRun.Name) {
			t.Fatalf("expected report to contain test name %q", testRun.Name)
		}
		if !strings.Contains(body, "Action Summary") {
			t.Fatalf("expected report to contain action summary, got %q", body)
		}
		if !strings.Contains(body, "Summary Timeline") {
			t.Fatalf("expected report to contain summary timeline, got %q", body)
		}
	})
}

func TestArchiveTransactionsApplyConfiguredRedactionRules(t *testing.T) {
	archiveDir := t.TempDir()
	t.Setenv("STORMRUNNER_ARCHIVE_DIR", archiveDir)

	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "stormrunner.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	server := NewServer(ServerConfig{Store: store})
	protocolName := "archive-redaction-" + strings.ReplaceAll(strings.ToLower(t.Name()), "/", "-")
	protocol.Register(protocolName, func() protocol.Protocol {
		return &archiveRedactionProtocol{}
	})

	server.runner.StoreScript(&dsl.Script{
		ID:       "script-archive-redaction",
		Name:     "Archive Redaction Script",
		Protocol: protocolName,
		Version:  "1.0.0",
		Actions:  []dsl.Action{{Name: "lookup", Type: "request", Target: "/"}},
	})

	testRun, err := server.runner.CreateTest(CreateTestRequest{
		Name:     "Archive Redaction Run",
		Duration: "2200ms",
		ScriptID: "script-archive-redaction",
		VUsers:   1,
		RuntimeSettings: &RuntimeSettingsOverride{
			RedactionRules: []engine.RedactionRule{{Name: "customer id", Pattern: `customer-[0-9]+`}},
		},
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

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+testRun.ID+"/archive/transactions?limit=5&offset=0", nil)
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
	snapshot := payload.Transactions[0].Snapshot
	if snapshot == nil {
		t.Fatal("expected archived transaction snapshot")
	}
	if snapshot.RequestURL != "https://archive.example.test/customers/[REDACTED]" {
		t.Fatalf("request url = %q, want redacted customer id", snapshot.RequestURL)
	}
	if snapshot.RequestHeaders["X-Customer"] != "[REDACTED]" {
		t.Fatalf("request header X-Customer = %q, want [REDACTED]", snapshot.RequestHeaders["X-Customer"])
	}
	if snapshot.RequestBody != "customer=[REDACTED]" {
		t.Fatalf("request body = %q, want customer=[REDACTED]", snapshot.RequestBody)
	}
	if snapshot.ResponseBody != `{"customer":"[REDACTED]"}` {
		t.Fatalf("response body = %q, want custom rule redaction", snapshot.ResponseBody)
	}
}
