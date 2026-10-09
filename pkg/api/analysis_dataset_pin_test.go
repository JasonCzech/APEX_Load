package api

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestDatasetArtifactPinsRefuseAChangedDataset(t *testing.T) {
	dataset := &storage.AnalysisImport{ID: "dataset", Normalized: []string{"b.parquet", "a.parquet"}, NormalizedSHA256: map[string]string{"a.parquet": strings.Repeat("1", 64), "b.parquet": strings.Repeat("2", 64)}}
	pin := analysisDatasetArtifactSHA256(dataset)
	if !analysisDatasetArtifactDigestValid(pin) {
		t.Fatalf("artifact digest %q", pin)
	}
	// The digest names the artifacts, not their listing order or the
	// dataset's run association.
	reordered := *dataset
	reordered.Normalized, reordered.RunID, reordered.Revision = []string{"a.parquet", "b.parquet"}, "run-1", 9
	if analysisDatasetArtifactSHA256(&reordered) != pin {
		t.Fatal("the digest depends on listing order or association")
	}
	if err := checkAnalysisDatasetPin(pin, dataset); err != nil {
		t.Fatalf("matching pin refused: %v", err)
	}
	if err := checkAnalysisDatasetPin("", dataset); err != nil {
		t.Fatalf("an unpinned selection was refused: %v", err)
	}
	recommitted := *dataset
	recommitted.NormalizedSHA256 = map[string]string{"a.parquet": strings.Repeat("3", 64), "b.parquet": strings.Repeat("2", 64)}
	err := checkAnalysisDatasetPin(pin, &recommitted)
	if !errors.Is(err, errAnalysisDatasetChanged) || !strings.Contains(err.Error(), pin[:12]) {
		t.Fatalf("changed dataset = %v", err)
	}
}

func TestDatasetArtifactPinsAreValidated(t *testing.T) {
	from := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	request := AnalysisComparisonRequest{From: from, To: from.Add(time.Minute), BucketSeconds: 10, Mode: "overlay",
		Left:  AnalysisSeriesSelection{RunID: "run", Metric: "response_time", Aggregation: "p95"},
		Right: AnalysisSeriesSelection{RunID: "other", Metric: "response_time", Aggregation: "p95", DatasetArtifactSHA256: strings.Repeat("a", 64)}}
	if err := request.validate(); err == nil || !strings.Contains(err.Error(), "dataset_artifact_sha256") {
		t.Fatalf("a pin on a run selection = %v", err)
	}
	q := nativeEvidenceQuery("archive")
	q.OverlaySelection = &storage.NativeOverlaySelection{DatasetID: "dataset", Metric: "latency", Aggregation: "avg", DatasetArtifactSHA256: "NOT-A-DIGEST"}
	if err := storage.ValidateNativeEvidenceQuery(q); err == nil || !strings.Contains(err.Error(), "dataset_artifact_sha256") {
		t.Fatalf("malformed overlay pin = %v", err)
	}
	q.OverlaySelection.DatasetArtifactSHA256 = strings.Repeat("b", 64)
	if err := storage.ValidateNativeEvidenceQuery(q); err != nil {
		t.Fatalf("well-formed overlay pin = %v", err)
	}
}
