package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestIntervalDistributionFromNativeNamesItsDistribution(t *testing.T) {
	durations := []float64{10, 20, 30}
	digest, sketch := boundaryDistributions(durations)
	_, otherSketch := boundaryDistributions([]float64{5})
	for _, tc := range []struct {
		name          string
		metric        storage.NativeIntervalMetric
		quality       string
		digest, keeps bool
	}{
		{"both", storage.NativeIntervalMetric{Count: 3, DurationDigest: digest, DurationSketch: sketch}, intervalPercentileNativeSketch, true, true},
		{"recorded before the sketch", storage.NativeIntervalMetric{Count: 3, DurationDigest: digest}, intervalPercentileNativeDigest, true, false},
		{"a sketch of other durations", storage.NativeIntervalMetric{Count: 3, DurationDigest: digest, DurationSketch: otherSketch}, intervalPercentileNativeDigest, true, false},
		{"scalars only", storage.NativeIntervalMetric{Count: 3}, intervalPercentileNativeScalar, false, false},
	} {
		got := intervalDistributionFromNative(tc.metric)
		if got.PercentileQuality != tc.quality || (got.DurationDigest != nil) != tc.digest || (got.DurationSketch != nil) != tc.keeps {
			t.Fatalf("%s: quality %q, digest %v, sketch %v", tc.name, got.PercentileQuality, got.DurationDigest != nil, got.DurationSketch != nil)
		}
		if tc.keeps && !reflect.DeepEqual(got.DurationSketch, tc.metric.DurationSketch) {
			t.Fatalf("%s: sketch %+v, want %+v", tc.name, got.DurationSketch, tc.metric.DurationSketch)
		}
	}
}

func TestJointIntervalRowsCarryTheSketch(t *testing.T) {
	digest, sketch := boundaryDistributions([]float64{10, 20, 30})
	row := jointIntervalRow(JointIntervalRecord{SchemaVersion: jointIntervalSchemaVersion, Availability: "measured"}, storage.NativeIntervalMetric{Count: 3, DurationDigest: digest, DurationSketch: sketch})
	if !reflect.DeepEqual(row.DurationSketch, sketch) || row.DurationSketch == sketch {
		t.Fatalf("row sketch = %+v, want an independent copy of %+v", row.DurationSketch, sketch)
	}
	var parquetSketch engine.DurationSketchSnapshot
	if err := json.Unmarshal([]byte(toParquetJointInterval(row).DurationSketchJSON), &parquetSketch); err != nil || !reflect.DeepEqual(&parquetSketch, sketch) {
		t.Fatalf("parquet sketch = %+v (%v), want %+v", parquetSketch, err, sketch)
	}
	if empty := toParquetJointInterval(JointIntervalRecord{}); empty.DurationSketchJSON != "" {
		t.Fatalf("a row without a sketch wrote %q", empty.DurationSketchJSON)
	}
}

func TestLoadLevelDurationSketchesKeepOnlyCoveringSketches(t *testing.T) {
	_, three := boundaryDistributions([]float64{10, 20, 30})
	_, two := boundaryDistributions([]float64{40, 50})
	got := loadLevelDurationSketches(map[string]*GroupLiveMetrics{
		"covered":   {Transactions: 2, Errors: 1, DurationSketch: three},
		"other":     {Transactions: 3, DurationSketch: two},
		"no sketch": {Transactions: 1},
		"empty":     nil,
	})
	if len(got) != 2 || !reflect.DeepEqual(got["covered"], three) {
		t.Fatalf("load-level sketches = %+v", got)
	}
	if total := got["__total__"]; total == nil || total.Count != 3 {
		t.Fatalf("total = %+v, want the covered group's three durations", total)
	}
	if loadLevelDurationSketches(map[string]*GroupLiveMetrics{"no sketch": {Transactions: 1}}) != nil {
		t.Fatal("groups without sketches produced load-level sketches")
	}
}

func TestMergedGeneratorArchiveQualityNamesTheDistribution(t *testing.T) {
	base := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	durations := timeoutBoundaryDurations(9, 2_000, 10)
	merge := func(t *testing.T, sketched ...bool) IntervalSummaryRecord {
		t.Helper()
		root := t.TempDir()
		var bundles []remoteArchiveBundle
		for g, withSketch := range sketched {
			part := durations[g*len(durations)/len(sketched) : (g+1)*len(durations)/len(sketched)]
			digest, sketch := boundaryDistributions(part)
			if !withSketch {
				sketch = nil
			}
			path := filepath.Join(root, string(rune('a'+g))+".jsonl")
			writeGeneratorSummary(t, path, IntervalSummaryRecord{Timestamp: base.Add(time.Duration(g) * time.Millisecond), TestID: "quality", TotalTx: int64(len(part)), DurationDigest: digest}, sketch)
			bundles = append(bundles, remoteArchiveBundle{target: string(rune('a' + g)), summariesPath: path, manifest: RunArchiveManifest{IntervalSummaryCount: 1}})
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
		return merged
	}
	if got := merge(t, true, true); got.PercentileQuality != archivePercentileQualityMergedSketch || got.P99Ms <= 0 {
		t.Fatalf("generators with sketches: quality %q, p99 %v", got.PercentileQuality, got.P99Ms)
	}
	if got := merge(t, true, false); got.PercentileQuality != archivePercentileQualityMergedDigest || got.P99Ms <= 0 {
		t.Fatalf("a generator that predates the sketch: quality %q, p99 %v", got.PercentileQuality, got.P99Ms)
	}
}

// A bucket mixing evidence recorded before the sketch with newer evidence
// merges the t-digests both carry.
func TestMergedNativeLiveEvidenceFallsBackToTDigests(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	durations := timeoutBoundaryDurations(4, 2_000, 10)
	publication := func(at time.Duration, part []float64, withSketch bool) *LiveMetrics {
		digest, sketch := boundaryDistributions(part)
		if !withSketch {
			sketch = nil
		}
		return &LiveMetrics{Timestamp: start.Add(at), NativeEvidence: &storage.NativeIntervalEvidence{Version: 2, WindowSeconds: 1,
			Dimensions: map[string]map[string]storage.NativeIntervalMetric{"transaction": {"Checkout": {Count: int64(len(part)), Avg: meanMs(part), DurationDigest: digest, DurationSketch: sketch}}}}}
	}
	first, second := durations[:1_000], durations[1_000:]
	history := []*LiveMetrics{publication(time.Second, first, false), publication(2*time.Second, second, true)}
	q := storage.AnalyticsQuery{EvidenceMode: "live", LiveBucketMode: "merged", Population: "transaction", Metric: "response_time", Aggregation: "p99",
		BucketSeconds: 2, EndSeconds: 2, MaxSeries: 1, Filters: map[string]string{"action": "Checkout"}}
	out, err := buildMergedNativeLiveEvidence(t.Context(), start, history, q)
	if err != nil {
		t.Fatal(err)
	}
	firstDigest, _ := boundaryDistributions(first)
	secondDigest, _ := boundaryDistributions(second)
	want := mergedDigestP99(firstDigest, secondDigest)
	if point := out.Series[0].Points[0]; point.Value == nil || *point.Value != want {
		t.Fatalf("merged p99 = %v, want the t-digests' %v", point.Value, want)
	}
}
