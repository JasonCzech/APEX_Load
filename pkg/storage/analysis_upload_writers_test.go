package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func uploadWriterFixture(t *testing.T, s *SQLiteStore) *AnalysisImport {
	t.Helper()
	in, err := s.SaveAnalysisImport(AnalysisImport{ID: uuid.NewString(), ProjectID: "monitor-p", Name: "owned upload", State: "uploading", UploadWriterVersion: AnalysisUploadWriterVersion, Files: []AnalysisImportFile{{ID: uuid.NewString(), RelativePath: "input.parquet", Size: 100}}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	return in
}
func uploadWriterRequest(in *AnalysisImport) AnalysisUploadRequest {
	return AnalysisUploadRequest{ProjectID: in.ProjectID, ImportID: in.ID, FileID: in.Files[0].ID, Revision: in.Revision, Offset: 0, Bytes: 100, SHA256: strings.Repeat("a", 64)}
}
func TestAnalysisUploadWriterTwoSQLiteStoresExactCursorAndPhysicalSeal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upload.db")
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
	exerciseAnalysisUploadWriterAuthority(t, first, second)
}

// The PostgreSQL lane invokes this same source suite using two actual stores.
func exerciseAnalysisUploadWriterAuthority(t *testing.T, first, second *SQLiteStore) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	in := uploadWriterFixture(t, first)
	req := uploadWriterRequest(in)
	type result struct {
		w   *AnalysisUploadWriter
		err error
	}
	out := make(chan result, 2)
	var joined sync.WaitGroup
	joined.Add(2)
	for _, store := range []*SQLiteStore{first, second} {
		go func(s *SQLiteStore) {
			defer joined.Done()
			w, err := s.BeginAnalysisUploadWriter(ctx, req)
			out <- result{w, err}
		}(store)
	}
	joined.Wait()
	close(out)
	var winner *AnalysisUploadWriter
	success := 0
	for got := range out {
		if got.err == nil {
			winner = got.w
			success++
		} else if !errors.Is(got.err, ErrAnalysisUploadInFlight) {
			t.Fatal("unexpected contender", got.err)
		}
	}
	if success != 1 {
		t.Fatal("physical cursor selected multiple writers", success)
	}
	if _, err := second.BeginAnalysisUploadWriter(ctx, req); !errors.Is(err, ErrAnalysisUploadInFlight) {
		t.Fatal("logical time allowed pending writer replacement", err)
	}
	forged := *in
	forged.Files = append([]AnalysisImportFile{}, in.Files...)
	forged.Files[0].Uploaded = 100
	if _, err := second.SaveAnalysisImport(forged, in.Revision); err == nil {
		t.Fatal("generic save bypassed physical cursor publication")
	}
	ready, err := first.AnalysisDatasetCleanupReady(ctx, in.ProjectID, in.ID)
	if err != nil || ready {
		t.Fatal("pending physical writer allowed whole namespace cleanup", ready, err)
	}
	closed, err := first.CompleteAnalysisUploadWriter(ctx, in.ProjectID, winner.ID, false)
	if err != nil || !closed.PhysicallyClosed || closed.State != "abandoned" {
		t.Fatal("failure did not seal exact writer", closed, err)
	}
	retry, err := second.BeginAnalysisUploadWriter(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if winner.ObjectKey == retry.ObjectKey || winner.TemporaryKey == retry.TemporaryKey {
		t.Fatal("replacement reused obsolete writer namespace")
	}
	plan, err := first.AnalysisUploadWriterCleanupPlan(ctx, AnalysisCleanupJob{Kind: "upload-writer", ProjectID: in.ProjectID, ResourceID: winner.ID})
	if err != nil || plan.Blocked || !reflect.DeepEqual(plan.Keys, []string{winner.TemporaryKey, winner.ObjectKey}) {
		t.Fatal("cleanup did not use exact obsolete plan", plan, err)
	}
	for _, key := range plan.Keys {
		if key == retry.ObjectKey || key == retry.TemporaryKey {
			t.Fatal("cleanup granted replacement bytes")
		}
	}
	accepted, err := second.CompleteAnalysisUploadWriter(ctx, in.ProjectID, retry.ID, true)
	if err != nil || accepted.State != "accepted" || !accepted.PhysicallyClosed {
		t.Fatal(accepted, err)
	}
	current, err := first.GetAnalysisImport(in.ID)
	if err != nil || current.Revision != accepted.AppliedRevision || current.Files[0].Uploaded != 100 || len(current.Files[0].Chunks) != 1 || current.Files[0].Chunks[0].ObjectKey != retry.ObjectKey {
		t.Fatal("seal/cursor publication diverged", current, err)
	}
	replay, err := first.CompleteAnalysisUploadWriter(ctx, in.ProjectID, retry.ID, false)
	if err != nil || !reflect.DeepEqual(accepted, replay) {
		t.Fatal("completion replay changed accepted receipt", replay, err)
	}
	plan, err = first.AnalysisUploadWriterCleanupPlan(ctx, AnalysisCleanupJob{Kind: "upload-writer", ProjectID: in.ProjectID, ResourceID: retry.ID})
	if err != nil || !plan.Blocked {
		t.Fatal("live accepted chunk granted cleanup", plan, err)
	}
	if _, err = first.ResolveAnalysisUploadWriter(ctx, "other-project", in.ID, retry.ID); !errors.Is(err, ErrAnalysisUploadNotCommitted) {
		t.Fatal("private journal crossed project authority", err)
	}
	key, err := AnalysisUploadChunkObjectKey(in.ProjectID, in.ID, current.Files[0], current.Files[0].Chunks[0])
	if err != nil || key != retry.ObjectKey {
		t.Fatal("committed key failed read authority", key, err)
	}
	// Physical closure alone cannot authorize deletion of a live dataset.
	// Revoking the live state is the separate namespace-removal boundary.
	ready, err = first.AnalysisDatasetCleanupReady(ctx, in.ProjectID, in.ID)
	if err != nil || ready {
		t.Fatal("accepted live cursor granted namespace cleanup", ready, err)
	}
	current.State = "cancelled"
	if _, err = second.SaveAnalysisImport(*current, current.Revision); err != nil {
		t.Fatal(err)
	}
	ready, err = first.AnalysisDatasetCleanupReady(ctx, in.ProjectID, in.ID)
	if err != nil || !ready {
		t.Fatal("closed revoked durable uploader failed namespace cleanup readiness", ready, err)
	}
}
func TestAnalysisUploadCancelAndProjectDeleteRetainExactUnjoinedIntent(t *testing.T) {
	for _, remove := range []string{"cancel", "project"} {
		t.Run(remove, func(t *testing.T) {
			s, _, _ := monitorFixture(t)
			in := uploadWriterFixture(t, s)
			w, err := s.BeginAnalysisUploadWriter(t.Context(), uploadWriterRequest(in))
			if err != nil {
				t.Fatal(err)
			}
			if remove == "cancel" {
				in.State = "cancelled"
				if _, err = s.SaveAnalysisImport(*in, in.Revision); err != nil {
					t.Fatal(err)
				}
			} else {
				if err = s.DeleteProject(in.ProjectID); err != nil {
					t.Fatal(err)
				}
			}
			retained, err := s.ResolveAnalysisUploadWriter(t.Context(), in.ProjectID, in.ID, w.ID)
			if err != nil || retained.PhysicallyClosed || retained.State != "abandoned" {
				t.Fatal("revocation invented physical seal", retained, err)
			}
			plan, err := s.AnalysisUploadWriterCleanupPlan(t.Context(), AnalysisCleanupJob{Kind: "upload-writer", ProjectID: in.ProjectID, ResourceID: w.ID})
			if err != nil || !plan.Blocked {
				t.Fatal("revocation reclaimed active writer", plan, err)
			}
			ready, err := s.AnalysisDatasetCleanupReady(t.Context(), in.ProjectID, in.ID)
			if err != nil || ready {
				t.Fatal("revocation allowed active namespace cleanup", ready, err)
			}
			sealed, err := s.CompleteAnalysisUploadWriter(t.Context(), in.ProjectID, w.ID, true)
			if err != nil || !sealed.PhysicallyClosed || sealed.State != "abandoned" {
				t.Fatal("late completion resurrected import", sealed, err)
			}
			plan, err = s.AnalysisUploadWriterCleanupPlan(t.Context(), AnalysisCleanupJob{Kind: "upload-writer", ProjectID: in.ProjectID, ResourceID: w.ID})
			if err != nil || plan.Blocked || len(plan.Keys) != 2 {
				t.Fatal("late physical seal did not release exact plan", plan, err)
			}
			if remove == "project" {
				if _, err = s.GetAnalysisImport(in.ID); !errors.Is(err, sql.ErrNoRows) {
					t.Fatal("deleted import reappeared", err)
				}
			}
		})
	}
}
func TestAnalysisUploadKnownMetadataLimitStillSealsAndQueuesExactOutput(t *testing.T) {
	s, _, _ := monitorFixture(t)
	in := uploadWriterFixture(t, s)
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	in.Error = strings.Repeat("x", (2<<20)-len(raw)-80)
	in, err = s.SaveAnalysisImport(*in, in.Revision)
	if err != nil {
		t.Fatal(err)
	}
	w, err := s.BeginAnalysisUploadWriter(t.Context(), uploadWriterRequest(in))
	if err != nil {
		t.Fatal(err)
	}
	closed, err := s.CompleteAnalysisUploadWriter(t.Context(), in.ProjectID, w.ID, true)
	if err != nil || !closed.PhysicallyClosed || closed.State != "abandoned" || closed.Rejection != "metadata_limit" {
		t.Fatal("known refusal retained unresolved writer", closed, err)
	}
	current, err := s.GetAnalysisImport(in.ID)
	if err != nil || current.Revision != in.Revision || current.Files[0].Uploaded != 0 {
		t.Fatal("limit refusal published partial cursor", current, err)
	}
	plan, err := s.AnalysisUploadWriterCleanupPlan(t.Context(), AnalysisCleanupJob{Kind: "upload-writer", ProjectID: in.ProjectID, ResourceID: w.ID})
	if err != nil || plan.Blocked || len(plan.Keys) != 2 {
		t.Fatal("metadata refusal lost cleanup plan", plan, err)
	}
}
func TestAnalysisUploadMalformedRetainedPlansAndKeysFailClosed(t *testing.T) {
	s, _, _ := monitorFixture(t)
	in := uploadWriterFixture(t, s)
	w, err := s.BeginAnalysisUploadWriter(t.Context(), uploadWriterRequest(in))
	if err != nil {
		t.Fatal(err)
	}
	w, err = s.CompleteAnalysisUploadWriter(t.Context(), in.ProjectID, w.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	w.TemporaryKey = analysisAttemptKey("another-project", in.ID, "upload-"+w.ID+".tmp")
	raw, _ := json.Marshal(w)
	if _, err = s.db.Exec(`UPDATE analysis_upload_writers SET payload=? WHERE id=?`, string(raw), w.ID); err != nil {
		t.Fatal(err)
	}
	plan, err := s.AnalysisUploadWriterCleanupPlan(t.Context(), AnalysisCleanupJob{Kind: "upload-writer", ProjectID: in.ProjectID, ResourceID: w.ID})
	if !errors.Is(err, ErrAnalysisAuthority) || !plan.Blocked || len(plan.Keys) != 0 {
		t.Fatal("malformed retained plan granted deletion", plan, err)
	}
	chunk := AnalysisChunk{Offset: 0, Size: 100, SHA256: strings.Repeat("a", 64), ObjectKey: "../../outside"}
	if _, err = AnalysisUploadChunkObjectKey(in.ProjectID, in.ID, in.Files[0], chunk); !errors.Is(err, ErrAnalysisAuthority) {
		t.Fatal("arbitrary read path accepted", err)
	}
}
func TestAnalysisUploadLegacyUnknownWriterWithNoOutputSurvivesCancelAndProjectDelete(t *testing.T) {
	for _, remove := range []string{"cancel", "project"} {
		t.Run(remove, func(t *testing.T) {
			s, _, _ := monitorFixture(t)
			in, err := s.SaveAnalysisImport(AnalysisImport{ID: uuid.NewString(), ProjectID: "monitor-p", Name: "legacy upload", State: "uploading", Files: []AnalysisImportFile{{ID: uuid.NewString(), RelativePath: "input.parquet", Size: 100}}}, 0)
			if err != nil {
				t.Fatal(err)
			}
			if remove == "cancel" {
				in.State = "cancelled"
				if _, err = s.SaveAnalysisImport(*in, in.Revision); err != nil {
					t.Fatal(err)
				}
			} else if err = s.DeleteProject(in.ProjectID); err != nil {
				t.Fatal(err)
			}
			ready, err := s.AnalysisDatasetCleanupReady(t.Context(), in.ProjectID, in.ID)
			if err != nil || ready {
				t.Fatal("unknown legacy writer treated as no physical owner", ready, err)
			}
		})
	}
}
func exerciseAnalysisUploadProjectDeletion(t *testing.T, s *SQLiteStore) {
	t.Helper()
	in := uploadWriterFixture(t, s)
	w, err := s.BeginAnalysisUploadWriter(t.Context(), uploadWriterRequest(in))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteProject(in.ProjectID); err != nil {
		t.Fatal(err)
	}
	retained, err := s.ResolveAnalysisUploadWriter(t.Context(), in.ProjectID, in.ID, w.ID)
	if err != nil || retained.PhysicallyClosed || retained.State != "abandoned" {
		t.Fatal("project foreign-key deletion erased exact owner", retained, err)
	}
	plan, err := s.AnalysisUploadWriterCleanupPlan(t.Context(), AnalysisCleanupJob{Kind: "upload-writer", ProjectID: in.ProjectID, ResourceID: w.ID})
	if err != nil || !plan.Blocked {
		t.Fatal("deleted project bypassed pending physical writer", plan, err)
	}
	sealed, err := s.CompleteAnalysisUploadWriter(t.Context(), in.ProjectID, w.ID, true)
	if err != nil || sealed.State != "abandoned" || !sealed.PhysicallyClosed {
		t.Fatal("late writer resurrected deleted project", sealed, err)
	}
	if _, err = s.GetAnalysisImport(in.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("deleted import reappeared", err)
	}
}
func TestAnalysisUploadMissingProjectCannotAcceptLegacyOrphanImport(t *testing.T) {
	s, _, _ := monitorFixture(t)
	in := uploadWriterFixture(t, s)
	w, err := s.BeginAnalysisUploadWriter(t.Context(), uploadWriterRequest(in))
	if err != nil {
		t.Fatal(err)
	}
	original := *w
	if err = s.DeleteProject(in.ProjectID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = s.db.Exec(`PRAGMA foreign_keys=ON`) })
	body, _ := json.Marshal(in)
	if _, err = s.db.Exec(`INSERT INTO analysis_imports(id,project_id,revision,state,payload,owner,lease_expires_at) VALUES(?,?,?,?,?,?,?)`, in.ID, in.ProjectID, in.Revision, in.State, string(body), "", time.Time{}); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(original)
	if _, err = s.db.Exec(`UPDATE analysis_upload_writers SET payload=? WHERE id=?`, string(raw), w.ID); err != nil {
		t.Fatal(err)
	}
	receipt, err := s.CompleteAnalysisUploadWriter(t.Context(), in.ProjectID, w.ID, true)
	if err != nil || receipt.State != "abandoned" || !receipt.PhysicallyClosed {
		t.Fatal("missing project authorized legacy orphan publication", receipt, err)
	}
	current, err := s.GetAnalysisImport(in.ID)
	if err != nil || current.Revision != in.Revision || current.Files[0].Uploaded != 0 {
		t.Fatal("orphan cursor was mutated", current, err)
	}
}
func TestAnalysisUploadSchemaMigrationRecreatesJournalAndPreservesPriorLedgerIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upgrade.db")
	first, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	seedMonitorFixture(t, first)
	if _, err = first.db.Exec(`DROP TABLE analysis_upload_writers`); err != nil {
		t.Fatal(err)
	}
	if _, err = first.db.Exec(`DELETE FROM schema_migrations WHERE version=34`); err != nil {
		t.Fatal(err)
	}
	if err = first.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	var prior string
	if err = upgraded.db.QueryRow(`SELECT fingerprint FROM schema_migrations WHERE version=33`).Scan(&prior); err != nil || prior != "348e5bff6128c23431046d91f301e65f219eaa542faead33fc42d1508c4587df" {
		t.Fatal("prior migration identity changed", prior, err)
	}
	var current string
	if err = upgraded.db.QueryRow(`SELECT fingerprint FROM schema_migrations WHERE version=34`).Scan(&current); err != nil || current != "52971cd3c20e72aefeb2c719f02e125a3689f107a50981c4da14d01a8e34783b" {
		t.Fatal("upload migration did not converge", current, err)
	}
	in := uploadWriterFixture(t, upgraded)
	if _, err = upgraded.BeginAnalysisUploadWriter(t.Context(), uploadWriterRequest(in)); err != nil {
		t.Fatal("recreated upload journal unavailable", err)
	}
}
func TestAnalysisUploadQuiescenceAcceptsExactWriterBudgetAndRejectsOneBeyond(t *testing.T) {
	s, _, _ := monitorFixture(t)
	in := uploadWriterFixture(t, s)
	in.State = "cancelled"
	in, err := s.SaveAnalysisImport(*in, in.Revision)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := s.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	appendClosed := func(tx *storeTx) {
		t.Helper()
		w := &AnalysisUploadWriter{ID: uuid.NewString(), ProjectID: in.ProjectID, ImportID: in.ID, FileID: in.Files[0].ID, Revision: 1, Offset: 0, Bytes: 100, SHA256: strings.Repeat("a", 64), State: "abandoned", CreatedAt: in.CreatedAt, PhysicallyClosed: true}
		w.ObjectKey = analysisUploadObjectKey(*w)
		w.TemporaryKey = analysisAttemptKey(w.ProjectID, w.ImportID, "upload-"+w.ID+".tmp")
		if err := analysisUploadWrite(t.Context(), tx, w, true); err != nil {
			t.Fatal(err)
		}
	}
	for range analysisUploadMaxWriters {
		appendClosed(tx)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	ready, err := s.AnalysisDatasetCleanupReady(t.Context(), in.ProjectID, in.ID)
	if err != nil || !ready {
		t.Fatal("exact writer budget rejected", ready, err)
	}
	extra, err := s.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer extra.Rollback()
	appendClosed(extra)
	if err = extra.Commit(); err != nil {
		t.Fatal(err)
	}
	ready, err = s.AnalysisDatasetCleanupReady(t.Context(), in.ProjectID, in.ID)
	if !errors.Is(err, ErrAnalysisAuthority) || ready {
		t.Fatal("one beyond writer budget was scored quiescent", ready, err)
	}
}
func TestAnalysisUploadJournalSQLIdentityMismatchBlocksWholeDatasetCleanup(t *testing.T) {
	s, _, _ := monitorFixture(t)
	in := uploadWriterFixture(t, s)
	w, err := s.BeginAnalysisUploadWriter(t.Context(), uploadWriterRequest(in))
	if err != nil {
		t.Fatal(err)
	}
	in.State = "cancelled"
	if _, err = s.SaveAnalysisImport(*in, in.Revision); err != nil {
		t.Fatal(err)
	}
	w.ID = uuid.NewString()
	w.State = "abandoned"
	w.PhysicallyClosed = true
	w.ObjectKey = analysisUploadObjectKey(*w)
	w.TemporaryKey = analysisAttemptKey(w.ProjectID, w.ImportID, "upload-"+w.ID+".tmp")
	raw, _ := json.Marshal(w)
	if _, err = s.db.Exec(`UPDATE analysis_upload_writers SET payload=? WHERE project_id=? AND import_id=?`, string(raw), in.ProjectID, in.ID); err != nil {
		t.Fatal(err)
	}
	ready, err := s.AnalysisDatasetCleanupReady(t.Context(), in.ProjectID, in.ID)
	if !errors.Is(err, ErrAnalysisAuthority) || ready {
		t.Fatal("mismatched durable identity granted namespace cleanup", ready, err)
	}
}
func TestAnalysisUploadCursorFailureRollsBackPhysicalSealAndRetriesAtomically(t *testing.T) {
	s, _, _ := monitorFixture(t)
	in := uploadWriterFixture(t, s)
	w, err := s.BeginAnalysisUploadWriter(t.Context(), uploadWriterRequest(in))
	if err != nil {
		t.Fatal(err)
	}
	// The UUID is fixture-generated/canonical; no SQL operation is caller-supplied.
	trigger := fmt.Sprintf(`CREATE TRIGGER fail_upload_cursor BEFORE UPDATE ON analysis_imports WHEN NEW.id='%s' BEGIN SELECT RAISE(ABORT,'controlled cursor refusal'); END`, in.ID)
	if _, err = s.db.Exec(trigger); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = s.db.Exec(`DROP TRIGGER IF EXISTS fail_upload_cursor`) })
	if _, err = s.CompleteAnalysisUploadWriter(t.Context(), in.ProjectID, w.ID, true); err == nil {
		t.Fatal("controlled cursor failure accepted")
	}
	pending, err := s.ResolveAnalysisUploadWriter(t.Context(), in.ProjectID, in.ID, w.ID)
	if err != nil || pending.PhysicallyClosed || pending.State != "pending" {
		t.Fatal("journal seal escaped failed cursor transaction", pending, err)
	}
	current, err := s.GetAnalysisImport(in.ID)
	if err != nil || current.Revision != in.Revision || current.Files[0].Uploaded != 0 {
		t.Fatal("failed cursor transaction changed durable metadata", current, err)
	}
	if _, err = s.db.Exec(`DROP TRIGGER fail_upload_cursor`); err != nil {
		t.Fatal(err)
	}
	accepted, err := s.CompleteAnalysisUploadWriter(t.Context(), in.ProjectID, w.ID, true)
	if err != nil || !accepted.PhysicallyClosed || accepted.State != "accepted" {
		t.Fatal("already-joined retry failed", accepted, err)
	}
	current, err = s.GetAnalysisImport(in.ID)
	if err != nil || len(current.Files[0].Chunks) != 1 || current.Revision != in.Revision+1 {
		t.Fatal("retry published duplicate cursor", current, err)
	}
}
func TestAnalysisUploadRequestBoundsBeforeDurableOwnership(t *testing.T) {
	s, _, _ := monitorFixture(t)
	in := uploadWriterFixture(t, s)
	good := uploadWriterRequest(in)
	for _, mutate := range []func(*AnalysisUploadRequest){func(r *AnalysisUploadRequest) { r.ImportID = "../outside" }, func(r *AnalysisUploadRequest) { r.FileID = strings.ToUpper("a0000000-0000-4000-8000-000000000000") }, func(r *AnalysisUploadRequest) { r.ProjectID = strings.Repeat("p", 129) }, func(r *AnalysisUploadRequest) { r.Bytes = analysisUploadChunkBytes + 1 }, func(r *AnalysisUploadRequest) { r.Offset = 20 << 30 }, func(r *AnalysisUploadRequest) { r.SHA256 = "invalid" }} {
		req := good
		mutate(&req)
		if w, err := s.BeginAnalysisUploadWriter(t.Context(), req); w != nil || !errors.Is(err, ErrAnalysisAuthority) {
			t.Fatal("invalid request obtained physical plan", w, err)
		}
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM analysis_upload_writers WHERE import_id=?`, in.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("request rejection created durable writer", count, err)
	}
}
