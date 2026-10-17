package api

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Distributions a run persists (native interval evidence, archive load
// levels, generator archives, and intervals rebuilt from transaction rows)
// must merge without a mode-boundary error: with timeouts just under 1% of
// requests, the t-digest's interpolation reported values from the gap
// between the modes.

// boundaryDistributions returns the t-digest and the sketch of durations in
// milliseconds, as a source records them.
func boundaryDistributions(durations []float64) (*engine.TDigestSnapshot, *engine.DurationSketchSnapshot) {
	digest, sketch := engine.NewTDigest(200), engine.NewDurationSketch()
	for _, ms := range durations {
		digest.Add(ms * float64(time.Millisecond))
		sketch.Add(ms * float64(time.Millisecond))
	}
	digestSnapshot, sketchSnapshot := digest.Snapshot(), sketch.Snapshot()
	return &digestSnapshot, &sketchSnapshot
}

func meanMs(durations []float64) float64 {
	sum := 0.0
	for _, ms := range durations {
		sum += ms
	}
	return sum / float64(len(durations))
}

func TestMergedNativeLivePercentilesHaveNoModeBoundaryError(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	const n = 20_000
	for slow := 190; slow < 200; slow++ {
		durations := timeoutBoundaryDurations(int64(slow), n, slow)
		var history []*LiveMetrics
		for half := 0; half < 2; half++ {
			part := durations[half*n/2 : (half+1)*n/2]
			digest, sketch := boundaryDistributions(part)
			live := &LiveMetrics{Timestamp: start.Add(time.Duration(half+1) * time.Second), Transactions: map[string]*TxMetrics{
				"Checkout": {Name: "Checkout", Count: int64(len(part)), AvgMs: meanMs(part), DurationDigest: digest, DurationSketch: sketch},
			}}
			live.NativeEvidence = nativeIntervalEvidence(live, nil, false, 1)
			live.NativeEvidence.Version = 2
			history = append(history, live)
		}
		q := storage.AnalyticsQuery{EvidenceMode: "live", LiveBucketMode: "merged", Population: "transaction", Metric: "response_time", Aggregation: "p99",
			BucketSeconds: 2, EndSeconds: 2, MaxSeries: 1, Filters: map[string]string{"action": "Checkout"}}
		if err := storage.ValidateNativeEvidenceQuery(q); err != nil {
			t.Fatal(err)
		}
		out, err := buildMergedNativeLiveEvidence(context.Background(), start, history, q)
		if err != nil {
			t.Fatal(err)
		}
		if len(out.Series) != 1 || len(out.Series[0].Points) != 1 || out.Series[0].Points[0].Value == nil {
			t.Fatalf("merged live evidence = %+v", out.Series)
		}
		got := *out.Series[0].Points[0].Value
		if err := archiveBracketError(sortedGateSamples(durations), 0.99, got); err > 0 {
			t.Errorf("%d timeouts in %d requests: merged live p99 = %.1f ms, outside the sketch's band by %.3g", slow, n, got, err)
		}
	}
}

func TestArchiveLoadLevelPercentilesHaveNoModeBoundaryError(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	level := ActiveLoadLevel{Group: "checkout", StageIndex: 0, Name: "Level 1", TargetVUsers: 10, Duration: "20s", StartedAt: start}
	const n = 20_000
	for slow := 190; slow < 200; slow++ {
		t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
		writer, err := newRunArchiveWriter(&ManagedTest{ID: "load-level-boundary", Name: "Load level boundary"}, RunArchiveProvenance{})
		if err != nil {
			t.Fatal(err)
		}
		durations := timeoutBoundaryDurations(int64(slow), n, slow)
		for half := 0; half < 2; half++ {
			part := durations[half*n/2 : (half+1)*n/2]
			digest, sketch := boundaryDistributions(part)
			if err := writer.RecordIntervalSummary(&LiveMetrics{
				Timestamp: start.Add(time.Duration(half+1) * 10 * time.Second), ActiveRuntimeSeconds: float64(half+1) * 10, SampleWindowSeconds: 10,
				TotalTx: int64(len(part)), LoadLevels: []ActiveLoadLevel{level},
				GroupMetrics: map[string]*GroupLiveMetrics{"checkout": {Name: "checkout", Transactions: int64(len(part)), TPS: 1, DurationDigest: digest, DurationSketch: sketch}},
			}); err != nil {
				t.Fatal(err)
			}
		}
		record, err := writer.Finalize("COMPLETE", start.Add(time.Minute), "", false, false, nil)
		if err != nil {
			t.Fatal(err)
		}
		summaries, err := readAllArchiveSummaries(record)
		if err != nil {
			t.Fatal(err)
		}
		levels := buildRunArchiveLoadLevelSummaries(summaries)
		if len(levels) != 1 || levels[0].Requests != n {
			t.Fatalf("load levels = %+v", levels)
		}
		if err := archiveBracketError(sortedGateSamples(durations), 0.99, levels[0].P99Ms); err > 0 {
			t.Errorf("%d timeouts in %d requests: load level p99 = %.1f ms, outside the sketch's band by %.3g", slow, n, levels[0].P99Ms, err)
		}
	}
}

// writeGeneratorSummary writes one generator's interval summary row with both
// distributions, as a generator archive records them.
func writeGeneratorSummary(t *testing.T, path string, record IntervalSummaryRecord, sketch *engine.DurationSketchSnapshot) {
	t.Helper()
	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	var row map[string]any
	if err := json.Unmarshal(encoded, &row); err != nil {
		t.Fatal(err)
	}
	row["duration_sketch"] = sketch
	encoded, err = json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(encoded, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestMergedGeneratorArchivePercentilesHaveNoModeBoundaryError(t *testing.T) {
	base := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	const n = 20_000
	for slow := 190; slow < 200; slow++ {
		root := t.TempDir()
		durations := timeoutBoundaryDurations(int64(slow), n, slow)
		var bundles []remoteArchiveBundle
		for g, target := range []string{"a", "b"} {
			part := durations[g*n/2 : (g+1)*n/2]
			digest, sketch := boundaryDistributions(part)
			path := filepath.Join(root, target+".jsonl")
			writeGeneratorSummary(t, path, IntervalSummaryRecord{Timestamp: base.Add(time.Duration(g) * 20 * time.Millisecond), TestID: "boundary", TotalTx: int64(len(part)), DurationDigest: digest}, sketch)
			bundles = append(bundles, remoteArchiveBundle{target: target, summariesPath: path, manifest: RunArchiveManifest{IntervalSummaryCount: 1}})
		}
		destination := filepath.Join(root, "merged.jsonl")
		if _, err := mergeRemoteArchiveIntervalSummaries(destination, bundles); err != nil {
			t.Fatal(err)
		}
		payload, err := os.ReadFile(destination)
		if err != nil {
			t.Fatal(err)
		}
		var merged IntervalSummaryRecord
		if err := json.Unmarshal(payload, &merged); err != nil {
			t.Fatal(err)
		}
		if merged.TotalTx != n {
			t.Fatalf("merged %d requests, want %d", merged.TotalTx, n)
		}
		if err := archiveBracketError(sortedGateSamples(durations), 0.99, merged.P99Ms); err > 0 {
			t.Errorf("%d timeouts in %d requests: merged archive p99 = %.1f ms (%s), outside the sketch's band by %.3g", slow, n, merged.P99Ms, merged.PercentileQuality, err)
		}
	}
}

// writeRowArchive writes an archive whose transaction rows fall in one
// 60-second interval, with no stored distributions to copy.
func writeRowArchive(t *testing.T, end time.Time, durations []float64) *storage.RunArchiveRecord {
	t.Helper()
	dir := t.TempDir()
	manifest := RunArchiveManifest{SchemaVersion: archiveSchemaVersion, TestID: "rows", TransactionsFile: "transactions.jsonl", ResourceSamplesFile: "resources.jsonl",
		IntervalSummariesFile: "interval_summaries.jsonl", TransactionCount: int64(len(durations)), IntervalSummaryCount: 1}
	file, err := os.Create(filepath.Join(dir, manifest.TransactionsFile))
	if err != nil {
		t.Fatal(err)
	}
	encoder := json.NewEncoder(file)
	for i, duration := range durations {
		at := end.Add(-time.Duration(len(durations)-i) * time.Millisecond)
		if err := encoder.Encode(TransactionEvent{Timestamp: at, ActionName: "Checkout", BusinessTransaction: true, Success: true, DurationMs: duration}); err != nil {
			file.Close()
			t.Fatal(err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	summary, err := json.Marshal(IntervalSummaryRecord{Timestamp: end, TestID: "rows", SampleWindowSeconds: 60, TotalTx: int64(len(durations))})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, manifest.IntervalSummariesFile), append(summary, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, manifest.ResourceSamplesFile), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(manifestPath, manifestBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	return &storage.RunArchiveRecord{ArchivePath: dir, ManifestPath: manifestPath}
}

func TestRebuiltIntervalPercentilesHaveNoModeBoundaryError(t *testing.T) {
	end := time.Date(2026, 10, 1, 12, 1, 0, 0, time.UTC)
	const n = 20_000
	for slow := 190; slow < 200; slow++ {
		durations := timeoutBoundaryDurations(int64(slow), n, slow)
		var rebuilt []IntervalDistributionRecord
		if _, err := reconstructIntervalDistributions(context.Background(), writeRowArchive(t, end, durations), func(r IntervalDistributionRecord) error {
			rebuilt = append(rebuilt, r)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if len(rebuilt) != 1 || rebuilt[0].Dimensions["transaction"]["Checkout"].Count != n {
			t.Fatalf("rebuilt intervals = %+v", rebuilt)
		}
		got := rebuilt[0].Dimensions["transaction"]["Checkout"].P99
		if err := archiveBracketError(sortedGateSamples(durations), 0.99, got); err > 0 {
			t.Errorf("%d timeouts in %d requests: rebuilt interval p99 = %.1f ms, outside the sketch's band by %.3g", slow, n, got, err)
		}
	}
}
