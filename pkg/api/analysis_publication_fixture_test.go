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
