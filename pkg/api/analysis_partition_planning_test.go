package api

import (
	"context"
	"maps"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	analytics "github.com/jasonczech/apexload/pkg/analysis/duckdb"
	"github.com/jasonczech/apexload/pkg/storage"
)

func analysisPartitionPlanningFixture(t *testing.T) (*storage.AnalysisImport, analytics.SeriesQuery) {
	t.Helper()
	id := "00000000-0000-0000-0000-000000000007"
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	metric := "latency"
	mapping := &analysis.ParquetMapping{Version: 2, Timestamp: []string{"time"}, TimestampEncoding: "unix_s", Timezone: "UTC", Identity: &analysis.ParquetIdentityMapping{Source: []string{"source"}}, Measures: []analysis.ParquetMeasure{{Name: metric, Value: []string{"value"}, Kind: analysis.KindGauge, Unit: "ms"}}}
	// These object keys intentionally do not exist. Producer metadata must plan
	// selected geometry without Stat, Attributes, Read or native admission.
	parts := []analysis.NormalizedPartition{}
	keys := []string{}
	hashes := map[string]string{}
	for i, host := range []string{"left", "right"} {
		key := analysisObjectKey("p", id, "part-"+host+".parquet")
		hash := strings.Repeat(string(rune('a'+i)), 64)
		parts = append(parts, analysis.NormalizedPartition{ObjectKey: key, SHA256: hash, Bytes: int64(100 + i*100), Stats: analysis.NormalizedPartitionStats{Version: 1, SchemaVersion: analysis.SchemaVersion, IdentityVersion: 2, Kind: analysis.KindGauge, Unit: "ms", Rows: 1, Valid: 1, MinTime: &from, MaxTime: &from, Metric: &metric, Identity: &analysis.NormalizedPartitionIdentity{Source: "source", Host: host, Service: "svc", Dimensions: map[string]string{"region": host}}}})
		keys = append(keys, key)
		hashes[key] = hash
	}
	manifest, err := analysis.NewNormalizedPartitionManifest("p", id, *mapping, parts)
	if err != nil {
		t.Fatal(err)
	}
	in := &storage.AnalysisImport{ID: id, ProjectID: "p", State: "ready", Revision: 9, Accepted: 2, Mapping: mapping, Normalized: keys, NormalizedSHA256: hashes, PartitionManifestVersion: 1, PartitionManifest: manifest}
	query := analytics.SeriesQuery{ProjectID: "p", DatasetID: id, Metric: metric, IdentitySchemaVersion: 2, From: from, To: from.Add(time.Minute), BucketSeconds: 1, Aggregation: "avg"}
	return in, query
}

func TestNativePartitionPlansUseExactProducerProofBeforeAnyArtifactIO(t *testing.T) {
	in, q := analysisPartitionPlanningFixture(t)
	for _, tc := range []struct {
		name, host string
		expected   int
		bytes      int64
	}{
		{"all", "", 2, 300}, {"left", "left", 1, 100}, {"right", "right", 1, 200}, {"excluded", "unknown", 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			query := q
			query.Host = tc.host
			got, total, err := selectAnalysisDatasetParts(t.Context(), in, query)
			if err != nil || len(got) != tc.expected || total != tc.bytes {
				t.Fatalf("selection=%v bytes=%d err=%v", got, total, err)
			}
		})
	}
	missing := in.PartitionManifest.Clone()
	missing.Partitions[0].Stats.Valid = 0
	missing.Partitions[0].Stats.Missing = 1
	m, err := analysis.NewNormalizedPartitionManifest(in.ProjectID, in.ID, *in.Mapping, missing.Partitions)
	if err != nil {
		t.Fatal(err)
	}
	copy := *in
	copy.PartitionManifest = m
	q.Host = "left"
	got, _, err := selectAnalysisDatasetParts(t.Context(), &copy, q)
	if err != nil || len(got) != 1 {
		t.Fatal("all-missing physical rows were pruned", got, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := selectAnalysisDatasetParts(ctx, in, q); err == nil {
		t.Fatal("cancelled query planned")
	}
}

func TestNativePartitionSnapshotsRejectIncompleteInvalidOrReboundProof(t *testing.T) {
	original, _ := analysisPartitionPlanningFixture(t)
	for _, tc := range []struct {
		name   string
		change func(*storage.AnalysisImport)
	}{
		{"missing", func(in *storage.AnalysisImport) { in.PartitionManifest = nil }},
		{"unexpected", func(in *storage.AnalysisImport) { in.PartitionManifestVersion = 0 }},
		{"future", func(in *storage.AnalysisImport) { in.PartitionManifestVersion = 2 }},
		{"mapping", func(in *storage.AnalysisImport) { in.Mapping.Measures[0].Value = []string{"another"} }},
		{"accepted", func(in *storage.AnalysisImport) { in.Accepted++ }},
		{"hash", func(in *storage.AnalysisImport) { in.NormalizedSHA256[in.Normalized[0]] = strings.Repeat("c", 64) }},
		{"digest", func(in *storage.AnalysisImport) { in.PartitionManifest.Digest = strings.Repeat("d", 64) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, err := snapshotAnalysisQueryDataset(original)
			if err != nil {
				t.Fatal(err)
			}
			tc.change(in)
			if _, err := snapshotAnalysisQueryDataset(in); err == nil {
				t.Fatal("invalid proof accepted")
			}
		})
	}
	snapshot, err := snapshotAnalysisQueryDataset(original)
	if err != nil {
		t.Fatal(err)
	}
	original.Mapping.Measures[0].Value[0] = "edited"
	original.PartitionManifest.Partitions[0].Stats.Identity.Dimensions["region"] = "edited"
	if snapshot.Mapping.Measures[0].Value[0] != "value" || snapshot.PartitionManifest.Partitions[0].Stats.Identity.Dimensions["region"] != "left" {
		t.Fatal("manifest snapshot aliases callers")
	}
	if _, err := snapshotAnalysisQueryDataset(snapshot); err != nil {
		t.Fatal("second coherent snapshot lost complete mapping proof", err)
	}
}

func TestNativeComparisonPlanKeysContainCompleteEffectiveQuery(t *testing.T) {
	in, q := analysisPartitionPlanningFixture(t)
	q.Dimensions = map[string]string{"region": "east", "transaction": "order"}
	base, err := analysisNativeQueryIdentity(in, q)
	if err != nil {
		t.Fatal(err)
	}
	equivalent := q
	equivalent.Dimensions = map[string]string{"transaction": "order", "region": "east"}
	equivalent.From = q.From.In(time.FixedZone("same instant", 3600))
	equivalent.To = q.To.In(time.FixedZone("same instant", 3600))
	key, err := analysisNativeQueryIdentity(in, equivalent)
	if err != nil || key != base {
		t.Fatal("equivalent effective query differs", err)
	}
	for _, tc := range []struct {
		name   string
		change func(*analytics.SeriesQuery, *storage.AnalysisImport)
	}{
		{"source", func(q *analytics.SeriesQuery, _ *storage.AnalysisImport) { q.SourceID = "different" }},
		{"host", func(q *analytics.SeriesQuery, _ *storage.AnalysisImport) { q.Host = "different" }},
		{"service", func(q *analytics.SeriesQuery, _ *storage.AnalysisImport) { q.Service = "different" }},
		{"dimension", func(q *analytics.SeriesQuery, _ *storage.AnalysisImport) { q.Dimensions["region"] = "west" }},
		{"from", func(q *analytics.SeriesQuery, _ *storage.AnalysisImport) { q.From = q.From.Add(time.Microsecond) }},
		{"to", func(q *analytics.SeriesQuery, _ *storage.AnalysisImport) { q.To = q.To.Add(time.Microsecond) }},
		{"bucket", func(q *analytics.SeriesQuery, _ *storage.AnalysisImport) { q.BucketSeconds = 2 }},
		{"aggregation", func(q *analytics.SeriesQuery, _ *storage.AnalysisImport) { q.Aggregation = "max" }},
		{"revision", func(_ *analytics.SeriesQuery, in *storage.AnalysisImport) { in.Revision++ }},
		{"manifest", func(_ *analytics.SeriesQuery, in *storage.AnalysisImport) {
			in.PartitionManifest = in.PartitionManifest.Clone()
			in.PartitionManifest.Digest = strings.Repeat("b", 64)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copy := *in
			query := q
			query.Dimensions = maps.Clone(q.Dimensions)
			tc.change(&query, &copy)
			got, err := analysisNativeQueryIdentity(&copy, query)
			if err != nil {
				t.Fatal(err)
			}
			if got == base {
				t.Fatal("distinct query reused reservation")
			}
		})
	}
}

func TestNativeArtifactSelectionReportsOnlyVerifiedArtifactGeometry(t *testing.T) {
	in, q := analysisPartitionPlanningFixture(t)
	q.Host = "left"
	parts, bytes, err := selectAnalysisDatasetParts(t.Context(), in, q)
	if err != nil {
		t.Fatal(err)
	}
	ctx := withAnalysisArtifactSelections(t.Context())
	if err := recordAnalysisArtifactSelection(ctx, in, parts, bytes); err != nil {
		t.Fatal(err)
	}
	values := completedAnalysisArtifactSelections(ctx)
	if len(values) != 1 || values[0].Mode != "producer_manifest" || values[0].ManifestDigest != in.PartitionManifest.Digest || values[0].TotalObjects != 2 || values[0].SelectedObjects != 1 || values[0].PrunedObjects != 1 || values[0].TotalArtifactBytes != 300 || values[0].StagedArtifactBytes != 100 || values[0].AvoidedArtifactBytes != 200 {
		t.Fatal("artifact byte/count proof differs", values)
	}
	values[0].DatasetID = "changed"
	if completedAnalysisArtifactSelections(ctx)[0].DatasetID != in.ID {
		t.Fatal("selection response aliases retained report")
	}
	if err := recordAnalysisArtifactSelection(ctx, in, parts, bytes); err != nil {
		t.Fatal(err)
	}
	if err := recordAnalysisArtifactSelection(ctx, in, parts, bytes); err == nil {
		t.Fatal("third side report exceeded comparison admission")
	}
	legacy := *in
	legacy.PartitionManifestVersion = 0
	legacy.PartitionManifest = nil
	ctx = withAnalysisArtifactSelections(t.Context())
	if err := recordAnalysisArtifactSelection(ctx, &legacy, []analysisDatasetQueryPart{{size: 100}, {size: 200}}, 300); err != nil {
		t.Fatal(err)
	}
	value := completedAnalysisArtifactSelections(ctx)[0]
	if value.Mode != "legacy_full_scan" || value.AvoidedArtifactBytes != 0 || value.ManifestDigest != "" || value.TotalArtifactBytes != 300 {
		t.Fatal("legacy metadata invented avoided bytes", value)
	}
}

func TestNativePartitionEmptySelectionCannotBypassUnavailableBuild(t *testing.T) {
	if analytics.Available() {
		t.Skip("CGO-free contract is exercised by the default build gate")
	}
	in, q := analysisPartitionPlanningFixture(t)
	q.Host = "absent"
	if points, err := queryAnalysisDataset(t.Context(), in, q); err == nil || len(points) != 0 {
		t.Fatal("empty plan bypassed unavailable native capability", points, err)
	}
}
