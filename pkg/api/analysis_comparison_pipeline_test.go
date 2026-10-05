package api

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestAnalysisComparisonChargesBothStagingSidesAndLargestSequentialChild(t *testing.T) {
	for _, tc := range []struct {
		name           string
		sides          []int64
		input, staging int64
		bad            bool
	}{
		{name: "two", sides: []int64{100, 200}, input: 200, staging: 300},
		{name: "same-source-two-copies", sides: []int64{100, 100}, input: 100, staging: 200},
		{name: "one-native-side", sides: []int64{100}, input: 100, staging: 100},
		{name: "empty-selected", sides: []int64{0, 0}, input: 12, staging: 0},
		{name: "exact", sides: []int64{analysisImportMaxBytes - 12, 12}, input: analysisImportMaxBytes - 12, staging: analysisImportMaxBytes},
		{name: "above-aggregate", sides: []int64{analysisImportMaxBytes - 12, 13}, bad: true},
		{name: "negative", sides: []int64{-1}, bad: true},
		{name: "invalid-small", sides: []int64{11}, bad: true},
		{name: "zero-sides", bad: true},
		{name: "third-side", sides: []int64{12, 12, 12}, bad: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input, staging, err := analysisComparisonGeometry(tc.sides)
			if tc.bad {
				if err == nil {
					t.Fatal("invalid comparison geometry accepted")
				}
				return
			}
			if err != nil || input != tc.input || staging != tc.staging {
				t.Fatal("comparison reservation geometry differs", input, staging, err)
			}
		})
	}
}
func TestAnalysisComparisonSnapshotsOnlyOwnedBoundedDatasetMetadata(t *testing.T) {
	id := "00000000-0000-0000-0000-000000000001"
	key := analysisObjectKey("proj-a", id, "part-0-0.parquet")
	in := &storage.AnalysisImport{ID: id, ProjectID: "proj-a", Revision: 7, State: "ready", RunID: "associated-run", Normalized: []string{key}, NormalizedSHA256: map[string]string{key: strings.Repeat("a", 64)}, Mapping: &analysis.ParquetMapping{Measures: []analysis.ParquetMeasure{{Name: "latency", Value: []string{"value"}}}}}
	snapshot, err := snapshotAnalysisQueryDataset(in)
	if err != nil {
		t.Fatal(err)
	}
	in.Normalized[0] = "outside"
	in.NormalizedSHA256[key] = strings.Repeat("b", 64)
	in.Mapping.Measures[0].Name = "edited"
	in.RunID = "edited-association"
	if snapshot.Normalized[0] != key || snapshot.NormalizedSHA256[key] != strings.Repeat("a", 64) || snapshot.Mapping.Measures[0].Name != "latency" || snapshot.RunID != "associated-run" || snapshot.Revision != 7 || snapshot.Mapping.Measures[0].Value != nil {
		t.Fatal("query metadata retained mutable or unnecessary caller fields", snapshot)
	}
	for _, change := range []func(*storage.AnalysisImport){
		func(in *storage.AnalysisImport) {
			in.Normalized[0] = analysisObjectKey("proj-b", id, "part-0-0.parquet")
		},
		func(in *storage.AnalysisImport) { in.NormalizedSHA256[key] = "missing" },
		func(in *storage.AnalysisImport) { in.NormalizedSHA256[key] = strings.Repeat("A", 64) },
		func(in *storage.AnalysisImport) { in.Normalized = make([]string, 1025) },
		func(in *storage.AnalysisImport) { in.Mapping.Measures = make([]analysis.ParquetMeasure, 65) },
	} {
		candidate, err := snapshotAnalysisQueryDataset(snapshot)
		if err != nil {
			t.Fatal(err)
		}
		change(candidate)
		if _, err := snapshotAnalysisQueryDataset(candidate); err == nil {
			t.Fatal("invalid ownership/digest/population accepted")
		}
	}
}

func TestAnalysisComparisonRejectsUnimplementedDatasetFilters(t *testing.T) {
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	base := AnalysisComparisonRequest{Left: AnalysisSeriesSelection{DatasetID: "dataset", Metric: "latency", Aggregation: "avg"}, Right: AnalysisSeriesSelection{RunID: "run", Metric: "latency", Aggregation: "avg"}, From: from, To: from.Add(time.Minute), BucketSeconds: 1, Mode: "overlay"}
	if err := base.validate(); err != nil {
		t.Fatal("metric-only comparison rejected", err)
	}
	for _, change := range []func(*AnalysisSeriesSelection){
		func(s *AnalysisSeriesSelection) { s.SourceID = "source" },
		func(s *AnalysisSeriesSelection) { s.Transaction = "transaction" },
		func(s *AnalysisSeriesSelection) { s.Protocol = "grpc" },
		func(s *AnalysisSeriesSelection) { s.Group = "group" },
		func(s *AnalysisSeriesSelection) { s.Host = "host" },
		func(s *AnalysisSeriesSelection) { s.Dimensions = map[string]string{"region": "east"} },
	} {
		candidate := base
		change(&candidate.Left)
		if err := candidate.validate(); err == nil {
			t.Fatal("requested native identity filter was silently ignored")
		}
		candidate = base
		change(&candidate.Right)
		if err := candidate.validate(); err != nil {
			t.Fatal("implemented managed-run filter rejected", err)
		}
	}
}
func TestAnalysisComparisonCancellationAndMissingScopeCannotServeRetainedPlan(t *testing.T) {
	server, _ := newTimeslotServer(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := server.verifyNativeAnalysisComparison(ctx, "proj-a"); err == nil {
		t.Fatal("cancelled comparison served result")
	}
	plan := &analysisComparisonPipeline{plans: map[analysisDatasetQueryIdentity]analysisDatasetQueryPlan{{"deleted", "latency"}: {dataset: &storage.AnalysisImport{ID: "deleted", ProjectID: "proj-a", State: "ready", Revision: 1}}}}
	ctx = context.WithValue(t.Context(), analysisComparisonPipelineKey{}, plan)
	if err := server.verifyNativeAnalysisComparison(ctx, "proj-a"); err == nil {
		t.Fatal("retained private plan served missing dataset")
	}
	if err := server.verifyNativeAnalysisComparison(ctx, "deleted-project"); err == nil {
		t.Fatal("retained private plan served missing project")
	}
}
