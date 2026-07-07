package api

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
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
	t.Setenv("APEXLOAD_ARCHIVE_DIR", archiveDir)

	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
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
		Name:         "Archive API Run",
		Duration:     "2200ms",
		ScriptID:     "script-archive-api",
		VUsers:       1,
		ScenarioMode: "goal_oriented",
		GoalConfig:   &GoalConfig{P95LatencyMs: ptr(100000.0), MaxErrorPct: ptr(99.0)},
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
	baselineID := "baseline-" + testRun.ID
	baselineArchive := *completed.Archive
	baselineArchive.TestID = baselineID
	laterID := "later-" + testRun.ID
	laterArchive := *completed.Archive
	laterArchive.TestID = laterID
	noArchiveID := "no-archive-" + testRun.ID
	server.runner.mu.Lock()
	server.runner.tests[baselineID] = &ManagedTest{
		ID:        baselineID,
		Name:      "Archive API Baseline",
		Status:    "COMPLETE",
		CreatedAt: completed.CreatedAt.Add(-time.Minute),
		EndedAt:   completed.EndedAt,
		Archive:   &baselineArchive,
	}
	server.runner.tests[laterID] = &ManagedTest{
		ID:        laterID,
		Name:      "Archive API Later",
		Status:    "COMPLETE",
		CreatedAt: completed.CreatedAt.Add(time.Minute),
		EndedAt:   completed.EndedAt,
		Archive:   &laterArchive,
	}
	server.runner.tests[noArchiveID] = &ManagedTest{
		ID:        noArchiveID,
		Name:      "Archive API Missing Archive",
		Status:    "COMPLETE",
		CreatedAt: completed.CreatedAt.Add(2 * time.Minute),
		EndedAt:   completed.EndedAt,
	}
	server.runner.archives[baselineID] = &baselineArchive
	server.runner.archives[laterID] = &laterArchive
	server.runner.mu.Unlock()

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
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+testRun.ID+"/archive/transactions?limit=5&offset=0&action=step-1&success=true&trace_id=4bf92f3577b34da6a3ce929d0e0e4736&dependency_type=http&dependency_target=archive.example.test&soap_operation=CreateOrder", nil)
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
		if payload.Transactions[0].CorrelationID != "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01" {
			t.Fatalf("transaction correlation_id = %q, want W3C traceparent", payload.Transactions[0].CorrelationID)
		}
		if payload.Transactions[0].TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" || payload.Transactions[0].SpanID != "00f067aa0ba902b7" {
			t.Fatalf("transaction APM IDs = (%q, %q), want trace/span ids", payload.Transactions[0].TraceID, payload.Transactions[0].SpanID)
		}
		if payload.Transactions[0].DependencyType != "http" || payload.Transactions[0].DependencyTarget != "archive.example.test" {
			t.Fatalf("unexpected transaction dependency metadata: %#v", payload.Transactions[0])
		}
		if payload.Transactions[0].Extractions["soap_operation"] != "CreateOrder" {
			t.Fatalf("transaction soap_operation = %q, want CreateOrder", payload.Transactions[0].Extractions["soap_operation"])
		}
	})

	t.Run("transactions csv", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+testRun.ID+"/archive/transactions?format=csv&limit=5&action=step-1", nil)
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)

		if resp.Code != http.StatusOK {
			t.Fatalf("transactions csv status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
		}
		rows, err := csv.NewReader(strings.NewReader(resp.Body.String())).ReadAll()
		if err != nil {
			t.Fatalf("read transactions csv: %v", err)
		}
		if len(rows) < 2 || rows[0][4] != "action_name" || rows[1][4] != "step-1" {
			t.Fatalf("transactions csv rows = %#v, want action_name step-1", rows)
		}
		if got := resp.Header().Get("Content-Disposition"); !strings.Contains(got, "archive-transactions") {
			t.Fatalf("transactions csv content disposition = %q, want archive-transactions", got)
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

	t.Run("summaries group filter echoes filters", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+testRun.ID+"/archive/summaries?limit=5&groups=Wikipedia,BBC", nil)
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)

		if resp.Code != http.StatusOK {
			t.Fatalf("summaries group filter status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
		}
		var payload struct {
			Filters map[string]any `json:"filters"`
		}
		if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
			t.Fatalf("unmarshal summaries group filter: %v", err)
		}
		groups, ok := payload.Filters["groups"].([]any)
		if !ok || len(groups) != 2 || groups[0] != "Wikipedia" || groups[1] != "BBC" {
			t.Fatalf("filters.groups = %#v, want Wikipedia and BBC", payload.Filters["groups"])
		}
	})

	t.Run("summaries csv", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+testRun.ID+"/archive/summaries?format=csv&limit=5", nil)
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)

		if resp.Code != http.StatusOK {
			t.Fatalf("summaries csv status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
		}
		rows, err := csv.NewReader(strings.NewReader(resp.Body.String())).ReadAll()
		if err != nil {
			t.Fatalf("read summaries csv: %v", err)
		}
		if len(rows) < 2 || rows[0][4] != "tps" {
			t.Fatalf("summaries csv rows = %#v, want tps header", rows)
		}
	})

	t.Run("summaries action filter csv", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+testRun.ID+"/archive/summaries?format=csv&limit=5&action=step-1", nil)
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)

		if resp.Code != http.StatusOK {
			t.Fatalf("action summaries csv status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
		}
		rows, err := csv.NewReader(strings.NewReader(resp.Body.String())).ReadAll()
		if err != nil {
			t.Fatalf("read action summaries csv: %v", err)
		}
		if len(rows) < 2 || rows[0][4] != "tps" {
			t.Fatalf("action summaries csv rows = %#v, want tps header", rows)
		}
		if got := resp.Header().Get("Content-Disposition"); !strings.Contains(got, "step-1") {
			t.Fatalf("action summaries content disposition = %q, want action token", got)
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

	t.Run("resources csv", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+testRun.ID+"/archive/resources?format=csv&limit=5", nil)
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)

		if resp.Code != http.StatusOK {
			t.Fatalf("resources csv status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
		}
		rows, err := csv.NewReader(strings.NewReader(resp.Body.String())).ReadAll()
		if err != nil {
			t.Fatalf("read resources csv: %v", err)
		}
		if len(rows) < 2 || rows[0][3] != "active_vusers" {
			t.Fatalf("resources csv rows = %#v, want active_vusers header", rows)
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
		if payload.Verdict == nil || payload.Verdict.Status == "" || payload.Verdict.Explanation == "" {
			t.Fatalf("expected report verdict, got %#v", payload.Verdict)
		}
		stats, ok := payload.ByAction["step-1"]
		if !ok {
			t.Fatal("expected action breakdown for step-1")
		}
		if stats.Transactions == 0 {
			t.Fatal("expected action breakdown to include transactions")
		}
		if stats.P95Ms == 0 {
			t.Fatal("expected action breakdown to include percentile metrics")
		}
		if stats.PeakTPS == 0 {
			t.Fatal("expected action breakdown to include peak TPS")
		}
		if payload.BySOAPOperation["CreateOrder"].Transactions == 0 {
			t.Fatalf("expected SOAP operation breakdown, got %#v", payload.BySOAPOperation)
		}
		slaRow, ok := payload.SLARows["step-1"]
		if !ok {
			t.Fatalf("expected SLA row for step-1, got %#v", payload.SLARows)
		}
		if !slaRow.HasSLA || slaRow.ResultKind != "pass" {
			t.Fatalf("expected passing evaluated SLA row, got %#v", slaRow)
		}
		if payload.SLARollup == nil || payload.SLARollup.Evaluated == 0 || payload.SLARollup.WorstKind != "pass" {
			t.Fatalf("expected passing SLA rollup, got %#v", payload.SLARollup)
		}
		if len(payload.SummaryTimeline) == 0 {
			t.Fatal("expected report to include summary timeline")
		}
	})

	t.Run("audit json report includes integrity verdict", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+testRun.ID+"/archive/report?profile=audit", nil)
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)

		if resp.Code != http.StatusOK {
			t.Fatalf("audit json report status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
		}
		var payload RunArchiveReport
		if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
			t.Fatalf("unmarshal audit report: %v", err)
		}
		if payload.Integrity == nil || payload.Integrity.Status == "" {
			t.Fatalf("audit json report missing integrity verdict: %#v", payload.Integrity)
		}
	})

	t.Run("analysis graph and drilldown", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+testRun.ID+"/archive/analysis?baseline_id="+baselineID+"&limit=5&action=step-1", nil)
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)

		if resp.Code != http.StatusOK {
			t.Fatalf("analysis status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
		}
		var payload RunArchiveAnalysisResponse
		if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
			t.Fatalf("unmarshal analysis: %v", err)
		}
		if payload.SchemaVersion != runArchiveAnalysisSchemaVersion || payload.BaselineID != baselineID {
			t.Fatalf("analysis metadata = %#v", payload)
		}
		if payload.Total == 0 || len(payload.Transactions) == 0 {
			t.Fatalf("analysis drilldown total=%d transactions=%d, want rows", payload.Total, len(payload.Transactions))
		}
		if payload.Transactions[0].ActionName != "step-1" || len(payload.Transactions[0].Path) == 0 {
			t.Fatalf("drilldown row = %#v, want step path", payload.Transactions[0])
		}
		actionNode := findArchiveAnalysisNode(payload.Graph.Nodes, "action", "step-1")
		if actionNode == nil || len(actionNode.Runs) != 2 {
			t.Fatalf("action node = %#v, want baseline/current merged runs", actionNode)
		}
		dependencyNode := findArchiveAnalysisNode(payload.Graph.Nodes, "dependency", "http:archive.example.test")
		if dependencyNode == nil {
			t.Fatalf("nodes = %#v, want dependency node", payload.Graph.Nodes)
		}
		if !hasArchiveAnalysisEdge(payload.Graph.Edges, "action_dependency", actionNode.ID, dependencyNode.ID) {
			t.Fatalf("edges = %#v, want action_dependency edge", payload.Graph.Edges)
		}
	})

	t.Run("csv report", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+testRun.ID+"/archive/report?format=csv", nil)
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)

		if resp.Code != http.StatusOK {
			t.Fatalf("csv report status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
		}
		if got := resp.Header().Get("Content-Type"); !strings.Contains(got, "text/csv") {
			t.Fatalf("csv report content type = %q, want text/csv", got)
		}
		rows, err := csv.NewReader(strings.NewReader(resp.Body.String())).ReadAll()
		if err != nil {
			t.Fatalf("read csv report: %v", err)
		}
		if len(rows) < 4 {
			t.Fatalf("csv rows = %#v, want header and report rows", rows)
		}
		if strings.Join(rows[0], "|") != "section|key|metric|value" {
			t.Fatalf("csv header = %#v", rows[0])
		}
		if !archiveReportCSVHasRow(rows, "overview", "", "transactions") {
			t.Fatalf("csv rows missing overview transactions: %#v", rows)
		}
		if !archiveReportCSVHasRow(rows, "action", "step-1", "transactions") {
			t.Fatalf("csv rows missing action transactions: %#v", rows)
		}
	})

	t.Run("executive csv report omits drilldown", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+testRun.ID+"/archive/report?format=csv&profile=executive", nil)
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)

		if resp.Code != http.StatusOK {
			t.Fatalf("executive csv report status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
		}
		rows, err := csv.NewReader(strings.NewReader(resp.Body.String())).ReadAll()
		if err != nil {
			t.Fatalf("read executive csv report: %v", err)
		}
		if !archiveReportCSVHasRow(rows, "overview", "", "transactions") {
			t.Fatalf("executive csv rows missing overview transactions: %#v", rows)
		}
		if archiveReportCSVHasRow(rows, "action", "step-1", "transactions") {
			t.Fatalf("executive csv rows should omit action drilldown: %#v", rows)
		}
	})

	t.Run("pdf report", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+testRun.ID+"/archive/report?format=pdf&profile=engineer", nil)
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)

		if resp.Code != http.StatusOK {
			t.Fatalf("pdf report status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
		}
		if got := resp.Header().Get("Content-Type"); !strings.Contains(got, "application/pdf") {
			t.Fatalf("pdf report content type = %q, want application/pdf", got)
		}
		if got := resp.Header().Get("Content-Disposition"); !strings.Contains(got, "apexload-archive-report-"+testRun.ID+"-engineer.pdf") {
			t.Fatalf("pdf report content disposition = %q, want engineer pdf filename", got)
		}
		body := resp.Body.String()
		if !strings.HasPrefix(body, "%PDF-1.4") || !strings.Contains(body, "APEX Load Archive Report") || !strings.Contains(body, "Top Actions") {
			t.Fatalf("pdf report body missing expected PDF report content:\n%s", body)
		}
	})

	t.Run("executive pdf report omits drilldown", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+testRun.ID+"/archive/report?format=pdf&profile=executive", nil)
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)

		if resp.Code != http.StatusOK {
			t.Fatalf("executive pdf report status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
		}
		body := resp.Body.String()
		if !strings.Contains(body, "Profile: Executive") {
			t.Fatalf("executive pdf body missing profile label:\n%s", body)
		}
		if strings.Contains(body, "Top Actions") {
			t.Fatalf("executive pdf report should omit action drilldown:\n%s", body)
		}
	})

	t.Run("audit pdf report includes evidence package", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+testRun.ID+"/archive/report?format=pdf&profile=audit", nil)
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)

		if resp.Code != http.StatusOK {
			t.Fatalf("audit pdf report status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
		}
		body := resp.Body.String()
		if !strings.HasPrefix(body, "%PDF-1.4") || !strings.Contains(body, "Profile: Audit") || !strings.Contains(body, "Artifact Integrity") {
			t.Fatalf("audit pdf body missing evidence package content:\n%s", body)
		}
		if strings.Contains(body, "Top Actions") {
			t.Fatalf("audit pdf report should omit action drilldown:\n%s", body)
		}
	})

	t.Run("audit csv report includes evidence inventory", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+testRun.ID+"/archive/report?format=csv&profile=audit", nil)
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)

		if resp.Code != http.StatusOK {
			t.Fatalf("audit csv report status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
		}
		rows, err := csv.NewReader(strings.NewReader(resp.Body.String())).ReadAll()
		if err != nil {
			t.Fatalf("read audit csv report: %v", err)
		}
		if !archiveReportCSVHasRow(rows, "schema_compatibility", "", "status") {
			t.Fatalf("audit csv rows missing schema compatibility: %#v", rows)
		}
		if !archiveReportCSVHasRow(rows, "artifact_inventory", "transactions.jsonl", "count") {
			t.Fatalf("audit csv rows missing artifact inventory: %#v", rows)
		}
		if !archiveReportCSVHasRow(rows, "artifact_integrity", "", "status") {
			t.Fatalf("audit csv rows missing integrity verdict: %#v", rows)
		}
		if archiveReportCSVHasRow(rows, "action", "step-1", "transactions") {
			t.Fatalf("audit csv rows should omit action drilldown: %#v", rows)
		}
	})

	t.Run("unknown csv report profile", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+testRun.ID+"/archive/report?format=csv&profile=unknown", nil)
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)

		if resp.Code != http.StatusBadRequest {
			t.Fatalf("unknown csv profile status = %d, want %d body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
		}
	})

	t.Run("comparison csv", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+testRun.ID+"/archive/comparison?format=csv&baseline_id="+baselineID+"&breakdown=overall", nil)
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)

		if resp.Code != http.StatusOK {
			t.Fatalf("comparison csv status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
		}
		rows, err := csv.NewReader(strings.NewReader(resp.Body.String())).ReadAll()
		if err != nil {
			t.Fatalf("read comparison csv: %v", err)
		}
		if len(rows) != 3 {
			t.Fatalf("comparison csv rows = %#v, want header plus two runs", rows)
		}
		if rows[0][0] != "run_id" || rows[0][2] != "breakdown" || rows[1][2] != "overall" || rows[2][2] != "overall" {
			t.Fatalf("comparison csv rows = %#v, want overall comparison rows", rows)
		}
		if got := resp.Header().Get("Content-Disposition"); !strings.Contains(got, "archive-comparison") {
			t.Fatalf("comparison csv content disposition = %q, want archive-comparison", got)
		}
	})

	t.Run("comparison csv action filter", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+testRun.ID+"/archive/comparison?format=csv&baseline_id="+baselineID+"&breakdown=device&action=step-1", nil)
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)

		if resp.Code != http.StatusOK {
			t.Fatalf("action comparison csv status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
		}
		rows, err := csv.NewReader(strings.NewReader(resp.Body.String())).ReadAll()
		if err != nil {
			t.Fatalf("read action comparison csv: %v", err)
		}
		if len(rows) != 3 {
			t.Fatalf("action comparison csv rows = %#v, want header plus two runs", rows)
		}
		index := csvHeaderIndex(rows[0])
		if rows[1][index["action_name"]] != "step-1" || rows[1][index["breakdown"]] != "action" {
			t.Fatalf("action comparison row = %#v, want action breakdown", rows[1])
		}
		if rows[1][index["transactions"]] == "0" || rows[1][index["avg_response_ms"]] == "" || rows[1][index["peak_tps"]] == "" {
			t.Fatalf("action comparison metrics row = %#v, want filtered metrics", rows[1])
		}
		if got := resp.Header().Get("Content-Disposition"); !strings.Contains(got, "step-1") {
			t.Fatalf("action comparison content disposition = %q, want action token", got)
		}
	})

	t.Run("multi comparison csv orders runs oldest to newest", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+baselineID+"/archive/comparison?format=csv&comparison_ids="+testRun.ID+","+laterID+"&breakdown=overall", nil)
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)

		if resp.Code != http.StatusOK {
			t.Fatalf("multi comparison csv status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
		}
		rows, err := csv.NewReader(strings.NewReader(resp.Body.String())).ReadAll()
		if err != nil {
			t.Fatalf("read multi comparison csv: %v", err)
		}
		if len(rows) != 4 {
			t.Fatalf("multi comparison csv rows = %#v, want header plus three runs", rows)
		}
		gotIDs := []string{rows[1][0], rows[2][0], rows[3][0]}
		wantIDs := []string{baselineID, testRun.ID, laterID}
		for index := range wantIDs {
			if gotIDs[index] != wantIDs[index] {
				t.Fatalf("multi comparison csv run order = %#v, want %#v", gotIDs, wantIDs)
			}
		}
		if got := resp.Header().Get("Content-Disposition"); !strings.Contains(got, "3-runs") {
			t.Fatalf("multi comparison content disposition = %q, want run count", got)
		}
	})

	t.Run("comparison csv rejects invalid breakdown", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+testRun.ID+"/archive/comparison?format=csv&baseline_id="+baselineID+"&breakdown=unknown", nil)
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)

		if resp.Code != http.StatusBadRequest {
			t.Fatalf("invalid comparison breakdown status = %d, want %d body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
		}
	})

	t.Run("multi comparison csv rejects unsupported format", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+baselineID+"/archive/comparison?format=json&comparison_ids="+testRun.ID+"&breakdown=overall", nil)
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)

		if resp.Code != http.StatusBadRequest {
			t.Fatalf("unsupported multi comparison format status = %d, want %d body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
		}
	})

	t.Run("multi comparison csv rejects more than ten runs", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+baselineID+"/archive/comparison?format=csv&comparison_ids=run-1,run-2,run-3,run-4,run-5,run-6,run-7,run-8,run-9,run-10&breakdown=overall", nil)
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)

		if resp.Code != http.StatusBadRequest {
			t.Fatalf("too many multi comparison runs status = %d, want %d body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
		}
	})

	t.Run("multi comparison csv rejects unknown comparison run", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+baselineID+"/archive/comparison?format=csv&comparison_ids=unknown-run&breakdown=overall", nil)
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)

		if resp.Code != http.StatusNotFound {
			t.Fatalf("unknown multi comparison run status = %d, want %d body=%s", resp.Code, http.StatusNotFound, resp.Body.String())
		}
	})

	t.Run("multi comparison csv rejects missing comparison archive", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+baselineID+"/archive/comparison?format=csv&comparison_ids="+noArchiveID+"&breakdown=overall", nil)
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)

		if resp.Code != http.StatusNotFound {
			t.Fatalf("missing multi comparison archive status = %d, want %d body=%s", resp.Code, http.StatusNotFound, resp.Body.String())
		}
	})

	t.Run("multi comparison csv rejects missing action", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+baselineID+"/archive/comparison?format=csv&comparison_ids="+testRun.ID+"&breakdown=overall&action=missing-action", nil)
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)

		if resp.Code != http.StatusBadRequest {
			t.Fatalf("missing action comparison status = %d, want %d body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
		}
	})

	t.Run("html report", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+testRun.ID+"/report?profile=executive", nil)
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
		if !strings.Contains(body, "Executive Summary Report") {
			t.Fatalf("expected report to contain executive profile label, got %q", body)
		}
		if strings.Contains(body, "Action Summary") {
			t.Fatalf("executive report should omit action summary, got %q", body)
		}
		if strings.Contains(body, "Summary Timeline") {
			t.Fatalf("executive report should omit summary timeline, got %q", body)
		}
	})

	t.Run("unknown html report profile", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+testRun.ID+"/report?profile=unknown", nil)
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)

		if resp.Code != http.StatusBadRequest {
			t.Fatalf("unknown profile status = %d, want %d body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
		}
	})
}

func TestArchiveReportReturnsNotFoundWhenManifestMissing(t *testing.T) {
	archiveDir := t.TempDir()
	store := &metricsStoreSpy{
		listedTests: []*storage.TestRecord{{
			ID:          "test-missing-archive",
			Name:        "Recovered Run",
			Status:      "COMPLETE",
			ProjectID:   storage.DefaultProjectID,
			TotalVUsers: 1,
		}},
		archiveByTestID: map[string]*storage.RunArchiveRecord{
			"test-missing-archive": {
				TestID:       "test-missing-archive",
				ArchivePath:  archiveDir,
				ManifestPath: filepath.Join(archiveDir, "manifest.json"),
			},
		},
	}
	server := NewServer(ServerConfig{Store: store})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tests/test-missing-archive/archive/report", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusNotFound {
		t.Fatalf("report status = %d, want %d body=%s", resp.Code, http.StatusNotFound, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "archive not found") {
		t.Fatalf("expected archive not found error, got %q", resp.Body.String())
	}
}

func archiveReportCSVHasRow(rows [][]string, section string, key string, metric string) bool {
	for _, row := range rows {
		if len(row) >= 4 && row[0] == section && row[1] == key && row[2] == metric {
			return true
		}
	}
	return false
}

func findArchiveAnalysisNode(nodes []RunArchiveAnalysisNode, kind string, label string) *RunArchiveAnalysisNode {
	for i := range nodes {
		if nodes[i].Kind == kind && nodes[i].Label == label {
			return &nodes[i]
		}
	}
	return nil
}

func hasArchiveAnalysisEdge(edges []RunArchiveAnalysisEdge, kind string, from string, to string) bool {
	for _, edge := range edges {
		if edge.Kind == kind && edge.From == from && edge.To == to {
			return true
		}
	}
	return false
}

func TestLoadArchiveIntervalSummariesForActionAggregatesTransactions(t *testing.T) {
	archiveDir := t.TempDir()
	start := time.Date(2026, 5, 12, 18, 0, 0, 0, time.UTC)
	manifest := RunArchiveManifest{
		TestID:                "run-action-summary",
		TestName:              "Action Summary Run",
		CreatedAt:             start,
		TransactionsFile:      "transactions.jsonl",
		IntervalSummariesFile: "interval_summaries.jsonl",
	}
	writeJSONL(t, filepath.Join(archiveDir, "manifest.json"), manifest)
	writeJSONL(t, filepath.Join(archiveDir, manifest.IntervalSummariesFile),
		IntervalSummaryRecord{Timestamp: start.Add(time.Second), TestID: manifest.TestID, TestName: manifest.TestName, ActiveVUsers: 2},
		IntervalSummaryRecord{Timestamp: start.Add(2 * time.Second), TestID: manifest.TestID, TestName: manifest.TestName, ActiveVUsers: 3},
		IntervalSummaryRecord{Timestamp: start.Add(3 * time.Second), TestID: manifest.TestID, TestName: manifest.TestName, ActiveVUsers: 4},
	)
	writeJSONL(t, filepath.Join(archiveDir, manifest.TransactionsFile),
		TransactionEvent{Timestamp: start.Add(500 * time.Millisecond), TestID: manifest.TestID, TestName: manifest.TestName, ActionName: "Checkout", DurationMs: 100, Success: true},
		TransactionEvent{Timestamp: start.Add(1500 * time.Millisecond), TestID: manifest.TestID, TestName: manifest.TestName, ActionName: "Search", DurationMs: 900, Success: true},
		TransactionEvent{Timestamp: start.Add(1500 * time.Millisecond), TestID: manifest.TestID, TestName: manifest.TestName, ActionName: "Checkout", DurationMs: 300, Success: false},
		TransactionEvent{Timestamp: start.Add(2500 * time.Millisecond), TestID: manifest.TestID, TestName: manifest.TestName, ActionName: "Checkout", DurationMs: 200, Success: true},
	)
	record := &storage.RunArchiveRecord{
		TestID:       manifest.TestID,
		ArchivePath:  archiveDir,
		ManifestPath: filepath.Join(archiveDir, "manifest.json"),
	}

	summaries, total, err := loadArchiveIntervalSummaries(record, archivePage{Limit: 10}, "Checkout", groupFilterQuery{})
	if err != nil {
		t.Fatalf("loadArchiveIntervalSummaries returned error: %v", err)
	}
	if total != 3 || len(summaries) != 3 {
		t.Fatalf("filtered summaries total=%d len=%d, want 3", total, len(summaries))
	}
	if summaries[0].TotalTx != 1 || summaries[0].TotalErrors != 0 || summaries[0].TPS != 1 || summaries[0].AvgResponseMs != 100 || summaries[0].P95Ms != 100 {
		t.Fatalf("first filtered bucket = %#v, want one successful checkout", summaries[0])
	}
	if summaries[1].TotalTx != 2 || summaries[1].TotalErrors != 1 || summaries[1].TPS != 1 || summaries[1].AvgResponseMs != 300 || summaries[1].ErrorPct != 100 || summaries[1].P50Ms != 300 {
		t.Fatalf("second filtered bucket = %#v, want one failed checkout and cumulative totals", summaries[1])
	}
	if summaries[2].TotalTx != 3 || summaries[2].TotalErrors != 1 || summaries[2].TPS != 1 || summaries[2].AvgResponseMs != 200 || summaries[2].P99Ms != 200 {
		t.Fatalf("third filtered bucket = %#v, want final checkout cumulative totals", summaries[2])
	}
}

func TestLoadArchiveIntervalSummariesForGroupsAggregatesTransactions(t *testing.T) {
	archiveDir := t.TempDir()
	start := time.Date(2026, 5, 12, 18, 0, 0, 0, time.UTC)
	manifest := RunArchiveManifest{
		TestID:                "run-group-summary",
		TestName:              "Group Summary Run",
		CreatedAt:             start,
		TransactionsFile:      "transactions.jsonl",
		IntervalSummariesFile: "interval_summaries.jsonl",
	}
	writeJSONL(t, filepath.Join(archiveDir, "manifest.json"), manifest)
	writeJSONL(t, filepath.Join(archiveDir, manifest.IntervalSummariesFile),
		IntervalSummaryRecord{
			Timestamp:    start.Add(time.Second),
			TestID:       manifest.TestID,
			TestName:     manifest.TestName,
			ActiveVUsers: 10,
			Groups: map[string]*GroupLiveMetrics{
				"Checkout": &GroupLiveMetrics{Name: "Checkout", VUsers: 4},
				"Browse":   &GroupLiveMetrics{Name: "Browse", VUsers: 6},
			},
		},
		IntervalSummaryRecord{
			Timestamp:    start.Add(2 * time.Second),
			TestID:       manifest.TestID,
			TestName:     manifest.TestName,
			ActiveVUsers: 12,
			Groups: map[string]*GroupLiveMetrics{
				"Checkout": &GroupLiveMetrics{Name: "Checkout", VUsers: 5},
				"Browse":   &GroupLiveMetrics{Name: "Browse", VUsers: 7},
			},
		},
	)
	writeJSONL(t, filepath.Join(archiveDir, manifest.TransactionsFile),
		TransactionEvent{Timestamp: start.Add(500 * time.Millisecond), TestID: manifest.TestID, TestName: manifest.TestName, Group: "Checkout", ActionName: "Checkout", DurationMs: 100, Success: true},
		TransactionEvent{Timestamp: start.Add(1500 * time.Millisecond), TestID: manifest.TestID, TestName: manifest.TestName, Group: "Browse", ActionName: "Browse", DurationMs: 900, Success: true},
		TransactionEvent{Timestamp: start.Add(1500 * time.Millisecond), TestID: manifest.TestID, TestName: manifest.TestName, Group: "Checkout", ActionName: "Checkout", DurationMs: 300, Success: false},
	)
	record := &storage.RunArchiveRecord{
		TestID:       manifest.TestID,
		ArchivePath:  archiveDir,
		ManifestPath: filepath.Join(archiveDir, "manifest.json"),
	}

	summaries, total, err := loadArchiveIntervalSummaries(record, archivePage{Limit: 10}, "", groupFilterQuery{Names: []string{"Checkout"}, Explicit: true})
	if err != nil {
		t.Fatalf("loadArchiveIntervalSummaries returned error: %v", err)
	}
	if total != 2 || len(summaries) != 2 {
		t.Fatalf("filtered summaries total=%d len=%d, want 2", total, len(summaries))
	}
	if summaries[0].ActiveVUsers != 4 || summaries[0].TotalTx != 1 || summaries[0].AvgResponseMs != 100 {
		t.Fatalf("first group bucket = %#v, want Checkout-only summary", summaries[0])
	}
	if summaries[1].ActiveVUsers != 5 || summaries[1].TotalTx != 2 || summaries[1].TotalErrors != 1 || summaries[1].ErrorPct != 100 {
		t.Fatalf("second group bucket = %#v, want Checkout-only failed summary", summaries[1])
	}

	empty, _, err := loadArchiveIntervalSummaries(record, archivePage{Limit: 10}, "", groupFilterQuery{Explicit: true})
	if err != nil {
		t.Fatalf("empty group filter returned error: %v", err)
	}
	if empty[0].ActiveVUsers != 0 || empty[0].TotalTx != 0 || empty[1].TotalTx != 0 {
		t.Fatalf("empty group filter = %#v, want zeroed performance summaries", empty)
	}
}

func TestLoadArchiveIntervalSummariesForGroupsEstimatesLegacyVUsers(t *testing.T) {
	archiveDir := t.TempDir()
	start := time.Date(2026, 6, 8, 17, 0, 0, 0, time.UTC)
	manifest := RunArchiveManifest{
		TestID:                "run-legacy-group-summary",
		TestName:              "Legacy Group Summary Run",
		CreatedAt:             start,
		TransactionsFile:      "transactions.jsonl",
		IntervalSummariesFile: "interval_summaries.jsonl",
		Provenance: RunArchiveProvenance{
			Groups: []TestGroup{
				{Name: "Wikipedia", VUsers: 10},
				{Name: "BBC", VUsers: 10},
			},
		},
	}
	writeJSONL(t, filepath.Join(archiveDir, "manifest.json"), manifest)
	writeJSONL(t, filepath.Join(archiveDir, manifest.IntervalSummariesFile),
		IntervalSummaryRecord{Timestamp: start.Add(time.Second), TestID: manifest.TestID, TestName: manifest.TestName, ActiveVUsers: 2},
		IntervalSummaryRecord{Timestamp: start.Add(2 * time.Second), TestID: manifest.TestID, TestName: manifest.TestName, ActiveVUsers: 4},
	)
	writeJSONL(t, filepath.Join(archiveDir, manifest.TransactionsFile),
		TransactionEvent{Timestamp: start.Add(500 * time.Millisecond), TestID: manifest.TestID, TestName: manifest.TestName, Group: "Wikipedia", ActionName: "Home", DurationMs: 100, Success: true},
		TransactionEvent{Timestamp: start.Add(1500 * time.Millisecond), TestID: manifest.TestID, TestName: manifest.TestName, Group: "BBC", ActionName: "Home", DurationMs: 900, Success: true},
		TransactionEvent{Timestamp: start.Add(1500 * time.Millisecond), TestID: manifest.TestID, TestName: manifest.TestName, Group: "Wikipedia", ActionName: "Home", DurationMs: 300, Success: false},
	)
	record := &storage.RunArchiveRecord{
		TestID:       manifest.TestID,
		ArchivePath:  archiveDir,
		ManifestPath: filepath.Join(archiveDir, "manifest.json"),
	}

	summaries, total, err := loadArchiveIntervalSummaries(record, archivePage{Limit: 10}, "", groupFilterQuery{Names: []string{"Wikipedia"}, Explicit: true})
	if err != nil {
		t.Fatalf("loadArchiveIntervalSummaries returned error: %v", err)
	}
	if total != 2 || len(summaries) != 2 {
		t.Fatalf("filtered summaries total=%d len=%d, want 2", total, len(summaries))
	}
	if summaries[0].ActiveVUsers != 1 || summaries[0].TotalTx != 1 || summaries[0].AvgResponseMs != 100 || summaries[0].P95Ms != 100 {
		t.Fatalf("first legacy group bucket = %#v, want estimated VUsers and Wikipedia transaction", summaries[0])
	}
	if summaries[1].ActiveVUsers != 2 || summaries[1].TotalTx != 2 || summaries[1].TotalErrors != 1 || summaries[1].ErrorPct != 100 || summaries[1].P50Ms != 300 {
		t.Fatalf("second legacy group bucket = %#v, want estimated VUsers and Wikipedia failure", summaries[1])
	}
}

func TestArchiveFilteredThroughputTarget(t *testing.T) {
	groupSet := map[string]struct{}{"Checkout": {}}
	withGroupTarget := IntervalSummaryRecord{
		// Whole-test target/gap must not leak into the filtered subset.
		TargetTPS:              999,
		ThroughputGapPct:       88,
		ThroughputTargetMissed: true,
		Groups: map[string]*GroupLiveMetrics{
			"Checkout": {Name: "Checkout", TargetTPS: 100},
			"Browse":   {Name: "Browse", TargetTPS: 50},
		},
	}
	// Subset TPS 80 vs Checkout's own target 100 -> 20% gap, missed.
	if target, gap, missed := archiveFilteredThroughputTarget(withGroupTarget, groupSet, true, 80); target != 100 || gap != 20 || !missed {
		t.Fatalf("filtered target = (%v,%v,%v), want (100,20,true) from Checkout target only", target, gap, missed)
	}
	// Subset meeting its own target -> no gap, not missed.
	if target, gap, missed := archiveFilteredThroughputTarget(withGroupTarget, groupSet, true, 120); target != 100 || gap != 0 || missed {
		t.Fatalf("filtered target = (%v,%v,%v), want (100,0,false) when subset meets target", target, gap, missed)
	}

	// No per-group target captured -> zeroed, despite whole-test target/gap.
	noGroupTarget := IntervalSummaryRecord{
		TargetTPS:              999,
		ThroughputGapPct:       88,
		ThroughputTargetMissed: true,
		Groups:                 map[string]*GroupLiveMetrics{"Checkout": {Name: "Checkout"}},
	}
	if target, gap, missed := archiveFilteredThroughputTarget(noGroupTarget, groupSet, true, 10); target != 0 || gap != 0 || missed {
		t.Fatalf("no-group-target filtered = (%v,%v,%v), want all zero", target, gap, missed)
	}

	// Action-only filter (not explicit group) -> zeroed.
	if target, gap, missed := archiveFilteredThroughputTarget(withGroupTarget, nil, false, 80); target != 0 || gap != 0 || missed {
		t.Fatalf("action-only filtered = (%v,%v,%v), want all zero", target, gap, missed)
	}
}

func TestArchiveSummaryActiveVUsersFallsBackToRunWide(t *testing.T) {
	groupSet := map[string]struct{}{"Checkout": {}}

	// No per-group metrics and no configured groups: a group that ran must not
	// report flat zero; fall back to the run-wide active VUser count.
	summary := IntervalSummaryRecord{ActiveVUsers: 9}
	if got := archiveSummaryActiveVUsersForGroups(summary, groupSet, true, nil); got != 9 {
		t.Fatalf("active vusers = %d, want run-wide fallback 9", got)
	}

	// Selected group absent from the configured set: still falls back run-wide.
	configured := []TestGroup{{Name: "Other", VUsers: 5}}
	if got := archiveSummaryActiveVUsersForGroups(summary, groupSet, true, configured); got != 9 {
		t.Fatalf("active vusers = %d, want run-wide fallback 9 when group not configured", got)
	}

	// No VUser data at all -> zero.
	if got := archiveSummaryActiveVUsersForGroups(IntervalSummaryRecord{}, groupSet, true, nil); got != 0 {
		t.Fatalf("active vusers = %d, want 0 when no VUser data exists", got)
	}

	// Per-group counts still take precedence when present.
	withGroups := IntervalSummaryRecord{
		ActiveVUsers: 9,
		Groups:       map[string]*GroupLiveMetrics{"Checkout": {Name: "Checkout", VUsers: 4}},
	}
	if got := archiveSummaryActiveVUsersForGroups(withGroups, groupSet, true, nil); got != 4 {
		t.Fatalf("active vusers = %d, want exact per-group count 4", got)
	}
}

func TestArchiveTransactionsApplyConfiguredRedactionRules(t *testing.T) {
	archiveDir := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", archiveDir)

	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
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
		Config:   dsl.ScriptConfig{BaseURL: "https://example.com"},
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
