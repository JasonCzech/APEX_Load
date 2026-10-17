package generatorrpc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/engine"
)

// A generator archives its fleet sketch beside the t-digest, in the JSONL row
// and the Parquet copy, so merging generator archives can prefer it.
func TestGeneratorArchiveIntervalSummaryKeepsTheFleetSketch(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv(envGeneratorArchiveMinFree, "1")
	writer, err := newArchiveRecorder("sketch-generator", "sketch-run", "Sketch")
	if err != nil {
		t.Fatalf("newArchiveRecorder: %v", err)
	}
	generator := newMetricAccumulator("fleet")
	for _, ms := range []float64{10, 20, 30000} {
		generator.add(TransactionMetric{Name: "fleet", DurationMs: ms, Success: true})
	}
	fleet := generator.finalize(1)
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	(&Service{}).recordMetricsBatch("sketch-run", writer, &pb.MetricsBatch{TestId: "sketch-run", TimestampUnixMs: at.UnixMilli(), WindowMs: 1000, FleetMetrics: metricAggregateToProto(fleet)})
	record, err := writer.Finalize("COMPLETE", at, "")
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(record.ArchivePath, "interval_summaries.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var summary archiveIntervalSummary
	if err := json.Unmarshal(data, &summary); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(summary.DurationSketch, fleet.DurationSketch) || summary.DurationDigest == nil {
		t.Fatalf("archived sketch %+v (digest %v), want %+v", summary.DurationSketch, summary.DurationDigest != nil, fleet.DurationSketch)
	}
	if want := engine.DurationSketchForCount(fleet.DurationSketch, fleet.Count).Quantile(0.99) / float64(time.Millisecond); summary.P99Ms != want {
		t.Fatalf("archived p99 = %v, want the sketch's %v", summary.P99Ms, want)
	}
	var parquetSketch engine.DurationSketchSnapshot
	if err := json.Unmarshal([]byte(toParquetSummaries([]archiveIntervalSummary{summary})[0].DurationSketchJSON), &parquetSketch); err != nil || !reflect.DeepEqual(&parquetSketch, fleet.DurationSketch) {
		t.Fatalf("parquet sketch = %+v (%v), want %+v", parquetSketch, err, fleet.DurationSketch)
	}
}
