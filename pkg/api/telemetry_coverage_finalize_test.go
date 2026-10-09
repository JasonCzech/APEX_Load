package api

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

// coverageFinalizeFixture writes an archive of healthy one-second intervals
// whose provenance names shards, and returns its record and manifest.
func coverageFinalizeFixture(t *testing.T, start time.Time, seconds int, shards []GeneratorShardSummary) (*storage.RunArchiveRecord, *RunArchiveManifest) {
	t.Helper()
	root := t.TempDir()
	end := start.Add(time.Duration(seconds) * time.Second)
	rows := healthyRows(start, seconds)
	file, err := os.Create(filepath.Join(root, "interval_summaries.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	encoder := json.NewEncoder(file)
	for _, row := range rows {
		if err := encoder.Encode(row); err != nil {
			t.Fatal(err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	manifest := &RunArchiveManifest{SchemaVersion: archiveSchemaVersion, TestID: "coverage-run", TestName: "coverage-run", Status: "COMPLETE", CreatedAt: start, CompletedAt: end,
		Provenance: RunArchiveProvenance{StartedAt: &start, GeneratorShards: shards}, IntervalSummariesFile: "interval_summaries.jsonl", IntervalSummaryCount: int64(len(rows))}
	data, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(root, "manifest.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	record := &storage.RunArchiveRecord{TestID: "coverage-run", SchemaVersion: archiveSchemaVersion, ArchivePath: root, ManifestPath: filepath.Join(root, "manifest.json"), CreatedAt: start, CompletedAt: end, IntervalSummaryCount: int64(len(rows))}
	return record, manifest
}

// At finalize a local run whose runtime-resource placement names the
// controller's own process is observed throughout, and a distributed run
// rebuilt from storage (no ledger, no clients) is unknown throughout,
// known as distributed only from its durable shard placement.
func TestTelemetryCoverageAtFinalizeFromDurablePlacement(t *testing.T) {
	start := time.Unix(1_800_000_000, 0).UTC()
	end := start.Add(60 * time.Second)
	tr := &TestRunner{}
	local := []GeneratorShardSummary{{Group: "consumers", Target: localRuntimeResourceTarget, VUsers: 2}}
	record, manifest := coverageFinalizeFixture(t, start, 60, local)
	test := &ManagedTest{ID: "coverage-run", Status: "COMPLETE", StartedAt: &start, EndedAt: &end, GeneratorShards: local}
	if err := tr.persistTelemetryCoverage(context.Background(), test, record, manifest); err != nil {
		t.Fatal(err)
	}
	if coverage := manifest.TelemetryCoverage; coverage == nil || coverage.HealthyMs != 60_000 || coverage.UnknownMs != 0 || len(coverage.Coverage.Sources) != 0 || coverage.Coverage.ObserverLost != "" || !coverage.Coverage.SourceRequirements {
		t.Fatalf("local run with a local placement: %+v", coverage)
	}

	remote := []GeneratorShardSummary{{Group: "shop", Target: "gen-a:9090", VUsers: 10}}
	record, manifest = coverageFinalizeFixture(t, start, 60, remote)
	rebuilt := &ManagedTest{ID: "coverage-run", Status: "COMPLETE", StartedAt: &start, EndedAt: &end}
	if err := tr.persistTelemetryCoverage(context.Background(), rebuilt, record, manifest); err != nil {
		t.Fatal(err)
	}
	// The time is unknown here because this controller lost what was
	// observed, which the record says: its observed time is a lower bound.
	if coverage := manifest.TelemetryCoverage; coverage == nil || coverage.HealthyMs != 0 || coverage.UnknownMs != 60_000 || coverage.Coverage.ObserverLost != telemetryCoverageLedgerLost || !coverage.Coverage.SourceRequirements {
		t.Fatalf("distributed run rebuilt from storage: %+v", coverage)
	}
	// A controller that adopted the run observed it only from then.
	record, manifest = coverageFinalizeFixture(t, start, 60, remote)
	adopted := &ManagedTest{ID: "coverage-run", Status: "COMPLETE", StartedAt: &start, EndedAt: &end, telemetrySources: newTelemetrySourceLedger()}
	adopted.telemetrySources.markAdopted()
	if err := tr.persistTelemetryCoverage(context.Background(), adopted, record, manifest); err != nil {
		t.Fatal(err)
	}
	if coverage := manifest.TelemetryCoverage; coverage == nil || coverage.Coverage.ObserverLost != telemetryCoverageAdopted {
		t.Fatalf("adopted run: %+v", coverage)
	}
}
