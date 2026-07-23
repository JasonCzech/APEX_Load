package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

// fakePowerBI is an httptest-backed stand-in for the Azure AD token endpoint
// plus the Power BI groups/datasets/tables/rows API surface the client uses.
type fakePowerBI struct {
	mu sync.Mutex

	tokenRequests int
	failToken     bool
	// unauthorizedDatasetsOnce makes the next datasets GET return 401,
	// exercising the client's token-cache invalidation retry.
	unauthorizedDatasetsOnce bool

	datasets       []powerBIDataset
	tablesByID     map[string][]string
	rowsByTable    map[string][][]map[string]any
	rowPostCount   int
	rowFailures    map[int]int // 1-based row POST index -> status to return once
	pushSequence   []string    // table name per successful rows POST, in order
	createdPayload map[string]any

	srv *httptest.Server
}

func newFakePowerBI(t *testing.T) *fakePowerBI {
	f := &fakePowerBI{
		tablesByID:  map[string][]string{},
		rowsByTable: map[string][][]map[string]any{},
		rowFailures: map[int]int{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /tenant-1/oauth2/v2.0/token", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.tokenRequests++
		if f.failToken {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid_client"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"fake-token","token_type":"Bearer","expires_in":3600}`))
	})
	mux.HandleFunc("GET /v1.0/myorg/groups/{gid}/datasets", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.unauthorizedDatasetsOnce {
			f.unauthorizedDatasetsOnce = false
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"value": f.datasets})
	})
	mux.HandleFunc("POST /v1.0/myorg/groups/{gid}/datasets", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		f.createdPayload = payload
		id := fmt.Sprintf("ds-%d", len(f.datasets)+1)
		name, _ := payload["name"].(string)
		f.datasets = append(f.datasets, powerBIDataset{ID: id, Name: name})
		tables := []string{}
		if declared, ok := payload["tables"].([]any); ok {
			for _, entry := range declared {
				if table, ok := entry.(map[string]any); ok {
					if tableName, ok := table["name"].(string); ok {
						tables = append(tables, tableName)
					}
				}
			}
		}
		f.tablesByID[id] = tables
		writeJSON(w, http.StatusCreated, map[string]any{"id": id, "name": name})
	})
	mux.HandleFunc("GET /v1.0/myorg/groups/{gid}/datasets/{did}/tables", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		value := []map[string]any{}
		for _, name := range f.tablesByID[r.PathValue("did")] {
			value = append(value, map[string]any{"name": name})
		}
		writeJSON(w, http.StatusOK, map[string]any{"value": value})
	})
	mux.HandleFunc("POST /v1.0/myorg/groups/{gid}/datasets/{did}/tables/{table}/rows", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.rowPostCount++
		if status, ok := f.rowFailures[f.rowPostCount]; ok {
			delete(f.rowFailures, f.rowPostCount)
			if status == http.StatusTooManyRequests {
				w.Header().Set("Retry-After", "0")
			}
			w.WriteHeader(status)
			return
		}
		var payload struct {
			Rows []map[string]any `json:"rows"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		table := r.PathValue("table")
		f.rowsByTable[table] = append(f.rowsByTable[table], payload.Rows)
		f.pushSequence = append(f.pushSequence, table)
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("DELETE /v1.0/myorg/groups/{gid}/datasets/{did}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		id := r.PathValue("did")
		kept := f.datasets[:0]
		for _, ds := range f.datasets {
			if ds.ID != id {
				kept = append(kept, ds)
			}
		}
		f.datasets = kept
		delete(f.tablesByID, id)
		w.WriteHeader(http.StatusOK)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakePowerBI) settings(t *testing.T) PowerBISettings {
	t.Setenv("POWERBI_TEST_SECRET", "s3cret")
	// The fake is plain-http loopback; the publish path enforces the
	// credential-transport policy, so opt into the documented dev escape hatch.
	t.Setenv(powerBIInsecureHTTPEnv, "true")
	return normalizePowerBISettings(PowerBISettings{
		Enabled:            true,
		TenantID:           "tenant-1",
		ClientID:           "client-1",
		ClientSecretEnvVar: "POWERBI_TEST_SECRET",
		WorkspaceID:        "11111111-2222-3333-4444-555555555555",
		AuthBaseURL:        f.srv.URL,
		APIBaseURL:         f.srv.URL + "/v1.0/myorg",
		TimeoutSeconds:     5,
	})
}

func (f *fakePowerBI) totalRows(table string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	total := 0
	for _, chunk := range f.rowsByTable[table] {
		total += len(chunk)
	}
	return total
}

func TestPowerBIClientEnsureDatasetCreatesAndReuses(t *testing.T) {
	fake := newFakePowerBI(t)
	client := newPowerBIClient(fake.settings(t))
	ctx := context.Background()

	id, created, err := client.ensureDataset(ctx)
	if err != nil {
		t.Fatalf("ensureDataset: %v", err)
	}
	if !created || id == "" {
		t.Fatalf("expected fresh dataset, got id=%q created=%v", id, created)
	}
	if fake.createdPayload["defaultMode"] != "Push" {
		t.Fatalf("dataset not created in push mode: %v", fake.createdPayload["defaultMode"])
	}
	if _, ok := fake.createdPayload["relationships"]; !ok {
		t.Fatalf("dataset created without the run_id relationship")
	}

	again, created, err := client.ensureDataset(ctx)
	if err != nil {
		t.Fatalf("ensureDataset reuse: %v", err)
	}
	if created || again != id {
		t.Fatalf("expected reuse of %q, got %q created=%v", id, again, created)
	}
	if fake.tokenRequests != 1 {
		t.Fatalf("token should be cached across calls, got %d requests", fake.tokenRequests)
	}
}

func TestPowerBIClientTokenFailure(t *testing.T) {
	fake := newFakePowerBI(t)
	fake.failToken = true
	client := newPowerBIClient(fake.settings(t))
	if _, _, err := client.ensureDataset(context.Background()); err == nil || !strings.Contains(err.Error(), "token") {
		t.Fatalf("expected token error, got %v", err)
	}
}

func TestPowerBIClientMissingSecret(t *testing.T) {
	fake := newFakePowerBI(t)
	settings := fake.settings(t)
	settings.ClientSecretEnvVar = "POWERBI_TEST_SECRET_UNSET"
	client := newPowerBIClient(settings)
	if _, _, err := client.ensureDataset(context.Background()); err == nil || !strings.Contains(err.Error(), "environment variable") {
		t.Fatalf("expected missing-secret error, got %v", err)
	}
}

func TestPowerBIClientPushRowsChunksAndRetries(t *testing.T) {
	fake := newFakePowerBI(t)
	client := newPowerBIClient(fake.settings(t))
	ctx := context.Background()
	id, _, err := client.ensureDataset(ctx)
	if err != nil {
		t.Fatalf("ensureDataset: %v", err)
	}

	rows := make([]map[string]any, powerBIMaxRowsPerPost+25)
	for i := range rows {
		rows[i] = map[string]any{"run_id": "r", "name": fmt.Sprintf("tx-%d", i)}
	}
	// First rows POST gets a 429 with Retry-After; the retry must succeed and
	// chunking must split into two POSTs (10k + 25).
	fake.rowFailures[1] = http.StatusTooManyRequests

	var progress []int
	pushed, err := client.pushRows(ctx, id, powerBITransactionsTable, rows, func(p int) { progress = append(progress, p) })
	if err != nil {
		t.Fatalf("pushRows: %v", err)
	}
	if pushed != len(rows) {
		t.Fatalf("expected %d rows pushed, got %d", len(rows), pushed)
	}
	if got := fake.totalRows(powerBITransactionsTable); got != len(rows) {
		t.Fatalf("fake recorded %d rows, want %d", got, len(rows))
	}
	if len(progress) != 2 || progress[0] != powerBIMaxRowsPerPost || progress[1] != len(rows) {
		t.Fatalf("unexpected chunk progress: %v", progress)
	}
}

func TestPowerBIClientPushRowsPartialFailure(t *testing.T) {
	fake := newFakePowerBI(t)
	client := newPowerBIClient(fake.settings(t))
	ctx := context.Background()
	id, _, err := client.ensureDataset(ctx)
	if err != nil {
		t.Fatalf("ensureDataset: %v", err)
	}
	rows := make([]map[string]any, powerBIMaxRowsPerPost+10)
	for i := range rows {
		rows[i] = map[string]any{"name": fmt.Sprintf("tx-%d", i)}
	}
	// Second chunk fails hard (400 is non-retryable) after the first lands.
	fake.rowFailures[2] = http.StatusBadRequest
	fake.rowFailures[3] = http.StatusBadRequest
	fake.rowFailures[4] = http.StatusBadRequest

	pushed, err := client.pushRows(ctx, id, powerBITransactionsTable, rows, nil)
	if err == nil {
		t.Fatalf("expected push failure")
	}
	if pushed != powerBIMaxRowsPerPost {
		t.Fatalf("expected %d rows pushed before failure, got %d", powerBIMaxRowsPerPost, pushed)
	}
}

func TestCheckPowerBIDatasetDrift(t *testing.T) {
	fake := newFakePowerBI(t)
	client := newPowerBIClient(fake.settings(t))
	ctx := context.Background()
	id, _, err := client.ensureDataset(ctx)
	if err != nil {
		t.Fatalf("ensureDataset: %v", err)
	}
	server := &Server{}

	if err := server.checkPowerBIDatasetDrift(ctx, client, id, nil); err != nil {
		t.Fatalf("fresh dataset should not drift: %v", err)
	}

	// Same dataset id but an older recorded schema version → drift.
	stale := &storage.PowerBIPublish{DatasetID: id, SchemaVersion: powerBIDatasetSchemaVersion + 1}
	if err := server.checkPowerBIDatasetDrift(ctx, client, id, stale); err == nil || !strings.Contains(err.Error(), "schema version") {
		t.Fatalf("expected schema-version drift error, got %v", err)
	}

	// Remote table set missing a table → drift.
	fake.mu.Lock()
	fake.tablesByID[id] = []string{powerBIRunsTable}
	fake.mu.Unlock()
	if err := server.checkPowerBIDatasetDrift(ctx, client, id, nil); err == nil || !strings.Contains(err.Error(), "expected runs/transactions tables") {
		t.Fatalf("expected table drift error, got %v", err)
	}
}

func TestPowerBIClientRefreshesTokenOn401(t *testing.T) {
	fake := newFakePowerBI(t)
	fake.unauthorizedDatasetsOnce = true
	client := newPowerBIClient(fake.settings(t))
	if _, _, err := client.ensureDataset(context.Background()); err != nil {
		t.Fatalf("ensureDataset should survive a 401 via token refresh: %v", err)
	}
	if fake.tokenRequests != 2 {
		t.Fatalf("expected a fresh token after the 401, got %d token requests", fake.tokenRequests)
	}
}

func TestValidatePowerBISettingsProductionRequiresHTTPS(t *testing.T) {
	t.Setenv("APEXLOAD_ENV", "production")
	settings := validEnabledPowerBISettings()
	settings.APIBaseURL = "http://powerbi.bi.internal/v1.0/myorg"
	err := validatePowerBISettings(settings)
	if err == nil || !strings.Contains(err.Error(), "https in production") {
		t.Fatalf("expected production https error, got %v", err)
	}
	settings.APIBaseURL = "https://powerbi.bi.internal/v1.0/myorg"
	if err := validatePowerBISettings(settings); err != nil {
		t.Fatalf("https settings should validate in production: %v", err)
	}
}

// TestPowerBIPublishHandlerEndToEnd drives the real HTTP handler through the
// server mux against a completed run's real archive and the fake Power BI:
// publish → duplicate 409 → forced re-publish, plus the partial-failure path
// where the runs commit-marker push fails.
func TestPowerBIPublishHandlerEndToEnd(t *testing.T) {
	archiveDir := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", archiveDir)

	fake := newFakePowerBI(t)
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	server := NewServer(ServerConfig{Store: store})
	protocolName := "powerbi-publish-stub-" + strings.ReplaceAll(strings.ToLower(t.Name()), "/", "-")
	protocol.Register(protocolName, func() protocol.Protocol {
		return &archiveQueryStubProtocol{}
	})
	server.runner.StoreScript(&dsl.Script{
		ID:       "script-powerbi-publish",
		Name:     "PowerBI Publish Script",
		Protocol: protocolName,
		Version:  "1.0.0",
		Actions:  []dsl.Action{{Name: "step-1", Type: "request", Target: "/"}},
	})
	testRun, err := server.runner.CreateTest(CreateTestRequest{
		Name:         "PowerBI Publish Run",
		Duration:     "1500ms",
		ScriptID:     "script-powerbi-publish",
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
	waitForTestStatus(t, server.runner, testRun.ID, "COMPLETE")

	settings, _ := server.runner.systemSettingsSnapshot()
	settings.PowerBI = fake.settings(t)
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings returned error: %v", err)
	}

	publishPath := "/api/v1/tests/" + testRun.ID + "/publish/powerbi"
	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, publishPath, strings.NewReader(body))
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		return resp
	}

	// The runs commit-marker push (2nd rows POST) fails hard → partial.
	fake.mu.Lock()
	fake.rowFailures[2] = http.StatusBadRequest
	fake.mu.Unlock()
	if resp := post(""); resp.Code != http.StatusBadGateway {
		t.Fatalf("partial publish status = %d, want 502; body=%s", resp.Code, resp.Body.String())
	}
	latest, err := store.GetLatestPowerBIPublishForTest(testRun.ID)
	if err != nil || latest == nil {
		t.Fatalf("expected a recorded publish, got %v err=%v", latest, err)
	}
	if latest.Status != storage.PowerBIPublishStatusPartial || latest.TransactionRowsPushed == 0 {
		t.Fatalf("expected partial publish with pushed tx rows, got %+v", latest)
	}

	// A partial publish blocks a plain re-publish: its rows are already in the
	// dataset and would double-count.
	if resp := post(""); resp.Code != http.StatusConflict {
		t.Fatalf("re-publish after partial status = %d, want 409; body=%s", resp.Code, resp.Body.String())
	}

	// Forced re-publish completes; the runs row must land last.
	resp := post(`{"force":true}`)
	if resp.Code != http.StatusOK {
		t.Fatalf("forced publish status = %d, want 200; body=%s", resp.Code, resp.Body.String())
	}
	var publishResponse struct {
		Publish storage.PowerBIPublish `json:"publish"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &publishResponse); err != nil {
		t.Fatalf("unmarshal publish response: %v", err)
	}
	if publishResponse.Publish.Status != storage.PowerBIPublishStatusComplete || publishResponse.Publish.RunRowsPushed != 1 {
		t.Fatalf("unexpected publish record: %+v", publishResponse.Publish)
	}
	fake.mu.Lock()
	sequence := append([]string(nil), fake.pushSequence...)
	fake.mu.Unlock()
	if len(sequence) < 2 || sequence[len(sequence)-1] != powerBIRunsTable || sequence[len(sequence)-2] != powerBITransactionsTable {
		t.Fatalf("runs row must be pushed last, sequence=%v", sequence)
	}
	if got := fake.totalRows(powerBIRunsTable); got != 1 {
		t.Fatalf("expected exactly 1 runs row in dataset, got %d", got)
	}
	// The partial attempt's transaction rows remain in the push dataset and the
	// forced re-publish appended a full second set — the double-count premise
	// the 409 guard and the publish_id dedupe guidance exist for.
	if got, want := fake.totalRows(powerBITransactionsTable), 2*publishResponse.Publish.TransactionRowsPushed; got != want {
		t.Fatalf("expected %d transaction rows (orphaned partial set + forced set), got %d", want, got)
	}

	// A completed publish blocks a plain re-publish and surfaces the record.
	resp = post("")
	if resp.Code != http.StatusConflict {
		t.Fatalf("duplicate publish status = %d, want 409; body=%s", resp.Code, resp.Body.String())
	}
	var conflict struct {
		Error   string                  `json:"error"`
		Publish *storage.PowerBIPublish `json:"publish"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &conflict); err != nil {
		t.Fatalf("unmarshal conflict response: %v", err)
	}
	if conflict.Publish == nil || conflict.Publish.Status != storage.PowerBIPublishStatusComplete || conflict.Error == "" {
		t.Fatalf("conflict payload missing prior publish: %+v", conflict)
	}

	// Status endpoint reflects the history, newest first.
	statusReq := httptest.NewRequest(http.MethodGet, publishPath, nil)
	statusResp := httptest.NewRecorder()
	server.mux.ServeHTTP(statusResp, statusReq)
	if statusResp.Code != http.StatusOK {
		t.Fatalf("status endpoint = %d, want 200", statusResp.Code)
	}
	var status struct {
		Enabled   bool                      `json:"enabled"`
		Publishes []*storage.PowerBIPublish `json:"publishes"`
	}
	if err := json.Unmarshal(statusResp.Body.Bytes(), &status); err != nil {
		t.Fatalf("unmarshal status response: %v", err)
	}
	if !status.Enabled || len(status.Publishes) < 2 || status.Publishes[0].Status != storage.PowerBIPublishStatusComplete {
		t.Fatalf("unexpected status payload: enabled=%v publishes=%d", status.Enabled, len(status.Publishes))
	}
}

func TestPowerBIPublishConflict(t *testing.T) {
	complete := &storage.PowerBIPublish{DatasetID: "ds-1", Status: storage.PowerBIPublishStatusComplete}
	partial := &storage.PowerBIPublish{DatasetID: "ds-1", Status: storage.PowerBIPublishStatusPartial, TransactionRowsPushed: 5, TransactionRowsTotal: 12}
	stalePending := &storage.PowerBIPublish{DatasetID: "ds-1", Status: storage.PowerBIPublishStatusPending, TransactionRowsPushed: 3}
	freshPending := &storage.PowerBIPublish{DatasetID: "ds-1", Status: storage.PowerBIPublishStatusPending}
	failed := &storage.PowerBIPublish{DatasetID: "ds-1", Status: storage.PowerBIPublishStatusFailed}

	cases := []struct {
		name      string
		latest    *storage.PowerBIPublish
		datasetID string
		force     bool
		want      bool
	}{
		{name: "no history", latest: nil, datasetID: "ds-1", want: false},
		{name: "complete blocks", latest: complete, datasetID: "ds-1", want: true},
		{name: "complete forced", latest: complete, datasetID: "ds-1", force: true, want: false},
		{name: "partial blocks without force", latest: partial, datasetID: "ds-1", want: true},
		{name: "pending with pushed rows blocks", latest: stalePending, datasetID: "ds-1", want: true},
		{name: "pending with no rows does not block", latest: freshPending, datasetID: "ds-1", want: false},
		{name: "failed with no rows does not block", latest: failed, datasetID: "ds-1", want: false},
		{name: "recreated dataset does not conflict with old history", latest: complete, datasetID: "ds-2", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := powerBIPublishConflict(tc.latest, tc.datasetID, tc.force)
			if got != tc.want {
				t.Fatalf("conflict=%v want %v (reason %q)", got, tc.want, reason)
			}
			if got && reason == "" {
				t.Fatalf("conflict without a reason message")
			}
		})
	}
}

func TestValidPowerBILinkHost(t *testing.T) {
	valid := []string{"apexload.example.com", "apexload.example.com:8443", "127.0.0.1:8080", "[::1]:8080", "[::1]", "internal-host"}
	for _, host := range valid {
		if !validPowerBILinkHost(host) {
			t.Fatalf("expected %q to be a valid link host", host)
		}
	}
	invalid := []string{"", "evil.example/path", "a.example.com@b.example.com", "host:port:extra", "host/../x", "host:notaport",
		// Bare unbracketed IPv6 would render a malformed URL; signed/zero ports
		// slip past a naive Atoi check.
		"::1", "host:0", "host:+8080", "host:-1"}
	for _, host := range invalid {
		if validPowerBILinkHost(host) {
			t.Fatalf("expected %q to be rejected", host)
		}
	}
}

func TestApexloadRunURLPrefersConfiguredBase(t *testing.T) {
	settings := normalizePowerBISettings(PowerBISettings{DashboardBaseURL: "https://apexload.example.com/"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tests/t1/publish/powerbi", nil)
	req.Host = "evil.example/injected"
	if got := apexloadRunURL(req, settings, "t1"); got != "https://apexload.example.com/tests/t1/report" {
		t.Fatalf("configured base not used: %q", got)
	}

	// Without a configured base, a forged Host that is not a plain host[:port]
	// must yield no link at all rather than a poisoned one.
	settings.DashboardBaseURL = ""
	if got := apexloadRunURL(req, settings, "t1"); got != "" {
		t.Fatalf("expected empty link for forged host, got %q", got)
	}
	req.Host = "apexload.internal:8080"
	if got := apexloadRunURL(req, settings, "t1"); got != "http://apexload.internal:8080/tests/t1/report" {
		t.Fatalf("unexpected fallback link: %q", got)
	}
}

func TestApplyPowerBIVerdictOverride(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	report := &RunArchiveReport{Verdict: &ReportVerdict{Status: reportVerdictFail, Failed: true}}
	applyPowerBIVerdictOverride(store, report, "test-1")
	if report.Verdict.Status != reportVerdictFail {
		t.Fatalf("verdict changed without any saved override: %+v", report.Verdict)
	}

	if err := saveReportAnnotations(store, &ReportAnnotations{
		TestID:             "test-1",
		Profile:            "executive",
		VerdictOverride:    "Conditional Pass",
		VerdictExplanation: "transient breach, self-recovered",
		SectionComments:    map[string]string{},
	}); err != nil {
		t.Fatalf("save annotations: %v", err)
	}
	applyPowerBIVerdictOverride(store, report, "test-1")
	if report.Verdict.Status != reportVerdictConditional || !report.Verdict.Conditional {
		t.Fatalf("executive override not applied: %+v", report.Verdict)
	}
}

func TestBuildPowerBIRows(t *testing.T) {
	completed := time.Date(2026, 7, 20, 12, 10, 0, 0, time.UTC)
	started := completed.Add(-10 * time.Minute)
	report := &RunArchiveReport{
		Manifest: &RunArchiveManifest{
			TestID:      "test-9",
			TestName:    "Checkout Load",
			Status:      "COMPLETED",
			CompletedAt: completed,
			Provenance: RunArchiveProvenance{
				ProjectID:   "proj-1",
				ReleaseID:   "rel-2026.07",
				StartedAt:   &started,
				TotalVUsers: 200,
			},
		},
		Overview: RunArchiveOverview{
			Transactions:     6000,
			Errors:           60,
			ErrorPct:         1.0,
			AvgResponseMs:    120,
			P95Ms:            300,
			PeakTPS:          25,
			PeakActiveVUsers: 200,
		},
		Verdict:   &ReportVerdict{Status: "Pass"},
		SLARollup: &RunArchiveSLARollup{Evaluated: 2, Passed: 1, Failed: 1},
		ByAction: map[string]RunArchiveActionStats{
			"checkout": {Transactions: 4000, ErrorPct: 0.5, P95Ms: 310},
			"browse":   {Transactions: 2000, ErrorPct: 2.0, P95Ms: 150},
		},
		ByGroup: map[string]RunArchiveActionStats{
			"buyers": {Transactions: 6000},
		},
		SLARows: map[string]SLAComplianceRow{
			"checkout": {Name: "checkout", HasSLA: true, SLAMs: 350, ResultKind: "pass"},
		},
	}

	publishedAt := completed.Add(time.Hour)
	runRow := buildPowerBIRunRow(report, "pub-1", publishedAt, "https://apex.example/tests/test-9/report")
	if runRow["run_id"] != "test-9" || runRow["release_id"] != "rel-2026.07" || runRow["verdict"] != "Pass" {
		t.Fatalf("unexpected run row identity fields: %+v", runRow)
	}
	if runRow["duration_seconds"] != 600.0 {
		t.Fatalf("expected 600s duration, got %v", runRow["duration_seconds"])
	}
	if runRow["avg_tps"] != 10.0 {
		t.Fatalf("expected avg_tps 10, got %v", runRow["avg_tps"])
	}
	if runRow["sla_failed"] != int64(1) {
		t.Fatalf("sla rollup not mapped: %v", runRow["sla_failed"])
	}

	txRows := buildPowerBITransactionRows(report, "pub-1", publishedAt)
	if len(txRows) != 3 {
		t.Fatalf("expected 3 transaction rows, got %d", len(txRows))
	}
	// Sorted actions first (browse, checkout), then groups.
	if txRows[0]["name"] != "browse" || txRows[0]["kind"] != "action" {
		t.Fatalf("unexpected first row: %+v", txRows[0])
	}
	if txRows[1]["name"] != "checkout" || txRows[1]["has_sla"] != true || txRows[1]["sla_result"] != "pass" || txRows[1]["sla_ms"] != 350.0 {
		t.Fatalf("sla fields not mapped onto checkout row: %+v", txRows[1])
	}
	if txRows[2]["name"] != "buyers" || txRows[2]["kind"] != "group" {
		t.Fatalf("unexpected group row: %+v", txRows[2])
	}

	// Every row key must exist in the declared table schema (≤75 columns).
	runCols := map[string]bool{}
	for _, col := range powerBIRunsColumns() {
		runCols[col["name"].(string)] = true
	}
	for key := range runRow {
		if !runCols[key] {
			t.Fatalf("run row key %q missing from declared schema", key)
		}
	}
	txCols := map[string]bool{}
	for _, col := range powerBITransactionsColumns() {
		txCols[col["name"].(string)] = true
	}
	for key := range txRows[0] {
		if !txCols[key] {
			t.Fatalf("transaction row key %q missing from declared schema", key)
		}
	}
}
