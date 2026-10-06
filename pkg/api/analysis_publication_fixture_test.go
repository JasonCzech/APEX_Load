package api

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/storage"
)

// A fixture enters the same durable publication protocol as a real import.
// The optional callback finishes its synchronous artifact writes before sealing;
// it does not grant production cleanup or any unknown writer closure authority.
func publishAnalysisAPIFixture(t *testing.T, store storage.AnalysisImportStore, in *storage.AnalysisImport, prepare func(*storage.AnalysisImport, *storage.AnalysisAttempt)) *storage.AnalysisImport {
	t.Helper()
	in.State = "mapped"
	in.Mapping = &analysis.ParquetMapping{Version: 1, Timestamp: analysis.ParquetColumnPath{"time"}, TimestampEncoding: "unix_s", Timezone: "UTC", Measures: []analysis.ParquetMeasure{{Value: analysis.ParquetColumnPath{"value"}, Name: "value", Kind: analysis.KindGauge, Unit: "ms"}}}
	var err error
	in, err = store.SaveAnalysisImport(*in, in.Revision)
	if err != nil {
		t.Fatal(err)
	}
	attempts := store.(storage.AnalysisAttemptStore)
	ctx := context.Background()
	in, attempt, err := attempts.ClaimAnalysisImportAttempt(ctx, in.ID, in.ProjectID, in.Revision, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	in.Files = append([]storage.AnalysisImportFile(nil), in.Files...)
	for i := range in.Files {
		in.Files[i].SHA256 = strings.Repeat("a", 64)
	}
	in.NormalizedSHA256 = map[string]string{}
	for _, key := range in.Normalized {
		in.NormalizedSHA256[key] = strings.Repeat("b", 64)
	}
	in.RejectionArtifacts = nil
	in.RejectionSHA256 = nil
	if prepare != nil {
		prepare(in, attempt)
	}
	addAnalysisPublicationFixtureManifest(t, in)
	digest, err := storage.AnalysisAttemptResultDigest(*in)
	if err != nil {
		t.Fatal(err)
	}
	if err = attempts.ConfirmAnalysisAttemptWritersQuiesced(ctx, in.ID, in.ProjectID, attempt.ID); err != nil {
		t.Fatal(err)
	}
	ready, err := attempts.PublishAnalysisImportAttempt(ctx, storage.AnalysisAttemptPublication{Import: *in, AttemptID: attempt.ID, Digest: digest})
	if err != nil {
		t.Fatal(err)
	}
	return ready
}

// Metadata-only publication fixtures do not claim a native physical producer.
// Their explicit synthetic row/size proof lets authority tests enter the real
// required publication transaction without weakening production admission.
func addAnalysisPublicationFixtureManifest(t *testing.T, in *storage.AnalysisImport) {
	t.Helper()
	if in.PartitionManifestVersion == 0 {
		return
	}
	if in.Mapping == nil || len(in.Mapping.Measures) == 0 {
		t.Fatal("fixture mapping unavailable")
	}
	measure := in.Mapping.Measures[0]
	unit, _, err := analysis.NormalizeUnit(measure.Unit)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Unix(1700000000, 0).UTC()
	parts := make([]analysis.NormalizedPartition, 0, len(in.Normalized))
	for i, key := range in.Normalized {
		stats := analysis.NormalizedPartitionStats{Version: 1, SchemaVersion: analysis.SchemaVersion, IdentityVersion: in.Mapping.Version, Kind: measure.Kind, Unit: unit}
		if i == 0 && in.Accepted > 0 {
			stats.Rows, stats.Valid = in.Accepted, in.Accepted
			stats.MinTime, stats.MaxTime = &at, &at
		}
		parts = append(parts, analysis.NormalizedPartition{ObjectKey: key, SHA256: in.NormalizedSHA256[key], Bytes: 12, Stats: stats})
	}
	in.PartitionManifest, err = analysis.NewNormalizedPartitionManifest(in.ProjectID, in.ID, *in.Mapping, parts)
	if err != nil {
		t.Fatal(err)
	}
}
