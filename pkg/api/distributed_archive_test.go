package api

import (
	"context"
	"database/sql"
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
	"github.com/jasonczech/apexload/pkg/storage"
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
	transactionsPath := writeJSONL("transactions.jsonl", TransactionEvent{Timestamp: startedAt, TestID: testID, TestName: "Distributed parity", ActionName: "checkout", Success: true})
	resourcesPath := writeJSONL("resources.jsonl", ResourceSample{Timestamp: startedAt, TestID: testID, TestName: "Distributed parity", ActiveVUsers: 5})
	summariesPath := writeJSONL("interval_summaries.jsonl", IntervalSummaryRecord{Timestamp: startedAt, TestID: testID, TestName: "Distributed parity", TotalTx: 1})
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
		GeneratorShards:       []GeneratorShardSummary{{Group: "checkout", Target: "generator-a", VUsers: 5}},
		Groups:                []TestGroup{{Name: "checkout", ScriptID: "script-a", ScriptName: "Checkout", Protocol: "http", VUsers: 5}},
		LiveMetrics:           &LiveMetrics{GoalStatus: &GoalStatus{LatencyBreached: true, ErrorCountBreached: true}},
		remoteTarget:          "generator-a",
		remoteClient:          client,
		remoteClientsByTarget: map[string]generatorClient{"generator-a": client},
	}
	runner := NewTestRunner(1, nil, store)
	runner.mu.Lock()
	runner.tests[test.ID] = test
	runner.mu.Unlock()

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
	if manifest.Provenance.ControllerEnv["APEXLOAD_REGION"] != "eastus-test" {
		t.Fatalf("controller environment provenance = %#v", manifest.Provenance.ControllerEnv)
	}
	if !manifest.SLABreached || len(manifest.SLADetails) != 2 {
		t.Fatalf("SLA snapshot = breached:%v details:%#v", manifest.SLABreached, manifest.SLADetails)
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
