package api

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/storage"
)

func sourceBatch(epoch string, started int64, sequence uint64) generatorrpc.MetricsBatch {
	return generatorrpc.MetricsBatch{ExecutionGeneration: 7, SourceEpoch: epoch, SourceEpochStartedUnixMs: started, BatchSequence: sequence}
}

func observeSequences(ledger *telemetrySourceLedger, source, epoch string, started int64, sequences ...uint64) {
	at := time.Unix(1_800_000_000, 0)
	for _, sequence := range sequences {
		at = at.Add(time.Second)
		ledger.observe(source, sourceBatch(epoch, started, sequence), at)
	}
}

// finishEpoch observes the epoch's terminal publication.
func finishEpoch(ledger *telemetrySourceLedger, source, epoch string, started int64, sequence uint64) {
	batch := sourceBatch(epoch, started, sequence)
	batch.LifecycleState = "COMPLETE"
	ledger.observe(source, batch, time.Unix(1_900_000_000, 0))
}

func onlyEpoch(t *testing.T, ledger *telemetrySourceLedger) TelemetrySourceEpoch {
	t.Helper()
	epochs, _ := ledger.snapshot()
	if len(epochs) != 1 {
		t.Fatalf("epochs = %d, want 1", len(epochs))
	}
	return epochs[0]
}

func TestTelemetrySourceLossIsExactWithinAnObservedEpoch(t *testing.T) {
	contiguous := newTelemetrySourceLedger()
	observeSequences(contiguous, "gen-a", "epoch-1", 1000, 1, 2, 3, 4)
	finishEpoch(contiguous, "gen-a", "epoch-1", 1000, 5)
	if epoch := onlyEpoch(t, contiguous); epoch.LostPublications != 0 || !epoch.LossExact || epoch.Received != 5 || epoch.TerminalSequence != 5 {
		t.Fatalf("contiguous: %#v", epoch)
	}
	gapped := newTelemetrySourceLedger()
	observeSequences(gapped, "gen-a", "epoch-1", 1000, 1, 2, 5, 6)
	finishEpoch(gapped, "gen-a", "epoch-1", 1000, 7)
	if epoch := onlyEpoch(t, gapped); epoch.LostPublications != 2 || !epoch.LossExact {
		t.Fatalf("gap of two: %#v", epoch)
	}
	// A retried and a late publication count once; the late one fills the gap.
	late := newTelemetrySourceLedger()
	observeSequences(late, "gen-a", "epoch-1", 1000, 1, 2, 4, 4, 3)
	finishEpoch(late, "gen-a", "epoch-1", 1000, 5)
	if epoch := onlyEpoch(t, late); epoch.LostPublications != 0 || epoch.Duplicates != 1 || epoch.OutOfOrder != 1 || epoch.Received != 6 || !epoch.LossExact {
		t.Fatalf("duplicate and late: %#v", epoch)
	}
}

// Publications after the last one received are unknown until the epoch's
// terminal publication arrives: a stream that ended early is a lower bound.
func TestTelemetrySourceTailLossIsNotExactWithoutTheTerminalPublication(t *testing.T) {
	ledger := newTelemetrySourceLedger()
	sequences := []uint64{}
	for sequence := uint64(1); sequence <= 95; sequence++ {
		sequences = append(sequences, sequence)
	}
	observeSequences(ledger, "gen-a", "epoch-1", 1000, sequences...)
	if epoch := onlyEpoch(t, ledger); epoch.LossExact || epoch.LostPublications != 0 {
		t.Fatalf("a stream that ended at 95 of 100 claimed exact coverage: %#v", epoch)
	}
	// A terminal publication with a gap before it makes the tail exact.
	finishEpoch(ledger, "gen-a", "epoch-1", 1000, 101)
	if epoch := onlyEpoch(t, ledger); !epoch.LossExact || epoch.LostPublications != 5 {
		t.Fatalf("after the terminal publication: %#v", epoch)
	}
}

func TestTelemetrySourceLossIsALowerBoundWithoutAKnownOrigin(t *testing.T) {
	// Observed from sequence 40 (a stream joined midway, as a generator that
	// reconnects to a restarted controller does): publications 1-39 are
	// unknown, so the count is only a lower bound.
	midstream := newTelemetrySourceLedger()
	observeSequences(midstream, "gen-a", "epoch-1", 1000, 40, 41, 43)
	if epoch := onlyEpoch(t, midstream); epoch.LostPublications != 1 || epoch.LossExact {
		t.Fatalf("midstream: %#v", epoch)
	}
	legacy := newTelemetrySourceLedger()
	observeSequences(legacy, "gen-a", "", 0, 1, 2, 3)
	if epoch := onlyEpoch(t, legacy); epoch.LossExact {
		t.Fatalf("an older peer without epochs cannot prove exact loss: %#v", epoch)
	}
	unsequenced := newTelemetrySourceLedger()
	observeSequences(unsequenced, "gen-a", "", 0, 0, 0)
	if epoch := onlyEpoch(t, unsequenced); epoch.Unsequenced != 2 || epoch.LastSequence != 0 || epoch.LossExact {
		t.Fatalf("unsequenced: %#v", epoch)
	}
}

func TestTelemetrySourceRangeTruncationKeepsALowerBound(t *testing.T) {
	ledger := newTelemetrySourceLedger()
	sequences := []uint64{}
	for sequence := uint64(1); sequence <= 2*maxTelemetrySourceRanges+10; sequence += 2 {
		sequences = append(sequences, sequence)
	}
	observeSequences(ledger, "gen-a", "epoch-1", 1000, sequences...)
	epoch := onlyEpoch(t, ledger)
	trueLoss := int64(len(sequences) - 1)
	if !epoch.RangesTruncated || epoch.LossExact || epoch.LostPublications >= trueLoss || epoch.LostPublications <= 0 {
		t.Fatalf("truncated ranges must under-count loss and say so: lost=%d true=%d %#v", epoch.LostPublications, trueLoss, epoch.RangesTruncated)
	}
}

func TestTelemetrySourceRestartStartsANewEpoch(t *testing.T) {
	ledger := newTelemetrySourceLedger()
	observeSequences(ledger, "gen-a", "epoch-1", 1000, 1, 2, 3)
	observeSequences(ledger, "gen-a", "epoch-2", 2000, 1, 2)
	finishEpoch(ledger, "gen-a", "epoch-2", 2000, 3)
	observeSequences(ledger, "gen-b", "epoch-9", 1000, 1, 3)
	finishEpoch(ledger, "gen-b", "epoch-9", 1000, 4)
	epochs, truncated := ledger.snapshot()
	if len(epochs) != 3 || truncated {
		t.Fatalf("epochs: %#v", epochs)
	}
	// The replaced epoch never published its terminal record, so its tail is
	// unknown and the run's coverage cannot be exact.
	summary := ledger.summary()
	if summary.Sources != 2 || summary.Epochs != 3 || summary.LostPublications != 1 || summary.LossExact {
		t.Fatalf("summary: %#v", summary)
	}
}

// An adopted run's earlier execution generations were never observed by this
// controller; they are reported, and coverage is not exact.
func TestTelemetrySourceUnobservedGenerationsAfterAdoption(t *testing.T) {
	ledger := newTelemetrySourceLedger()
	batch := sourceBatch("epoch-1", 1000, 1)
	batch.ExecutionGeneration = 8
	ledger.observe("gen-a", batch, time.Unix(1_800_000_000, 0))
	batch.BatchSequence, batch.LifecycleState = 2, "COMPLETE"
	ledger.observe("gen-a", batch, time.Unix(1_800_000_001, 0))
	failovers := []RunFailover{{PriorGeneration: 7, Generation: 8, AdoptedAt: time.Unix(1_799_999_999, 0)}}
	summary := ledger.summary(failovers...)
	if summary.LossExact || summary.UnobservedGenerations != 1 || summary.Epochs != 1 {
		t.Fatalf("adopted summary: %#v", summary)
	}
	epochs, _ := ledger.snapshot(failovers...)
	unobserved := 0
	for _, epoch := range epochs {
		if epoch.Unobserved && epoch.ExecutionGeneration == 7 && !epoch.LossExact {
			unobserved++
		}
	}
	if unobserved != 1 {
		t.Fatalf("snapshot: %#v", epochs)
	}
	if exact := ledger.summary(); !exact.LossExact {
		t.Fatalf("without failovers the observed generation is exact: %#v", exact)
	}
	var none *telemetrySourceLedger
	if summary := none.summary(failovers...); summary == nil || summary.UnobservedGenerations != 1 || summary.LossExact {
		t.Fatalf("a run observed only before adoption: %#v", summary)
	}
}

func TestRemoteBatchFromANewerEpochSupersedesWhenSequencesRestart(t *testing.T) {
	prior := sourceBatch("epoch-1", 1000, 50)
	prior.LifecycleState = "RUNNING"
	restarted := sourceBatch("epoch-2", 2000, 1)
	restarted.LifecycleState = "RUNNING"
	if !remoteMetricsBatchSupersedes(prior, restarted) {
		t.Fatal("a restarted generator's first window was rejected as stale")
	}
	if remoteMetricsBatchSupersedes(restarted, prior) {
		t.Fatal("a late window from the older epoch replaced the newer epoch")
	}
	terminal := prior
	terminal.LifecycleState = "COMPLETE"
	if remoteMetricsBatchSupersedes(terminal, restarted) {
		t.Fatal("a non-terminal window replaced a terminal state")
	}
	sameEpochStale := sourceBatch("epoch-1", 1000, 49)
	if remoteMetricsBatchSupersedes(prior, sameEpochStale) {
		t.Fatal("an older sequence within one epoch replaced a newer one")
	}
}

func TestTelemetrySourceArchiveSurvivesAFinalizationRetry(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	test := &ManagedTest{ID: "source-run"}
	ledger := telemetrySourceLedgerLocked(test)
	observeSequences(ledger, "gen-a", "epoch-1", 1000, 1, 2, 4)
	finishEpoch(ledger, "gen-a", "epoch-1", 1000, 5)
	archive := t.TempDir()
	record := &storage.RunArchiveRecord{TestID: test.ID, ArchivePath: archive, ManifestPath: archive + "/manifest.json"}
	admitArchiveForTest(t, record)
	manifest := &RunArchiveManifest{}
	if err := runner.persistTelemetrySourceArchive(context.Background(), test, record, manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.TelemetrySourcesFile != telemetrySourcesFileName || manifest.TelemetrySourceEpochCount != 1 || len(manifest.Artifacts) != 1 || manifest.Artifacts[0].RecordType != telemetrySourcesRecordType {
		t.Fatalf("manifest %+v", manifest)
	}
	body, err := os.ReadFile(filepath.Join(archive, telemetrySourcesFileName))
	if err != nil {
		t.Fatal(err)
	}
	var epoch TelemetrySourceEpoch
	if err := json.Unmarshal(bytes.TrimSpace(body), &epoch); err != nil || epoch.LostPublications != 1 || !epoch.LossExact || epoch.Schema != telemetrySourceEpochSchema {
		t.Fatalf("sidecar line %s %v", body, err)
	}
	// A later finalization step can fail; the retry reloads a manifest
	// without the sidecar and must find the same evidence.
	retried := &RunArchiveManifest{}
	if err := runner.persistTelemetrySourceArchive(context.Background(), test, record, retried); err != nil || retried.TelemetrySourcesFile != telemetrySourcesFileName || retried.TelemetrySourceEpochCount != 1 {
		t.Fatalf("finalization retry lost the ledger: %+v %v", retried, err)
	}
}

// A publication received while another target's evacuation bumps the
// topology generation is still counted: the consumer re-fences its store
// loop and stores it, so coverage must not report it lost. A stream that no
// longer owns its target counts nothing.
func TestTelemetryPublicationIsCountedWhileTheStreamOwnsItsTarget(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	stream := &generatorrpc.MetricsStream{}
	test := &ManagedTest{
		ID: "observe-run", Status: "RUNNING", executionGeneration: 7, remoteControlGeneration: 1,
		remoteMetricsStreamsByTarget: map[string]*generatorrpc.MetricsStream{"gen-a": stream},
	}
	runner.mu.Lock()
	runner.tests[test.ID] = test
	runner.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	fence := distributedMetricsConsumerFence{ctx: ctx, executionGeneration: 7, topologyGeneration: 1, target: "gen-a", stream: stream, enforced: true}

	runner.mu.Lock()
	defer runner.mu.Unlock()
	test.remoteControlGeneration = 2 // another target was evacuated
	if distributedMetricsConsumerFenceCurrentLocked(runner, test, "gen-a", fence, false) {
		t.Fatal("fixture: the topology change did not stale the fence")
	}
	if !observeTelemetryPublicationLocked(runner, test, "gen-a", fence, false, sourceBatch("epoch-1", 1000, 1), time.Now()) {
		t.Fatal("a publication the stream still owns was not counted")
	}
	test.remoteMetricsStreamsByTarget["gen-a"] = &generatorrpc.MetricsStream{} // replaced by a newer stream
	if observeTelemetryPublicationLocked(runner, test, "gen-a", fence, false, sourceBatch("epoch-1", 1000, 2), time.Now()) {
		t.Fatal("a retired stream's publication was counted")
	}
	if epoch := onlyEpoch(t, test.telemetrySources); epoch.Received != 1 {
		t.Fatalf("received = %d, want 1", epoch.Received)
	}
}

// The live summary and the archived sidecar both count generations adopted
// from a failed-over controller as unobserved.
func TestTelemetrySourceRollupsCountTheRunFailovers(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	test := &ManagedTest{ID: "adopted-run", continuityFailovers: []RunFailover{{PriorGeneration: 6, Generation: 7, AdoptedAt: time.Unix(1_799_999_999, 0)}}}
	ledger := telemetrySourceLedgerLocked(test)
	observeSequences(ledger, "gen-a", "epoch-1", 1000, 1, 2)
	finishEpoch(ledger, "gen-a", "epoch-1", 1000, 3)
	if summary := telemetrySourceSummaryLocked(test); summary.UnobservedGenerations != 1 || summary.LossExact {
		t.Fatalf("live summary ignored the failover: %#v", summary)
	}
	archive := t.TempDir()
	record := &storage.RunArchiveRecord{TestID: test.ID, ArchivePath: archive, ManifestPath: archive + "/manifest.json"}
	admitArchiveForTest(t, record)
	manifest := &RunArchiveManifest{}
	if err := runner.persistTelemetrySourceArchive(context.Background(), test, record, manifest); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(archive, telemetrySourcesFileName))
	if err != nil {
		t.Fatal(err)
	}
	unobserved := 0
	for _, line := range bytes.Split(bytes.TrimSpace(body), []byte("\n")) {
		var epoch TelemetrySourceEpoch
		if err := json.Unmarshal(line, &epoch); err != nil {
			t.Fatal(err)
		}
		if epoch.Unobserved && epoch.ExecutionGeneration == 6 {
			unobserved++
		}
	}
	if unobserved != 1 || manifest.TelemetrySourceEpochCount != 2 {
		t.Fatalf("archived sidecar ignored the failover (count %d): %s", manifest.TelemetrySourceEpochCount, body)
	}
}

// Sequences restart with each source epoch, so the live missing-window count
// measures gaps within one epoch only: a restarted generator's first window
// is not a gap from the old epoch's last sequence.
func TestMissingMetricWindowsAreCountedWithinOneSourceEpoch(t *testing.T) {
	runner, run, backend := newDistributedThresholdTestRunner(t)
	store := func(epoch string, sequence uint64, at int64) int64 {
		t.Helper()
		batch := distributedP99Batch(run.ID, at, 100)
		batch.SourceEpoch, batch.SourceEpochStartedUnixMs, batch.BatchSequence = epoch, 1000, sequence
		if epoch == "epoch-2" {
			batch.SourceEpochStartedUnixMs = 2000
		}
		if !backend.storeRemoteMetricsBatchOnly(runner, run, "generator-a", batch) {
			t.Fatalf("batch %s/%d rejected", epoch, sequence)
		}
		stored := run.remoteMetricsBatchesByTarget["generator-a"]
		if stored.Telemetry == nil {
			return 0
		}
		return stored.Telemetry.MissingMetricWindows
	}
	store("epoch-1", 1, 1000)
	if missing := store("epoch-1", 4, 4000); missing != 2 {
		t.Fatalf("gap within an epoch: missing = %d, want 2", missing)
	}
	// The new epoch is first seen at sequence 7 (its earlier windows went to a
	// stream that was replaced); 7 is not a gap after the old epoch's 4.
	if missing := store("epoch-2", 7, 5000); missing != 2 {
		t.Fatalf("a new epoch's first window added a gap: missing = %d, want 2", missing)
	}
}
