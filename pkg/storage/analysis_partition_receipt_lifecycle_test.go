package storage

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

func TestAnalysisPartitionResultDigestDurableEmptyCollections(t *testing.T) {
	s, _, _ := monitorFixture(t)
	mapped := nativeAttemptMappedFixture(t, s)
	in, a, err := s.ClaimAnalysisImportAttempt(t.Context(), mapped.ID, mapped.ProjectID, mapped.Revision, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	nilValue := nativeAttemptPublicationFixture(t, in, a).Import
	emptyValue := nilValue
	emptyValue.RejectionArtifacts = []string{}
	emptyValue.RejectionSHA256 = map[string]string{}
	emptyValue.Rejections = []analysis.ParquetRejectionSummary{}
	nilDigest, err := AnalysisAttemptResultDigest(nilValue)
	if err != nil {
		t.Fatal(err)
	}
	emptyDigest, err := AnalysisAttemptResultDigest(emptyValue)
	if err != nil || nilDigest != emptyDigest {
		t.Fatal("v1 digest differs after durable omission of empty output fields", err)
	}
	// Historical owners keep their original exact null/[]/{} digest shapes.
	nilValue.PartitionManifestVersion, nilValue.PartitionManifest = 0, nil
	emptyValue.PartitionManifestVersion, emptyValue.PartitionManifest = 0, nil
	nilDigest, err = AnalysisAttemptResultDigest(nilValue)
	if err != nil {
		t.Fatal(err)
	}
	emptyDigest, err = AnalysisAttemptResultDigest(emptyValue)
	if err != nil || nilDigest == emptyDigest {
		t.Fatal("version0 historical empty output shape was canonicalized", err)
	}
}

type partitionReceiptLifecycleResult struct {
	importID, projectID, attemptID, digest string
	published                              *AnalysisImport
}

func TestAnalysisPartitionReceiptLifecycleSQLiteReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "partition-receipt.db")
	first, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	seedMonitorFixture(t, first)
	second, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	result := exerciseAnalysisPartitionReceiptLifecycle(t, first, second)
	exerciseAnalysisPartitionCancellationAndRetry(t, first, second)
	if err = second.Close(); err != nil {
		t.Fatal(err)
	}
	if err = first.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	requireAnalysisPartitionReceiptLifecycleReopen(t, reopened, result)
}

// Real stores exercise metadata and retained receipt authority only. These
// fixtures do not claim physical Parquet copying or OS writer quiescence.
func exerciseAnalysisPartitionReceiptLifecycle(t *testing.T, first, reader *SQLiteStore) partitionReceiptLifecycleResult {
	t.Helper()
	ctx := t.Context()
	mapped := nativeAttemptMappedFixture(t, first)
	in, a, err := first.ClaimAnalysisImportAttempt(ctx, mapped.ID, mapped.ProjectID, mapped.Revision, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	publication := nativeAttemptPublicationFixture(t, in, a)
	// Match actual no-rejection normalization output, not only nil test fixtures.
	publication.Import.RejectionArtifacts = []string{}
	publication.Import.RejectionSHA256 = map[string]string{}
	publication.Import.Rejections = []analysis.ParquetRejectionSummary{}
	publication.Digest, err = AnalysisAttemptResultDigest(publication.Import)
	if err != nil {
		t.Fatal(err)
	}
	if err = first.ConfirmAnalysisAttemptWritersQuiesced(ctx, in.ID, in.ProjectID, a.ID); err != nil {
		t.Fatal(err)
	}
	published, err := first.PublishAnalysisImportAttempt(ctx, publication)
	if err != nil {
		t.Fatal(err)
	}
	result := partitionReceiptLifecycleResult{in.ID, in.ProjectID, a.ID, publication.Digest, published}
	loaded, err := reader.GetAnalysisImport(in.ID)
	if err != nil || !analysisImportOwnedOutputsEqual(*loaded, *published) {
		t.Fatal("durable published output changed", err)
	}
	if loaded.RejectionArtifacts != nil || loaded.RejectionSHA256 != nil || loaded.Rejections != nil {
		t.Fatal("fixture failed to cross omitted-empty durable boundary")
	}
	resolution, err := reader.ResolveAnalysisAttemptPublication(ctx, in.ID, in.ProjectID, a.ID, publication.Digest)
	if err != nil || !resolution.Published || resolution.Import == nil || !analysisImportOwnedOutputsEqual(*resolution.Import, *published) {
		t.Fatal("exact publication receipt failed after durable empty-output round trip", err)
	}
	loaded.RunID = "monitor-run"
	loaded, err = first.SaveAnalysisImport(*loaded, loaded.Revision)
	if err != nil {
		t.Fatal("association metadata update failed", err)
	}
	if loaded.Revision != published.Revision+1 || loaded.RunID != "monitor-run" {
		t.Fatal("association revision not durable")
	}
	for _, state := range []string{"deleting", "deleting", "deleted", "deleted"} {
		assertAnalysisPartitionOutputMutationRefused(t, first, loaded)
		prior := *loaded
		loaded.State = state
		loaded, err = first.SaveAnalysisImport(*loaded, loaded.Revision)
		if err != nil {
			t.Fatal("partition publication terminal transition failed", state, err)
		}
		if _, err = reader.SaveAnalysisImport(prior, prior.Revision); !errors.Is(err, ErrAnalysisRevision) {
			t.Fatal("stale terminal metadata save succeeded", state, err)
		}
		observed, readErr := reader.GetAnalysisImport(in.ID)
		if readErr != nil || observed.State != state || observed.RunID != "monitor-run" || !analysisImportOwnedOutputsEqual(*observed, *published) {
			t.Fatal("terminal metadata erased published proof", state, readErr)
		}
		plan, planErr := reader.AnalysisAttemptCleanupPlan(ctx, AnalysisCleanupJob{Kind: "attempt", ProjectID: in.ProjectID, ResourceID: a.ID})
		if planErr != nil || plan.Blocked || !reflect.DeepEqual(plan.Keys, a.Normalized) {
			t.Fatal("sealed published cleanup plan lost exact output authority", plan, planErr)
		}
		ready, readyErr := reader.AnalysisDatasetCleanupReady(ctx, in.ProjectID, in.ID)
		if readyErr != nil || !ready {
			t.Fatal("terminal metadata lost sealed cleanup readiness", ready, readyErr)
		}
		resolution, err = reader.ResolveAnalysisAttemptPublication(ctx, in.ID, in.ProjectID, a.ID, publication.Digest)
		if err != nil || !resolution.Published || resolution.Import == nil || resolution.Import.State != "ready" || resolution.Import.Revision != published.Revision || resolution.Import.RunID != published.RunID || !analysisImportOwnedOutputsEqual(*resolution.Import, *published) {
			t.Fatal("terminal metadata rewrote immutable publication receipt", err)
		}
	}
	resurrection := *loaded
	resurrection.State = "ready"
	if _, err = first.SaveAnalysisImport(resurrection, resurrection.Revision); err == nil {
		t.Fatal("deleted dataset resurrected")
	}
	if _, err = reader.PublishAnalysisImportAttempt(ctx, publication); err != nil {
		t.Fatal("exact historical publication retry failed after deletion", err)
	}
	return result
}

func assertAnalysisPartitionOutputMutationRefused(t *testing.T, s *SQLiteStore, in *AnalysisImport) {
	t.Helper()
	for name, mutate := range map[string]func(*AnalysisImport){
		"erase":     func(v *AnalysisImport) { v.PartitionManifest = nil },
		"downgrade": func(v *AnalysisImport) { v.PartitionManifestVersion = 0 },
		"replace":   func(v *AnalysisImport) { v.PartitionManifest.Partitions[0].Bytes++ },
	} {
		changed := *in
		changed.PartitionManifest = in.PartitionManifest.Clone()
		mutate(&changed)
		if _, err := s.SaveAnalysisImport(changed, changed.Revision); err == nil {
			t.Fatal("generic metadata save changed published manifest", name)
		}
	}
}

func exerciseAnalysisPartitionCancellationAndRetry(t *testing.T, first, reader *SQLiteStore) {
	t.Helper()
	ctx := t.Context()
	mapped := nativeAttemptMappedFixture(t, first)
	in, a, err := first.ClaimAnalysisImportAttempt(ctx, mapped.ID, mapped.ProjectID, mapped.Revision, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	cancelled := *in
	cancelled.State = "cancelled"
	current, err := first.SaveAnalysisImport(cancelled, cancelled.Revision)
	if err != nil {
		t.Fatal("current partition attempt cancellation failed", err)
	}
	if current.PartitionManifestVersion != 1 || current.PartitionManifest != nil || current.Owner != "" {
		t.Fatal("cancel acquired or erased partition authority")
	}
	if _, err = reader.SaveAnalysisImport(cancelled, cancelled.Revision); !errors.Is(err, ErrAnalysisRevision) {
		t.Fatal("stale cancellation retry succeeded", err)
	}
	current, err = reader.SaveAnalysisImport(*current, current.Revision)
	if err != nil {
		t.Fatal("current cancellation retry failed", err)
	}
	if _, err = first.PublishAnalysisImportAttempt(ctx, nativeAttemptPublicationFixture(t, in, a)); !errors.Is(err, ErrAnalysisAuthority) {
		t.Fatal("cancelled attempt published", err)
	}
	ready, err := reader.AnalysisDatasetCleanupReady(ctx, in.ProjectID, in.ID)
	if err != nil || ready {
		t.Fatal("cancelled writer assumed physical closure", ready, err)
	}
	if err = first.ConfirmAnalysisAttemptWritersQuiesced(ctx, in.ID, in.ProjectID, a.ID); err != nil {
		t.Fatal(err)
	}
	ready, err = reader.AnalysisDatasetCleanupReady(ctx, in.ProjectID, in.ID)
	if err != nil || !ready {
		t.Fatal("cancelled sealed writer lost cleanup readiness", ready, err)
	}
	if _, _, err = reader.ClaimAnalysisImportAttempt(ctx, current.ID, current.ProjectID, current.Revision, time.Minute); !errors.Is(err, ErrAnalysisAuthority) {
		t.Fatal("cancelled import acquired new owner", err)
	}

	retry := nativeAttemptMappedFixture(t, first)
	failed, oldAttempt, err := first.ClaimAnalysisImportAttempt(ctx, retry.ID, retry.ProjectID, retry.Revision, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err = first.FailAnalysisImportAttempt(ctx, failed.ID, failed.ProjectID, oldAttempt.ID, failed.Revision, ""); err != nil {
		t.Fatal(err)
	}
	failed, err = reader.GetAnalysisImport(failed.ID)
	if err != nil || failed.State != "failed" {
		t.Fatal("failed attempt lost durable state", err)
	}
	failed.State = "mapped"
	remapped, err := first.SaveAnalysisImport(*failed, failed.Revision)
	if err != nil {
		t.Fatal("failed attempt could not remap for retry", err)
	}
	if err = first.ConfirmAnalysisAttemptWritersQuiesced(ctx, retry.ID, retry.ProjectID, oldAttempt.ID); err != nil {
		t.Fatal(err)
	}
	next, nextAttempt, err := reader.ClaimAnalysisImportAttempt(ctx, remapped.ID, remapped.ProjectID, remapped.Revision, time.Minute)
	if err != nil || nextAttempt.ID == oldAttempt.ID || next.PartitionManifest != nil || next.PartitionManifestVersion != 1 {
		t.Fatal("retry inherited old partition proof or owner", err)
	}
	publication := nativeAttemptPublicationFixture(t, next, nextAttempt)
	if err = reader.ConfirmAnalysisAttemptWritersQuiesced(ctx, next.ID, next.ProjectID, nextAttempt.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = reader.PublishAnalysisImportAttempt(ctx, publication); err != nil {
		t.Fatal("new retry owner failed publication", err)
	}
}

func requireAnalysisPartitionReceiptLifecycleReopen(t *testing.T, s *SQLiteStore, expected partitionReceiptLifecycleResult) {
	t.Helper()
	loaded, err := s.GetAnalysisImport(expected.importID)
	if err != nil || loaded.State != "deleted" || loaded.RunID != "monitor-run" || !analysisImportOwnedOutputsEqual(*loaded, *expected.published) {
		t.Fatal("reopen lost terminal manifest metadata", err)
	}
	resolution, err := s.ResolveAnalysisAttemptPublication(context.Background(), expected.importID, expected.projectID, expected.attemptID, expected.digest)
	if err != nil || !resolution.Published || resolution.Import == nil || resolution.Import.State != "ready" || resolution.Import.Revision != expected.published.Revision || !analysisImportOwnedOutputsEqual(*resolution.Import, *expected.published) {
		t.Fatal("reopen lost immutable publication receipt", err)
	}
}
