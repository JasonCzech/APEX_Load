package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
	localParquet "github.com/xitongsys/parquet-go-source/local"
	parquetReader "github.com/xitongsys/parquet-go/reader"
)

type archiveStreamingFakeClient struct {
	startupLifecycleFakeClient
	mu          sync.Mutex
	artifacts   map[string]string
	streamCalls int
	legacyCalls int
}

type parallelArchiveClient struct {
	*archiveStreamingFakeClient
	started chan<- string
	release <-chan struct{}
}

func (c *parallelArchiveClient) StreamArchiveArtifact(ctx context.Context, request generatorrpc.ArchiveArtifactRequest, destination io.Writer, maxBytes int64) (int64, error) {
	if request.Name == runArchiveManifestFileName {
		select {
		case c.started <- request.TestID:
		case <-ctx.Done():
			return 0, ctx.Err()
		}
		select {
		case <-c.release:
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	return c.archiveStreamingFakeClient.StreamArchiveArtifact(ctx, request, destination, maxBytes)
}

type retryingArchivePersistence struct {
	mu        sync.Mutex
	calls     int
	failCalls int
	record    *storage.RunArchiveRecord
}

func (s *retryingArchivePersistence) SaveRunArchive(record *storage.RunArchiveRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.calls <= s.failCalls {
		return errors.New("archive catalog unavailable")
	}
	copy := *record
	s.record = &copy
	return nil
}

func (s *retryingArchivePersistence) GetRunArchive(testID string) (*storage.RunArchiveRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.record == nil || s.record.TestID != testID {
		return nil, sql.ErrNoRows
	}
	copy := *s.record
	return &copy, nil
}

func (c *archiveStreamingFakeClient) GetArchiveArtifact(context.Context, generatorrpc.ArchiveArtifactRequest) ([]byte, error) {
	c.mu.Lock()
	c.legacyCalls++
	c.mu.Unlock()
	return nil, fmt.Errorf("legacy unary archive transfer must not be used")
}

func (c *archiveStreamingFakeClient) StreamArchiveArtifact(ctx context.Context, request generatorrpc.ArchiveArtifactRequest, destination io.Writer, maxBytes int64) (int64, error) {
	c.mu.Lock()
	c.streamCalls++
	path := c.artifacts[request.Name]
	c.mu.Unlock()
	if path == "" {
		return 0, os.ErrNotExist
	}
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return 0, err
	}
	if info.Size() > maxBytes {
		return 0, fmt.Errorf("artifact is %d bytes, limit is %d", info.Size(), maxBytes)
	}
	return copyWithContext(ctx, destination, file)
}

func copyWithContext(ctx context.Context, destination io.Writer, source io.Reader) (int64, error) {
	buffer := make([]byte, 32<<10)
	var copied int64
	for {
		if err := ctx.Err(); err != nil {
			return copied, err
		}
		n, readErr := source.Read(buffer)
		if n > 0 {
			written, writeErr := destination.Write(buffer[:n])
			copied += int64(written)
			if writeErr != nil {
				return copied, writeErr
			}
			if written != n {
				return copied, io.ErrShortWrite
			}
		}
		if readErr == io.EOF {
			return copied, nil
		}
		if readErr != nil {
			return copied, readErr
		}
	}
}

func TestStageRemoteArchiveUsesControllerTerminalStateAndFinalManifestParity(t *testing.T) {
	archiveRoot := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", archiveRoot)
	t.Setenv("APEXLOAD_REGION", "eastus-test")
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.SaveProject(&storage.Project{ID: "project-a", Name: "Project A", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}

	sourceDir := t.TempDir()
	testID := "distributed-final-parity"
	startedAt := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	completedAt := startedAt.Add(5 * time.Minute)
	writeJSONL := func(name string, value any) string {
		t.Helper()
		path := filepath.Join(sourceDir, name)
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.NewEncoder(file).Encode(value); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		return path
	}
	transactionsPath := writeJSONL("transactions.jsonl", TransactionEvent{Timestamp: startedAt, TestID: testID, TestName: "Distributed parity", Group: "checkout", ActionName: "checkout", DurationMs: 50, Success: true, BusinessTransaction: true})
	resourcesPath := writeJSONL("resources.jsonl", ResourceSample{Timestamp: startedAt, TestID: testID, TestName: "Distributed parity", ActiveVUsers: 5})
	summariesPath := writeJSONL("interval_summaries.jsonl", IntervalSummaryRecord{
		Timestamp: startedAt, TestID: testID, TestName: "Distributed parity", ActiveVUsers: 5, TPS: 1, TotalTx: 1,
		Groups: map[string]*GroupLiveMetrics{
			"checkout": {Name: "checkout", VUsers: 5, Transactions: 1, TPS: 1, AvgResponseMs: 50, P95Ms: 50},
		},
		Browser: &BrowserLiveMetrics{Samples: 2, AvgLCPMs: 300},
		BrowserByDevice: map[string]*BrowserDeviceLiveMetrics{
			"mobile": {DeviceProfile: "mobile", DevicePlatform: "ios", BrowserEngine: "webkit", Samples: 2, AvgLCPMs: 300},
		},
		Telemetry: &TelemetryHealth{DroppedResults: 2, GeneratorProcessingLagMs: 15},
	})
	childManifest := RunArchiveManifest{
		SchemaVersion:         archiveSchemaVersion,
		TestID:                testID,
		TestName:              "Distributed parity",
		CreatedAt:             startedAt,
		CompletedAt:           completedAt.Add(-time.Minute),
		Status:                "COMPLETE", // Must not override controller FAILED.
		TransactionsFile:      "transactions.jsonl",
		ResourceSamplesFile:   "resources.jsonl",
		IntervalSummariesFile: "interval_summaries.jsonl",
		TransactionCount:      1,
		ResourceSampleCount:   1,
		IntervalSummaryCount:  1,
	}
	manifestPath := filepath.Join(sourceDir, "manifest.json")
	if err := writeIndentedJSONFile(manifestPath, childManifest); err != nil {
		t.Fatal(err)
	}
	client := &archiveStreamingFakeClient{artifacts: map[string]string{
		"manifest.json":            manifestPath,
		"transactions.jsonl":       transactionsPath,
		"resources.jsonl":          resourcesPath,
		"interval_summaries.jsonl": summariesPath,
	}}

	backend := newDistributedExecutionBackend([]string{"generator-a"}, time.Second, nil)
	live := &LiveMetrics{
		Timestamp: startedAt, TotalTx: 1, TPS: 1, TargetTPS: 2,
		GroupMetrics: map[string]*GroupLiveMetrics{"checkout": {Name: "checkout", TPS: 1, TargetTPS: 2}},
		GoalStatus:   &GoalStatus{LatencyBreached: true, ErrorCountBreached: true},
		Telemetry: &TelemetryHealth{
			DroppedResults: 2, GeneratorProcessingLagMs: 15, TransportLagMs: 7, HistoryPoints: 4,
			WebsocketClients: 3, WebsocketDroppedMessages: 1,
		},
		TelemetryStatus: &TelemetryRunHealth{Status: "degraded", Severity: "warning", Conditions: []string{"generator_lag"}},
	}
	test := &ManagedTest{
		ID:                    testID,
		RefKey:                "RUN-20260714-0001",
		Name:                  "Distributed parity",
		Status:                "RUNNING",
		ProjectID:             "project-a",
		ScenarioID:            "scenario-a",
		ApplicationID:         "application-a",
		ReleaseID:             "release-a",
		CreatedAt:             startedAt.Add(-time.Minute),
		StartedAt:             &startedAt,
		Duration:              5 * time.Minute,
		DurationStr:           "5m",
		TotalVUsers:           5,
		GoalConfig:            &GoalConfig{P95LatencyMs: ptr(250.0), TransactionResponseTimeSLAMs: ptr(100.0)},
		GeneratorShards:       []GeneratorShardSummary{{Group: "checkout", Target: "generator-a", VUsers: 5}},
		Groups:                []TestGroup{{Name: "checkout", ScriptID: "script-a", ScriptName: "Checkout", Protocol: "http", VUsers: 5}},
		LiveMetrics:           live,
		History:               []*LiveMetrics{live},
		remoteTarget:          "generator-a",
		remoteClient:          client,
		remoteClientsByTarget: map[string]generatorClient{"generator-a": client},
	}
	initialProvenance := RunArchiveProvenance{
		ProjectID:   "project-a",
		ScenarioID:  "scenario-a",
		TotalVUsers: 3,
		Duration:    "3m",
		Groups:      []TestGroup{{Name: "checkout", VUsers: 3}},
	}
	test.initialArchiveProvenance = &initialProvenance
	runner := NewTestRunner(1, nil, store)
	runner.mu.Lock()
	runner.tests[test.ID] = test
	runner.mu.Unlock()
	if err := store.SaveTest(&storage.TestRecord{ID: test.ID, Name: test.Name, ProjectID: test.ProjectID, StartTime: test.CreatedAt, Status: test.Status}); err != nil {
		t.Fatal(err)
	}

	record, err := backend.stageRemoteArchive(runner, test, "FAILED", "controller-authoritative failure", completedAt, false)
	if err != nil {
		t.Fatalf("stageRemoteArchive: %v", err)
	}
	if record == nil {
		t.Fatal("stageRemoteArchive returned nil record")
	}
	test.Status = "FAILED"
	test.Error = "controller-authoritative failure"
	test.EndedAt = &completedAt
	if err := store.SaveTest(&storage.TestRecord{
		ID: test.ID, RefKey: test.RefKey, Name: test.Name, Status: test.Status,
		ProjectID: test.ProjectID, StartTime: startedAt, EndTime: &completedAt,
		Error: test.Error,
	}); err != nil {
		t.Fatalf("persist terminal test: %v", err)
	}
	if err := runner.finalizeRunArchiveDurably(context.Background(), test, record, true); err != nil {
		t.Fatalf("durable archive finalization: %v", err)
	}
	manifest, err := loadRunArchiveManifest(record.ManifestPath)
	if err != nil {
		t.Fatalf("load merged manifest: %v", err)
	}
	if manifest.Status != "FAILED" || manifest.Error != "controller-authoritative failure" {
		t.Fatalf("terminal state = %q / %q, want controller FAILED/error", manifest.Status, manifest.Error)
	}
	if !manifest.CompletedAt.Equal(completedAt) {
		t.Fatalf("completed_at = %s, want %s", manifest.CompletedAt, completedAt)
	}
	if manifest.Provenance.ProjectID != "project-a" || manifest.Provenance.ScenarioID != "scenario-a" || len(manifest.Provenance.GeneratorShards) != 1 {
		t.Fatalf("distributed provenance is incomplete: %#v", manifest.Provenance)
	}
	if manifest.InitialProvenance == nil || manifest.InitialProvenance.TotalVUsers != 3 || manifest.InitialProvenance.Duration != "3m" {
		t.Fatalf("distributed initial provenance = %#v, want immutable start snapshot", manifest.InitialProvenance)
	}
	if manifest.Provenance.ControllerEnv["APEXLOAD_REGION"] != "eastus-test" {
		t.Fatalf("controller environment provenance = %#v", manifest.Provenance.ControllerEnv)
	}
	if !manifest.SLABreached || len(manifest.SLADetails) != 2 {
		t.Fatalf("SLA snapshot = breached:%v details:%#v", manifest.SLABreached, manifest.SLADetails)
	}
	if !manifest.SLAEvaluated {
		t.Fatal("distributed archive did not retain evaluated SLA evidence")
	}
	if len(manifest.TransactionResponseTimeSLAResults) != 1 || manifest.TransactionResponseTimeSLAResults[0].Status != "pass" || manifest.TransactionResponseTimeSLAResults[0].Group != "checkout" {
		t.Fatalf("distributed response-time SLA results = %#v, want one passing checkout row", manifest.TransactionResponseTimeSLAResults)
	}
	mergedSummaryFile, err := os.Open(filepath.Join(record.ArchivePath, manifest.IntervalSummariesFile))
	if err != nil {
		t.Fatalf("open merged summary: %v", err)
	}
	var mergedSummary IntervalSummaryRecord
	if err := json.NewDecoder(mergedSummaryFile).Decode(&mergedSummary); err != nil {
		_ = mergedSummaryFile.Close()
		t.Fatalf("decode merged summary: %v", err)
	}
	if err := mergedSummaryFile.Close(); err != nil {
		t.Fatal(err)
	}
	checkout := mergedSummary.Groups["checkout"]
	if mergedSummary.TargetTPS != 2 || mergedSummary.ThroughputGapPct != 50 || checkout == nil || checkout.TargetTPS != 2 || checkout.ThroughputGapPct != 50 {
		t.Fatalf("controller target history was not overlaid on the final summary: %#v", mergedSummary)
	}
	if checkout.PercentileQuality != archivePercentileQualityRecomputed || mergedSummary.Browser == nil || mergedSummary.BrowserByDevice["mobile"] == nil ||
		mergedSummary.Telemetry == nil || mergedSummary.Telemetry.WebsocketClients != 3 || mergedSummary.TelemetryStatus == nil || mergedSummary.TelemetryStatus.Status != "degraded" {
		t.Fatalf("final summary lost nested generator evidence: %#v", mergedSummary)
	}
	if transaction := mergedSummary.Transactions["checkout"]; transaction == nil || transaction.TPS != 1 || transaction.Count != 1 || transaction.Errors != 0 || transaction.AvgMs != 50 {
		t.Fatalf("final summary lost recomputed transaction series: %#v", mergedSummary.Transactions)
	}
	parquetFile, err := localParquet.NewLocalFileReader(filepath.Join(record.ArchivePath, manifest.IntervalSummariesParquetFile))
	if err != nil {
		t.Fatalf("open final summary parquet: %v", err)
	}
	parquetRows, err := parquetReader.NewParquetReader(parquetFile, new(parquetIntervalSummary), 1)
	if err != nil {
		_ = parquetFile.Close()
		t.Fatalf("read final summary parquet: %v", err)
	}
	rows := make([]parquetIntervalSummary, int(parquetRows.GetNumRows()))
	if err := parquetRows.Read(&rows); err != nil {
		parquetRows.ReadStop()
		_ = parquetFile.Close()
		t.Fatalf("decode final summary parquet: %v", err)
	}
	parquetRows.ReadStop()
	if err := parquetFile.Close(); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].TargetTPS != 2 || rows[0].GroupsJSON == "" || rows[0].TransactionsJSON == "" || rows[0].BrowserJSON == "" ||
		rows[0].BrowserDeviceJSON == "" || rows[0].TelemetryJSON == "" || rows[0].TelemetryStatusJSON == "" {
		t.Fatalf("final staged summary parquet lost nested fields: %#v", rows)
	}
	var summaryCSV bytes.Buffer
	if err := writeArchiveSummariesCSV([]IntervalSummaryRecord{mergedSummary}, &summaryCSV); err != nil {
		t.Fatalf("write final summary csv: %v", err)
	}
	csvRows, err := csv.NewReader(&summaryCSV).ReadAll()
	if err != nil {
		t.Fatalf("read final summary csv: %v", err)
	}
	if len(csvRows) != 2 {
		t.Fatalf("summary csv rows = %#v", csvRows)
	}
	csvValues := make(map[string]string, len(csvRows[0]))
	for index, name := range csvRows[0] {
		csvValues[name] = csvRows[1][index]
	}
	for _, column := range []string{"groups_json", "transactions_json", "browser_json", "browser_by_device_json", "telemetry_json", "telemetry_status_json"} {
		if csvValues[column] == "" {
			t.Fatalf("final summary csv column %q is empty: %#v", column, csvValues)
		}
	}
	if manifest.TestManifestFile != testRunManifestFileName || manifest.TestNotesFile != testNotesArchiveFileName {
		t.Fatalf("persisted adjunct files = %q / %q", manifest.TestManifestFile, manifest.TestNotesFile)
	}
	if len(manifest.Artifacts) != 8 {
		t.Fatalf("artifact inventory has %d entries, want 8: %#v", len(manifest.Artifacts), manifest.Artifacts)
	}
	integrity := verifyRunArchiveIntegrity(record, manifest)
	if !integrity.Verified || integrity.Status != integrityStatusVerified {
		t.Fatalf("archive integrity = %#v", integrity)
	}
	testManifest, err := loadTestRunManifest(filepath.Join(record.ArchivePath, manifest.TestManifestFile))
	if err != nil {
		t.Fatalf("load test manifest: %v", err)
	}
	if testManifest.Status != "FAILED" || testManifest.Archive == nil || testManifest.Archive.Error != "controller-authoritative failure" {
		t.Fatalf("test manifest terminal state is not authoritative: %#v", testManifest)
	}
	client.mu.Lock()
	streamCalls, legacyCalls := client.streamCalls, client.legacyCalls
	client.mu.Unlock()
	if streamCalls != 4 || legacyCalls != 0 {
		t.Fatalf("archive transport calls = stream:%d unary:%d, want 4/0", streamCalls, legacyCalls)
	}
}

func TestFetchRemoteArchiveRejectsManifestForDifferentTest(t *testing.T) {
	sourceDir := t.TempDir()
	manifestPath := filepath.Join(sourceDir, "manifest.json")
	if err := writeIndentedJSONFile(manifestPath, RunArchiveManifest{
		SchemaVersion:         archiveSchemaVersion,
		TestID:                "different-run",
		TransactionsFile:      "transactions.jsonl",
		ResourceSamplesFile:   "resources.jsonl",
		IntervalSummariesFile: "interval_summaries.jsonl",
	}); err != nil {
		t.Fatal(err)
	}
	client := &archiveStreamingFakeClient{artifacts: map[string]string{"manifest.json": manifestPath}}
	backend := newDistributedExecutionBackend([]string{"generator-a"}, time.Second, nil)
	test := &ManagedTest{
		ID:                    "requested-run",
		remoteTarget:          "generator-a",
		remoteClient:          client,
		remoteClientsByTarget: map[string]generatorClient{"generator-a": client},
	}
	_, err := backend.fetchRemoteArchiveBundles(&TestRunner{}, test, false)
	if err == nil || !strings.Contains(err.Error(), "does not match requested test") {
		t.Fatalf("fetchRemoteArchiveBundles error = %v, want test identity rejection", err)
	}
	client.mu.Lock()
	streamCalls := client.streamCalls
	client.mu.Unlock()
	if streamCalls != 1 {
		t.Fatalf("stream calls = %d, want manifest only before rejection", streamCalls)
	}
}

func TestStageRemoteArchiveRejectsGeneratorManifestCountMismatch(t *testing.T) {
	archiveRoot := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", archiveRoot)
	sourceDir := t.TempDir()
	testID := "distributed-count-mismatch"
	write := func(name string, content []byte) string {
		t.Helper()
		path := filepath.Join(sourceDir, name)
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	manifestBytes, err := json.Marshal(RunArchiveManifest{
		SchemaVersion: archiveSchemaVersion, TestID: testID,
		TransactionsFile: "transactions.jsonl", ResourceSamplesFile: "resources.jsonl", IntervalSummariesFile: "summaries.jsonl",
		TransactionCount: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	client := &archiveStreamingFakeClient{artifacts: map[string]string{
		runArchiveManifestFileName: write("manifest.json", manifestBytes),
		"transactions.jsonl":       write("transactions.jsonl", nil),
		"resources.jsonl":          write("resources.jsonl", nil),
		"summaries.jsonl":          write("summaries.jsonl", nil),
	}}
	backend := newDistributedExecutionBackend([]string{"generator-a"}, time.Second, nil)
	test := &ManagedTest{
		ID: testID, Name: "Count mismatch", CreatedAt: time.Now().UTC(),
		remoteTarget: "generator-a", remoteClient: client,
		remoteClientsByTarget: map[string]generatorClient{"generator-a": client},
	}
	if _, err := backend.stageRemoteArchive(nil, test, "COMPLETE", "", time.Now().UTC(), true); err == nil || !strings.Contains(err.Error(), "remote transaction count") {
		t.Fatalf("stageRemoteArchive count mismatch error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(archiveRoot, "remote", testID, runArchiveManifestFileName)); !os.IsNotExist(err) {
		t.Fatalf("count-mismatched archive was committed: %v", err)
	}
}

func TestDistributedArchiveSLASnapshotIsRaceSafe(t *testing.T) {
	runner := &TestRunner{tests: make(map[string]*ManagedTest)}
	test := &ManagedTest{
		ID:             "distributed-sla-race",
		GoalConfig:     &GoalConfig{P95LatencyMs: ptr(100.0)},
		ciSLAEvaluated: true,
		ciSLADetails:   []string{"P95 latency breached threshold"},
		LiveMetrics: &LiveMetrics{
			TotalTx:    1,
			GoalStatus: &GoalStatus{},
		},
	}
	runner.tests[test.ID] = test

	const iterations = 1_000
	var writers sync.WaitGroup
	writers.Add(1)
	go func() {
		defer writers.Done()
		for index := range iterations {
			runner.mu.Lock()
			test.ciSLAEvaluated = true
			test.ciSLABreached = index%2 == 0
			test.ciSLADetails = []string{"P95 latency breached threshold"}
			test.LiveMetrics.GoalStatus.LatencyBreached = index%2 == 0
			runner.mu.Unlock()
		}
	}()
	for range iterations {
		evaluated, _, details := distributedArchiveSLA(runner, test)
		if !evaluated || len(details) == 0 {
			t.Fatalf("distributed SLA snapshot lost evaluated evidence: evaluated=%t details=%#v", evaluated, details)
		}
	}
	writers.Wait()
}

func TestFetchRemoteArchivesStartsTargetsInParallelAndCancelsAsAGroup(t *testing.T) {
	testID := "parallel-archive"
	source := t.TempDir()
	write := func(name string, content []byte) string {
		t.Helper()
		path := filepath.Join(source, name)
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	manifestBytes, err := json.Marshal(RunArchiveManifest{
		SchemaVersion: archiveSchemaVersion, TestID: testID,
		TransactionsFile: "transactions.jsonl", ResourceSamplesFile: "resources.jsonl", IntervalSummariesFile: "summaries.jsonl",
	})
	if err != nil {
		t.Fatal(err)
	}
	artifacts := map[string]string{
		runArchiveManifestFileName: write("manifest.json", manifestBytes),
		"transactions.jsonl":       write("transactions.jsonl", nil),
		"resources.jsonl":          write("resources.jsonl", nil),
		"summaries.jsonl":          write("summaries.jsonl", nil),
	}
	started := make(chan string, 2)
	release := make(chan struct{})
	clientA := &parallelArchiveClient{archiveStreamingFakeClient: &archiveStreamingFakeClient{artifacts: artifacts}, started: started, release: release}
	clientB := &parallelArchiveClient{archiveStreamingFakeClient: &archiveStreamingFakeClient{artifacts: artifacts}, started: started, release: release}
	backend := newDistributedExecutionBackend([]string{"generator-a", "generator-b"}, time.Second, nil)
	test := &ManagedTest{
		ID:                           testID,
		remoteClientsByTarget:        map[string]generatorClient{"generator-a": clientA},
		remoteArchiveClientsByTarget: map[string]generatorClient{"generator-b": clientB},
	}
	done := make(chan error, 1)
	go func() {
		bundles, err := backend.fetchRemoteArchiveBundlesContext(context.Background(), &TestRunner{}, test, true)
		if err == nil {
			cleanupRemoteArchiveBundles(bundles)
		}
		done <- err
	}()
	for count := 0; count < 2; count++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("remote archive targets were fetched sequentially")
		}
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	blocked := make(chan struct{})
	cancelClient := &parallelArchiveClient{archiveStreamingFakeClient: &archiveStreamingFakeClient{artifacts: artifacts}, started: make(chan string, 1), release: blocked}
	cancelTest := &ManagedTest{ID: testID, remoteClientsByTarget: map[string]generatorClient{"generator-a": cancelClient}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := backend.fetchRemoteArchiveBundlesContext(ctx, &TestRunner{}, cancelTest, true); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled fetch error = %v, want context canceled", err)
	}
}

func TestDistributedArchiveMetadataRetryPreservesCommittedCompleteOutcome(t *testing.T) {
	archiveRoot := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", archiveRoot)
	sourceDir := t.TempDir()
	testID := "distributed-metadata-retry"
	now := time.Now().UTC()
	writeLine := func(name string, value any) string {
		t.Helper()
		path := filepath.Join(sourceDir, name)
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.NewEncoder(file).Encode(value); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		return path
	}
	transactions := writeLine("transactions.jsonl", TransactionEvent{Timestamp: now, TestID: testID, TestName: "Metadata retry", ActionName: "Home", Success: true})
	resources := writeLine("resources.jsonl", ResourceSample{Timestamp: now, TestID: testID, TestName: "Metadata retry", ActiveVUsers: 1})
	summaries := writeLine("interval_summaries.jsonl", IntervalSummaryRecord{Timestamp: now, TestID: testID, TestName: "Metadata retry", TotalTx: 1})
	manifestPath := filepath.Join(sourceDir, "manifest.json")
	if err := writeIndentedJSONFile(manifestPath, RunArchiveManifest{
		SchemaVersion: archiveSchemaVersion, TestID: testID, TestName: "Metadata retry", CreatedAt: now.Add(-time.Minute), CompletedAt: now,
		Status: "COMPLETE", TransactionsFile: "transactions.jsonl", ResourceSamplesFile: "resources.jsonl", IntervalSummariesFile: "interval_summaries.jsonl",
		TransactionCount: 1, ResourceSampleCount: 1, IntervalSummaryCount: 1,
	}); err != nil {
		t.Fatal(err)
	}
	client := &archiveStreamingFakeClient{artifacts: map[string]string{
		"manifest.json": manifestPath, "transactions.jsonl": transactions, "resources.jsonl": resources, "interval_summaries.jsonl": summaries,
	}}
	test := &ManagedTest{
		ID: testID, Name: "Metadata retry", Status: "RUNNING", CreatedAt: now.Add(-time.Minute), ProjectID: storage.DefaultProjectID,
		remoteTarget: "generator-a", remoteClient: client, remoteClientsByTarget: map[string]generatorClient{"generator-a": client},
	}
	runner := NewTestRunner(1, NewHub(), nil)
	archiveStore := &retryingArchivePersistence{failCalls: durableTransitionAttempts}
	runner.archiveStore = archiveStore
	runner.mu.Lock()
	runner.tests[test.ID] = test
	runner.mu.Unlock()
	backend := newDistributedExecutionBackend([]string{"generator-a"}, time.Second, nil)

	backend.completeDistributedTestRun(runner, test, "COMPLETE", "")
	if test.Status != "COMPLETE" || test.Error != "" {
		t.Fatalf("test outcome = %s/%q, want committed COMPLETE", test.Status, test.Error)
	}
	runner.mu.RLock()
	initialArchive := test.Archive
	runner.mu.RUnlock()
	if initialArchive != nil {
		t.Fatal("unpublished archive was exposed before its catalog record committed")
	}
	deadline := time.Now().Add(2 * time.Second)
	var attached *storage.RunArchiveRecord
	for {
		archiveStore.mu.Lock()
		calls, persisted := archiveStore.calls, archiveStore.record
		archiveStore.mu.Unlock()
		runner.mu.RLock()
		if test.Archive != nil {
			copy := *test.Archive
			attached = &copy
		}
		runner.mu.RUnlock()
		if persisted != nil && attached != nil {
			if calls <= durableTransitionAttempts {
				t.Fatalf("archive metadata calls = %d, want background retry", calls)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("archive metadata did not retry after %d calls", calls)
		}
		time.Sleep(5 * time.Millisecond)
	}
	manifest, err := loadRunArchiveManifest(attached.ManifestPath)
	if err != nil {
		t.Fatalf("loadRunArchiveManifest: %v", err)
	}
	if manifest.Status != "COMPLETE" || manifest.Error != "" {
		t.Fatalf("archive outcome = %s/%q, want COMPLETE", manifest.Status, manifest.Error)
	}
}

func TestMergeRemoteArchiveIntervalSummariesAggregatesFleetTimeline(t *testing.T) {
	root := t.TempDir()
	base := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	writeSummaries := func(name string, records []IntervalSummaryRecord) string {
		t.Helper()
		path := filepath.Join(root, name)
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		encoder := json.NewEncoder(file)
		for _, record := range records {
			if err := encoder.Encode(record); err != nil {
				_ = file.Close()
				t.Fatal(err)
			}
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		return path
	}
	a := writeSummaries("a.jsonl", []IntervalSummaryRecord{
		{Timestamp: base.Add(490 * time.Millisecond), TestID: "run-1", ActiveVUsers: 2, TPS: 10, AvgResponseMs: 100, TotalTx: 10, TotalErrors: 1, P50Ms: 100, P90Ms: 140, P95Ms: 150, P99Ms: 175},
		{Timestamp: base.Add(1490 * time.Millisecond), TestID: "run-1", ActiveVUsers: 2, TPS: 5, AvgResponseMs: 120, TotalTx: 5, P50Ms: 120, P90Ms: 150, P95Ms: 170, P99Ms: 190},
	})
	b := writeSummaries("b.jsonl", []IntervalSummaryRecord{
		{Timestamp: base.Add(510 * time.Millisecond), TestID: "run-1", ActiveVUsers: 3, TPS: 20, AvgResponseMs: 200, TotalTx: 20, TotalErrors: 2, P50Ms: 200, P90Ms: 240, P95Ms: 250, P99Ms: 275},
		{Timestamp: base.Add(1510 * time.Millisecond), TestID: "run-1", ActiveVUsers: 3, TPS: 7, AvgResponseMs: 180, TotalTx: 7, TotalErrors: 1, P50Ms: 180, P90Ms: 220, P95Ms: 240, P99Ms: 260},
	})
	destination := filepath.Join(root, "merged.jsonl")
	count, err := mergeRemoteArchiveIntervalSummaries(destination, []remoteArchiveBundle{
		{target: "a", summariesPath: a, manifest: RunArchiveManifest{IntervalSummaryCount: 2}},
		{target: "b", summariesPath: b, manifest: RunArchiveManifest{IntervalSummaryCount: 2}},
	})
	if err != nil {
		t.Fatalf("mergeRemoteArchiveIntervalSummaries: %v", err)
	}
	if count != 2 {
		t.Fatalf("summary count = %d, want 2 fleet intervals", count)
	}
	file, err := os.Open(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	var first, second IntervalSummaryRecord
	if err := decoder.Decode(&first); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(&second); err != nil {
		t.Fatal(err)
	}
	if first.ActiveVUsers != 5 || first.TPS != 30 || first.TotalTx != 30 || first.TotalErrors != 3 {
		t.Fatalf("first fleet interval = %+v, want summed fleet values", first)
	}
	if first.ErrorPct < 9.09 || first.ErrorPct > 9.10 {
		t.Fatalf("first error pct = %f, want per-window 3/33", first.ErrorPct)
	}
	if first.Timestamp != base.Add(510*time.Millisecond) || second.Timestamp != base.Add(1510*time.Millisecond) {
		t.Fatalf("fleet timestamps = %s, %s; want aligned .510 window ends", first.Timestamp, second.Timestamp)
	}
	if first.PercentileQuality != archivePercentileQualityUnavailable || first.P50Ms != 0 || first.P90Ms != 0 || first.P95Ms != 0 || first.P99Ms != 0 {
		t.Fatalf("first fleet percentiles = %+v, want explicitly unavailable rather than averaged", first)
	}
	if second.TotalTx != 42 || second.TotalErrors != 4 || !second.Timestamp.After(first.Timestamp) {
		t.Fatalf("second fleet interval = %+v, want sorted cumulative totals", second)
	}

	for _, name := range []string{"transactions.jsonl", "resources.jsonl"} {
		if err := os.WriteFile(filepath.Join(root, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	manifest := RunArchiveManifest{
		SchemaVersion:         archiveSchemaVersion,
		TestID:                "run-1",
		TransactionsFile:      "transactions.jsonl",
		ResourceSamplesFile:   "resources.jsonl",
		IntervalSummariesFile: filepath.Base(destination),
		IntervalSummaryCount:  count,
	}
	manifestPath := filepath.Join(root, runArchiveManifestFileName)
	if err := writeIndentedJSONFile(manifestPath, manifest); err != nil {
		t.Fatal(err)
	}
	report, err := buildRunArchiveReport(&storage.RunArchiveRecord{TestID: "run-1", ArchivePath: root, ManifestPath: manifestPath})
	if err != nil {
		t.Fatalf("buildRunArchiveReport: %v", err)
	}
	if len(report.SummaryTimeline) != 2 || report.SummaryTimeline[0].PercentileQuality != archivePercentileQualityUnavailable || report.SummaryTimeline[0].HasP95() {
		t.Fatalf("report summary percentile quality = %#v, want propagated unavailable state", report.SummaryTimeline)
	}
	if chart := buildReportCharts(report).ResponseTime; chart != "" {
		t.Fatalf("unavailable fleet percentiles produced a misleading response-time chart: %s", chart)
	}
	if _, available := nearestSummaryP95(first.Timestamp, []IntervalSummaryRecord{first, second}); available {
		t.Fatal("monitoring correlation treated an unavailable fleet percentile as a real zero")
	}
	htmlReport, err := renderRunArchiveHTMLReport(report)
	if err != nil {
		t.Fatalf("renderRunArchiveHTMLReport: %v", err)
	}
	if !strings.Contains(string(htmlReport), "unavailable") {
		t.Fatal("HTML report did not label the unavailable fleet percentile")
	}
}

func TestMergeRemoteArchiveIntervalSummariesUsesGeneratorDigestsWithoutRawEvents(t *testing.T) {
	root := t.TempDir()
	base := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	digest := func(values ...time.Duration) *engine.TDigestSnapshot {
		t.Helper()
		value := engine.NewTDigest(200)
		for _, duration := range values {
			value.Add(float64(duration))
		}
		snapshot := value.Snapshot()
		return &snapshot
	}
	writeSummary := func(name string, record IntervalSummaryRecord) string {
		t.Helper()
		path := filepath.Join(root, name)
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.NewEncoder(file).Encode(record); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		return path
	}

	a := writeSummary("a-digest.jsonl", IntervalSummaryRecord{
		Timestamp: base, TestID: "run-digest", TotalTx: 2,
		DurationDigest: digest(10*time.Millisecond, 20*time.Millisecond),
	})
	b := writeSummary("b-digest.jsonl", IntervalSummaryRecord{
		Timestamp: base.Add(20 * time.Millisecond), TestID: "run-digest", TotalTx: 2,
		DurationDigest: digest(100*time.Millisecond, 200*time.Millisecond),
	})
	destination := filepath.Join(root, "merged-digest.jsonl")
	count, err := mergeRemoteArchiveIntervalSummaries(destination, []remoteArchiveBundle{
		{target: "a", summariesPath: a, manifest: RunArchiveManifest{IntervalSummaryCount: 1}},
		{target: "b", summariesPath: b, manifest: RunArchiveManifest{IntervalSummaryCount: 1}},
	})
	if err != nil {
		t.Fatalf("mergeRemoteArchiveIntervalSummaries: %v", err)
	}
	if count != 1 {
		t.Fatalf("summary count = %d, want 1", count)
	}
	file, err := os.Open(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var merged IntervalSummaryRecord
	if err := json.NewDecoder(file).Decode(&merged); err != nil {
		t.Fatal(err)
	}
	if merged.PercentileQuality != archivePercentileQualityMergedDigest || merged.DurationDigest != nil {
		t.Fatalf("merged digest evidence = quality:%q digest:%#v", merged.PercentileQuality, merged.DurationDigest)
	}
	if merged.P50Ms <= 0 || merged.P95Ms < 100 || merged.P99Ms < merged.P95Ms {
		t.Fatalf("merged percentiles = p50:%f p95:%f p99:%f", merged.P50Ms, merged.P95Ms, merged.P99Ms)
	}
}

func TestMergeRemoteArchiveIntervalSummariesPreservesNestedFleetEvidence(t *testing.T) {
	root := t.TempDir()
	base := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	breachA := base.Add(-10 * time.Second)
	breachB := base.Add(-20 * time.Second)
	writeSummary := func(name string, record IntervalSummaryRecord) string {
		t.Helper()
		path := filepath.Join(root, name)
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.NewEncoder(file).Encode(record); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		return path
	}
	a := writeSummary("nested-a.jsonl", IntervalSummaryRecord{
		Timestamp: base.Add(490 * time.Millisecond), TestID: "run-nested", TestName: "Nested evidence",
		ActiveVUsers: 2, TPS: 30, TargetTPS: 50, ThroughputGapPct: 40, ThroughputTargetMissed: true,
		TotalTx: 3, TotalErrors: 1,
		Groups: map[string]*GroupLiveMetrics{
			"buyers": {Name: "buyers", VUsers: 2, Transactions: 3, Errors: 1, TPS: 3, TargetTPS: 5, AvgResponseMs: 100, P50Ms: 90, P90Ms: 130, P95Ms: 140, P99Ms: 150},
		},
		Browser: &BrowserLiveMetrics{Samples: 2, AvgLCPMs: 100, AvgINPMs: 20},
		BrowserByDevice: map[string]*BrowserDeviceLiveMetrics{
			"mobile":  {DeviceProfile: "mobile", DevicePlatform: "ios", DeviceTier: "low", BrowserEngine: "webkit", Samples: 1, AvgLCPMs: 100},
			"desktop": {DeviceProfile: "desktop", DevicePlatform: "linux", BrowserEngine: "chromium", Samples: 2, AvgLCPMs: 80},
		},
		Telemetry: &TelemetryHealth{
			DroppedResults: 2, SheddedArrivals: 1, DroppedSubscriberBatches: 1,
			HistoryPoints: 5, WebsocketClients: 1, WebsocketDroppedMessages: 2,
			CollectedAt: base.Add(-2 * time.Second), GeneratorPublishedAt: base.Add(-900 * time.Millisecond),
			GeneratorProcessingLagMs: 100, TransportLagMs: 20, SampleAgeMs: 150,
		},
		TelemetryStatus: &TelemetryRunHealth{
			Status: "degraded", Severity: "warning", Summary: "generator A degraded", CurrentSignature: "a",
			Conditions: []string{"stale_sample"}, BreachActive: true, BreachDurationSec: 10,
			BreachStartedAt: &breachA, LastObservedAt: base.Add(490 * time.Millisecond), LastTransitionAt: breachA,
		},
	})
	b := writeSummary("nested-b.jsonl", IntervalSummaryRecord{
		Timestamp: base.Add(510 * time.Millisecond), TestID: "run-nested", TestName: "Nested evidence",
		ActiveVUsers: 3, TPS: 60, TargetTPS: 50,
		TotalTx: 5, TotalErrors: 1,
		Groups: map[string]*GroupLiveMetrics{
			"buyers":  {Name: "buyers", VUsers: 3, Transactions: 5, Errors: 1, TPS: 5, TargetTPS: 5, AvgResponseMs: 200, P50Ms: 190, P90Ms: 230, P95Ms: 240, P99Ms: 250},
			"sellers": {Name: "sellers", VUsers: 1, Transactions: 2, TPS: 2, AvgResponseMs: 75},
		},
		Browser: &BrowserLiveMetrics{Samples: 3, AvgLCPMs: 300, AvgINPMs: 70},
		BrowserByDevice: map[string]*BrowserDeviceLiveMetrics{
			"mobile": {DeviceProfile: "mobile", DevicePlatform: "ios", DeviceTier: "low", BrowserEngine: "webkit", Samples: 3, AvgLCPMs: 300},
		},
		Telemetry: &TelemetryHealth{
			DroppedResults: 3, SheddedArrivals: 4, DroppedSubscriberBatches: 2,
			HistoryPoints: 7, WebsocketClients: 2, WebsocketDroppedMessages: 3,
			CollectedAt: base.Add(-time.Second), GeneratorPublishedAt: base.Add(-500 * time.Millisecond),
			GeneratorProcessingLagMs: 250, TransportLagMs: 15, SampleAgeMs: 300,
		},
		TelemetryStatus: &TelemetryRunHealth{
			Status: "breached", Severity: "danger", Summary: "generator B breached", CurrentSignature: "b",
			Conditions: []string{"generator_lag", "stale_sample"}, BreachActive: true, SustainedBreach: true, BreachDurationSec: 60,
			BreachStartedAt: &breachB, LastObservedAt: base.Add(510 * time.Millisecond), LastTransitionAt: breachB,
		},
	})
	destination := filepath.Join(root, "nested-merged.jsonl")
	count, err := mergeRemoteArchiveIntervalSummaries(destination, []remoteArchiveBundle{
		{target: "a", summariesPath: a, manifest: RunArchiveManifest{IntervalSummaryCount: 1}},
		{target: "b", summariesPath: b, manifest: RunArchiveManifest{IntervalSummaryCount: 1}},
	})
	if err != nil {
		t.Fatalf("mergeRemoteArchiveIntervalSummaries: %v", err)
	}
	if count != 1 {
		t.Fatalf("summary count = %d, want 1", count)
	}
	file, err := os.Open(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var merged IntervalSummaryRecord
	if err := json.NewDecoder(file).Decode(&merged); err != nil {
		t.Fatal(err)
	}
	if merged.TargetTPS != 100 || merged.ThroughputGapPct != 10 || !merged.ThroughputTargetMissed {
		t.Fatalf("fleet throughput target = %.2f gap=%.2f missed=%v", merged.TargetTPS, merged.ThroughputGapPct, merged.ThroughputTargetMissed)
	}
	buyers := merged.Groups["buyers"]
	if buyers == nil || buyers.VUsers != 5 || buyers.Transactions != 8 || buyers.Errors != 2 || buyers.TPS != 8 || buyers.TargetTPS != 10 || buyers.ThroughputGapPct != 20 || !buyers.ThroughputTargetMissed {
		t.Fatalf("merged buyers metrics = %#v", buyers)
	}
	if buyers.AvgResponseMs != 160 || buyers.P50Ms != 0 || buyers.P90Ms != 0 || buyers.P95Ms != 0 || buyers.P99Ms != 0 ||
		buyers.PercentileQuality != archivePercentileQualityUnavailable || merged.Groups["sellers"] == nil ||
		merged.Groups["sellers"].PercentileQuality != archivePercentileQualitySourceGenerator {
		t.Fatalf("merged group percentile provenance = %#v", merged.Groups)
	}
	if merged.Browser == nil || merged.Browser.Samples != 5 || merged.Browser.AvgLCPMs != 220 || merged.Browser.AvgINPMs != 50 {
		t.Fatalf("merged browser metrics = %#v", merged.Browser)
	}
	if mobile := merged.BrowserByDevice["mobile"]; mobile == nil || mobile.Samples != 4 || mobile.AvgLCPMs != 250 || mobile.BrowserEngine != "webkit" {
		t.Fatalf("merged mobile metrics = %#v", mobile)
	}
	if merged.BrowserByDevice["desktop"] == nil {
		t.Fatalf("browser device union = %#v", merged.BrowserByDevice)
	}
	if merged.Telemetry == nil || merged.Telemetry.DroppedResults != 5 || merged.Telemetry.SheddedArrivals != 5 ||
		merged.Telemetry.DroppedSubscriberBatches != 3 || merged.Telemetry.HistoryPoints != 7 || merged.Telemetry.WebsocketClients != 3 ||
		merged.Telemetry.WebsocketDroppedMessages != 5 || !merged.Telemetry.CollectedAt.Equal(base.Add(-2*time.Second)) ||
		!merged.Telemetry.GeneratorPublishedAt.Equal(base.Add(-500*time.Millisecond)) || merged.Telemetry.GeneratorProcessingLagMs != 250 ||
		merged.Telemetry.TransportLagMs != 20 || merged.Telemetry.SampleAgeMs != 300 {
		t.Fatalf("merged telemetry = %#v", merged.Telemetry)
	}
	if merged.TelemetryStatus == nil || merged.TelemetryStatus.Status != "breached" || merged.TelemetryStatus.Severity != "danger" ||
		merged.TelemetryStatus.Summary != "generator B breached" || !merged.TelemetryStatus.SustainedBreach || merged.TelemetryStatus.BreachDurationSec != 60 ||
		merged.TelemetryStatus.BreachStartedAt == nil || !merged.TelemetryStatus.BreachStartedAt.Equal(breachB) ||
		strings.Join(merged.TelemetryStatus.Conditions, ",") != "generator_lag,stale_sample" {
		t.Fatalf("merged telemetry status = %#v", merged.TelemetryStatus)
	}
}

func TestMergeRemoteArchiveIntervalSummariesOverlaysTimeVaryingControllerTargets(t *testing.T) {
	root := t.TempDir()
	base := time.Date(2026, 8, 3, 14, 0, 0, 0, time.UTC)
	source := filepath.Join(root, "source.jsonl")
	file, err := os.Create(source)
	if err != nil {
		t.Fatal(err)
	}
	encoder := json.NewEncoder(file)
	for _, record := range []IntervalSummaryRecord{
		{Timestamp: base, TestID: "target-history", TPS: 4, Groups: map[string]*GroupLiveMetrics{"buyers": {Name: "buyers", TPS: 2}}},
		{Timestamp: base.Add(time.Second), TestID: "target-history", TPS: 8, Groups: map[string]*GroupLiveMetrics{"buyers": {Name: "buyers", TPS: 6}}},
	} {
		if err := encoder.Encode(record); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(root, "merged.jsonl")
	count, err := mergeRemoteArchiveIntervalSummariesWithTargets(destination, []remoteArchiveBundle{{
		target: "generator-a", summariesPath: source, manifest: RunArchiveManifest{IntervalSummaryCount: 2},
	}}, []remoteArchiveTargetSnapshot{
		{timestamp: base, targetTPS: 5, groups: map[string]float64{"buyers": 3}},
		{timestamp: base.Add(time.Second), targetTPS: 10, groups: map[string]float64{"buyers": 7}},
	})
	if err != nil {
		t.Fatalf("mergeRemoteArchiveIntervalSummariesWithTargets: %v", err)
	}
	if count != 2 {
		t.Fatalf("summary count = %d, want 2", count)
	}
	mergedFile, err := os.Open(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer mergedFile.Close()
	decoder := json.NewDecoder(mergedFile)
	var first, second IntervalSummaryRecord
	if err := decoder.Decode(&first); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(&second); err != nil {
		t.Fatal(err)
	}
	if first.TargetTPS != 5 || second.TargetTPS != 10 || first.Groups["buyers"].TargetTPS != 3 || second.Groups["buyers"].TargetTPS != 7 {
		t.Fatalf("time-varying targets = first %#v, second %#v", first, second)
	}
	if first.ThroughputGapPct != 20 || second.ThroughputGapPct != 20 || !first.ThroughputTargetMissed || !second.ThroughputTargetMissed {
		t.Fatalf("time-varying fleet target gaps = first %.2f/%v second %.2f/%v", first.ThroughputGapPct, first.ThroughputTargetMissed, second.ThroughputGapPct, second.ThroughputTargetMissed)
	}
}

func TestTerminalArchiveTargetSnapshotSurvivesManagedHistoryCompaction(t *testing.T) {
	base := time.Date(2026, 8, 3, 15, 0, 0, 0, time.UTC)
	first := &LiveMetrics{Timestamp: base, TargetTPS: 5, GroupMetrics: map[string]*GroupLiveMetrics{"buyers": {Name: "buyers", TargetTPS: 3}}}
	second := &LiveMetrics{Timestamp: base.Add(time.Second), TargetTPS: 10, GroupMetrics: map[string]*GroupLiveMetrics{"buyers": {Name: "buyers", TargetTPS: 7}}}
	test := &ManagedTest{ID: "failed-history", Status: "RUNNING", History: []*LiveMetrics{first, second}, LiveMetrics: second}
	runner := NewTestRunner(1, NewHub(), nil)
	runner.tests[test.ID] = test

	targetHistory := snapshotRemoteArchiveTargetHistory(runner, test)
	runner.mu.Lock()
	test.Status = "FAILED"
	compactTerminalManagedTestLocked(test)
	runner.mu.Unlock()
	if len(test.History) != 1 {
		t.Fatalf("terminal history length = %d, want compacted latest point", len(test.History))
	}
	if len(targetHistory) != 2 || targetHistory[0].targetTPS != 5 || targetHistory[1].targetTPS != 10 {
		t.Fatalf("pre-terminal target snapshot = %#v, want both controller intervals", targetHistory)
	}
}

func TestMergeRemoteArchiveIntervalSummariesPairsPeriodicAndTerminalDrainsByGenerator(t *testing.T) {
	root := t.TempDir()
	base := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	writeRecords := func(name string, values any) string {
		t.Helper()
		path := filepath.Join(root, name)
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		encoder := json.NewEncoder(file)
		switch records := values.(type) {
		case []IntervalSummaryRecord:
			for _, record := range records {
				if err := encoder.Encode(record); err != nil {
					_ = file.Close()
					t.Fatal(err)
				}
			}
		case []TransactionEvent:
			for _, record := range records {
				if err := encoder.Encode(record); err != nil {
					_ = file.Close()
					t.Fatal(err)
				}
			}
		default:
			_ = file.Close()
			t.Fatalf("unsupported records %T", values)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		return path
	}
	aSummaries := writeRecords("ordinal-a-summaries.jsonl", []IntervalSummaryRecord{
		{Timestamp: base.Add(time.Second), TestID: "run-ordinal", TPS: 10, TotalTx: 1},
		{Timestamp: base.Add(1010 * time.Millisecond), TestID: "run-ordinal", TPS: 10, TotalTx: 1},
	})
	bSummaries := writeRecords("ordinal-b-summaries.jsonl", []IntervalSummaryRecord{
		{Timestamp: base.Add(1020 * time.Millisecond), TestID: "run-ordinal", TPS: 10, TotalTx: 1},
		{Timestamp: base.Add(1030 * time.Millisecond), TestID: "run-ordinal", TotalErrors: 1},
	})
	aTransactions := writeRecords("ordinal-a-transactions.jsonl", []TransactionEvent{
		{Timestamp: base.Add(900 * time.Millisecond), TestID: "run-ordinal", DurationMs: 100, Success: true},
		{Timestamp: base.Add(1005 * time.Millisecond), TestID: "run-ordinal", DurationMs: 1000, Success: true},
	})
	bTransactions := writeRecords("ordinal-b-transactions.jsonl", []TransactionEvent{
		{Timestamp: base.Add(1015 * time.Millisecond), TestID: "run-ordinal", DurationMs: 200, Success: true},
		{Timestamp: base.Add(1025 * time.Millisecond), TestID: "run-ordinal", DurationMs: 2000, Success: false},
	})
	destination := filepath.Join(root, "ordinal-merged.jsonl")
	count, err := mergeRemoteArchiveIntervalSummaries(destination, []remoteArchiveBundle{
		{target: "a", summariesPath: aSummaries, transactionsPath: aTransactions, manifest: RunArchiveManifest{IntervalSummaryCount: 2, TransactionCount: 2}},
		{target: "b", summariesPath: bSummaries, transactionsPath: bTransactions, manifest: RunArchiveManifest{IntervalSummaryCount: 2, TransactionCount: 2}},
	})
	if err != nil {
		t.Fatalf("mergeRemoteArchiveIntervalSummaries: %v", err)
	}
	if count != 2 {
		t.Fatalf("summary count = %d, want periodic and terminal fleet windows", count)
	}
	file, err := os.Open(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	var periodic, terminal IntervalSummaryRecord
	if err := decoder.Decode(&periodic); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(&terminal); err != nil {
		t.Fatal(err)
	}
	if periodic.Timestamp != base.Add(1020*time.Millisecond) || terminal.Timestamp != base.Add(1030*time.Millisecond) {
		t.Fatalf("fleet timestamps = %s, %s", periodic.Timestamp, terminal.Timestamp)
	}
	if periodic.TotalTx != 2 || periodic.TotalErrors != 0 || terminal.TotalTx != 3 || terminal.TotalErrors != 1 {
		t.Fatalf("fleet raw totals = first %d/%d second %d/%d", periodic.TotalTx, periodic.TotalErrors, terminal.TotalTx, terminal.TotalErrors)
	}
	if periodic.AvgResponseMs != 150 || periodic.P50Ms != 150 || periodic.P95Ms != 195 {
		t.Fatalf("periodic raw distribution = avg %.2f p50 %.2f p95 %.2f", periodic.AvgResponseMs, periodic.P50Ms, periodic.P95Ms)
	}
	if terminal.AvgResponseMs != 1500 || terminal.P50Ms != 1500 || terminal.P95Ms != 1950 || terminal.ErrorPct != 50 {
		t.Fatalf("terminal raw distribution = avg %.2f p50 %.2f p95 %.2f errors %.2f", terminal.AvgResponseMs, terminal.P50Ms, terminal.P95Ms, terminal.ErrorPct)
	}
}

func TestMergeRemoteArchiveIntervalSummariesKeepsUnevenOrdinalTimelineMonotonic(t *testing.T) {
	root := t.TempDir()
	base := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	write := func(name string, records []IntervalSummaryRecord) string {
		t.Helper()
		path := filepath.Join(root, name)
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		encoder := json.NewEncoder(file)
		for _, record := range records {
			if err := encoder.Encode(record); err != nil {
				_ = file.Close()
				t.Fatal(err)
			}
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		return path
	}
	a := write("uneven-a.jsonl", []IntervalSummaryRecord{
		{Timestamp: base.Add(time.Second), TestID: "run-uneven", TotalTx: 1},
		{Timestamp: base.Add(1100 * time.Millisecond), TestID: "run-uneven", TotalTx: 1},
	})
	b := write("uneven-b.jsonl", []IntervalSummaryRecord{
		{Timestamp: base.Add(1490 * time.Millisecond), TestID: "run-uneven", TotalTx: 1},
	})
	destination := filepath.Join(root, "uneven-merged.jsonl")
	count, err := mergeRemoteArchiveIntervalSummaries(destination, []remoteArchiveBundle{
		{target: "a", summariesPath: a, manifest: RunArchiveManifest{IntervalSummaryCount: 2}},
		{target: "b", summariesPath: b, manifest: RunArchiveManifest{IntervalSummaryCount: 1}},
	})
	if err != nil {
		t.Fatalf("mergeRemoteArchiveIntervalSummaries: %v", err)
	}
	if count != 2 {
		t.Fatalf("summary count = %d, want 2", count)
	}
	file, err := os.Open(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	var first, second IntervalSummaryRecord
	if err := decoder.Decode(&first); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(&second); err != nil {
		t.Fatal(err)
	}
	if !second.Timestamp.After(first.Timestamp) {
		t.Fatalf("uneven ordinal fleet timestamps = %s then %s, want monotonic", first.Timestamp, second.Timestamp)
	}
}

func TestMergeRemoteArchiveIntervalSummariesUsesBusinessTransactionsOnlyForSeries(t *testing.T) {
	root := t.TempDir()
	base := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	writeRecords := func(name string, records ...any) string {
		t.Helper()
		path := filepath.Join(root, name)
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		encoder := json.NewEncoder(file)
		for _, record := range records {
			if err := encoder.Encode(record); err != nil {
				_ = file.Close()
				t.Fatal(err)
			}
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		return path
	}
	summariesPath := writeRecords("business-summaries.jsonl",
		IntervalSummaryRecord{Timestamp: base.Add(time.Second), TestID: "business-series", TotalTx: 2, TPS: 2},
		IntervalSummaryRecord{Timestamp: base.Add(2 * time.Second), TestID: "business-series", TotalTx: 3, TPS: 1},
	)
	transactionsPath := writeRecords("business-transactions.jsonl",
		TransactionEvent{Timestamp: base.Add(400 * time.Millisecond), TestID: "business-series", ActionName: "HTTP request", DurationMs: 40, Success: true},
		TransactionEvent{Timestamp: base.Add(600 * time.Millisecond), TestID: "business-series", ActionName: "Login", BusinessTransaction: true, DurationMs: 100, Success: true},
		TransactionEvent{Timestamp: base.Add(1500 * time.Millisecond), TestID: "business-series", ActionName: "HTTP browse", DurationMs: 60, Success: true},
	)
	destination := filepath.Join(root, "business-merged.jsonl")
	count, err := mergeRemoteArchiveIntervalSummaries(destination, []remoteArchiveBundle{{
		target: "generator-a", summariesPath: summariesPath, transactionsPath: transactionsPath,
		manifest: RunArchiveManifest{IntervalSummaryCount: 2, TransactionCount: 3},
	}})
	if err != nil {
		t.Fatalf("mergeRemoteArchiveIntervalSummaries: %v", err)
	}
	if count != 2 {
		t.Fatalf("summary count = %d, want 2", count)
	}
	file, err := os.Open(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	var first, second IntervalSummaryRecord
	if err := decoder.Decode(&first); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(&second); err != nil {
		t.Fatal(err)
	}
	if first.TotalTx != 1 || second.TotalTx != 1 || first.AvgResponseMs != 100 {
		t.Fatalf("business-provenance fleet totals = first %#v second %#v, want only the logical Login transaction", first, second)
	}
	if len(first.Transactions) != 1 || first.Transactions["Login"] == nil {
		t.Fatalf("first transaction series = %#v, want only proven Login", first.Transactions)
	}
	if len(second.Transactions) != 0 {
		t.Fatalf("second transaction series = %#v, want a gap without a proven business transaction", second.Transactions)
	}
}

func TestMergeRemoteArchiveIntervalSummariesChoosesBusinessProvenancePerGenerator(t *testing.T) {
	root := t.TempDir()
	base := time.Date(2026, 8, 12, 13, 0, 0, 0, time.UTC)
	writeRecords := func(name string, records ...any) string {
		t.Helper()
		path := filepath.Join(root, name)
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		encoder := json.NewEncoder(file)
		for _, record := range records {
			if err := encoder.Encode(record); err != nil {
				_ = file.Close()
				t.Fatal(err)
			}
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		return path
	}
	newSummaries := writeRecords("new-summaries.jsonl", IntervalSummaryRecord{Timestamp: base.Add(time.Second), TestID: "mixed-generators", TotalTx: 1, TPS: 1})
	legacySummaries := writeRecords("legacy-summaries.jsonl", IntervalSummaryRecord{Timestamp: base.Add(time.Second), TestID: "mixed-generators", TotalTx: 1, TPS: 1})
	newTransactions := writeRecords("new-transactions.jsonl",
		TransactionEvent{Timestamp: base.Add(400 * time.Millisecond), TestID: "mixed-generators", Group: "modern", ActionName: "HTTP request", DurationMs: 40, Success: true},
		TransactionEvent{Timestamp: base.Add(600 * time.Millisecond), TestID: "mixed-generators", Group: "modern", ActionName: "Login", BusinessTransaction: true, DurationMs: 100, Success: true},
	)
	legacyTransactions := writeRecords("legacy-transactions.jsonl",
		TransactionEvent{Timestamp: base.Add(500 * time.Millisecond), TestID: "mixed-generators", Group: "legacy", ActionName: "LegacyCheckout", DurationMs: 80, Success: true},
	)
	destination := filepath.Join(root, "mixed-merged.jsonl")
	_, err := mergeRemoteArchiveIntervalSummaries(destination, []remoteArchiveBundle{
		{target: "new", summariesPath: newSummaries, transactionsPath: newTransactions, manifest: RunArchiveManifest{IntervalSummaryCount: 1, TransactionCount: 2}},
		{target: "legacy", summariesPath: legacySummaries, transactionsPath: legacyTransactions, manifest: RunArchiveManifest{IntervalSummaryCount: 1, TransactionCount: 1}},
	})
	if err != nil {
		t.Fatalf("mergeRemoteArchiveIntervalSummaries: %v", err)
	}
	file, err := os.Open(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var summary IntervalSummaryRecord
	if err := json.NewDecoder(file).Decode(&summary); err != nil {
		t.Fatal(err)
	}
	if len(summary.Transactions) != 2 || summary.Transactions["Login"] == nil || summary.Transactions["LegacyCheckout"] == nil || summary.Transactions["HTTP request"] != nil {
		t.Fatalf("mixed-version fleet transaction series = %#v, want new proven Login plus legacy fallback", summary.Transactions)
	}
	if summary.TotalTx != 2 || summary.TotalErrors != 0 || summary.TPS != 2 || summary.AvgResponseMs != 90 {
		t.Fatalf("mixed-version fleet metrics = %#v, want modern business plus legacy fallback without the modern action row", summary)
	}
	if modern := summary.Groups["modern"]; modern == nil || modern.Transactions != 1 || modern.Errors != 0 || modern.AvgResponseMs != 100 {
		t.Fatalf("modern group metrics = %#v, want only logical Login", modern)
	}
	if legacy := summary.Groups["legacy"]; legacy == nil || legacy.Transactions != 1 || legacy.Errors != 0 || legacy.AvgResponseMs != 80 {
		t.Fatalf("legacy group metrics = %#v, want ordinary-row fallback", legacy)
	}
}

func TestMergeRemoteArchiveIntervalSummariesRejectsManifestCountMismatches(t *testing.T) {
	t.Run("transactions", func(t *testing.T) {
		root := t.TempDir()
		summaryPath := filepath.Join(root, "summary.jsonl")
		if err := os.WriteFile(summaryPath, []byte(`{"timestamp":"2026-08-03T12:00:00Z","test_id":"run-count"}`+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		transactionsPath := filepath.Join(root, "transactions.jsonl")
		if err := os.WriteFile(transactionsPath, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := mergeRemoteArchiveIntervalSummaries(filepath.Join(root, "merged.jsonl"), []remoteArchiveBundle{{
			target: "counted", summariesPath: summaryPath, transactionsPath: transactionsPath,
			manifest: RunArchiveManifest{IntervalSummaryCount: 1, TransactionCount: 10},
		}})
		if err == nil || !strings.Contains(err.Error(), "remote transaction count") {
			t.Fatalf("transaction count mismatch error = %v", err)
		}
	})

	t.Run("summaries", func(t *testing.T) {
		root := t.TempDir()
		summaryPath := filepath.Join(root, "summary.jsonl")
		if err := os.WriteFile(summaryPath, []byte(`{"timestamp":"2026-08-03T12:00:00Z","test_id":"run-count"}`+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := mergeRemoteArchiveIntervalSummaries(filepath.Join(root, "merged.jsonl"), []remoteArchiveBundle{{
			target: "counted", summariesPath: summaryPath, manifest: RunArchiveManifest{IntervalSummaryCount: 2},
		}})
		if err == nil || !strings.Contains(err.Error(), "remote interval summary count") {
			t.Fatalf("summary count mismatch error = %v", err)
		}
	})
}

func TestMergeRemoteArchiveDatasetRejectsManifestCountMismatches(t *testing.T) {
	tests := []struct {
		name          string
		kind          string
		configure     func(*remoteArchiveBundle, string)
		expectedCount func(remoteArchiveBundle) int64
	}{
		{
			name: "transactions", kind: "transaction",
			configure: func(bundle *remoteArchiveBundle, path string) {
				bundle.transactionsPath = path
				bundle.manifest.TransactionCount = 1
			},
			expectedCount: func(bundle remoteArchiveBundle) int64 { return bundle.manifest.TransactionCount },
		},
		{
			name: "resources", kind: "resource sample",
			configure: func(bundle *remoteArchiveBundle, path string) {
				bundle.resourcesPath = path
				bundle.manifest.ResourceSampleCount = 1
			},
			expectedCount: func(bundle remoteArchiveBundle) int64 { return bundle.manifest.ResourceSampleCount },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "empty.jsonl")
			if err := os.WriteFile(source, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			bundle := remoteArchiveBundle{target: "counted"}
			test.configure(&bundle, source)
			err := mergeRemoteArchiveDataset(
				filepath.Join(root, "merged.jsonl"), []remoteArchiveBundle{bundle}, test.kind,
				func(remoteArchiveBundle) string { return source }, test.expectedCount,
			)
			if err == nil || !strings.Contains(err.Error(), "manifest declares 1") {
				t.Fatalf("%s count mismatch error = %v", test.name, err)
			}
		})
	}
}

func TestMergeRemoteArchiveIntervalSummariesPreservesPeriodicAndTerminalDrains(t *testing.T) {
	root := t.TempDir()
	base := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)

	summariesPath := filepath.Join(root, "generator-summaries.jsonl")
	summariesFile, err := os.Create(summariesPath)
	if err != nil {
		t.Fatal(err)
	}
	summaryEncoder := json.NewEncoder(summariesFile)
	for _, summary := range []IntervalSummaryRecord{
		{
			Timestamp: base.Add(time.Second), TestID: "run-terminal-drain",
			ActiveVUsers: 4, TPS: 20, AvgResponseMs: 900,
			TotalTx: 20, TotalErrors: 2, P50Ms: 900, P90Ms: 900, P95Ms: 900, P99Ms: 900,
		},
		{
			// The final out-of-band drain can follow the periodic tick by much less
			// than the fleet skew window. It is a distinct result interval, not a
			// replacement snapshot.
			Timestamp: base.Add(1100 * time.Millisecond), TestID: "run-terminal-drain",
			ActiveVUsers: 0, TPS: 10, AvgResponseMs: 800,
			TotalTx: 10, TotalErrors: 4, P50Ms: 800, P90Ms: 800, P95Ms: 800, P99Ms: 800,
		},
	} {
		if err := summaryEncoder.Encode(summary); err != nil {
			_ = summariesFile.Close()
			t.Fatal(err)
		}
	}
	if err := summariesFile.Close(); err != nil {
		t.Fatal(err)
	}
	peerSummariesPath := filepath.Join(root, "peer-summaries.jsonl")
	peerSummariesFile, err := os.Create(peerSummariesPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(peerSummariesFile).Encode(IntervalSummaryRecord{
		Timestamp: base.Add(1020 * time.Millisecond), TestID: "run-terminal-drain",
		ActiveVUsers: 2, TPS: 5, AvgResponseMs: 700,
		TotalTx: 5, P50Ms: 700, P90Ms: 700, P95Ms: 700, P99Ms: 700,
	}); err != nil {
		_ = peerSummariesFile.Close()
		t.Fatal(err)
	}
	if err := peerSummariesFile.Close(); err != nil {
		t.Fatal(err)
	}

	transactionsPath := filepath.Join(root, "generator-transactions.jsonl")
	transactionsFile, err := os.Create(transactionsPath)
	if err != nil {
		t.Fatal(err)
	}
	transactionEncoder := json.NewEncoder(transactionsFile)
	for _, transaction := range []TransactionEvent{
		{Timestamp: base.Add(900 * time.Millisecond), TestID: "run-terminal-drain", DurationMs: 100, Success: true},
		{Timestamp: base.Add(950 * time.Millisecond), TestID: "run-terminal-drain", DurationMs: 200, Success: true},
		{Timestamp: base.Add(980 * time.Millisecond), TestID: "run-terminal-drain", DurationMs: 300, Success: false},
		{Timestamp: base.Add(1050 * time.Millisecond), TestID: "run-terminal-drain", DurationMs: 50, Success: true},
		{Timestamp: base.Add(1060 * time.Millisecond), TestID: "run-terminal-drain", DurationMs: 150, Success: false},
	} {
		if err := transactionEncoder.Encode(transaction); err != nil {
			_ = transactionsFile.Close()
			t.Fatal(err)
		}
	}
	if err := transactionsFile.Close(); err != nil {
		t.Fatal(err)
	}
	peerTransactionsPath := filepath.Join(root, "peer-transactions.jsonl")
	peerTransactionsFile, err := os.Create(peerTransactionsPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(peerTransactionsFile).Encode(TransactionEvent{
		Timestamp: base.Add(990 * time.Millisecond), TestID: "run-terminal-drain", DurationMs: 400, Success: true,
	}); err != nil {
		_ = peerTransactionsFile.Close()
		t.Fatal(err)
	}
	if err := peerTransactionsFile.Close(); err != nil {
		t.Fatal(err)
	}

	destination := filepath.Join(root, "merged-terminal-drain.jsonl")
	count, err := mergeRemoteArchiveIntervalSummaries(destination, []remoteArchiveBundle{
		{target: "generator-a", summariesPath: summariesPath, transactionsPath: transactionsPath, manifest: RunArchiveManifest{IntervalSummaryCount: 2, TransactionCount: 5}},
		{target: "generator-b", summariesPath: peerSummariesPath, transactionsPath: peerTransactionsPath, manifest: RunArchiveManifest{IntervalSummaryCount: 1, TransactionCount: 1}},
	})
	if err != nil {
		t.Fatalf("mergeRemoteArchiveIntervalSummaries: %v", err)
	}
	if count != 2 {
		t.Fatalf("summary count = %d, want both periodic and terminal drained intervals", count)
	}

	mergedFile, err := os.Open(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer mergedFile.Close()
	decoder := json.NewDecoder(mergedFile)
	var periodic, terminal IntervalSummaryRecord
	if err := decoder.Decode(&periodic); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(&terminal); err != nil {
		t.Fatal(err)
	}
	if periodic.Timestamp != base.Add(1020*time.Millisecond) || terminal.Timestamp != base.Add(1100*time.Millisecond) {
		t.Fatalf("merged timestamps = %s, %s; want both source drain endpoints", periodic.Timestamp, terminal.Timestamp)
	}
	if periodic.TotalTx != 3 || periodic.TotalErrors != 1 || terminal.TotalTx != 4 || terminal.TotalErrors != 2 {
		t.Fatalf("raw-recomputed cumulative totals = first %d/%d second %d/%d, want 3/1 then 4/2",
			periodic.TotalTx, periodic.TotalErrors, terminal.TotalTx, terminal.TotalErrors)
	}
	if periodic.TPS != 3 || terminal.TPS != 1 {
		t.Fatalf("raw-recomputed TPS = first %.2f second %.2f, want 3 and 1", periodic.TPS, terminal.TPS)
	}
	if periodic.AvgResponseMs != 250 || terminal.AvgResponseMs != 100 {
		t.Fatalf("raw-recomputed averages = first %.2f second %.2f, want 250 and 100", periodic.AvgResponseMs, terminal.AvgResponseMs)
	}
	if periodic.ErrorPct != 25 || terminal.ErrorPct != 50 {
		t.Fatalf("raw-recomputed error rates = first %.4f second %.4f, want 25 and 50", periodic.ErrorPct, terminal.ErrorPct)
	}
	if periodic.PercentileQuality != archivePercentileQualityRecomputed || terminal.PercentileQuality != archivePercentileQualityRecomputed {
		t.Fatalf("percentile quality = %q, %q; want exact recomputation", periodic.PercentileQuality, terminal.PercentileQuality)
	}
}

func TestMergeRemoteArchiveIntervalSummariesRecomputesSkewedFleetPercentiles(t *testing.T) {
	root := t.TempDir()
	base := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	writeSummaries := func(name string, record IntervalSummaryRecord) string {
		t.Helper()
		path := filepath.Join(root, name)
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.NewEncoder(file).Encode(record); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		return path
	}
	writeTransactions := func(name string, count int, timestamp time.Time, durationMs float64) string {
		t.Helper()
		path := filepath.Join(root, name)
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		encoder := json.NewEncoder(file)
		for index := 0; index < count; index++ {
			if err := encoder.Encode(TransactionEvent{Timestamp: timestamp, TestID: "run-skew", Group: "buyers", ActionName: "checkout", DurationMs: durationMs, Success: true}); err != nil {
				_ = file.Close()
				t.Fatal(err)
			}
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		return path
	}

	aSummary := writeSummaries("a-summary.jsonl", IntervalSummaryRecord{
		Timestamp: base.Add(490 * time.Millisecond), TestID: "run-skew", TotalTx: 100,
		P50Ms: 1, P90Ms: 1, P95Ms: 1, P99Ms: 1,
		Groups: map[string]*GroupLiveMetrics{
			"buyers": {Name: "buyers", Transactions: 100, TPS: 100, AvgResponseMs: 1, P50Ms: 1, P90Ms: 1, P95Ms: 1, P99Ms: 1},
		},
	})
	bSummary := writeSummaries("b-summary.jsonl", IntervalSummaryRecord{
		Timestamp: base.Add(510 * time.Millisecond), TestID: "run-skew", TotalTx: 1,
		P50Ms: 1000, P90Ms: 1000, P95Ms: 1000, P99Ms: 1000,
		Groups: map[string]*GroupLiveMetrics{
			"buyers": {Name: "buyers", Transactions: 1, TPS: 1, AvgResponseMs: 1000, P50Ms: 1000, P90Ms: 1000, P95Ms: 1000, P99Ms: 1000},
		},
	})
	aTransactions := writeTransactions("a-transactions.jsonl", 100, base.Add(250*time.Millisecond), 1)
	bTransactions := writeTransactions("b-transactions.jsonl", 1, base.Add(300*time.Millisecond), 1000)
	destination := filepath.Join(root, "merged.jsonl")
	count, err := mergeRemoteArchiveIntervalSummaries(destination, []remoteArchiveBundle{
		{target: "a", summariesPath: aSummary, transactionsPath: aTransactions, manifest: RunArchiveManifest{IntervalSummaryCount: 1, TransactionCount: 100}},
		{target: "b", summariesPath: bSummary, transactionsPath: bTransactions, manifest: RunArchiveManifest{IntervalSummaryCount: 1, TransactionCount: 1}},
	})
	if err != nil {
		t.Fatalf("mergeRemoteArchiveIntervalSummaries: %v", err)
	}
	if count != 1 {
		t.Fatalf("summary count = %d, want 1 fleet interval", count)
	}
	file, err := os.Open(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var merged IntervalSummaryRecord
	if err := json.NewDecoder(file).Decode(&merged); err != nil {
		t.Fatal(err)
	}
	if merged.TotalTx != 101 {
		t.Fatalf("merged TotalTx = %d, want 101", merged.TotalTx)
	}
	if merged.PercentileQuality != archivePercentileQualityRecomputed {
		t.Fatalf("percentile quality = %q, want exact transaction recomputation", merged.PercentileQuality)
	}
	if merged.P50Ms != 1 || merged.P90Ms != 1 || merged.P95Ms != 1 || merged.P99Ms != 1 {
		t.Fatalf("merged percentiles = p50 %.2f p90 %.2f p95 %.2f p99 %.2f, want exact skewed fleet distribution values of 1ms", merged.P50Ms, merged.P90Ms, merged.P95Ms, merged.P99Ms)
	}
	buyers := merged.Groups["buyers"]
	if buyers == nil || buyers.Transactions != 101 || buyers.PercentileQuality != archivePercentileQualityRecomputed ||
		buyers.P50Ms != 1 || buyers.P90Ms != 1 || buyers.P95Ms != 1 || buyers.P99Ms != 1 {
		t.Fatalf("merged buyers distribution = %#v, want exact skewed per-group values of 1ms", buyers)
	}
	parquetRows := toParquetSummaries([]IntervalSummaryRecord{merged})
	if len(parquetRows) != 1 || parquetRows[0].PercentileQuality != archivePercentileQualityRecomputed {
		t.Fatalf("parquet percentile quality = %#v, want recomputation provenance preserved", parquetRows)
	}
}

func TestMergeRemoteArchiveIntervalSummariesPreservesPerTransactionSourceWindows(t *testing.T) {
	root := t.TempDir()
	base := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	writeSummary := func(name string, timestamp time.Time, tps float64) string {
		t.Helper()
		path := filepath.Join(root, name)
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.NewEncoder(file).Encode(IntervalSummaryRecord{
			Timestamp: timestamp, TestID: "heterogeneous-windows", TotalTx: 2, TPS: tps,
		}); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		return path
	}
	writeTransactions := func(name string, timestamp time.Time, actions ...string) string {
		t.Helper()
		path := filepath.Join(root, name)
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		encoder := json.NewEncoder(file)
		for _, action := range actions {
			if err := encoder.Encode(TransactionEvent{
				Timestamp: timestamp, TestID: "heterogeneous-windows", ActionName: action, DurationMs: 100, Success: true,
			}); err != nil {
				_ = file.Close()
				t.Fatal(err)
			}
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		return path
	}

	aSummary := writeSummary("a-summary.jsonl", base.Add(490*time.Millisecond), 2) // two successes over one second
	bSummary := writeSummary("b-summary.jsonl", base.Add(510*time.Millisecond), 4) // two successes over half a second
	aTransactions := writeTransactions("a-transactions.jsonl", base.Add(250*time.Millisecond), "Shared", "Search")
	bTransactions := writeTransactions("b-transactions.jsonl", base.Add(300*time.Millisecond), "Shared", "Checkout")
	destination := filepath.Join(root, "merged.jsonl")
	count, err := mergeRemoteArchiveIntervalSummaries(destination, []remoteArchiveBundle{
		{target: "a", summariesPath: aSummary, transactionsPath: aTransactions, manifest: RunArchiveManifest{IntervalSummaryCount: 1, TransactionCount: 2}},
		{target: "b", summariesPath: bSummary, transactionsPath: bTransactions, manifest: RunArchiveManifest{IntervalSummaryCount: 1, TransactionCount: 2}},
	})
	if err != nil {
		t.Fatalf("mergeRemoteArchiveIntervalSummaries: %v", err)
	}
	if count != 1 {
		t.Fatalf("summary count = %d, want 1", count)
	}
	file, err := os.Open(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var merged IntervalSummaryRecord
	if err := json.NewDecoder(file).Decode(&merged); err != nil {
		t.Fatal(err)
	}
	if merged.TPS != 6 {
		t.Fatalf("fleet TPS = %v, want the additive source rate 6", merged.TPS)
	}
	shared, search, checkout := merged.Transactions["Shared"], merged.Transactions["Search"], merged.Transactions["Checkout"]
	if shared == nil || shared.Count != 2 || shared.TPS != 3 {
		t.Fatalf("Shared transaction = %#v, want count 2 and additive TPS 3", shared)
	}
	if search == nil || search.Count != 1 || search.TPS != 1 {
		t.Fatalf("Search transaction = %#v, want source-A TPS 1", search)
	}
	if checkout == nil || checkout.Count != 1 || checkout.TPS != 2 {
		t.Fatalf("Checkout transaction = %#v, want source-B TPS 2", checkout)
	}
}

func TestStageRemoteArchiveRejectsUnsafeIDBeforeFilesystemOrGeneratorIO(t *testing.T) {
	archiveRoot := filepath.Join(t.TempDir(), "must-not-be-created")
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	t.Setenv("APEXLOAD_ARCHIVE_DIR", archiveRoot)
	client := &archiveStreamingFakeClient{artifacts: map[string]string{}}
	backend := newDistributedExecutionBackend([]string{"generator-a"}, time.Second, nil)
	test := &ManagedTest{
		ID: "../escape", Name: "unsafe", remoteTarget: "generator-a", remoteClient: client,
		remoteClientsByTarget: map[string]generatorClient{"generator-a": client},
	}

	if _, err := backend.stageRemoteArchive(nil, test, "FAILED", "unsafe", time.Now(), false); err == nil {
		t.Fatal("stageRemoteArchive accepted an unsafe test id")
	}
	if _, err := os.Lstat(archiveRoot); !os.IsNotExist(err) {
		t.Fatalf("unsafe distributed test id caused filesystem I/O: %v", err)
	}
	client.mu.Lock()
	streamCalls, legacyCalls := client.streamCalls, client.legacyCalls
	client.mu.Unlock()
	if streamCalls != 0 || legacyCalls != 0 {
		t.Fatalf("unsafe id reached generator: stream=%d legacy=%d", streamCalls, legacyCalls)
	}
}

func TestStageRemoteArchiveRejectsLinkedRemoteParentsAndRunRoots(t *testing.T) {
	for _, linkedComponent := range []string{"remote-parent", "run-root"} {
		t.Run(linkedComponent, func(t *testing.T) {
			archiveRoot := t.TempDir()
			t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
			t.Setenv("APEXLOAD_ARCHIVE_DIR", archiveRoot)
			outside := t.TempDir()
			sentinel := filepath.Join(outside, "sentinel")
			if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
				t.Fatal(err)
			}
			remoteParent := filepath.Join(archiveRoot, "remote")
			testID := "linked-distributed-run"
			if linkedComponent == "remote-parent" {
				if err := os.Symlink(outside, remoteParent); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Mkdir(remoteParent, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(remoteParent, testID)); err != nil {
					t.Fatal(err)
				}
			}
			client := &archiveStreamingFakeClient{artifacts: map[string]string{}}
			backend := newDistributedExecutionBackend([]string{"generator-a"}, time.Second, nil)
			test := &ManagedTest{
				ID: testID, Name: "linked", remoteTarget: "generator-a", remoteClient: client,
				remoteClientsByTarget: map[string]generatorClient{"generator-a": client},
			}

			if _, err := backend.stageRemoteArchive(nil, test, "FAILED", "linked", time.Now(), false); err == nil {
				t.Fatalf("stageRemoteArchive accepted linked %s", linkedComponent)
			}
			if content, err := os.ReadFile(sentinel); err != nil || string(content) != "keep" {
				t.Fatalf("linked distributed staging modified outside data: %q, %v", content, err)
			}
			client.mu.Lock()
			streamCalls, legacyCalls := client.streamCalls, client.legacyCalls
			client.mu.Unlock()
			if streamCalls != 0 || legacyCalls != 0 {
				t.Fatalf("linked output root reached generator: stream=%d legacy=%d", streamCalls, legacyCalls)
			}
		})
	}
}

func TestFetchRemoteArchiveArtifactFileRejectsLinkedDestination(t *testing.T) {
	destinationRoot := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.jsonl")
	if err := os.WriteFile(outside, []byte("outside-sentinel"), 0o600); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(destinationRoot, "transactions.jsonl")
	if err := os.Symlink(outside, destination); err != nil {
		t.Fatal(err)
	}
	client := &archiveStreamingFakeClient{artifacts: map[string]string{}}
	if _, err := fetchRemoteArchiveArtifactFile(context.Background(), client, "safe-run", "transactions.jsonl", destination, 1024); err == nil {
		t.Fatal("remote transfer accepted a linked destination")
	}
	content, err := os.ReadFile(outside)
	if err != nil || string(content) != "outside-sentinel" {
		t.Fatalf("remote transfer truncated linked destination: %q, %v", content, err)
	}
	client.mu.Lock()
	streamCalls, legacyCalls := client.streamCalls, client.legacyCalls
	client.mu.Unlock()
	if streamCalls != 0 || legacyCalls != 0 {
		t.Fatalf("linked destination reached generator: stream=%d legacy=%d", streamCalls, legacyCalls)
	}
}
