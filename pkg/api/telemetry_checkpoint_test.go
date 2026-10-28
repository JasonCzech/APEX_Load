package api

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/storage"
)

func checkpointTestRun() *ManagedTest {
	return &ManagedTest{ID: "durable-run", RefKey: "RUN-durable", ProjectID: storage.DefaultProjectID, Status: "RUNNING", Groups: []TestGroup{{Name: "checkout"}}, Assignment: &GeneratorAssignmentSettings{FailoverPolicy: failoverPolicyContinue}, CreatedAt: time.Unix(1800000000, 0).UTC(), executionGeneration: 7, initialArchiveProvenance: &RunArchiveProvenance{RunIncarnation: "incarnation-1"}}
}
func checkpointBatch(test *ManagedTest, collection uint64) generatorrpc.MetricsBatch {
	start := test.CreatedAt.Add(time.Duration(collection-1) * time.Second)
	return generatorrpc.MetricsBatch{TestID: test.ID, ExecutionGeneration: test.executionGeneration, SourceEpoch: "epoch-1", SourceEpochStartedUnixMs: test.CreatedAt.Add(-time.Minute).UnixMilli(), BatchSequence: collection, WindowMs: 1000, LifecycleState: "RUNNING", TimestampUnixMs: start.Add(time.Second).UnixMilli(), FleetMetrics: &generatorrpc.MetricAggregate{Name: "fleet", Count: 10, Errors: 2}, Telemetry: &generatorrpc.MetricsTelemetry{CollectionSequence: collection, PublishedAtUnixMs: start.Add(time.Second).UnixMilli(), DroppedResults: 3}, CollectionWindowsComplete: true, CollectionWindows: []generatorrpc.MetricsCollectionWindow{{Group: "checkout", Start: start, End: start.Add(time.Second)}}}
}
func acceptCheckpointBatch(test *ManagedTest, batch generatorrpc.MetricsBatch, at time.Time) {
	telemetrySourceLedgerLocked(test).observe("generator", batch, at)
	accepted := batch
	countDistributedWindowLocked(test, "generator", batch, &accepted)
}

func TestTelemetryCheckpointRoundTripAndReplay(t *testing.T) {
	test := checkpointTestRun()
	test.initialArchiveProvenance.TelemetryPolicy = captureTelemetryBudgetPolicy(RunArchiveExecutionSettingsSnapshot{}, true, test.CreatedAt)
	ledger := telemetrySourceLedgerLocked(test)
	ledger.assign("generator", test.CreatedAt)
	for i := uint64(1); i <= 3; i++ {
		batch := checkpointBatch(test, i)
		acceptCheckpointBatch(test, batch, time.UnixMilli(batch.TimestampUnixMs))
	}
	cp := captureTelemetryCheckpointLocked(test)
	raw, err := json.Marshal(cp)
	if err != nil {
		t.Fatal(err)
	}
	successor := checkpointTestRun()
	successor.executionGeneration++
	restored, totals, err := decodeTelemetryCheckpoint(string(raw), cp.Revision, successor, 0)
	if err != nil {
		t.Fatal(err)
	}
	successor.telemetrySources, successor.runTotals = restored, totals
	if len(restored.collections) != 3 || restored.collections[0].PolicyHash != test.initialArchiveProvenance.TelemetryPolicy.Hash {
		t.Fatal("policy/window provenance lost")
	}
	before, _ := ledger.snapshot()
	after, _ := restored.snapshot()
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("source evidence changed on restart\nbefore=%+v\nafter=%+v", before, after)
	}
	observed, _, _ := restored.coverageSnapshot()
	if observed["generator"].within(test.CreatedAt.UnixMilli(), test.CreatedAt.Add(time.Minute).UnixMilli()) != 3000 {
		t.Fatal("coverage lost")
	}
	if totals.total.tx != 24 || totals.total.errors != 6 || totals.groups["checkout"].tx != 24 || !totals.requestsUnknown || len(totals.publishedWindows) != 0 {
		t.Fatalf("totals/delivery proof %+v", totals)
	}
	// A new-generation replay of the same collection has a new publication
	// sequence, but cannot add totals or move old coverage into the takeover gap.
	replay := checkpointBatch(successor, 3)
	replay.BatchSequence = 1
	acceptCheckpointBatch(successor, replay, test.CreatedAt.Add(30*time.Second))
	observed, _, _ = restored.coverageSnapshot()
	if successor.runTotals.total.tx != 24 || observed["generator"].within(test.CreatedAt.UnixMilli(), test.CreatedAt.Add(time.Minute).UnixMilli()) != 3000 || len(restored.collections) != 3 {
		t.Fatal("replay inflated recovered evidence")
	}
	next := checkpointBatch(successor, 4)
	next.BatchSequence = 2
	acceptCheckpointBatch(successor, next, test.CreatedAt.Add(31*time.Second))
	observed, _, _ = restored.coverageSnapshot()
	if successor.runTotals.total.tx != 32 || len(restored.collections) != 4 || observed["generator"].within(test.CreatedAt.UnixMilli(), test.CreatedAt.Add(time.Minute).UnixMilli()) != 4000 {
		t.Fatal("new collection missing or takeover gap bridged")
	}
	if got := restored.lossTotals()[TelemetryCounterDroppedResults]; got.Value != 3 {
		t.Fatalf("cumulative loss counted twice %+v", got)
	}
	// Replayed older process evidence must not count as a new process merely
	// because the successor's live cache starts empty.
	stale := checkpointBatch(successor, 5)
	stale.SourceEpoch = "older-process"
	stale.SourceEpochStartedUnixMs -= 1000
	stale.BatchSequence = 3
	acceptCheckpointBatch(successor, stale, test.CreatedAt.Add(32*time.Second))
	observed, _, _ = restored.coverageSnapshot()
	if successor.runTotals.total.tx != 32 || observed["generator"].within(test.CreatedAt.UnixMilli(), test.CreatedAt.Add(time.Minute).UnixMilli()) != 4000 {
		t.Fatal("older process inflated recovered totals/coverage")
	}

	// A genuine new generation may use another process clock that started
	// earlier. Generation authority wins; do not require cross-host ordering.
	successor.executionGeneration++
	restarted := checkpointBatch(successor, 1)
	restarted.SourceEpoch = "new-process"
	restarted.SourceEpochStartedUnixMs -= 2000
	acceptCheckpointBatch(successor, restarted, test.CreatedAt.Add(34*time.Second))
	if successor.runTotals.total.tx != 40 {
		t.Fatal("new term rejected a different process clock")
	}

}

func TestTelemetryCheckpointRejectsIncompatibleEvidence(t *testing.T) {
	test := checkpointTestRun()
	batch := checkpointBatch(test, 1)
	acceptCheckpointBatch(test, batch, test.CreatedAt.Add(time.Second))
	original := captureTelemetryCheckpointLocked(test)
	for name, mutate := range map[string]func(*telemetryCheckpoint){
		"version":           func(c *telemetryCheckpoint) { c.Version++ },
		"incarnation":       func(c *telemetryCheckpoint) { c.Incarnation = "replacement" },
		"revision":          func(c *telemetryCheckpoint) { c.Revision++ },
		"future-generation": func(c *telemetryCheckpoint) { c.Generation++ },
		"duplicate-source":  func(c *telemetryCheckpoint) { c.Epochs = append(c.Epochs, c.Epochs[0]) },
		"oversized-windows": func(c *telemetryCheckpoint) {
			c.Collections = make([]TelemetryCollectionEvidence, maxRetainedTelemetryCollections+1)
		},
		"invalid-window": func(c *telemetryCheckpoint) { c.Collections[0].Windows[0].End = c.Collections[0].Windows[0].Start },
		"invalid-counter": func(c *telemetryCheckpoint) {
			v := int64(-1)
			c.Collections[0].CounterReadings[TelemetryCounterDroppedResults] = TelemetryCollectionCounter{Value: &v}
		},
		"negative-totals":  func(c *telemetryCheckpoint) { c.Totals[0] = -1 },
		"invalid-coverage": func(c *telemetryCheckpoint) { c.Coverage["generator"] = durableSpans{Spans: [][2]int64{{100, 90}}} },
	} {
		t.Run(name, func(t *testing.T) {
			raw, _ := json.Marshal(original)
			var cp telemetryCheckpoint
			if err := json.Unmarshal(raw, &cp); err != nil {
				t.Fatal(err)
			}
			mutate(&cp)
			raw, _ = json.Marshal(cp)
			if _, _, err := decodeTelemetryCheckpoint(string(raw), original.Revision, test, 0); err == nil {
				t.Fatal("invalid checkpoint accepted")
			}
		})
	}
	for _, field := range []string{"Totals", "RequestsUnknown", "Windows", "Truncated"} {
		raw, _ := json.Marshal(original)
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		delete(fields, field)
		raw, _ = json.Marshal(fields)
		if _, _, err := decodeTelemetryCheckpoint(string(raw), original.Revision, test, 0); err == nil {
			t.Fatalf("missing %s inferred zero or completeness", field)
		}
	}
	for _, raw := range []string{"", "null", "{", `{"Version":99}`} {
		if _, _, err := decodeTelemetryCheckpoint(raw, 1, test, 0); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
}

func TestTelemetryCheckpointPersistsOnlyAdoptableRuns(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "controller.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	test := checkpointTestRun()
	test.executionGeneration = 0
	if err := store.SaveTest(&storage.TestRecord{ID: test.ID, Name: test.ID, RefKey: test.RefKey, ProjectID: test.ProjectID, StartTime: test.CreatedAt, Status: "RUNNING", InitialProvenance: test.initialArchiveProvenance}); err != nil {
		t.Fatal(err)
	}
	durable, err := store.GetTest(test.ID)
	if err != nil {
		t.Fatal(err)
	}
	test.RefKey = durable.RefKey
	runner := &TestRunner{tests: map[string]*ManagedTest{test.ID: test}, historyStore: store}
	acceptCheckpointBatch(test, checkpointBatch(test, 1), test.CreatedAt.Add(time.Second))
	load := func() string {
		t.Helper()
		raw, _, err := store.LoadTelemetryCheckpoint(test.ID, test.RefKey, test.ProjectID, test.CreatedAt)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	for _, assignment := range []*GeneratorAssignmentSettings{nil, {FailoverPolicy: failoverPolicyFail}} {
		test.Assignment = assignment
		runner.persistTelemetryCheckpoint(test)
		if raw := load(); raw != "" {
			t.Fatalf("non-adoptable run %+v persisted %q", assignment, raw)
		}
	}
	test.Assignment = &GeneratorAssignmentSettings{FailoverPolicy: failoverPolicyContinue}
	runner.persistTelemetryCheckpoint(test)
	if load() == "" {
		t.Fatal("continue-policy run did not persist its checkpoint")
	}
}

// A snapshot from before the last term that armed continuity cannot contain
// that term's counted windows: recovery falls back to history and the new
// owner may replace the payload.
func TestTelemetryCheckpointFromAnEarlierTermIsSuperseded(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "controller.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	test := checkpointTestRun()
	test.executionGeneration = 0
	if err := store.SaveTest(&storage.TestRecord{ID: test.ID, Name: test.ID, RefKey: test.RefKey, ProjectID: test.ProjectID, StartTime: test.CreatedAt, Status: "RUNNING", InitialProvenance: test.initialArchiveProvenance}); err != nil {
		t.Fatal(err)
	}
	durable, err := store.GetTest(test.ID)
	if err != nil {
		t.Fatal(err)
	}
	test.RefKey = durable.RefKey
	runner := &TestRunner{tests: map[string]*ManagedTest{test.ID: test}, historyStore: store}
	acceptCheckpointBatch(test, checkpointBatch(test, 1), test.CreatedAt.Add(time.Second))
	runner.persistTelemetryCheckpoint(test)
	_, revision, err := store.LoadTelemetryCheckpoint(test.ID, test.RefKey, test.ProjectID, test.CreatedAt)
	if err != nil || revision == 0 {
		t.Fatal("fixture checkpoint", revision, err)
	}
	successor := checkpointTestRun()
	successor.RefKey, successor.executionGeneration = durable.RefKey, 0
	next := &TestRunner{tests: map[string]*ManagedTest{test.ID: successor}, historyStore: store}
	// Term 1 armed continuity after this snapshot; term 2 adopts.
	next.restoreTelemetryCheckpoint(successor, 2, 1)
	if successor.telemetryCheckpointRestored || successor.telemetryCheckpointReadOnly || successor.telemetrySources != nil || successor.runTotals.initialized || successor.telemetryCheckpointRevision != revision {
		t.Fatalf("superseded snapshot restored=%v readOnly=%v revision=%d", successor.telemetryCheckpointRestored, successor.telemetryCheckpointReadOnly, successor.telemetryCheckpointRevision)
	}
	next.restoreTelemetryCheckpoint(successor, 2, 0)
	if !successor.telemetryCheckpointRestored || successor.runTotals.total.tx != 8 {
		t.Fatal("snapshot from the adopted term was refused")
	}
}

func TestTelemetryAdoptionKeepsARestoredTerminalRetirement(t *testing.T) {
	test := checkpointTestRun()
	ledger := telemetrySourceLedgerLocked(test)
	ledger.assign("generator", test.CreatedAt)
	terminal := checkpointBatch(test, 1)
	terminal.LifecycleState = "COMPLETE"
	acceptCheckpointBatch(test, terminal, test.CreatedAt.Add(time.Second))
	retired := ledger.requirements["generator"].retiredAtMs
	if retired == 0 {
		t.Fatal("terminal publication did not retire the source")
	}
	adoptedAt := test.CreatedAt.Add(time.Hour)
	ledger.retireAtAdoption("generator", adoptedAt)
	if got := ledger.requirements["generator"].retiredAtMs; got != retired {
		t.Fatalf("adoption moved an observed retirement from %d to %d", retired, got)
	}
	ledger.assign("gap-only", adoptedAt)
	ledger.retireAtAdoption("gap-only", adoptedAt)
	if got := ledger.requirements["gap-only"].retiredAtMs; got != adoptedAt.UnixMilli() {
		t.Fatal("a source completed during the gap was not retired at the adoption", got)
	}
}

// A tight allowance trims optional windows on every epoch before it drops any
// later epoch's source/counter record.
func TestTelemetrySourceSidecarReservesEveryEpochBeforeWindows(t *testing.T) {
	test := checkpointTestRun()
	ledger := telemetrySourceLedgerLocked(test)
	const sources = 6
	for s := 0; s < sources; s++ {
		source := fmt.Sprintf("generator-%d", s)
		for i := uint64(1); i <= 20; i++ {
			batch := checkpointBatch(test, i)
			batch.SourceEpoch = "epoch-" + source
			ledger.observe(source, batch, time.UnixMilli(batch.TimestampUnixMs))
			accepted := batch
			countDistributedWindowLocked(test, source, batch, &accepted)
		}
	}
	base, _ := ledger.snapshotRecords(false)
	if len(base) != sources {
		t.Fatalf("fixture epochs %d", len(base))
	}
	natural := 0
	for _, epoch := range base {
		epoch.CollectionWindowsTruncated = true
		line, err := json.Marshal(epoch)
		if err != nil {
			t.Fatal(err)
		}
		natural += len(line) + 1
	}
	t.Setenv(envArchiveTelemetrySourcesMaxBytes, fmt.Sprint(natural+512))
	record := &storage.RunArchiveRecord{TestID: test.ID, ArchivePath: t.TempDir()}
	record.ManifestPath = filepath.Join(record.ArchivePath, "manifest.json")
	admitArchiveForTest(t, record)
	manifest := &RunArchiveManifest{}
	if err := (&TestRunner{}).persistTelemetrySourceArchive(context.Background(), test, record, manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.TelemetrySourceEpochCount != sources || !manifest.TelemetrySourcesTruncated {
		t.Fatalf("epochs written %d of %d (truncated=%v)", manifest.TelemetrySourceEpochCount, sources, manifest.TelemetrySourcesTruncated)
	}
}

func TestTelemetryCheckpointDurableRestartAndAtomicFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "controller.db")
	store, err := storage.NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	test := checkpointTestRun()
	test.executionGeneration = 0
	row := &storage.TestRecord{ID: test.ID, Name: test.ID, RefKey: test.RefKey, ProjectID: test.ProjectID, StartTime: test.CreatedAt, Status: "RUNNING", InitialProvenance: test.initialArchiveProvenance}
	if err := store.SaveTest(row); err != nil {
		t.Fatal(err)
	}
	durable, err := store.GetTest(test.ID)
	if err != nil {
		t.Fatal(err)
	}
	test.RefKey = durable.RefKey
	runner := &TestRunner{tests: map[string]*ManagedTest{test.ID: test}, historyStore: store}
	batch := checkpointBatch(test, 1)
	acceptCheckpointBatch(test, batch, test.CreatedAt.Add(time.Second))
	runner.persistTelemetryCheckpoint(test)
	// Uncommitted second window cannot advance only a cursor: both cursor and
	// effects recover from the first atomic snapshot after storage rejects the write.
	batch = checkpointBatch(test, 2)
	acceptCheckpointBatch(test, batch, test.CreatedAt.Add(2*time.Second))
	test.RefKey = "wrong-incarnation"
	runner.persistTelemetryCheckpoint(test)
	test.RefKey = durable.RefKey
	// An entry-bound failure, like a rejected database write, must preserve
	// the earlier recoverable cursor/effects pair rather than commit a payload
	// the recovery reader will reject.
	savedWindows := test.runTotals.windows
	test.runTotals.windows = map[string]distributedWindowMark{}
	for i := 0; i <= maxTelemetrySourceEpochs; i++ {
		test.runTotals.windows[fmt.Sprintf("generator-%d", i)] = savedWindows["generator"]
	}
	runner.persistTelemetryCheckpoint(test)
	test.runTotals.windows = savedWindows
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = storage.NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	successor := checkpointTestRun()
	successor.RefKey = durable.RefKey
	successor.executionGeneration = 0
	nextRunner := &TestRunner{tests: map[string]*ManagedTest{test.ID: successor}, historyStore: store}
	nextRunner.restoreTelemetryCheckpoint(successor, 1, 0)
	if !successor.telemetryCheckpointRestored || successor.runTotals.total.tx != 8 || successor.runTotals.windows["generator"].collection != 1 || len(successor.telemetrySources.collections) != 1 {
		t.Fatalf("cursor/effects split on restart: %+v", successor.runTotals)
	}
	recoveredTotals := nextRunner.adoptedRunTotals(successor)
	if recoveredTotals.total.tx != 8 {
		t.Fatal("adoption replaced durable totals with sampled history")
	}
	// Replaying the uncommitted window now adds it once.
	successor.executionGeneration = 1
	batch = checkpointBatch(successor, 2)
	acceptCheckpointBatch(successor, batch, test.CreatedAt.Add(10*time.Second))
	acceptCheckpointBatch(successor, batch, test.CreatedAt.Add(11*time.Second))
	if successor.runTotals.total.tx != 16 || len(successor.telemetrySources.collections) != 2 {
		t.Fatal("uncommitted retry was lost or counted twice")
	}
}

func TestTelemetryCheckpointRetentionAndArchiveCopies(t *testing.T) {
	test := checkpointTestRun()
	for i := uint64(1); i <= maxRetainedTelemetryCollections+2; i++ {
		batch := checkpointBatch(test, i)
		acceptCheckpointBatch(test, batch, time.UnixMilli(batch.TimestampUnixMs))
	}
	ledger := test.telemetrySources
	if len(ledger.collections) != maxRetainedTelemetryCollections || !ledger.collectionsTruncated || ledger.collections[0].Collection != 3 {
		t.Fatal("unbounded or unmarked evidence retention")
	}
	records, _ := ledger.snapshotRecords(true)
	if len(records) != 1 || !records[0].CollectionWindowsTruncated || len(records[0].CollectionWindows) != maxRetainedTelemetryCollections {
		t.Fatal("archive dropped retained window provenance")
	}
	records[0].CollectionWindows[0].Windows[0].Group = "mutated"
	if ledger.collections[0].Windows[0].Group != "checkout" {
		t.Fatal("archive projection aliases live windows")
	}
	cp := captureTelemetryCheckpointLocked(test)
	raw, err := json.Marshal(cp)
	if err != nil {
		t.Fatal(err)
	}
	restored, _, err := decodeTelemetryCheckpoint(string(raw), cp.Revision, test, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !restored.collectionsTruncated || len(restored.collections) != maxRetainedTelemetryCollections {
		t.Fatal("restart erased truncation")
	}
	wide := checkpointTestRun()
	for i := uint64(1); i <= 20; i++ {
		batch := checkpointBatch(wide, i)
		window := batch.CollectionWindows[0]
		batch.CollectionWindows = nil
		for group := 0; group < generatorrpc.MaxMetricsCollectionWindows; group++ {
			w := window
			w.Group = fmt.Sprintf("group-%d", group)
			batch.CollectionWindows = append(batch.CollectionWindows, w)
		}
		acceptCheckpointBatch(wide, batch, time.UnixMilli(batch.TimestampUnixMs))
	}
	if len(wide.telemetrySources.collections) != 16 || !wide.telemetrySources.collectionsTruncated || wide.runTotals.total.tx != 160 {
		t.Fatal("group-window bound lost count effects or retained too many intervals")
	}

}

func TestTelemetryCheckpointArchiveRetainsRecoveredWindows(t *testing.T) {
	test := checkpointTestRun()
	batch := checkpointBatch(test, 1)
	acceptCheckpointBatch(test, batch, test.CreatedAt.Add(time.Second))
	cp := captureTelemetryCheckpointLocked(test)
	raw, _ := json.Marshal(cp)
	ledger, totals, err := decodeTelemetryCheckpoint(string(raw), cp.Revision, test, 0)
	if err != nil {
		t.Fatal(err)
	}
	test.telemetrySources, test.runTotals = ledger, totals
	runner := &TestRunner{}
	record := &storage.RunArchiveRecord{TestID: test.ID, ArchivePath: t.TempDir()}
	record.ManifestPath = filepath.Join(record.ArchivePath, "manifest.json")
	admitArchiveForTest(t, record)
	manifest := &RunArchiveManifest{}
	if err := runner.persistTelemetrySourceArchive(context.Background(), test, record, manifest); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(record.ArchivePath, telemetrySourcesFileName))
	if err != nil {
		t.Fatal(err)
	}
	var epoch TelemetrySourceEpoch
	if err := json.Unmarshal(data, &epoch); err != nil {
		t.Fatal(err)
	}
	if len(epoch.CollectionWindows) != 1 || !epoch.CollectionWindows[0].Windows[0].Start.Equal(batch.CollectionWindows[0].Start) || epoch.CollectionWindows[0].Classification != "unknown" || epoch.CollectionWindows[0].CounterReadings[TelemetryCounterDroppedResults].Value == nil || *epoch.CollectionWindows[0].CounterReadings[TelemetryCounterDroppedResults].Value != 3 {
		t.Fatalf("recovered source evidence lost: %s", data)
	}
	// A tight allowance must retain the old source counters even when the new
	// optional per-collection provenance cannot fit.
	t.Setenv(envArchiveTelemetrySourcesMaxBytes, "2048")
	for i := uint64(2); i < 40; i++ {
		b := checkpointBatch(test, i)
		acceptCheckpointBatch(test, b, time.UnixMilli(b.TimestampUnixMs))
	}
	manifest = &RunArchiveManifest{}
	if err := runner.persistTelemetrySourceArchive(context.Background(), test, record, manifest); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(filepath.Join(record.ArchivePath, telemetrySourcesFileName))
	if err != nil {
		t.Fatal(err)
	}
	epoch = TelemetrySourceEpoch{}
	if err := json.Unmarshal(data, &epoch); err != nil {
		t.Fatal(err)
	}
	if !epoch.CollectionWindowsTruncated || epoch.Counters[TelemetryCounterDroppedResults].Value != 3 || manifest.TelemetrySourceEpochCount != 1 {
		t.Fatalf("optional windows displaced counter evidence: %s", data)
	}
}

type checkpointReplyFailureStore struct{ *storage.SQLiteStore }

func (s checkpointReplyFailureStore) SaveTelemetryCheckpoint(w storage.TelemetryCheckpointWrite) (bool, error) {
	committed, err := s.SQLiteStore.SaveTelemetryCheckpoint(w)
	if err != nil {
		return committed, err
	}
	return false, fmt.Errorf("injected lost commit reply")
}

func TestTelemetryCheckpointLegacyAndAmbiguousCommit(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "controller.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	test := checkpointTestRun()
	test.executionGeneration = 0
	if err := store.SaveTest(&storage.TestRecord{ID: test.ID, Name: test.ID, RefKey: test.RefKey, ProjectID: test.ProjectID, Status: "RUNNING", StartTime: test.CreatedAt, InitialProvenance: test.initialArchiveProvenance}); err != nil {
		t.Fatal(err)
	}
	row, err := store.GetTest(test.ID)
	if err != nil {
		t.Fatal(err)
	}
	test.RefKey = row.RefKey
	runner := &TestRunner{tests: map[string]*ManagedTest{test.ID: test}, historyStore: checkpointReplyFailureStore{store}}
	runner.restoreTelemetryCheckpoint(test, 0, 0)
	if test.telemetryCheckpointRestored || test.telemetrySources != nil || test.runTotals.initialized {
		t.Fatal("legacy absence acquired durable evidence")
	}
	ledger := telemetrySourceLedgerLocked(test)
	ledger.markAdopted()
	ledger.assign("generator", test.CreatedAt)
	if reason := telemetryCoverageObserverLost(ledger, []string{"generator"}, ledger.wasAdopted()); reason != telemetryCoverageAdopted {
		t.Fatal("legacy coverage not lower-bound")
	}
	batch := checkpointBatch(test, 1)
	ledger.observe("generator", batch, test.CreatedAt.Add(time.Second))
	backend := &distributedExecutionBackend{}
	if !backend.storeRemoteMetricsBatchOnly(runner, test, "generator", batch) {
		t.Fatal("accepted window rejected")
	}
	// The real acceptance seam commits cursor and totals despite a lost reply.
	runner.restoreTelemetryCheckpoint(test, 0, 0)
	if !test.telemetryCheckpointRestored || test.runTotals.total.tx != 8 || test.runTotals.windows["generator"].collection != 1 {
		t.Fatal("ambiguous successful commit lost cursor or effects")
	}
	raw, revision, err := store.LoadTelemetryCheckpoint(test.ID, test.RefKey, test.ProjectID, test.CreatedAt)
	if err != nil || raw == "" {
		t.Fatal(err)
	}
	var cp telemetryCheckpoint
	if err := json.Unmarshal([]byte(raw), &cp); err != nil {
		t.Fatal(err)
	}
	cp.Version = 99
	cp.Revision = revision + 1
	future, _ := json.Marshal(cp)
	if ok, err := store.SaveTelemetryCheckpoint(storage.TelemetryCheckpointWrite{ID: test.ID, RefKey: test.RefKey, ProjectID: test.ProjectID, StartTime: test.CreatedAt, RunIncarnation: managedTestRunIncarnation(test), Revision: cp.Revision, Payload: string(future)}); err != nil || !ok {
		t.Fatalf("future fixture %v %v", ok, err)
	}
	runner.restoreTelemetryCheckpoint(test, 0, 0)
	if test.telemetryCheckpointRestored || !test.telemetryCheckpointReadOnly || test.telemetrySources != nil {
		t.Fatal("future schema accepted or writable")
	}
	acceptCheckpointBatch(test, checkpointBatch(test, 2), test.CreatedAt.Add(3*time.Second))
	runner.persistTelemetryCheckpoint(test)
	got, _, err := store.LoadTelemetryCheckpoint(test.ID, test.RefKey, test.ProjectID, test.CreatedAt)
	if err != nil || got != string(future) {
		t.Fatal("older writer overwrote future evidence")
	}
}

func TestTelemetryCheckpointAdoptionDoesNotOverwriteNewerEffects(t *testing.T) {
	test := checkpointTestRun()
	acceptCheckpointBatch(test, checkpointBatch(test, 1), test.CreatedAt.Add(time.Second))
	cp := captureTelemetryCheckpointLocked(test)
	raw, _ := json.Marshal(cp)
	ledger, totals, err := decodeTelemetryCheckpoint(string(raw), cp.Revision, test, 0)
	if err != nil {
		t.Fatal(err)
	}
	test.telemetrySources, test.runTotals, test.telemetryCheckpointRestored = ledger, totals, true
	runner := &TestRunner{}
	earlier := runner.adoptedRunTotals(test)
	terminal := checkpointBatch(test, 2)
	terminal.LifecycleState = "COMPLETE"
	acceptCheckpointBatch(test, terminal, test.CreatedAt.Add(2*time.Second))
	installAdoptedRunTotalsLocked(test, earlier)
	if test.runTotals.total.tx != 16 || test.runTotals.windows["generator"].collection != 2 {
		t.Fatal("stream installation split the terminal cursor from its newer effects")
	}
}

func TestTelemetryCheckpointJointStreamsRoundTripAndBackwardCompatibility(t *testing.T) {
	test := checkpointTestRun()
	test.initialArchiveProvenance.JointEvidence = jointTestProjection.Clone()
	ledger := telemetrySourceLedgerLocked(test)

	// Sequence 1..2: state-only staging prefix on first running batch (sequence 3).
	batch3 := checkpointBatch(test, 1)
	batch3.BatchSequence = 3
	batch3.SequencePrefixStateOnly = true
	ledger.observe("generator", batch3, time.UnixMilli(batch3.TimestampUnixMs))
	rememberJointPublicationLocked(test, "generator", batch3)

	// Sequence 4: second accepted running batch.
	batch4 := checkpointBatch(test, 2)
	batch4.BatchSequence = 4
	ledger.observe("generator", batch4, time.UnixMilli(batch4.TimestampUnixMs))
	rememberJointPublicationLocked(test, "generator", batch4)

	// Sequence 5: terminal batch.
	batch5 := checkpointBatch(test, 3)
	batch5.BatchSequence = 5
	batch5.LifecycleState = "COMPLETE"
	ledger.observe("generator", batch5, time.UnixMilli(batch5.TimestampUnixMs))
	rememberJointPublicationLocked(test, "generator", batch5)

	// Sequence 6: duplicate terminal replay / post-completion batch (benign ignored).
	batch6 := checkpointBatch(test, 3)
	batch6.BatchSequence = 6
	batch6.LifecycleState = "RUNNING"
	ledger.observe("generator", batch6, time.UnixMilli(batch6.TimestampUnixMs))

	cp := captureTelemetryCheckpointLocked(test)
	if len(cp.JointStreams) != 1 {
		t.Fatalf("JointStreams len = %d, want 1", len(cp.JointStreams))
	}
	js := cp.JointStreams[0]
	if !reflect.DeepEqual(js.AcceptedRanges, [][2]uint64{{3, 5}}) {
		t.Fatalf("AcceptedRanges = %v, want [[3 5]]", js.AcceptedRanges)
	}
	if !reflect.DeepEqual(js.IgnoredRanges, [][2]uint64{{1, 2}, {6, 6}}) {
		t.Fatalf("IgnoredRanges = %v, want [[1 2] [6 6]]", js.IgnoredRanges)
	}
	if len(js.Windows) != 1 || js.Windows[0].FirstSequence != 3 || js.Windows[0].LastSequence != 5 {
		t.Fatalf("Windows = %+v, want merged [3..5]", js.Windows)
	}

	raw, err := json.Marshal(cp)
	if err != nil {
		t.Fatal(err)
	}
	restored, _, err := decodeTelemetryCheckpoint(string(raw), cp.Revision, test, 0)
	if err != nil {
		t.Fatal(err)
	}
	key := telemetrySourceKey{source: "generator", generation: test.executionGeneration, epoch: "epoch-1"}
	gotStream := restored.jointStreams[key]
	if gotStream == nil || !reflect.DeepEqual(gotStream.AcceptedRanges, js.AcceptedRanges) || !reflect.DeepEqual(gotStream.IgnoredRanges, js.IgnoredRanges) || !reflect.DeepEqual(gotStream.Windows, js.Windows) {
		t.Fatalf("restored jointStream = %+v, want %+v", gotStream, js)
	}

	// Backward-compatible with v1 checkpoints that omit JointStreams and JointStreamsTruncated.
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	delete(fields, "JointStreams")
	delete(fields, "JointStreamsTruncated")
	v1Raw, _ := json.Marshal(fields)
	v1Restored, _, err := decodeTelemetryCheckpoint(string(v1Raw), cp.Revision, test, 0)
	if err != nil {
		t.Fatalf("v1 checkpoint without JointStreams rejected: %v", err)
	}
	if len(v1Restored.jointStreams) != 0 {
		t.Fatalf("expected empty jointStreams on v1 checkpoint, got %d", len(v1Restored.jointStreams))
	}

	// Reject malformed JointStreams.
	bad := cp
	bad.JointStreams = []durableJointStream{{Source: "generator", ExecutionGeneration: test.executionGeneration, Epoch: "epoch-1", AcceptedRanges: [][2]uint64{{5, 3}}}}
	badRaw, _ := json.Marshal(bad)
	if _, _, err := decodeTelemetryCheckpoint(string(badRaw), cp.Revision, test, 0); err == nil {
		t.Fatal("expected inverted JointStreams range to be rejected")
	}
}
