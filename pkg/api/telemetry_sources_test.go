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
	if epoch := onlyEpoch(t, contiguous); epoch.LostPublications != 0 || !epoch.LossExact || epoch.Received != 4 {
		t.Fatalf("contiguous: %#v", epoch)
	}
	gapped := newTelemetrySourceLedger()
	observeSequences(gapped, "gen-a", "epoch-1", 1000, 1, 2, 5, 6)
	if epoch := onlyEpoch(t, gapped); epoch.LostPublications != 2 || !epoch.LossExact {
		t.Fatalf("gap of two: %#v", epoch)
	}
	// A retried and a late publication count once; the late one fills the gap.
	late := newTelemetrySourceLedger()
	observeSequences(late, "gen-a", "epoch-1", 1000, 1, 2, 4, 4, 3)
	if epoch := onlyEpoch(t, late); epoch.LostPublications != 0 || epoch.Duplicates != 1 || epoch.OutOfOrder != 1 || epoch.Received != 5 {
		t.Fatalf("duplicate and late: %#v", epoch)
	}
}

func TestTelemetrySourceLossIsALowerBoundWithoutAKnownOrigin(t *testing.T) {
	// Observed from sequence 40 (for example after controller takeover):
	// publications 1-39 are unknown, so the count is only a lower bound.
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
	observeSequences(ledger, "gen-b", "epoch-9", 1000, 1, 3)
	epochs, truncated := ledger.snapshot()
	if len(epochs) != 3 || truncated {
		t.Fatalf("epochs: %#v", epochs)
	}
	summary := ledger.summary()
	if summary.Sources != 2 || summary.Epochs != 3 || summary.LostPublications != 1 || !summary.LossExact {
		t.Fatalf("summary: %#v", summary)
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

func TestTelemetrySourceArchiveIsWrittenOnceAndReleasesTheLedger(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	test := &ManagedTest{ID: "source-run"}
	observeSequences(runner.telemetrySourceLedgerFor(test.ID), "gen-a", "epoch-1", 1000, 1, 2, 4)
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
	if runner.existingTelemetrySourceLedger(test.ID) != nil {
		t.Fatal("ledger retained after archiving")
	}
	if err := runner.persistTelemetrySourceArchive(context.Background(), test, record, manifest); err != nil || manifest.TelemetrySourceEpochCount != 1 {
		t.Fatalf("second persist changed the archived sidecar: %v", err)
	}
}
