package storage

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/analysis"
)

func nativeAttemptMappedFixture(t *testing.T, s *SQLiteStore) *AnalysisImport {
	t.Helper()
	in := AnalysisImport{ID: uuid.NewString(), ProjectID: "monitor-p", Name: "native fixture", State: "uploading", Files: []AnalysisImportFile{{ID: "file", RelativePath: "fixture.parquet", Size: 100, Uploaded: 100}}}
	saved, err := s.SaveAnalysisImport(in, 0)
	if err != nil {
		t.Fatal(err)
	}
	saved.Mapping = &analysis.ParquetMapping{Version: 1, Timestamp: analysis.ParquetColumnPath{"time"}, TimestampEncoding: "unix_s", Timezone: "UTC", Measures: []analysis.ParquetMeasure{{Value: analysis.ParquetColumnPath{"value"}, Name: "value", Kind: analysis.KindGauge, Unit: "ms"}}}
	saved.State = "mapped"
	saved, err = s.SaveAnalysisImport(*saved, saved.Revision)
	if err != nil {
		t.Fatal(err)
	}
	return saved
}
func nativeAttemptPublicationFixture(t *testing.T, in *AnalysisImport, a *AnalysisAttempt) AnalysisAttemptPublication {
	t.Helper()
	value := *in
	value.Files = append([]AnalysisImportFile{}, in.Files...)
	for i := range value.Files {
		value.Files[i].SHA256 = strings.Repeat("a", 64)
	}
	value.Normalized = append([]string{}, a.Normalized...)
	value.NormalizedSHA256 = map[string]string{}
	for _, key := range a.Normalized {
		value.NormalizedSHA256[key] = strings.Repeat("b", 64)
	}
	value.RejectionArtifacts = nil
	value.RejectionSHA256 = nil
	value.Accepted = 3
	digest, err := AnalysisAttemptResultDigest(value)
	if err != nil {
		t.Fatal(err)
	}
	return AnalysisAttemptPublication{Import: value, AttemptID: a.ID, Digest: digest}
}
func TestAnalysisNativeAttemptsSQLiteTwoStoresCASAndReconciliation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "native.db")
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
	exerciseAnalysisNativeAttemptAuthority(t, first, second)
}

// The PostgreSQL integration test invokes this exact suite with independently
// opened real stores; mocks and SQLite success do not qualify PostgreSQL locks.
func exerciseAnalysisNativeAttemptAuthority(t *testing.T, first, second *SQLiteStore) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	in := nativeAttemptMappedFixture(t, first)
	var workers sync.WaitGroup
	workers.Add(2)
	type claim struct {
		in      *AnalysisImport
		attempt *AnalysisAttempt
		err     error
	}
	results := make(chan claim, 2)
	for _, store := range []*SQLiteStore{first, second} {
		go func(s *SQLiteStore) {
			defer workers.Done()
			value, attempt, err := s.ClaimAnalysisImportAttempt(ctx, in.ID, in.ProjectID, in.Revision, time.Minute)
			results <- claim{value, attempt, err}
		}(store)
	}
	workers.Wait()
	close(results)
	var winner claim
	successes := 0
	for result := range results {
		if result.err == nil {
			successes++
			winner = result
		} else if !errors.Is(result.err, ErrAnalysisAuthority) {
			t.Fatal("unexpected claim error", result.err)
		}
	}
	if successes != 1 {
		t.Fatal("concurrent claims did not select one owner", successes)
	}
	forged := *winner.in
	forged.State = "ready"
	forged.Owner = ""
	forged.LeaseExpiresAt = time.Time{}
	if _, err := first.SaveAnalysisImport(forged, winner.in.Revision); err == nil {
		t.Fatal("generic publication bypass")
	}
	publication := nativeAttemptPublicationFixture(t, winner.in, winner.attempt)
	if err := first.ConfirmAnalysisAttemptWritersQuiesced(ctx, in.ID, in.ProjectID, winner.attempt.ID); err != nil {
		t.Fatal(err)
	}
	ready, err := first.PublishAnalysisImportAttempt(ctx, publication)
	if err != nil {
		t.Fatal(err)
	}
	if ready.State != "ready" || ready.Owner != "" {
		t.Fatal("publication did not clear active ownership")
	}
	ready.RunID = "monitor-run"
	ready, err = second.SaveAnalysisImport(*ready, ready.Revision)
	if err != nil {
		t.Fatal("historical/current ready association", err)
	}
	replay, err := second.PublishAnalysisImportAttempt(ctx, publication)
	if err != nil || replay.Revision >= ready.Revision {
		t.Fatal("matching lost-response publication did not reconcile original receipt", err)
	}
	resolution, err := second.ResolveAnalysisAttemptPublication(ctx, in.ID, in.ProjectID, winner.attempt.ID, publication.Digest)
	if err != nil || !resolution.Published {
		t.Fatal("published artifacts could be blindly cleaned", err)
	}
	changed := publication
	changed.Import.NormalizedSHA256 = map[string]string{}
	for key := range publication.Import.NormalizedSHA256 {
		changed.Import.NormalizedSHA256[key] = strings.Repeat("c", 64)
	}
	changed.Digest, _ = AnalysisAttemptResultDigest(changed.Import)
	if _, err = second.PublishAnalysisImportAttempt(ctx, changed); !errors.Is(err, ErrAnalysisAuthority) {
		t.Fatal("changed publication replay accepted", err)
	}
	if err = second.FailAnalysisImportAttempt(ctx, in.ID, in.ProjectID, winner.attempt.ID, winner.in.Revision); !errors.Is(err, ErrAnalysisAuthority) {
		t.Fatal("late failure changed ready data", err)
	}
	current, err := first.GetAnalysisImport(in.ID)
	if err != nil || current.State != "ready" || current.RunID != "monitor-run" {
		t.Fatal("receipt replay lost ready association", err)
	}
}
func TestAnalysisNativePublicationSQLFencesExpiryAfterEarlierClockCheck(t *testing.T) {
	s, _, _ := monitorFixture(t)
	exerciseAnalysisNativePublicationSQLFence(t, s)
}
func exerciseAnalysisNativePublicationSQLFence(t *testing.T, s *SQLiteStore) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	mapped := nativeAttemptMappedFixture(t, s)
	in, a, err := s.ClaimAnalysisImportAttempt(ctx, mapped.ID, mapped.ProjectID, mapped.Revision, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	publication := nativeAttemptPublicationFixture(t, in, a)
	committed := publication.Import
	committed.State = "ready"
	committed.Owner = ""
	committed.Revision++
	committed.LeaseExpiresAt = time.Time{}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = lockProjectForOwnedMutation(tx, in.ProjectID); err != nil {
		t.Fatal(err)
	}
	before, err := preciseDatabaseTimeContext(ctx, tx, tx.dialect)
	if err != nil {
		t.Fatal(err)
	}
	expiry := before.Add(-time.Millisecond)
	if _, err = tx.ExecContext(ctx, `UPDATE analysis_imports SET lease_expires_at=? WHERE id=?`, analysisAttemptLeaseValue(tx, expiry), in.ID); err != nil {
		t.Fatal(err)
	}
	// Models delay after the pre-write check/serialization. The actual conditional
	// write must re-evaluate database time and reject a now-expired lease.
	if err = analysisAttemptPublishUpdate(ctx, tx, &committed, in.Revision, in.Owner, a.AbsoluteExpiry); !errors.Is(err, ErrAnalysisAuthority) {
		t.Fatal("SQL write accepted expired current owner", err)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE analysis_imports SET lease_expires_at=? WHERE id=?`, analysisAttemptLeaseValue(tx, before.Add(time.Minute)), in.ID); err != nil {
		t.Fatal(err)
	}
	if err = analysisAttemptPublishUpdate(ctx, tx, &committed, in.Revision, in.Owner, before.Add(-time.Millisecond)); !errors.Is(err, ErrAnalysisAuthority) {
		t.Fatal("SQL write accepted expired absolute deadline", err)
	}
}
func TestAnalysisNativeAttemptCancellationAndReplacementCannotResurrect(t *testing.T) {
	s, _, _ := monitorFixture(t)
	ctx := context.Background()
	mapped := nativeAttemptMappedFixture(t, s)
	in, a, err := s.ClaimAnalysisImportAttempt(ctx, mapped.ID, mapped.ProjectID, mapped.Revision, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	publication := nativeAttemptPublicationFixture(t, in, a)
	cancelled := *in
	cancelled.State = "cancelled"
	cancelled.Owner = ""
	cancelled.LeaseExpiresAt = time.Time{}
	if _, err = s.SaveAnalysisImport(cancelled, in.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = s.PublishAnalysisImportAttempt(ctx, publication); !errors.Is(err, ErrAnalysisAuthority) {
		t.Fatal("cancellation resurrected by worker", err)
	}
	if err = s.FailAnalysisImportAttempt(ctx, in.ID, in.ProjectID, a.ID, in.Revision); !errors.Is(err, ErrAnalysisAuthority) {
		t.Fatal("late failure replaced cancellation", err)
	}
}
func TestAnalysisNativeAttemptCleanupDoesNotTreatObsoleteQueueAsAbandonment(t *testing.T) {
	s, _, _ := monitorFixture(t)
	ctx := context.Background()
	mapped := nativeAttemptMappedFixture(t, s)
	in, a, err := s.ClaimAnalysisImportAttempt(ctx, mapped.ID, mapped.ProjectID, mapped.Revision, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	// Simulates a retained legacy failed plan through the historical database,
	// without reopening a generic publication bypass.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	a.Obsolete = []string{analysisAttemptKey(in.ProjectID, in.ID, "normalized-legacy-part-0000-00.parquet")}
	if err = analysisAttemptJournalWrite(ctx, tx, a); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	plan, err := s.AnalysisAttemptCleanupPlan(ctx, AnalysisCleanupJob{Kind: "attempt", ProjectID: in.ProjectID, ResourceID: a.ID})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range plan.Keys {
		for _, current := range a.Normalized {
			if key == current {
				t.Fatal("obsolete queue deleted publishable plan")
			}
		}
	}
	if len(a.Obsolete) != 1 {
		t.Fatal("legacy plan lost")
	}
	if err = s.FailAnalysisImportAttempt(ctx, in.ID, in.ProjectID, a.ID, in.Revision); err != nil {
		t.Fatal(err)
	}
	plan, err = s.AnalysisAttemptCleanupPlan(ctx, AnalysisCleanupJob{Kind: "attempt", ProjectID: in.ProjectID, ResourceID: a.ID})
	if err != nil || !plan.Blocked {
		t.Fatal("failure/expiry substituted for physical quiescence", err)
	}
}

func TestAnalysisNativeCleanupObservesLatePriorOwnerSeal(t *testing.T) {
	s, _, _ := monitorFixture(t)
	ctx := context.Background()
	mapped := nativeAttemptMappedFixture(t, s)
	first, a, err := s.ClaimAnalysisImportAttempt(ctx, mapped.ID, mapped.ProjectID, mapped.Revision, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.FailAnalysisImportAttempt(ctx, first.ID, first.ProjectID, a.ID, first.Revision); err != nil {
		t.Fatal(err)
	}
	failed, err := s.GetAnalysisImport(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	second, b, err := s.ClaimAnalysisImportAttempt(ctx, first.ID, first.ProjectID, failed.Revision, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	job := AnalysisCleanupJob{Kind: "attempt", ProjectID: first.ProjectID, ResourceID: b.ID}
	before, err := s.AnalysisAttemptCleanupPlan(ctx, job)
	if err != nil || !before.Blocked || len(before.Keys) != 0 {
		t.Fatal("unsealed prior writer reclaimed", before, err)
	}
	if err = s.ConfirmAnalysisAttemptWritersQuiesced(ctx, first.ID, first.ProjectID, a.ID); err != nil {
		t.Fatal(err)
	}
	after, err := s.AnalysisAttemptCleanupPlan(ctx, job)
	if err != nil || after.Blocked || !reflect.DeepEqual(after.Keys, a.Normalized) {
		t.Fatal("late physical proof was not read through retained journal", after, err)
	}
	for _, key := range after.Keys {
		for _, protected := range second.Normalized {
			if key == protected {
				t.Fatal("current publishable output deleted")
			}
		}
	}
	cancelled := *second
	cancelled.State = "cancelled" // generic method centrally revokes hidden owner/lease.
	if _, err = s.SaveAnalysisImport(cancelled, second.Revision); err != nil {
		t.Fatal(err)
	}
	ready, err := s.AnalysisDatasetCleanupReady(ctx, first.ProjectID, first.ID)
	if err != nil || ready {
		t.Fatal("cancelled current physical writer authorized namespace deletion", ready, err)
	}
	if err = s.ConfirmAnalysisAttemptWritersQuiesced(ctx, first.ID, first.ProjectID, b.ID); err != nil {
		t.Fatal(err)
	}
	ready, err = s.AnalysisDatasetCleanupReady(ctx, first.ProjectID, first.ID)
	if err != nil || !ready {
		t.Fatal("physical seals did not release cancelled namespace", ready, err)
	}
}
func TestAnalysisNativeCleanupRejectsForeignOrOriginalKeys(t *testing.T) {
	s, _, _ := monitorFixture(t)
	ctx := context.Background()
	mapped := nativeAttemptMappedFixture(t, s)
	in, a, err := s.ClaimAnalysisImportAttempt(ctx, mapped.ID, mapped.ProjectID, mapped.Revision, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{analysisAttemptKey(in.ProjectID, in.ID, "original-file.parquet"), analysisAttemptKey("foreign", in.ID, "normalized-legacy.parquet"), analysisAttemptKey(in.ProjectID, in.ID, "normalized-../original.parquet")} {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		a.Obsolete = []string{key}
		a.ObsoleteQuiesced = map[string]bool{key: true}
		if err = analysisAttemptJournalWrite(ctx, tx, a); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
		if _, err = s.AnalysisAttemptCleanupPlan(ctx, AnalysisCleanupJob{Kind: "attempt", ProjectID: in.ProjectID, ResourceID: a.ID}); !errors.Is(err, ErrAnalysisAuthority) {
			t.Fatal("foreign/original key accepted", key, err)
		}
	}
}
func TestAnalysisNativeJournalInsertCannotReplaceOwnership(t *testing.T) {
	s, _, _ := monitorFixture(t)
	ctx := context.Background()
	mapped := nativeAttemptMappedFixture(t, s)
	in, a, err := s.ClaimAnalysisImportAttempt(ctx, mapped.ID, mapped.ProjectID, mapped.Revision, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	alias := *a
	alias.ImportID = uuid.NewString()
	if err = analysisAttemptJournalInsert(ctx, tx, &alias); !errors.Is(err, ErrAnalysisAuthority) {
		t.Fatal("journal UUID collision replaced ownership", err)
	}
	current, err := analysisAttemptJournalLoad(ctx, tx, a.ID, in.ProjectID)
	if err != nil || current.ImportID != in.ID {
		t.Fatal("collision changed exact journal", err)
	}
}

func TestAnalysisNativeLegacyEmptyWriterFenceSurvivesCancellationAndProjectDeletion(t *testing.T) {
	for _, operation := range []string{"cancel", "delete-project"} {
		t.Run(operation, func(t *testing.T) {
			s, _, _ := monitorFixture(t)
			ctx := context.Background()
			mapped := nativeAttemptMappedFixture(t, s)
			// Historical controller metadata before adoption: an active normalizer can
			// exist before it persists its first output. There is deliberately no journal.
			legacy := *mapped
			legacy.State = "normalizing"
			body, err := json.Marshal(legacy)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.db.Exec(`UPDATE analysis_imports SET payload=?,state='normalizing',owner='',lease_expires_at=? WHERE id=?`, string(body), time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano), legacy.ID); err != nil {
				t.Fatal(err)
			}
			if operation == "cancel" {
				legacy.State = "cancelled"
				if _, err = s.SaveAnalysisImport(legacy, legacy.Revision); err != nil {
					t.Fatal(err)
				}
			} else {
				if err = s.DeleteProject(legacy.ProjectID); err != nil {
					t.Fatal(err)
				}
			}
			ready, err := s.AnalysisDatasetCleanupReady(ctx, legacy.ProjectID, legacy.ID)
			if err != nil || ready {
				t.Fatal("legacy empty writer vanished during ownership revocation", ready, err)
			}
			var sentinelID, payload string
			if err = s.db.QueryRow(`SELECT id,payload FROM analysis_attempt_journal WHERE project_id=? AND import_id=?`, legacy.ProjectID, legacy.ID).Scan(&sentinelID, &payload); err != nil {
				t.Fatal(err)
			}
			var sentinel AnalysisAttempt
			if json.Unmarshal([]byte(payload), &sentinel) != nil || !sentinel.LegacyWriterUnknown || sentinel.WritersQuiesced || !sentinel.CleanupQueued {
				t.Fatal("legacy ownership not retained", payload)
			}
			if err = s.ConfirmAnalysisAttemptWritersQuiesced(ctx, legacy.ID, legacy.ProjectID, sentinelID); !errors.Is(err, ErrAnalysisAuthority) {
				t.Fatal("ordinary worker call fabricated legacy physical proof", err)
			}
		})
	}
}
