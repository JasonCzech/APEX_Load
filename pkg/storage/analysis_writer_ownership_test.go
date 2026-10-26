package storage

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/internal/procidentity"
)

// Two controller processes: A mints writer generations and then dies; B is a
// restarted controller or an HA replica that judges A.
var (
	writerIncarnationA = procidentity.Identity{Machine: "machine-1@host-a", Boot: "boot-1", Namespace: "pid:[1]", PID: 4101, Start: "100"}
	writerIncarnationB = procidentity.Identity{Machine: "machine-1@host-a", Boot: "boot-1", Namespace: "pid:[1]", PID: 4202, Start: "200"}
)

// writerIncarnationForeign ran on another boot: no judge here can decide it.
var writerIncarnationForeign = procidentity.Identity{Machine: "machine-2@host-b", Boot: "boot-9", Namespace: "pid:[1]", PID: 4303, Start: "300"}

// writerIncarnationOtherNamespace ran on host A's machine and boot, in another
// PID namespace: a restarted container or another pod on the same node.
var writerIncarnationOtherNamespace = procidentity.Identity{Machine: "machine-1@host-a", Boot: "boot-1", Namespace: "pid:[2]", PID: 4505, Start: "500"}

// hereOnHostA reports the identities host A (where B also runs) can judge.
func hereOnHostA(id procidentity.Identity) bool {
	return procidentity.Judgeable(procidentity.Host{Machine: writerIncarnationB.Machine, Boot: writerIncarnationB.Boot, Namespace: writerIncarnationB.Namespace}, id)
}

// cleanupQueued reports whether the exact generation has a cleanup job.
func cleanupQueued(t *testing.T, s *SQLiteStore, kind, id string) bool {
	t.Helper()
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM analysis_cleanup_jobs WHERE kind=? AND resource_id=?`, kind, id).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count == 1
}

// writerExitScope proves exactly the identities in exited, as B's host.
func writerExitScope(local bool, exited ...procidentity.Identity) AnalysisWriterExitScope {
	return AnalysisWriterExitScope{Prover: writerIncarnationB, LocalFilesystem: local, Exited: func(id procidentity.Identity) bool {
		return slices.Contains(exited, id)
	}}
}

func TestAnalysisWriterOwnershipSQLiteRestartAndReplica(t *testing.T) {
	path := filepath.Join(t.TempDir(), "writer-ownership.db")
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
	exerciseAnalysisWriterOwnership(t, first, second)
}

// exerciseAnalysisWriterOwnership injects a controller exit before and after
// each upload/normalization/publication/cleanup boundary. first is the exited
// controller's store; second is the restarted controller or HA replica.
// The PostgreSQL lane runs this same suite against two real stores.
func exerciseAnalysisWriterOwnership(t *testing.T, first, second *SQLiteStore) {
	t.Helper()
	t.Run("upload writer before completion", func(t *testing.T) {
		for _, local := range []bool{true, false} {
			name := map[bool]string{true: "filesystem", false: "remote"}[local]
			t.Run(name, func(t *testing.T) { exerciseExitedUploadWriter(t, first, second, local) })
		}
	})
	t.Run("upload writer after completion", func(t *testing.T) {
		ctx := WithAnalysisWriterIncarnation(t.Context(), writerIncarnationA)
		in := uploadWriterFixture(t, first)
		w, err := first.BeginAnalysisUploadWriter(ctx, uploadWriterRequest(in))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = first.CompleteAnalysisUploadWriter(ctx, in.ProjectID, w.ID, true); err != nil {
			t.Fatal(err)
		}
		outcome, err := second.ResolveExitedAnalysisUploadWriter(t.Context(), in.ProjectID, w.ID, writerExitScope(true, writerIncarnationA))
		if err != nil || outcome.Proved || !outcome.Quiesced {
			t.Fatal("a closed accepted writer was re-judged", outcome, err)
		}
		current, err := second.GetAnalysisImport(in.ID)
		if err != nil || current.Files[0].Uploaded != 100 || len(current.Files[0].Chunks) != 1 || current.Files[0].Chunks[0].ObjectKey != w.ObjectKey {
			t.Fatal("accepted cursor changed after exit", current, err)
		}
	})
	t.Run("legacy upload writer stays owned", func(t *testing.T) {
		in := uploadWriterFixture(t, first)
		req := uploadWriterRequest(in)
		w, err := first.BeginAnalysisUploadWriter(t.Context(), req)
		if err != nil || w.Incarnation != nil {
			t.Fatal("writer without a bound incarnation", w, err)
		}
		resolved, err := second.ResolveExitedAnalysisUploadCursor(t.Context(), req, AnalysisWriterExitScope{Prover: writerIncarnationB, LocalFilesystem: true, Exited: func(procidentity.Identity) bool { return true }})
		if err != nil || resolved != 0 {
			t.Fatal("an unrecorded writer was retired", resolved, err)
		}
		if _, err = second.BeginAnalysisUploadWriter(t.Context(), req); !errors.Is(err, ErrAnalysisUploadInFlight) {
			t.Fatal("an unrecorded writer stopped blocking its offset", err)
		}
		if _, err = first.CompleteAnalysisUploadWriter(t.Context(), in.ProjectID, w.ID, false); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("attempt before writer quiescence", func(t *testing.T) {
		for _, local := range []bool{true, false} {
			name := map[bool]string{true: "filesystem", false: "remote"}[local]
			t.Run(name, func(t *testing.T) { exerciseExitedAttempt(t, first, second, local) })
		}
	})
	t.Run("attempt after quiescence before publication", func(t *testing.T) {
		in, a := claimAttemptAs(t, first, writerIncarnationA)
		if err := first.ConfirmAnalysisAttemptWritersQuiesced(t.Context(), in.ID, in.ProjectID, a.ID); err != nil {
			t.Fatal(err)
		}
		outcome, err := second.ResolveExitedAnalysisAttempt(t.Context(), in.ProjectID, a.ID, writerExitScope(false, writerIncarnationA))
		// Failing the import records the exit proof; the generation's own
		// quiescence leaves no remote doubt.
		if err != nil || !outcome.ImportFailed || !outcome.Quiesced || !outcome.Proved || outcome.RemoteCommitUnproven {
			t.Fatal("a quiesced, unpublished, exited owner kept its import", outcome, err)
		}
		if _, err = first.PublishAnalysisImportAttempt(t.Context(), nativeAttemptPublicationFixture(t, in, a)); !errors.Is(err, ErrAnalysisAuthority) {
			t.Fatal("a late result of an exited attempt published", err)
		}
		requireImportState(t, second, in.ID, "failed")
	})
	t.Run("attempt after publication", func(t *testing.T) {
		in, a := claimAttemptAs(t, first, writerIncarnationA)
		if err := first.ConfirmAnalysisAttemptWritersQuiesced(t.Context(), in.ID, in.ProjectID, a.ID); err != nil {
			t.Fatal(err)
		}
		publication := nativeAttemptPublicationFixture(t, in, a)
		published, err := first.PublishAnalysisImportAttempt(t.Context(), publication)
		if err != nil {
			t.Fatal(err)
		}
		// An ambiguous commit reply leaves only the durable journal to decide.
		for _, local := range []bool{true, false} {
			outcome, err := second.ResolveExitedAnalysisAttempt(t.Context(), in.ProjectID, a.ID, writerExitScope(local, writerIncarnationA))
			if err != nil || outcome.ImportFailed || !outcome.Quiesced || outcome.Proved {
				t.Fatal("a published attempt was re-judged", outcome, err)
			}
		}
		resolution, err := second.ResolveAnalysisAttemptPublication(t.Context(), in.ID, in.ProjectID, a.ID, publication.Digest)
		if err != nil || !resolution.Published {
			t.Fatal("publication receipt lost", resolution, err)
		}
		current := requireImportState(t, second, in.ID, "ready")
		if current.Revision != published.Revision {
			t.Fatal("published import changed after exit", current.Revision, published.Revision)
		}
	})
	t.Run("cancellation while in flight", func(t *testing.T) {
		for _, local := range []bool{true, false} {
			in, a := claimAttemptAs(t, first, writerIncarnationA)
			current := requireImportState(t, first, in.ID, "normalizing")
			current.State = "cancelled"
			if _, err := first.SaveAnalysisImport(*current, current.Revision); err != nil {
				t.Fatal(err)
			}
			if ready, err := second.AnalysisDatasetCleanupReady(t.Context(), in.ProjectID, in.ID); err != nil || ready {
				t.Fatal("cancelled dataset deletable while its writer is unproven", ready, err)
			}
			outcome, err := second.ResolveExitedAnalysisAttempt(t.Context(), in.ProjectID, a.ID, writerExitScope(local, writerIncarnationA))
			if err != nil || outcome.ImportFailed || !outcome.Proved || outcome.Quiesced != local {
				t.Fatal("cancelled attempt exit", local, outcome, err)
			}
			if _, err = first.PublishAnalysisImportAttempt(t.Context(), nativeAttemptPublicationFixture(t, in, a)); !errors.Is(err, ErrAnalysisAuthority) {
				t.Fatal("cancelled import published a late result", err)
			}
			ready, err := second.AnalysisDatasetCleanupReady(t.Context(), in.ProjectID, in.ID)
			if err != nil || ready != local {
				t.Fatal("cancelled dataset deletion readiness after exit", local, ready, err)
			}
			requireImportState(t, second, in.ID, "cancelled")
		}
	})
	t.Run("live, self and unrecorded attempts stay owned", func(t *testing.T) {
		in, a := claimAttemptAs(t, first, writerIncarnationA)
		for name, scope := range map[string]AnalysisWriterExitScope{
			"live or other host": writerExitScope(true),
			"self":               {Prover: writerIncarnationA, LocalFilesystem: true, Exited: func(procidentity.Identity) bool { return true }},
		} {
			outcome, err := second.ResolveExitedAnalysisAttempt(t.Context(), in.ProjectID, a.ID, scope)
			if err != nil || outcome != (AnalysisWriterExitOutcome{}) {
				t.Fatal(name, "judged a writer it cannot prove exited", outcome, err)
			}
		}
		requireImportState(t, second, in.ID, "normalizing")
		unrecorded, b := claimAttemptAs(t, first, procidentity.Identity{})
		if b.Incarnation != nil {
			t.Fatal("an unbound claim recorded an incarnation")
		}
		outcome, err := second.ResolveExitedAnalysisAttempt(t.Context(), unrecorded.ProjectID, b.ID, AnalysisWriterExitScope{Prover: writerIncarnationB, LocalFilesystem: true, Exited: func(procidentity.Identity) bool { return true }})
		if err != nil || outcome != (AnalysisWriterExitOutcome{}) {
			t.Fatal("an unrecorded attempt was retired", outcome, err)
		}
		requireImportState(t, second, unrecorded.ID, "normalizing")
		if _, err = second.ResolveExitedAnalysisAttempt(t.Context(), in.ProjectID, a.ID, AnalysisWriterExitScope{Prover: writerIncarnationB}); !errors.Is(err, ErrAnalysisAuthority) {
			t.Fatal("a scope without a judge was accepted", err)
		}
	})
	t.Run("deletion-pending reasons", func(t *testing.T) {
		cancel := func(in *AnalysisImport) {
			t.Helper()
			current, err := first.GetAnalysisImport(in.ID)
			if err != nil {
				t.Fatal(err)
			}
			current.State = map[bool]string{true: "deleting", false: "cancelled"}[current.State == "ready"]
			if _, err = first.SaveAnalysisImport(*current, current.Revision); err != nil {
				t.Fatal(err)
			}
		}
		live, _ := claimAttemptAs(t, first, writerIncarnationA)
		if status, err := second.AnalysisDatasetCleanupStatus(t.Context(), live.ProjectID, live.ID, hereOnHostA); err != nil || !status.Live || status.Ready {
			t.Fatal("a live dataset reported cleanup status", status, err)
		}
		unrecorded, _ := claimAttemptAs(t, first, procidentity.Identity{})
		remote, remoteAttempt := claimAttemptAs(t, first, writerIncarnationA)
		upload := uploadWriterFixture(t, first)
		if _, err := first.BeginAnalysisUploadWriter(WithAnalysisWriterIncarnation(t.Context(), writerIncarnationA), uploadWriterRequest(upload)); err != nil {
			t.Fatal(err)
		}
		legacyUpload := uploadWriterFixture(t, first)
		if _, err := first.BeginAnalysisUploadWriter(t.Context(), uploadWriterRequest(legacyUpload)); err != nil {
			t.Fatal(err)
		}
		foreign, _ := claimAttemptAs(t, first, writerIncarnationForeign)
		otherNamespace, _ := claimAttemptAs(t, first, writerIncarnationOtherNamespace)
		remoteUpload := uploadWriterFixture(t, first)
		remoteWriter, err := first.BeginAnalysisUploadWriter(WithAnalysisWriterIncarnation(t.Context(), writerIncarnationA), uploadWriterRequest(remoteUpload))
		if err != nil {
			t.Fatal(err)
		}
		if outcome, err := second.ResolveExitedAnalysisUploadWriter(t.Context(), remoteUpload.ProjectID, remoteWriter.ID, writerExitScope(false, writerIncarnationA)); err != nil || !outcome.RemoteCommitUnproven {
			t.Fatal("remote upload writer exit", outcome, err)
		}
		// A published output whose journal is gone has no recorded writer.
		unplanned, unplannedAttempt := claimAttemptAs(t, first, writerIncarnationA)
		if err = first.ConfirmAnalysisAttemptWritersQuiesced(t.Context(), unplanned.ID, unplanned.ProjectID, unplannedAttempt.ID); err != nil {
			t.Fatal(err)
		}
		if _, err = first.PublishAnalysisImportAttempt(t.Context(), nativeAttemptPublicationFixture(t, unplanned, unplannedAttempt)); err != nil {
			t.Fatal(err)
		}
		if _, err = first.db.Exec(`DELETE FROM analysis_attempt_journal WHERE id=?`, unplannedAttempt.ID); err != nil {
			t.Fatal(err)
		}
		preJournal, err := first.SaveAnalysisImport(AnalysisImport{ID: uuid.NewString(), ProjectID: "monitor-p", Name: "pre-journal upload", State: "uploading", Files: []AnalysisImportFile{{ID: uuid.NewString(), RelativePath: "input.parquet", Size: 100}}}, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, in := range []*AnalysisImport{live, unrecorded, remote, upload, legacyUpload, preJournal, foreign, otherNamespace, remoteUpload, unplanned} {
			cancel(in)
		}
		if _, err := second.ResolveExitedAnalysisAttempt(t.Context(), remote.ProjectID, remoteAttempt.ID, writerExitScope(false, writerIncarnationA)); err != nil {
			t.Fatal(err)
		}
		for name, c := range map[string]struct {
			in   *AnalysisImport
			want AnalysisDatasetCleanupStatus
		}{
			"running writer":                  {live, AnalysisDatasetCleanupStatus{UnprovenWriters: 1}},
			"unrecorded writer":               {unrecorded, AnalysisDatasetCleanupStatus{UnrecordedWriters: 1}},
			"remote commit unproven":          {remote, AnalysisDatasetCleanupStatus{RemoteCommitsUnproven: 1}},
			"running upload writer":           {upload, AnalysisDatasetCleanupStatus{UnprovenWriters: 1}},
			"unrecorded upload writer":        {legacyUpload, AnalysisDatasetCleanupStatus{UnrecordedWriters: 1}},
			"pre-journal legacy upload":       {preJournal, AnalysisDatasetCleanupStatus{LegacyWriters: 1}},
			"writer of another host":          {foreign, AnalysisDatasetCleanupStatus{UnjudgeableWriters: 1}},
			"writer of another PID namespace": {otherNamespace, AnalysisDatasetCleanupStatus{UnjudgeableWriters: 1}},
			"remote upload commit":            {remoteUpload, AnalysisDatasetCleanupStatus{RemoteCommitsUnproven: 1}},
			"unplanned output":                {unplanned, AnalysisDatasetCleanupStatus{LegacyWriters: 1}},
		} {
			status, err := second.AnalysisDatasetCleanupStatus(t.Context(), c.in.ProjectID, c.in.ID, hereOnHostA)
			if err != nil || status != c.want {
				t.Fatal(name, status, err)
			}
			if ready, err := second.AnalysisDatasetCleanupReady(t.Context(), c.in.ProjectID, c.in.ID); err != nil || ready {
				t.Fatal(name, "ready", ready, err)
			}
		}
	})
	t.Run("lease expired, live re-claim, then exit proven", func(t *testing.T) {
		for _, local := range []bool{true, false} {
			in, a := claimAttemptAs(t, first, writerIncarnationA)
			tx, err := first.db.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = tx.ExecContext(t.Context(), `UPDATE analysis_imports SET lease_expires_at=? WHERE id=?`, analysisAttemptLeaseValue(tx, time.Now().Add(-time.Minute)), in.ID); err != nil {
				t.Fatal(err)
			}
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			live := procidentity.Identity{Machine: writerIncarnationB.Machine, Boot: writerIncarnationB.Boot, Namespace: writerIncarnationB.Namespace, PID: 4404, Start: "400"}
			retry, c, err := second.ClaimAnalysisImportAttempt(WithAnalysisWriterIncarnation(t.Context(), live), in.ID, in.ProjectID, in.Revision, time.Minute)
			if err != nil {
				t.Fatal("re-claim after lease expiry", err)
			}
			outcome, err := second.ResolveExitedAnalysisAttempt(t.Context(), in.ProjectID, a.ID, writerExitScope(local, writerIncarnationA))
			if err != nil || !outcome.Proved || !outcome.Recorded || outcome.ImportFailed || outcome.Quiesced != local || outcome.RemoteCommitUnproven == local {
				t.Fatal("exit of a replaced owner", local, outcome, err)
			}
			current := requireImportState(t, second, in.ID, "normalizing")
			if current.Owner != c.ID || current.Revision != retry.Revision {
				t.Fatal("the live re-claim's ownership changed", current.Owner, current.Revision, c.ID, retry.Revision)
			}
			if err = second.FailAnalysisImportAttempt(t.Context(), in.ID, in.ProjectID, c.ID, c.Revision, ""); err != nil {
				t.Fatal(err)
			}
		}
	})
	t.Run("pending deletion classification", func(t *testing.T) {
		for name, c := range map[string]struct {
			status          AnalysisDatasetCleanupStatus
			resolvable, now bool
		}{
			"unproven here":       {AnalysisDatasetCleanupStatus{UnprovenWriters: 1}, true, true},
			"another host":        {AnalysisDatasetCleanupStatus{UnjudgeableWriters: 1}, true, false},
			"unrecorded":          {AnalysisDatasetCleanupStatus{UnrecordedWriters: 1}, true, false},
			"legacy":              {AnalysisDatasetCleanupStatus{LegacyWriters: 1}, false, false},
			"remote":              {AnalysisDatasetCleanupStatus{RemoteCommitsUnproven: 1}, false, false},
			"legacy and remote":   {AnalysisDatasetCleanupStatus{LegacyWriters: 1, RemoteCommitsUnproven: 2}, false, false},
			"legacy and unproven": {AnalysisDatasetCleanupStatus{LegacyWriters: 1, UnprovenWriters: 1}, true, true},
		} {
			if c.status.Resolvable() != c.resolvable || c.status.ResolvesHere() != c.now {
				t.Error(name, c.status.Resolvable(), c.status.ResolvesHere())
			}
		}
	})
	t.Run("candidate inventory", func(t *testing.T) {
		_, owner := claimAttemptAs(t, first, writerIncarnationA)
		cancelled, retained := claimAttemptAs(t, first, writerIncarnationA)
		upload := uploadWriterFixture(t, first)
		writer, err := first.BeginAnalysisUploadWriter(WithAnalysisWriterIncarnation(t.Context(), writerIncarnationA), uploadWriterRequest(upload))
		if err != nil {
			t.Fatal(err)
		}
		for _, in := range []*AnalysisImport{cancelled, upload} {
			current, err := first.GetAnalysisImport(in.ID)
			if err != nil {
				t.Fatal(err)
			}
			current.State = "cancelled"
			if _, err = first.SaveAnalysisImport(*current, current.Revision); err != nil {
				t.Fatal(err)
			}
		}
		seen := map[string]string{}
		after := ""
		for range 1000 {
			page, next, err := second.ListAnalysisWriterOwnershipCandidates(t.Context(), after, 2)
			if err != nil {
				t.Fatal(err)
			}
			for _, c := range page {
				seen[c.ID] = c.Kind
				if c.ProjectID != "monitor-p" {
					t.Fatal("candidate project", c)
				}
			}
			if after = next; after == "" {
				break
			}
		}
		for id, kind := range map[string]string{owner.ID: "attempt", retained.ID: "attempt", writer.ID: "upload-writer"} {
			if seen[id] != kind {
				t.Fatal("generation missing from the inventory", kind, id, seen[id])
			}
		}
		for _, cursor := range []string{"x:1", "i" + strings.Repeat("a", 200)} {
			if _, _, err := second.ListAnalysisWriterOwnershipCandidates(t.Context(), cursor, 2); !errors.Is(err, ErrAnalysisAuthority) {
				t.Fatal("malformed inventory cursor accepted", cursor, err)
			}
		}
		if _, _, err := second.ListAnalysisWriterOwnershipCandidates(t.Context(), "", 0); !errors.Is(err, ErrAnalysisAuthority) {
			t.Fatal("unbounded inventory page accepted", err)
		}
	})
}

func claimAttemptAs(t *testing.T, s *SQLiteStore, incarnation procidentity.Identity) (*AnalysisImport, *AnalysisAttempt) {
	t.Helper()
	mapped := nativeAttemptMappedFixture(t, s)
	in, a, err := s.ClaimAnalysisImportAttempt(WithAnalysisWriterIncarnation(t.Context(), incarnation), mapped.ID, mapped.ProjectID, mapped.Revision, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return in, a
}

func requireImportState(t *testing.T, s *SQLiteStore, id, state string) *AnalysisImport {
	t.Helper()
	current, err := s.GetAnalysisImport(id)
	if err != nil || current.State != state {
		t.Fatalf("import state: want %s, got %+v (%v)", state, current, err)
	}
	return current
}

func exerciseExitedUploadWriter(t *testing.T, first, second *SQLiteStore, local bool) {
	ctx := WithAnalysisWriterIncarnation(t.Context(), writerIncarnationA)
	in := uploadWriterFixture(t, first)
	req := uploadWriterRequest(in)
	old, err := first.BeginAnalysisUploadWriter(ctx, req)
	if err != nil || old.Incarnation == nil || *old.Incarnation != writerIncarnationA {
		t.Fatal("writer incarnation not recorded at Begin", old, err)
	}
	replica := WithAnalysisWriterIncarnation(t.Context(), writerIncarnationB)
	if _, err = second.BeginAnalysisUploadWriter(replica, req); !errors.Is(err, ErrAnalysisUploadInFlight) {
		t.Fatal("a live writer's offset was reused", err)
	}
	for name, scope := range map[string]AnalysisWriterExitScope{
		"alive or another host": writerExitScope(local),
		"self":                  {Prover: writerIncarnationA, LocalFilesystem: local, Exited: func(procidentity.Identity) bool { return true }},
	} {
		if resolved, err := second.ResolveExitedAnalysisUploadCursor(t.Context(), req, scope); err != nil || resolved != 0 {
			t.Fatal(name, "retired a writer it cannot prove exited", resolved, err)
		}
	}
	if _, err = second.BeginAnalysisUploadWriter(replica, req); !errors.Is(err, ErrAnalysisUploadInFlight) {
		t.Fatal("an unproven writer stopped blocking", err)
	}
	stale := req
	stale.Revision--
	if _, err = second.ResolveExitedAnalysisUploadCursor(t.Context(), stale, writerExitScope(local, writerIncarnationA)); !errors.Is(err, ErrAnalysisAuthority) {
		t.Fatal("a stale cursor revision was resolved", err)
	}
	if cleanupQueued(t, second, "upload-writer", old.ID) {
		t.Fatal("a live writer was queued for cleanup")
	}
	resolved, err := second.ResolveExitedAnalysisUploadCursor(t.Context(), req, writerExitScope(local, writerIncarnationA))
	if err != nil || resolved != 1 {
		t.Fatal("exited writer not retired", resolved, err)
	}
	if !cleanupQueued(t, second, "upload-writer", old.ID) {
		t.Fatal("the exited writer's cleanup was not queued")
	}
	outcome, err := second.ResolveExitedAnalysisUploadWriter(t.Context(), in.ProjectID, old.ID, writerExitScope(local, writerIncarnationA))
	if err != nil || !outcome.Proved || outcome.Quiesced != local || outcome.RemoteCommitUnproven == local {
		t.Fatal("exited writer outcome", outcome, err)
	}
	replacement, err := second.BeginAnalysisUploadWriter(replica, req)
	if err != nil {
		t.Fatal("restart could not resume the upload offset", err)
	}
	if replacement.ObjectKey == old.ObjectKey || replacement.TemporaryKey == old.TemporaryKey || *replacement.Incarnation != writerIncarnationB {
		t.Fatal("replacement writer shares the exited generation's keys", replacement, old)
	}
	// A late completion from the exited generation cannot advance the cursor.
	late, err := first.CompleteAnalysisUploadWriter(ctx, in.ProjectID, old.ID, true)
	if err != nil || late.State != "abandoned" {
		t.Fatal("late completion of an exited writer", late, err)
	}
	if current := requireImportState(t, second, in.ID, "uploading"); current.Files[0].Uploaded != 0 {
		t.Fatal("late completion advanced the cursor", current.Files[0])
	}
	plan, err := second.AnalysisUploadWriterCleanupPlan(t.Context(), AnalysisCleanupJob{Kind: "upload-writer", ProjectID: in.ProjectID, ResourceID: old.ID})
	if err != nil {
		t.Fatal(err)
	}
	// The late Complete above is itself physical closure, so the exited
	// generation's exact keys are reclaimable either way, and only those.
	if plan.Blocked || !reflect.DeepEqual(plan.Keys, []string{old.TemporaryKey, old.ObjectKey}) {
		t.Fatal("exited generation cleanup plan", plan)
	}
	accepted, err := second.CompleteAnalysisUploadWriter(replica, in.ProjectID, replacement.ID, true)
	if err != nil || accepted.State != "accepted" {
		t.Fatal("replacement writer acceptance", accepted, err)
	}
	current := requireImportState(t, second, in.ID, "uploading")
	if len(current.Files[0].Chunks) != 1 || current.Files[0].Chunks[0].ObjectKey != replacement.ObjectKey {
		t.Fatal("cursor references a key other than the replacement's", current.Files[0].Chunks)
	}

	// Without the late completion, a remote generation stays cleanup- and
	// deletion-blocked; a filesystem one is reclaimable.
	in = uploadWriterFixture(t, first)
	req = uploadWriterRequest(in)
	old, err = first.BeginAnalysisUploadWriter(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if outcome, err = second.ResolveExitedAnalysisUploadWriter(t.Context(), in.ProjectID, old.ID, writerExitScope(local, writerIncarnationA)); err != nil || !outcome.Proved || !outcome.Recorded {
		t.Fatal(outcome, err)
	}
	if !cleanupQueued(t, second, "upload-writer", old.ID) {
		t.Fatal("the exited writer's cleanup was not queued")
	}
	plan, err = second.AnalysisUploadWriterCleanupPlan(t.Context(), AnalysisCleanupJob{Kind: "upload-writer", ProjectID: in.ProjectID, ResourceID: old.ID})
	if err != nil {
		t.Fatal(err)
	}
	if local && (plan.Blocked || !reflect.DeepEqual(plan.Keys, []string{old.TemporaryKey, old.ObjectKey})) || !local && (!plan.Blocked || len(plan.Keys) != 0) {
		t.Fatal("exited writer cleanup plan", local, plan)
	}
	current = requireImportState(t, second, in.ID, "uploading")
	current.State = "cancelled"
	if _, err = second.SaveAnalysisImport(*current, current.Revision); err != nil {
		t.Fatal(err)
	}
	ready, err := second.AnalysisDatasetCleanupReady(t.Context(), in.ProjectID, in.ID)
	if err != nil || ready != local {
		t.Fatal("remote commit unproven must keep deletion pending", local, ready, err)
	}
}

func exerciseExitedAttempt(t *testing.T, first, second *SQLiteStore, local bool) {
	in, a := claimAttemptAs(t, first, writerIncarnationA)
	if a.Incarnation == nil || *a.Incarnation != writerIncarnationA {
		t.Fatal("attempt incarnation not recorded at Claim", a.Incarnation)
	}
	if cleanupQueued(t, second, "attempt", a.ID) {
		t.Fatal("a live attempt was queued for cleanup")
	}
	outcome, err := second.ResolveExitedAnalysisAttempt(t.Context(), in.ProjectID, a.ID, writerExitScope(local, writerIncarnationA))
	if err != nil || !outcome.ImportFailed || !outcome.Proved || outcome.Quiesced != local || outcome.RemoteCommitUnproven == local {
		t.Fatal("exited attempt outcome", outcome, err)
	}
	if !cleanupQueued(t, second, "attempt", a.ID) {
		t.Fatal("the exited attempt's cleanup was not queued")
	}
	failed := requireImportState(t, second, in.ID, "failed")
	if failed.Owner != "" || failed.Revision != in.Revision+1 || !strings.Contains(failed.Error, "controller running it exited") {
		t.Fatal("exited attempt failure", failed)
	}
	again, err := second.ResolveExitedAnalysisAttempt(t.Context(), in.ProjectID, a.ID, writerExitScope(local, writerIncarnationA))
	if err != nil || again.ImportFailed || !again.Proved || again.Quiesced != local {
		t.Fatal("repeated resolution", again, err)
	}
	// A late result of the exited generation can neither publish nor fail
	// the import a later generation owns.
	if _, err = first.PublishAnalysisImportAttempt(t.Context(), nativeAttemptPublicationFixture(t, in, a)); !errors.Is(err, ErrAnalysisAuthority) {
		t.Fatal("late publication", err)
	}
	plan, err := second.AnalysisAttemptCleanupPlan(t.Context(), AnalysisCleanupJob{Kind: "attempt", ProjectID: in.ProjectID, ResourceID: a.ID})
	if err != nil {
		t.Fatal(err)
	}
	own := append(append([]string{}, a.Normalized...), a.Rejections...)
	if local && (plan.Blocked || !reflect.DeepEqual(plan.Keys, own)) || !local && (!plan.Blocked || len(plan.Keys) != 0) {
		t.Fatal("exited attempt cleanup plan", local, plan)
	}
	// The retry is a new generation with its own namespace; its cleanup never
	// deletes its own live outputs and reclaims the exited ones only on proof.
	retry, b, err := second.ClaimAnalysisImportAttempt(WithAnalysisWriterIncarnation(t.Context(), writerIncarnationB), in.ID, in.ProjectID, failed.Revision, time.Minute)
	if err != nil {
		t.Fatal("retry after a proven exit", err)
	}
	for _, key := range a.Normalized {
		if slices.Contains(b.Normalized, key) {
			t.Fatal("retry reuses an exited generation's key", key)
		}
	}
	plan, err = second.AnalysisAttemptCleanupPlan(t.Context(), AnalysisCleanupJob{Kind: "attempt", ProjectID: in.ProjectID, ResourceID: b.ID})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range plan.Keys {
		if slices.Contains(b.Normalized, key) || !slices.Contains(a.Normalized, key) {
			t.Fatal("retry cleanup touches a key outside the exited generation", key)
		}
	}
	if plan.Blocked == local {
		t.Fatal("retry's obsolete cleanup", local, plan)
	}
	if late := first.FailAnalysisImportAttempt(t.Context(), in.ID, in.ProjectID, a.ID, a.Revision, ""); !errors.Is(late, ErrAnalysisAuthority) {
		t.Fatal("late failure of an exited generation", late)
	}
	requireImportState(t, second, retry.ID, "normalizing")
	if outcome, err = second.ResolveExitedAnalysisAttempt(t.Context(), in.ProjectID, b.ID, writerExitScope(local, writerIncarnationA)); err != nil || outcome != (AnalysisWriterExitOutcome{}) {
		t.Fatal("the live retry was judged by its predecessor's exit", outcome, err)
	}
	if err = second.FailAnalysisImportAttempt(context.WithoutCancel(t.Context()), retry.ID, retry.ProjectID, b.ID, b.Revision, ""); err != nil {
		t.Fatal(err)
	}
}

func TestAnalysisUploadWriterTerminationValidation(t *testing.T) {
	base := AnalysisUploadWriter{ID: "8d7c1f3e-8f7b-4bb8-9b1c-3a2c1d0e9f01", ProjectID: "p", ImportID: "2b0c4f0a-6a3b-4b9e-8f0e-1d2c3b4a5f60", FileID: "6f5e4d3c-2b1a-4c9d-8e7f-0a1b2c3d4e5f", Revision: 1, Offset: 0, Bytes: 12, SHA256: strings.Repeat("a", 64), State: "abandoned", CreatedAt: time.Now()}
	base.ObjectKey = analysisUploadObjectKey(base)
	base.TemporaryKey = analysisAttemptKey(base.ProjectID, base.ImportID, "upload-"+base.ID+".tmp")
	incarnation := writerIncarnationA
	proof := &AnalysisWriterTermination{Proof: AnalysisWriterProofIncarnationExited, Prover: writerIncarnationB, At: time.Now()}
	for name, c := range map[string]struct {
		edit  func(*AnalysisUploadWriter)
		valid bool
	}{
		"filesystem proof closes": {func(w *AnalysisUploadWriter) {
			w.Incarnation, w.Termination, w.PhysicallyClosed = &incarnation, proof, true
		}, true},
		"remote proof stays open": {func(w *AnalysisUploadWriter) {
			p := *proof
			p.RemoteCommitUnproven = true
			w.Incarnation, w.Termination = &incarnation, &p
		}, true},
		"filesystem proof unclosed": {func(w *AnalysisUploadWriter) { w.Incarnation, w.Termination = &incarnation, proof }, false},
		"proof without incarnation": {func(w *AnalysisUploadWriter) { w.Termination, w.PhysicallyClosed = proof, true }, false},
		"proof of a pending writer": {func(w *AnalysisUploadWriter) { w.State, w.Incarnation, w.Termination = "pending", &incarnation, proof }, false},
		"self proof": {func(w *AnalysisUploadWriter) {
			p := *proof
			p.Prover = incarnation
			w.Incarnation, w.Termination, w.PhysicallyClosed = &incarnation, &p, true
		}, false},
		"unknown proof": {func(w *AnalysisUploadWriter) {
			p := *proof
			p.Proof = "lease_expired"
			w.Incarnation, w.Termination, w.PhysicallyClosed = &incarnation, &p, true
		}, false},
		"invalid incarnation": {func(w *AnalysisUploadWriter) { w.Incarnation = &procidentity.Identity{PID: 1} }, false},
	} {
		w := base
		c.edit(&w)
		if got := analysisUploadValidate(&w) == nil; got != c.valid {
			t.Errorf("%s: valid=%v, want %v", name, got, c.valid)
		}
	}
	if analysisWriterIncarnationFrom(WithAnalysisWriterIncarnation(context.Background(), procidentity.Identity{PID: 1})) != nil {
		t.Error("an incomplete incarnation was bound")
	}
	long := writerIncarnationA
	long.Machine = strings.Repeat("m", 321)
	if analysisWriterIncarnationFrom(WithAnalysisWriterIncarnation(context.Background(), long)) != nil {
		t.Error("an unbounded incarnation was bound")
	}
}
