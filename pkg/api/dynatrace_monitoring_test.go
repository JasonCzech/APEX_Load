package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestDynatraceLogFetchUsesFullSoakTestWindow(t *testing.T) {
	t.Setenv(devFakeSecretsEnv, "true")
	start := time.Date(2026, 9, 20, 1, 0, 0, 0, time.UTC)
	end := start.Add(6 * time.Hour)
	calls := 0
	fixture := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		prefix := `fetch logs, from: toTimestamp("2026-09-20T01:00:00Z"), to: toTimestamp("2026-09-20T07:00:00Z") |`
		if !strings.HasPrefix(body.Query, prefix) {
			t.Errorf("fetch window missing: %s", body.Query)
		}
		if strings.Contains(body.Query, "summarize") {
			_, _ = w.Write([]byte(`{"state":"SUCCEEDED","result":{"records":[{"logs_total":400,"errors_total":30}]}}`))
		} else {
			_, _ = w.Write([]byte(`{"state":"SUCCEEDED","result":{"records":[]}}`))
		}
	}))
	defer fixture.Close()
	cfg := &DynatraceCaptureConfig{PlatformURL: fixture.URL, PlatformAuthType: storage.DynatracePlatformAuthToken, PlatformTokenSecret: "token", LogTimestampField: "timestamp", LogStatusField: "status", LogMessageField: "content"}
	_, totals, err := queryDynatraceLogs(t.Context(), NewTestRunner(1, NewHub(), nil), cfg, "run", nil, start, end, fixture.Client())
	if err != nil || calls != 2 || totals["logs.total"] != 400 {
		t.Fatalf("totals=%v calls=%d err=%v", totals, calls, err)
	}
}

func TestDynatraceProblemCountsIncludeLaterPages(t *testing.T) {
	t.Setenv(devFakeSecretsEnv, "true")
	for _, mode := range []string{"complete", "cycle", "budget", "failure"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			fixture := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if calls > 1 && (len(r.URL.Query()) != 1 || r.URL.Query().Get("nextPageKey") == "") {
					t.Errorf("invalid next-page parameters: %s", r.URL.RawQuery)
				}
				if mode == "failure" && calls == 2 {
					w.WriteHeader(503)
					return
				}
				var problems []map[string]string
				for i := range 100 {
					problems = append(problems, map[string]string{"problemId": fmt.Sprintf("p-%d-%d", calls, i), "status": "OPEN"})
				}
				next := ""
				if calls == 1 || mode == "cycle" {
					next = "page-2"
				}
				if mode == "budget" {
					next = fmt.Sprintf("page-%d", calls+1)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"totalCount": 200, "nextPageKey": next, "problems": problems})
			}))
			defer fixture.Close()
			cfg := &DynatraceCaptureConfig{EnvironmentURL: fixture.URL, EnvironmentTokenSecret: "token", ProblemSelector: `severityLevel("ERROR")`}
			problems, total, open, err := queryDynatraceProblems(t.Context(), NewTestRunner(1, NewHub(), nil), cfg, time.Now().Add(-time.Hour), time.Now(), fixture.Client())
			if mode == "complete" {
				if err != nil || total != 200 || open != 200 || len(problems) != 200 || calls != 2 {
					t.Fatalf("problems=%d total=%v open=%v calls=%d err=%v", len(problems), total, open, calls, err)
				}
			} else if err == nil || open != 0 || total != 0 || len(problems) != 0 || calls > 10 {
				t.Fatalf("partial count published: %v %v %v, calls=%d", total, open, err, calls)
			}
		})
	}
}

func TestDynatraceDQLPollingHonorsCancellation(t *testing.T) {
	t.Setenv(devFakeSecretsEnv, "true")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	client := &http.Client{Transport: dynatraceRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := `{"state":"RUNNING","requestToken":"token"}`
		if strings.HasSuffix(r.URL.Path, "query:poll") {
			cancel()
			body = `{"state":"RUNNING"}`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	cfg := &DynatraceCaptureConfig{PlatformURL: "https://tenant.apps.dynatrace.com", PlatformAuthType: storage.DynatracePlatformAuthToken, PlatformTokenSecret: "token"}
	_, err := executeDynatraceDQL(ctx, NewTestRunner(1, NewHub(), nil), cfg, "fetch logs", client)
	if err != context.Canceled {
		t.Fatalf("cancellation = %v", err)
	}
}

type dynatraceRoundTripFunc func(*http.Request) (*http.Response, error)

func (f dynatraceRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestDynatraceConnectionResponseDoesNotDiscloseSecretReferences(t *testing.T) {
	c := &storage.DynatraceConnection{ID: "dt", ProjectID: "project", Name: "Dynatrace", EnvironmentTokenSecret: "environment-ref", PlatformTokenSecret: "platform-ref", OAuthClientIDSecret: "client-ref", OAuthClientSecretSecret: "secret-ref"}
	encoded, err := json.Marshal(dynatraceConnectionResponse(c))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"environment-ref", "platform-ref", "client-ref", "secret-ref"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("response disclosed %q: %s", secret, encoded)
		}
	}
	if !strings.Contains(string(encoded), `"has_environment_token":true`) || !strings.Contains(string(encoded), `"has_platform_token":true`) {
		t.Fatalf("configured flags missing: %s", encoded)
	}
}

func TestBuildDynatraceLogFilterUsesTestThenTraceCorrelation(t *testing.T) {
	start := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	cfg := &DynatraceCaptureConfig{LogTimestampField: "timestamp", LogTestIDField: "apex.test_id", LogTraceIDField: "trace_id", LogBaseFilter: `service.name == "checkout"`}
	withTest := buildDynatraceLogFilter(cfg, `run-"1`, []string{"trace-1"}, start, start.Add(time.Minute))
	if !strings.Contains(withTest, `apex.test_id == "run-\"1"`) || strings.Contains(withTest, "trace-1") {
		t.Fatalf("test filter = %s", withTest)
	}
	cfg.LogTestIDField = ""
	withTrace := buildDynatraceLogFilter(cfg, "run-1", []string{"trace-1", "trace-2"}, start, start.Add(time.Minute))
	if !strings.Contains(withTrace, `in(toString(trace_id), {"trace-1","trace-2"})`) {
		t.Fatalf("trace filter = %s", withTrace)
	}
}

func TestQueryDynatraceEnvironmentCapabilitiesAndGrailLogs(t *testing.T) {
	t.Setenv(devFakeSecretsEnv, "true")
	fixture := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Api-Token apexload-dev-secret" && r.Header.Get("Authorization") != "Bearer apexload-dev-secret" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		switch r.URL.Path {
		case "/api/v2/metrics/query":
			_, _ = w.Write([]byte(`{"result":[{"data":[{"values":[10,null,20]}]}]}`))
		case "/api/v2/problems":
			_, _ = w.Write([]byte(`{"totalCount":1,"problems":[{"problemId":"p-1","displayId":"P-1","title":"password=hunter2","severityLevel":"ERROR","status":"OPEN","startTime":1789905600000,"affectedEntities":[{"name":"checkout"}]}]}`))
		case "/platform/storage/query/v1/query:execute":
			_, _ = w.Write([]byte(`{"result":{"records":[{"timestamp":"2026-09-20T12:00:10Z","status":"ERROR","content":"Authorization: Bearer private-token","apex.test_id":"run-1","trace_id":"trace-1"}]}}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer fixture.Close()
	runner := NewTestRunner(1, NewHub(), nil)
	start := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	cfg := &DynatraceCaptureConfig{ProjectID: "project", EnvironmentURL: fixture.URL, PlatformURL: fixture.URL, MetricsEnabled: true, ProblemsEnabled: true, LogsEnabled: true,
		EnvironmentTokenSecret: "environment", PlatformAuthType: storage.DynatracePlatformAuthToken, PlatformTokenSecret: "platform",
		MetricQueries:     []storage.DynatraceMetricQuery{{Name: "latency", MetricSelector: "builtin:service.response.time", Aggregation: "avg"}},
		LogTimestampField: "timestamp", LogStatusField: "status", LogMessageField: "content", LogTestIDField: "apex.test_id", LogTraceIDField: "trace_id"}
	metrics, err := queryDynatraceMetrics(t.Context(), runner, cfg, start, start.Add(time.Minute), fixture.Client())
	if err != nil || metrics["latency"] != 15 {
		t.Fatalf("metrics = %#v, %v", metrics, err)
	}
	problems, count, open, err := queryDynatraceProblems(t.Context(), runner, cfg, start, start.Add(time.Minute), fixture.Client())
	if err != nil || count != 1 || open != 1 || len(problems) != 1 || strings.Contains(problems[0].Title, "hunter2") {
		t.Fatalf("problems = %#v count=%v err=%v", problems, count, err)
	}
	logs, totals, err := queryDynatraceLogs(t.Context(), runner, cfg, "run-1", nil, start, start.Add(time.Minute), fixture.Client())
	if err != nil || totals["logs.errors"] != 1 || totals["logs.total"] != 1 || len(logs) != 1 || strings.Contains(logs[0].Message, "private-token") {
		t.Fatalf("logs = %#v totals=%#v err=%v", logs, totals, err)
	}
}

func TestExecuteDynatraceDQLPollsBoundedAsyncQuery(t *testing.T) {
	t.Setenv(devFakeSecretsEnv, "true")
	polls := 0
	fixture := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/platform/storage/query/v1/query:execute":
			_, _ = w.Write([]byte(`{"requestToken":"request-1","state":"RUNNING"}`))
		case "/platform/storage/query/v1/query:poll":
			polls++
			if r.URL.Query().Get("request-token") != "request-1" {
				t.Errorf("poll token = %q", r.URL.Query().Get("request-token"))
			}
			if r.URL.Query().Get("request-timeout-milliseconds") != "5000" {
				t.Error("poll must request a bounded server wait")
			}
			if polls < 3 {
				_, _ = w.Write([]byte(`{"state":"RUNNING"}`))
				return
			}
			_, _ = w.Write([]byte(`{"state":"SUCCEEDED","result":{"records":[{"content":"done"}]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer fixture.Close()
	cfg := &DynatraceCaptureConfig{ProjectID: "project", ConnectionID: "dt", PlatformURL: fixture.URL, PlatformAuthType: storage.DynatracePlatformAuthToken, PlatformTokenSecret: "token"}
	records, err := executeDynatraceDQL(t.Context(), NewTestRunner(1, NewHub(), nil), cfg, "fetch logs", fixture.Client())
	if err != nil || polls != 3 || len(records) != 1 || records[0]["content"] != "done" {
		t.Fatalf("records=%#v polls=%d err=%v", records, polls, err)
	}
}

func TestExecuteDynatraceDQLReportsFailedQueryAsCaptureError(t *testing.T) {
	t.Setenv(devFakeSecretsEnv, "true")
	fixture := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"requestToken":"request-1","state":"FAILED"}`))
	}))
	defer fixture.Close()
	cfg := &DynatraceCaptureConfig{ProjectID: "project", ConnectionID: "dt", PlatformURL: fixture.URL, PlatformAuthType: storage.DynatracePlatformAuthToken, PlatformTokenSecret: "token"}
	if _, err := executeDynatraceDQL(t.Context(), NewTestRunner(1, NewHub(), nil), cfg, "fetch logs", fixture.Client()); err == nil {
		t.Fatal("failed Grail query was reported as successful")
	}
}

func TestDynatraceOAuthUsesProjectSecretsAndCachesToken(t *testing.T) {
	t.Setenv(devFakeSecretsEnv, "true")
	tokenCalls := 0
	client := &http.Client{Transport: dynatraceRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		tokenCalls++
		if request.URL.Hostname() != "sso.dynatrace.com" {
			t.Errorf("OAuth host = %q", request.URL.Hostname())
		}
		data, _ := io.ReadAll(request.Body)
		if !strings.Contains(string(data), "client_id=apexload-dev-secret") || !strings.Contains(string(data), "client_secret=apexload-dev-secret") {
			t.Errorf("OAuth form = %s", data)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"access_token":"oauth-token","expires_in":300}`)), Header: make(http.Header)}, nil
	})}
	runner := NewTestRunner(1, NewHub(), nil)
	cfg := &DynatraceCaptureConfig{ProjectID: "project", ConnectionID: "dt", PlatformAuthType: storage.DynatracePlatformAuthOAuth, OAuthClientIDSecret: "client-id", OAuthClientSecretSecret: "client-secret", OAuthResource: "urn:dtaccount:account", OAuthScopes: []string{"storage:logs:read"}}
	for index := 0; index < 2; index++ {
		request, _ := http.NewRequest(http.MethodPost, "https://tenant.apps.dynatrace.com/platform/storage/query/v1/query:execute", nil)
		if err := applyDynatracePlatformAuthentication(t.Context(), runner, cfg, request, client); err != nil {
			t.Fatal(err)
		}
		if request.Header.Get("Authorization") != "Bearer oauth-token" {
			t.Fatalf("authorization = %q", request.Header.Get("Authorization"))
		}
	}
	if tokenCalls != 1 {
		t.Fatalf("OAuth token requests = %d, want 1", tokenCalls)
	}
}

func TestDynatraceOAuthRefreshIsSingleFlight(t *testing.T) {
	t.Setenv(devFakeSecretsEnv, "true")
	var tokenCalls atomic.Int32
	client := &http.Client{Transport: dynatraceRoundTripFunc(func(*http.Request) (*http.Response, error) {
		tokenCalls.Add(1)
		time.Sleep(20 * time.Millisecond)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"access_token":"oauth-token","expires_in":300}`)), Header: make(http.Header)}, nil
	})}
	runner := NewTestRunner(1, NewHub(), nil)
	cfg := &DynatraceCaptureConfig{ProjectID: "project", ConnectionID: "dt", PlatformAuthType: storage.DynatracePlatformAuthOAuth, OAuthClientIDSecret: "client-id", OAuthClientSecretSecret: "client-secret", OAuthResource: "urn:dtaccount:account", OAuthScopes: []string{"storage:logs:read"}}
	start := make(chan struct{})
	errors := make(chan error, 8)
	var workers sync.WaitGroup
	for range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			request, _ := http.NewRequest(http.MethodPost, "https://tenant.apps.dynatrace.com/platform/storage/query/v1/query:execute", nil)
			errors <- applyDynatracePlatformAuthentication(t.Context(), runner, cfg, request, client)
		}()
	}
	close(start)
	workers.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := tokenCalls.Load(); got != 1 {
		t.Fatalf("OAuth token requests = %d, want 1", got)
	}
}

func TestDynatraceOAuthWaiterCanCancelSharedRefresh(t *testing.T) {
	t.Setenv(devFakeSecretsEnv, "true")
	started, release := make(chan struct{}), make(chan struct{})
	client := &http.Client{Transport: dynatraceRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		close(started)
		<-release
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"access_token":"oauth-token","expires_in":300}`))}, nil
	})}
	runner := NewTestRunner(1, NewHub(), nil)
	cfg := &DynatraceCaptureConfig{PlatformAuthType: storage.DynatracePlatformAuthOAuth, OAuthClientIDSecret: "client", OAuthClientSecretSecret: "secret"}
	owner := make(chan error, 1)
	go func() {
		req, _ := http.NewRequest(http.MethodPost, "https://tenant.apps.dynatrace.com", nil)
		owner <- applyDynatracePlatformAuthentication(t.Context(), runner, cfg, req, client)
	}()
	<-started
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	req, _ := http.NewRequest(http.MethodPost, "https://tenant.apps.dynatrace.com", nil)
	waiter := make(chan error, 1)
	go func() { waiter <- applyDynatracePlatformAuthentication(ctx, runner, cfg, req, client) }()
	select {
	case err := <-waiter:
		if err != context.Canceled {
			t.Errorf("waiter error = %v", err)
		}
	case <-time.After(time.Second):
		t.Error("cancelled run waited on another run's OAuth request")
	}
	close(release)
	if err := <-owner; err != nil {
		t.Fatal(err)
	}
}

func TestRunArchiveWriterRecordsDynatraceEvidence(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
	capture := &DynatraceCaptureConfig{ConnectionID: "dt-a", ConnectionName: "Production Dynatrace", EnvironmentURL: "https://private.live.dynatrace.com", EnvironmentTokenSecret: "private-secret-reference"}
	w, err := newRunArchiveWriter(&ManagedTest{ID: "dynatrace-evidence", Name: "Dynatrace evidence", DynatraceCapture: capture}, RunArchiveProvenance{DynatraceCapture: capture})
	if err != nil {
		t.Fatal(err)
	}
	logSample := DynatraceLogSample{Timestamp: time.Now().UTC(), Status: "error", Message: "password=hunter2"}
	problem := DynatraceProblemSummary{ID: "p-1", Title: "Authorization: Bearer private-token"}
	if err := w.RecordDynatraceEvidence([]DynatraceEvidenceRecord{{Type: "log_sample", Log: &logSample}, {Type: "problem_summary", Problem: &problem}}); err != nil {
		t.Fatal(err)
	}
	record, err := w.Finalize("COMPLETE", time.Now().UTC(), "", false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := loadRunArchiveManifest(record.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.DynatraceEvidenceCount != 2 || manifest.DynatraceEvidenceFile == "" {
		t.Fatalf("manifest = %#v", manifest)
	}
	manifestJSON, _ := json.Marshal(manifest)
	if strings.Contains(string(manifestJSON), "private.live.dynatrace.com") || strings.Contains(string(manifestJSON), "private-secret-reference") || !strings.Contains(string(manifestJSON), "Production Dynatrace") {
		t.Fatalf("manifest capture disclosure = %s", manifestJSON)
	}
	data, err := os.ReadFile(filepath.Join(record.ArchivePath, manifest.DynatraceEvidenceFile))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "hunter2") || strings.Contains(string(data), "private-token") {
		t.Fatalf("evidence was not redacted: %s", data)
	}
	report, err := buildRunArchiveReport(record)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.DynatraceEvidence) != 2 {
		t.Fatalf("report evidence = %#v", report.DynatraceEvidence)
	}
}

func TestResolveDynatraceCaptureRejectsCrossProjectAttachment(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir() + "/attachment.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	for _, id := range []string{"project-a", "project-b"} {
		if err := store.SaveProject(&storage.Project{ID: id, Name: id, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	c := &storage.DynatraceConnection{ID: "dt-a", ProjectID: "project-a", Name: "Dynatrace", Enabled: true, EnvironmentURL: "https://abc.live.dynatrace.com", MetricsEnabled: true, EnvironmentTokenSecret: "token", MetricQueries: []storage.DynatraceMetricQuery{{Name: "latency", MetricSelector: "builtin:service.response.time", Aggregation: "avg"}}}
	if err := store.CreateDynatraceConnection(c); err != nil {
		t.Fatal(err)
	}
	runner := NewTestRunner(1, NewHub(), store)
	if _, err := runner.resolveDynatraceCaptureForTest("project-b", &DynatraceCaptureConfig{ConnectionID: c.ID}); err == nil || !strings.Contains(err.Error(), "different project") {
		t.Fatalf("cross-project error = %v", err)
	}
	capture, err := runner.resolveDynatraceCaptureForTest("project-a", &DynatraceCaptureConfig{ConnectionID: c.ID})
	if err != nil || capture == nil || capture.EnvironmentTokenSecret != "token" {
		t.Fatalf("capture = %#v, %v", capture, err)
	}
}

func TestWriteDistributedDynatraceEvidenceArchivesControllerCapture(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "resources.jsonl"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	artifacts := &runArtifacts{dynatraceLogs: []DynatraceLogSample{{Message: "password=hunter2"}}, dynatraceProblems: []DynatraceProblemSummary{{ID: "p-1", Title: "Checkout failure"}}, dynatraceStatus: &DynatraceCaptureStatus{Metrics: map[string]float64{"logs.total": 7}}}
	test := &ManagedTest{ID: "distributed-dynatrace", Name: "Distributed", DynatraceCapture: &DynatraceCaptureConfig{ConnectionID: "dt-a"}, dynatraceArtifacts: artifacts}
	manifest := &RunArchiveManifest{ResourceSamplesFile: "resources.jsonl"}
	if err := writeDistributedDynatraceEvidence(root, manifest, nil, test, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if manifest.DynatraceEvidenceCount != 2 || manifest.ResourceSampleCount != 1 {
		t.Fatalf("manifest counts = %#v", manifest)
	}
	evidence, err := os.ReadFile(filepath.Join(root, manifest.DynatraceEvidenceFile))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(evidence), "hunter2") {
		t.Fatalf("distributed evidence was not redacted: %s", evidence)
	}
	resources, err := os.ReadFile(filepath.Join(root, manifest.ResourceSamplesFile))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(resources), `"dynatrace.dt-a.logs.total":7`) {
		t.Fatalf("distributed metrics missing: %s", resources)
	}
}
