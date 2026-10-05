package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestMonitoringCaptureDoesNotBlockPublicationAndCancelsRequests(t *testing.T) {
	t.Setenv(devFakeSecretsEnv, "true")
	started := make(chan string, 4)
	release := make(chan struct{})
	fixture := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		started <- r.URL.Path
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer fixture.Close()
	defer close(release)
	runner := NewTestRunner(1, NewHub(), nil)
	test := &ManagedTest{ID: "slow-monitoring", Status: "RUNNING", executionCtx: t.Context(), CreatedAt: time.Now(),
		ELKCapture:       &ELKCaptureConfig{ConnectionID: "elk", Endpoint: fixture.URL, IndexPattern: "logs", AuthType: storage.ELKAuthAPIKey, APIKeySecretName: "token"},
		DynatraceCapture: &DynatraceCaptureConfig{ConnectionID: "dt", EnvironmentURL: fixture.URL, EnvironmentTokenSecret: "token", ProblemsEnabled: true}}
	artifacts := &runArtifacts{runner: runner, test: test, monitorHTTPClient: fixture.Client()}
	defer artifacts.stopMonitoring()
	returned := make(chan struct{})
	go func() {
		artifacts.enrichLiveMetrics(test, &LiveMetrics{Timestamp: time.Now()})
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("metric publication waited for monitoring")
	}
	for range 2 {
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatal("independent provider workers did not start")
		}
	}
	// Repeated metric publication must not spawn more concurrent requests.
	for range 20 {
		artifacts.enrichLiveMetrics(test, &LiveMetrics{Timestamp: time.Now()})
	}
	stopped := make(chan struct{})
	go func() { artifacts.stopMonitoring(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("shutdown waited for the HTTP timeout instead of cancelling")
	}
	artifacts.enrichLiveMetrics(test, &LiveMetrics{Timestamp: time.Now()})
	select {
	case path := <-started:
		t.Fatalf("unexpected extra request: %s", path)
	default:
	}
}

func TestDistributedMonitoringFeedsObservedTracesIntoBothCaptures(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	test := &ManagedTest{ID: "remote", ELKCapture: &ELKCaptureConfig{}, DynatraceCapture: &DynatraceCaptureConfig{}}
	runner.tests[test.ID] = test
	artifacts := distributedMonitoringArtifacts(runner, test)
	transactions := make([]generatorrpc.TransactionMetric, 0, 80)
	for i := 1; i <= 80; i++ {
		transactions = append(transactions, generatorrpc.TransactionMetric{Name: fmt.Sprintf("request-%d", i), Success: true, CorrelationID: fmt.Sprintf("00-%032x-00f067aa0ba902b7-01", i)})
	}
	live := buildLiveMetricsFromDistributedBatch(test, generatorrpc.MetricsBatch{TestID: test.ID, TimestampUnixMs: time.Now().UnixMilli(), WindowMs: 1000, Transactions: transactions}, 80, nil)
	artifacts.enrichLiveMetrics(test, live)
	elk, dynatrace := artifacts.elkLookups.take(), artifacts.dynatraceLookups.take()
	if len(elk) != 64 || len(dynatrace) != 64 {
		t.Fatalf("trace bounds: ELK=%d Dynatrace=%d", len(elk), len(dynatrace))
	}
	for _, traceID := range elk {
		if len(traceID) != 32 {
			t.Fatalf("invalid observed trace ID: %s", traceID)
		}
	}
}

func TestDynatraceEvidenceReservesProblemsAndUpdatesResolutionInArchive(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	test := &ManagedTest{ID: "updated-problems", DynatraceCapture: &DynatraceCaptureConfig{ConnectionID: "dt"}}
	writer, err := newRunArchiveWriter(test, RunArchiveProvenance{})
	if err != nil {
		t.Fatal(err)
	}
	artifacts := &runArtifacts{test: test, recorder: writer}
	var logs []DynatraceLogSample
	var problems []DynatraceProblemSummary
	for i := range 100 {
		logs = append(logs, DynatraceLogSample{Message: fmt.Sprintf("log-%d", i)})
		problems = append(problems, DynatraceProblemSummary{ID: fmt.Sprintf("p-%d", i), Status: "OPEN"})
	}
	fresh := artifacts.retainDynatraceEvidence(logs, problems)
	if len(fresh) != 50 || len(artifacts.dynatraceProblems) != 50 {
		t.Fatal("capabilities did not get independent evidence budgets")
	}
	if err := writer.RecordDynatraceEvidence(fresh); err != nil {
		t.Fatal(err)
	}
	closed := DynatraceProblemSummary{ID: "p-0", Status: "CLOSED", EndTime: time.Now().UTC()}
	if fresh := artifacts.retainDynatraceEvidence(logs, []DynatraceProblemSummary{closed}); len(fresh) != 0 {
		t.Fatal("duplicate logs retained")
	}
	if artifacts.dynatraceProblems[0].Status != "CLOSED" || artifacts.dynatraceProblems[0].EndTime.IsZero() {
		t.Fatal("resolution was dropped at capacity")
	}
	record, status, finalErr := artifacts.finalize(test, "COMPLETE", time.Now(), "")
	if record == nil || status != "COMPLETE" || finalErr != "" {
		t.Fatalf("finalize: %v %s %s", record, status, finalErr)
	}
	manifest, err := loadRunArchiveManifest(record.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.DynatraceEvidenceCount != 100 {
		t.Fatalf("archive records = %d", manifest.DynatraceEvidenceCount)
	}
	data, err := os.ReadFile(filepath.Join(record.ArchivePath, manifest.DynatraceEvidenceFile))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"status":"CLOSED"`) {
		t.Fatal("archive did not contain latest problem resolution")
	}
}

func TestELKIncompletePollPreservesGoodValues(t *testing.T) {
	lastSuccess := time.Now().UTC().Add(-time.Minute)
	t.Setenv(devFakeSecretsEnv, "true")
	fixture := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"timed_out":true,"hits":{"total":{"value":1},"hits":[]}}`))
	}))
	defer fixture.Close()
	test := &ManagedTest{ID: "partial", ELKCapture: &ELKCaptureConfig{ConnectionID: "elk", Endpoint: fixture.URL, IndexPattern: "logs", AuthType: storage.ELKAuthAPIKey, APIKeySecretName: "token"}}
	artifacts := &runArtifacts{runner: NewTestRunner(1, NewHub(), nil), test: test, monitorHTTPClient: fixture.Client(), elkStatus: &ELKCaptureStatus{Status: "connected", Metrics: map[string]float64{"logs_total": 99}}}
	live := &LiveMetrics{Timestamp: time.Now()}
	artifacts.elkStatus.LastSuccessfulAt = &lastSuccess
	artifacts.collectELKMetrics(context.Background(), test, live)
	if live.ELK.LastSuccessfulAt == nil || !live.ELK.LastSuccessfulAt.Equal(lastSuccess) || live.ELK.LastPolledAt == nil || !live.ELK.LastPolledAt.After(lastSuccess) {
		t.Fatalf("failed poll changed successful freshness: %#v", live.ELK)
	}
	if live.ELK.Status != "unavailable" || live.ELK.Metrics["logs_total"] != 99 {
		t.Fatalf("partial response replaced good state: %#v", live.ELK)
	}
}

func TestDynatraceOutagePreservesGoodValuesWithoutPanic(t *testing.T) {
	lastSuccess := time.Now().UTC().Add(-time.Minute)
	t.Setenv(devFakeSecretsEnv, "true")
	fixture := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	defer fixture.Close()
	test := &ManagedTest{ID: "outage", DynatraceCapture: &DynatraceCaptureConfig{ConnectionID: "dt", EnvironmentURL: fixture.URL, EnvironmentTokenSecret: "token", ProblemsEnabled: true}}
	artifacts := &runArtifacts{runner: NewTestRunner(1, NewHub(), nil), test: test, monitorHTTPClient: fixture.Client(),
		dynatraceStatus: &DynatraceCaptureStatus{Status: "connected", Metrics: map[string]float64{"problems.total": 99, "problems.open": 55}}}
	live := &LiveMetrics{Timestamp: time.Now()}
	artifacts.dynatraceStatus.LastSuccessfulAt = &lastSuccess
	artifacts.collectDynatraceMetrics(t.Context(), test, live)
	if live.Dynatrace.LastSuccessfulAt == nil || !live.Dynatrace.LastSuccessfulAt.Equal(lastSuccess) || live.Dynatrace.LastPolledAt == nil || !live.Dynatrace.LastPolledAt.After(lastSuccess) {
		t.Fatalf("failed poll changed successful freshness: %#v", live.Dynatrace)
	}
	if live.Dynatrace.Status != "unavailable" || live.Dynatrace.Metrics["problems.open"] != 55 || live.Dynatrace.Metrics["problems.total"] != 99 {
		t.Fatalf("outage replaced good state: %#v", live.Dynatrace)
	}
}
