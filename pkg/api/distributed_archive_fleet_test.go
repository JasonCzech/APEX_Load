package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/storage"
	grpcCodes "google.golang.org/grpc/codes"
	grpcStatus "google.golang.org/grpc/status"
)

// fleetBundleArtifacts is the number of successful StreamArchiveArtifact calls
// fetchRemoteArchiveBundleTarget makes per generator: the manifest plus the
// transactions, resources and interval-summaries datasets.
const fleetBundleArtifacts = 4

// slowArchiveStreamClient makes a fleet's *bundles* finish in the reverse of the
// configured target order, so anything the merge produces must be independent of
// that completion order.
//
// Whole-bundle order is the only order that matters: fetchRemoteArchiveBundles
// parks each worker's result in ordered[result.index] and rebuilds the bundle
// slice by index (distributed_archive.go), so a merge that leaked completion
// order could only do so a whole bundle at a time. Pinning the order of the
// first artifact would leave that property untested.
//
// The order is pinned by a gate chain rather than by racing sleeps: a client
// with waitFor cannot start any transfer until that channel closes, and a client
// with signalDone closes its channel once it has completed every artifact of its
// own bundle (fleetBundleArtifacts successful calls). delay is layered on the
// generator that must finish *first*, so a fixture that lost the gate would
// report the configured order deterministically instead of flaking near a timing
// margin.
//
// Precondition: a gated client blocks inside StreamArchiveArtifact, which runs
// after the production fetch worker has already taken one of the
// remoteArchiveFetchConcurrency semaphore slots, so the whole gated fleet has to
// fit in that semaphore at once. requireFleetGateConcurrency asserts that
// explicitly instead of letting a lowered concurrency limit surface as a stall.
type slowArchiveStreamClient struct {
	*archiveStreamingFakeClient
	delay      time.Duration
	waitFor    <-chan struct{}
	signalDone chan struct{}
	order      *fleetCompletionOrder
	target     string

	gateMu    sync.Mutex
	completed int
}

// requireFleetGateConcurrency fails fast and legibly when the production fetch
// semaphore is too small to hold every gated client of the fixture at once.
// Without it, lowering remoteArchiveFetchConcurrency turns a merge test into a
// multi-second stall that reports a gating failure instead of the real cause.
func requireFleetGateConcurrency(t *testing.T, gatedTargets int) {
	t.Helper()
	if remoteArchiveFetchConcurrency < gatedTargets {
		t.Fatalf("gated fleet fixture needs remoteArchiveFetchConcurrency >= %d, have %d: every gated client waits inside StreamArchiveArtifact while already holding a fetch semaphore slot", gatedTargets, remoteArchiveFetchConcurrency)
	}
}

// fleetCompletionOrder records the order in which generators finished their
// whole archive bundle, which is the granularity fetchRemoteArchiveBundles
// assembles results at.
type fleetCompletionOrder struct {
	mu     sync.Mutex
	values []string
}

func (o *fleetCompletionOrder) record(target string) {
	o.mu.Lock()
	o.values = append(o.values, target)
	o.mu.Unlock()
}

func (o *fleetCompletionOrder) snapshot() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.values...)
}

func (c *slowArchiveStreamClient) StreamArchiveArtifact(ctx context.Context, request generatorrpc.ArchiveArtifactRequest, destination io.Writer, maxBytes int64) (int64, error) {
	if c.waitFor != nil {
		select {
		case <-c.waitFor:
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(10 * time.Second):
			return 0, fmt.Errorf("%s waited on the gating generator, which never completed its bundle (check remoteArchiveFetchConcurrency covers the whole fleet)", c.target)
		}
	}
	if c.delay > 0 {
		select {
		case <-time.After(c.delay):
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	copied, err := c.archiveStreamingFakeClient.StreamArchiveArtifact(ctx, request, destination, maxBytes)
	if err == nil {
		c.gateMu.Lock()
		c.completed++
		bundleComplete := c.completed == fleetBundleArtifacts
		c.gateMu.Unlock()
		if bundleComplete {
			if c.order != nil {
				c.order.record(c.target)
			}
			if c.signalDone != nil {
				close(c.signalDone)
			}
		}
	}
	return copied, err
}

// missingArchiveGeneratorClient models a generator that never produced an
// archive for this run (evacuated, restarted, or never assigned work).
type missingArchiveGeneratorClient struct {
	*archiveStreamingFakeClient
}

func (c *missingArchiveGeneratorClient) StreamArchiveArtifact(context.Context, generatorrpc.ArchiveArtifactRequest, io.Writer, int64) (int64, error) {
	return 0, grpcStatus.Error(grpcCodes.NotFound, "archive missing")
}

type fleetGeneratorFixture struct {
	target       string
	activeVUsers int64
	// perWindow transactions, of which errorsPerWindow fail.
	perWindow       int
	errorsPerWindow int
	durationMs      float64
	// summaryOffsets are the generator's publication instants relative to the run
	// start, one per interval window.
	summaryOffsets []time.Duration
	// transactionOffsets are the instants used for that window's transactions.
	transactionOffsets []time.Duration
}

// writeFleetGeneratorArtifacts materializes one generator's archive bundle on
// disk and returns the artifact paths keyed by archive file name.
func writeFleetGeneratorArtifacts(t *testing.T, dir, testID, testName string, start time.Time, fixture fleetGeneratorFixture) map[string]string {
	t.Helper()
	writeJSONL := func(name string, encode func(*json.Encoder)) string {
		t.Helper()
		path := filepath.Join(dir, name)
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		encoder := json.NewEncoder(file)
		encode(encoder)
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		return path
	}

	transactionsPath := writeJSONL("transactions.jsonl", func(encoder *json.Encoder) {
		for window, offset := range fixture.transactionOffsets {
			for index := range fixture.perWindow {
				event := TransactionEvent{
					Timestamp:  start.Add(offset),
					TestID:     testID,
					TestName:   testName,
					Group:      "checkout",
					ActionName: fmt.Sprintf("%s-w%d-tx%03d", fixture.target, window, index),
					DurationMs: fixture.durationMs,
					Success:    index >= fixture.errorsPerWindow,
				}
				if !event.Success {
					event.ErrorMessage = "synthetic fleet failure"
				}
				if err := encoder.Encode(event); err != nil {
					t.Fatal(err)
				}
			}
		}
	})
	resourcesPath := writeJSONL("resources.jsonl", func(encoder *json.Encoder) {
		for _, offset := range fixture.summaryOffsets {
			if err := encoder.Encode(ResourceSample{
				Timestamp:    start.Add(offset),
				TestID:       testID,
				TestName:     testName,
				ActiveVUsers: fixture.activeVUsers,
			}); err != nil {
				t.Fatal(err)
			}
		}
	})
	summariesPath := writeJSONL("interval_summaries.jsonl", func(encoder *json.Encoder) {
		successes := int64(fixture.perWindow - fixture.errorsPerWindow)
		for _, offset := range fixture.summaryOffsets {
			if err := encoder.Encode(IntervalSummaryRecord{
				Timestamp:     start.Add(offset),
				TestID:        testID,
				TestName:      testName,
				ActiveVUsers:  fixture.activeVUsers,
				TPS:           float64(successes),
				TotalTx:       successes,
				TotalErrors:   int64(fixture.errorsPerWindow),
				AvgResponseMs: fixture.durationMs,
				P50Ms:         fixture.durationMs,
				P95Ms:         fixture.durationMs,
				Groups: map[string]*GroupLiveMetrics{
					"checkout": {Name: "checkout", VUsers: int(fixture.activeVUsers), Transactions: successes, TPS: float64(successes), AvgResponseMs: fixture.durationMs},
				},
			}); err != nil {
				t.Fatal(err)
			}
		}
	})

	windows := int64(len(fixture.transactionOffsets))
	manifestPath := filepath.Join(dir, "manifest.json")
	if err := writeIndentedJSONFile(manifestPath, RunArchiveManifest{
		SchemaVersion:         archiveSchemaVersion,
		TestID:                testID,
		TestName:              testName,
		CreatedAt:             start.Add(fixture.summaryOffsets[0]),
		CompletedAt:           start.Add(fixture.summaryOffsets[len(fixture.summaryOffsets)-1]),
		Status:                "COMPLETE",
		TransactionsFile:      "transactions.jsonl",
		ResourceSamplesFile:   "resources.jsonl",
		IntervalSummariesFile: "interval_summaries.jsonl",
		TransactionCount:      windows * int64(fixture.perWindow),
		ResourceSampleCount:   int64(len(fixture.summaryOffsets)),
		IntervalSummaryCount:  int64(len(fixture.summaryOffsets)),
	}); err != nil {
		t.Fatal(err)
	}
	return map[string]string{
		"manifest.json":            manifestPath,
		"transactions.jsonl":       transactionsPath,
		"resources.jsonl":          resourcesPath,
		"interval_summaries.jsonl": summariesPath,
	}
}

func readMergedTransactions(t *testing.T, path string) []TransactionEvent {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open merged transactions: %v", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	events := make([]TransactionEvent, 0, 512)
	for {
		var event TransactionEvent
		if err := decoder.Decode(&event); err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("decode merged transaction: %v", err)
		}
		events = append(events, event)
	}
	return events
}

func readMergedSummaries(t *testing.T, path string) []IntervalSummaryRecord {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open merged summaries: %v", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	records := make([]IntervalSummaryRecord, 0, 16)
	for {
		var record IntervalSummaryRecord
		if err := decoder.Decode(&record); err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("decode merged summary: %v", err)
		}
		records = append(records, record)
	}
	return records
}

// TestStageRemoteArchiveMergesThreeGeneratorFleet exercises the whole staging
// pipeline at N=3 rather than the usual N=1: fleet-window alignment across three
// skewed publication clocks, cross-generator percentile recomputation (a branch
// the single-generator path never reaches), bundle-ordered dataset
// concatenation, and a transfer large enough to span many stream chunks.
func TestStageRemoteArchiveMergesThreeGeneratorFleet(t *testing.T) {
	archiveRoot := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", archiveRoot)
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.SaveProject(&storage.Project{ID: "project-fleet", Name: "Fleet", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}

	const testID = "distributed-fleet-merge"
	const testName = "Distributed fleet merge"
	start := time.Date(2026, 8, 6, 9, 0, 0, 0, time.UTC)
	completedAt := start.Add(2 * time.Second)

	fixtures := []fleetGeneratorFixture{
		{
			target: "generator-a", activeVUsers: 2, perWindow: 1, durationMs: 100,
			summaryOffsets:     []time.Duration{490 * time.Millisecond, 1490 * time.Millisecond},
			transactionOffsets: []time.Duration{400 * time.Millisecond, 1400 * time.Millisecond},
		},
		{
			target: "generator-b", activeVUsers: 3, perWindow: 1, durationMs: 200,
			summaryOffsets:     []time.Duration{510 * time.Millisecond, 1510 * time.Millisecond},
			transactionOffsets: []time.Duration{420 * time.Millisecond, 1420 * time.Millisecond},
		},
		{
			// Large enough that its transfer spans many stream chunks instead of a
			// single short write.
			target: "generator-c", activeVUsers: 5, perWindow: 200, errorsPerWindow: 10, durationMs: 50,
			summaryOffsets:     []time.Duration{505 * time.Millisecond, 1505 * time.Millisecond},
			transactionOffsets: []time.Duration{450 * time.Millisecond, 1450 * time.Millisecond},
		},
	}
	// Finish the fleet's *bundles* in reverse configured order so any ordering
	// dependence on completion order is visible in the merged dataset. The chain
	// is a strict happens-before, not a sleep race: generator-b cannot start any
	// transfer until generator-c has completed all four of its artifacts, and
	// generator-a cannot start until generator-b has completed all four of its
	// own. generator-c also carries the only delay, so a fixture that lost the
	// gate would report the configured order deterministically instead of flaking
	// near a timing margin.
	requireFleetGateConcurrency(t, len(fixtures))
	generatorCDone := make(chan struct{})
	generatorBDone := make(chan struct{})
	gates := map[string]<-chan struct{}{"generator-b": generatorCDone, "generator-a": generatorBDone}
	signals := map[string]chan struct{}{"generator-c": generatorCDone, "generator-b": generatorBDone}
	delays := map[string]time.Duration{"generator-c": 25 * time.Millisecond}

	completion := &fleetCompletionOrder{}
	clientsByTarget := make(map[string]generatorClient, len(fixtures))
	streamingClients := make(map[string]*archiveStreamingFakeClient, len(fixtures))
	shards := make([]GeneratorShardSummary, 0, len(fixtures))
	var expectedTransactions int64
	for _, fixture := range fixtures {
		artifacts := writeFleetGeneratorArtifacts(t, t.TempDir(), testID, testName, start, fixture)
		inner := &archiveStreamingFakeClient{artifacts: artifacts}
		streamingClients[fixture.target] = inner
		client := &slowArchiveStreamClient{
			archiveStreamingFakeClient: inner,
			delay:                      delays[fixture.target],
			waitFor:                    gates[fixture.target],
			signalDone:                 signals[fixture.target],
			order:                      completion,
			target:                     fixture.target,
		}
		clientsByTarget[fixture.target] = client
		shards = append(shards, GeneratorShardSummary{Group: "checkout", Target: fixture.target, VUsers: int(fixture.activeVUsers)})
		expectedTransactions += int64(len(fixture.transactionOffsets) * fixture.perWindow)
	}

	// Configured order is a, b, c; the archive bundles finish c, b, a.
	backend := newDistributedExecutionBackend([]string{"generator-a", "generator-b", "generator-c"}, time.Second, nil)
	test := &ManagedTest{
		ID:                    testID,
		RefKey:                "RUN-20260806-0007",
		Name:                  testName,
		Status:                "RUNNING",
		ProjectID:             "project-fleet",
		CreatedAt:             start.Add(-time.Minute),
		StartedAt:             &start,
		Duration:              2 * time.Second,
		DurationStr:           "2s",
		TotalVUsers:           10,
		GeneratorShards:       shards,
		Groups:                []TestGroup{{Name: "checkout", ScriptID: "script-a", ScriptName: "Checkout", Protocol: "http", VUsers: 10}},
		remoteClientsByTarget: clientsByTarget,
	}
	runner := NewTestRunner(1, nil, store)
	runner.mu.Lock()
	runner.tests[test.ID] = test
	runner.mu.Unlock()

	record, err := backend.stageRemoteArchive(runner, test, "COMPLETE", "", completedAt, true)
	if err != nil {
		t.Fatalf("stageRemoteArchive: %v", err)
	}
	if record == nil {
		t.Fatal("stageRemoteArchive returned nil record for a three-generator fleet")
	}
	// The whole fleet must have finished its bundles in the reverse of the
	// configured order; otherwise the merge assertions below prove nothing about
	// completion-order independence.
	wantCompletion := []string{"generator-c", "generator-b", "generator-a"}
	if order := completion.snapshot(); !slices.Equal(order, wantCompletion) {
		t.Fatalf("fleet bundle completion order = %v, want the reverse-order fixture to finish %v", order, wantCompletion)
	}

	manifest, err := loadRunArchiveManifest(record.ManifestPath)
	if err != nil {
		t.Fatalf("load merged manifest: %v", err)
	}
	if manifest.TransactionCount != expectedTransactions {
		t.Fatalf("merged transaction count = %d, want the fleet sum %d", manifest.TransactionCount, expectedTransactions)
	}
	if manifest.ResourceSampleCount != 6 {
		t.Fatalf("merged resource sample count = %d, want 6 (2 per generator)", manifest.ResourceSampleCount)
	}
	if manifest.IntervalSummaryCount != 2 {
		t.Fatalf("merged interval summary count = %d, want 2 fleet windows for six skewed publications", manifest.IntervalSummaryCount)
	}
	if !manifest.CreatedAt.Equal(start.Add(490 * time.Millisecond)) {
		t.Fatalf("merged created_at = %s, want the earliest generator manifest %s", manifest.CreatedAt, start.Add(490*time.Millisecond))
	}
	if len(manifest.Provenance.GeneratorShards) != 3 {
		t.Fatalf("merged provenance shards = %#v, want all three generators", manifest.Provenance.GeneratorShards)
	}

	events := readMergedTransactions(t, filepath.Join(record.ArchivePath, manifest.TransactionsFile))
	if int64(len(events)) != expectedTransactions {
		t.Fatalf("merged transaction rows = %d, want %d (a large multi-chunk transfer must not be truncated)", len(events), expectedTransactions)
	}
	// Dataset merge concatenates bundles in configured target order, which must
	// hold even though generator-c's whole bundle finished first and
	// generator-a's finished last.
	wantOrder := []string{"generator-a", "generator-a", "generator-b", "generator-b"}
	for index, want := range wantOrder {
		if !strings.HasPrefix(events[index].ActionName, want) {
			t.Fatalf("merged transaction %d = %q, want a %s row (configured bundle order)", index, events[index].ActionName, want)
		}
	}
	if !strings.HasPrefix(events[len(events)-1].ActionName, "generator-c") {
		t.Fatalf("last merged transaction = %q, want the final generator-c row", events[len(events)-1].ActionName)
	}
	if events[len(events)-1].ActionName != "generator-c-w1-tx199" {
		t.Fatalf("last merged transaction = %q, want generator-c-w1-tx199", events[len(events)-1].ActionName)
	}

	summaries := readMergedSummaries(t, filepath.Join(record.ArchivePath, manifest.IntervalSummariesFile))
	if len(summaries) != 2 {
		t.Fatalf("merged fleet summaries = %d, want 2", len(summaries))
	}
	first, second := summaries[0], summaries[1]
	if !first.Timestamp.Equal(start.Add(510*time.Millisecond)) || !second.Timestamp.Equal(start.Add(1510*time.Millisecond)) {
		t.Fatalf("fleet window timestamps = %s, %s; want the latest publication in each skew window", first.Timestamp, second.Timestamp)
	}
	if first.ActiveVUsers != 10 || second.ActiveVUsers != 10 {
		t.Fatalf("fleet active vusers = %d, %d; want the three-generator sum 10", first.ActiveVUsers, second.ActiveVUsers)
	}
	// 190 successes on generator-c plus one each on a and b, cumulative per window.
	if first.TotalTx != 192 || second.TotalTx != 384 {
		t.Fatalf("fleet cumulative transactions = %d, %d; want 192, 384", first.TotalTx, second.TotalTx)
	}
	if first.TotalErrors != 10 || second.TotalErrors != 20 {
		t.Fatalf("fleet cumulative errors = %d, %d; want 10, 20", first.TotalErrors, second.TotalErrors)
	}
	if first.PercentileQuality != archivePercentileQualityRecomputed {
		t.Fatalf("fleet percentile quality = %q, want %q (multi-generator percentiles must be rebuilt, never passed through)", first.PercentileQuality, archivePercentileQualityRecomputed)
	}
	// 200 rows at 50ms and one each at 100ms/200ms: the median stays at 50ms only
	// if every generator's rows landed in the window.
	if first.P50Ms != 50 {
		t.Fatalf("fleet p50 = %f, want 50 recomputed across the whole fleet", first.P50Ms)
	}
	if first.AvgResponseMs < 50.4 || first.AvgResponseMs > 51.5 {
		t.Fatalf("fleet average response = %f, want the transaction-weighted fleet mean near 51", first.AvgResponseMs)
	}
	checkout := first.Groups["checkout"]
	if checkout == nil || checkout.Transactions != 192 {
		t.Fatalf("fleet group rollup = %#v, want 192 checkout transactions", checkout)
	}

	integrity := verifyRunArchiveIntegrity(record, manifest)
	if !integrity.Verified || integrity.Status != integrityStatusVerified {
		t.Fatalf("fleet archive integrity = %#v", integrity)
	}
	for target, client := range streamingClients {
		client.mu.Lock()
		streamCalls, legacyCalls := client.streamCalls, client.legacyCalls
		client.mu.Unlock()
		if streamCalls != fleetBundleArtifacts || legacyCalls != 0 {
			t.Fatalf("%s archive transport calls = stream:%d unary:%d, want %d/0", target, streamCalls, legacyCalls, fleetBundleArtifacts)
		}
	}
}

// TestStageRemoteArchiveFleetHandlesOneMissingGenerator covers the partial-fleet
// path at N>1: a best-effort merge must keep the surviving generators in
// configured order, and a required merge must name the generator that is
// missing.
func TestStageRemoteArchiveFleetHandlesOneMissingGenerator(t *testing.T) {
	archiveRoot := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", archiveRoot)

	const testID = "distributed-fleet-partial"
	const testName = "Distributed fleet partial"
	start := time.Date(2026, 8, 6, 11, 0, 0, 0, time.UTC)

	fixtures := []fleetGeneratorFixture{
		{
			target: "generator-a", activeVUsers: 2, perWindow: 2, durationMs: 80,
			summaryOffsets:     []time.Duration{500 * time.Millisecond},
			transactionOffsets: []time.Duration{400 * time.Millisecond},
		},
		{
			target: "generator-c", activeVUsers: 4, perWindow: 3, durationMs: 120,
			summaryOffsets:     []time.Duration{505 * time.Millisecond},
			transactionOffsets: []time.Duration{450 * time.Millisecond},
		},
	}
	clientsByTarget := make(map[string]generatorClient, 3)
	for _, fixture := range fixtures {
		artifacts := writeFleetGeneratorArtifacts(t, t.TempDir(), testID, testName, start, fixture)
		clientsByTarget[fixture.target] = &archiveStreamingFakeClient{artifacts: artifacts}
	}
	clientsByTarget["generator-b"] = &missingArchiveGeneratorClient{archiveStreamingFakeClient: &archiveStreamingFakeClient{}}

	backend := newDistributedExecutionBackend([]string{"generator-a", "generator-b", "generator-c"}, time.Second, nil)
	test := &ManagedTest{
		ID:                    testID,
		Name:                  testName,
		Status:                "RUNNING",
		CreatedAt:             start.Add(-time.Minute),
		StartedAt:             &start,
		remoteClientsByTarget: clientsByTarget,
	}
	runner := NewTestRunner(1, nil, nil)
	runner.mu.Lock()
	runner.tests[test.ID] = test
	runner.mu.Unlock()

	if _, err := backend.fetchRemoteArchiveBundles(runner, test, true); err == nil || !strings.Contains(err.Error(), "generator-b") {
		t.Fatalf("required fleet fetch error = %v, want the missing generator named", err)
	}

	bundles, err := backend.fetchRemoteArchiveBundles(runner, test, false)
	if err != nil {
		t.Fatalf("best-effort fleet fetch: %v", err)
	}
	defer cleanupRemoteArchiveBundles(bundles)
	if len(bundles) != 2 {
		t.Fatalf("best-effort bundles = %d, want the two surviving generators", len(bundles))
	}
	if bundles[0].target != "generator-a" || bundles[1].target != "generator-c" {
		t.Fatalf("bundle order = %q,%q; want configured order with the gap closed", bundles[0].target, bundles[1].target)
	}

	root := t.TempDir()
	destination := filepath.Join(root, "merged.jsonl")
	count, err := mergeRemoteArchiveIntervalSummaries(destination, bundles)
	if err != nil {
		t.Fatalf("mergeRemoteArchiveIntervalSummaries: %v", err)
	}
	if count != 1 {
		t.Fatalf("merged fleet windows = %d, want 1", count)
	}
	merged := readMergedSummaries(t, destination)
	if len(merged) != 1 || merged[0].ActiveVUsers != 6 || merged[0].TotalTx != 5 {
		t.Fatalf("surviving fleet window = %#v, want 6 vusers and 5 transactions", merged)
	}
	if merged[0].PercentileQuality != archivePercentileQualityRecomputed {
		t.Fatalf("surviving fleet percentile quality = %q, want recomputed across the two remaining generators", merged[0].PercentileQuality)
	}
}
