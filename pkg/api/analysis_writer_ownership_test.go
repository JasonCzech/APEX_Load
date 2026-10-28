package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	analytics "github.com/jasonczech/apexload/pkg/analysis/duckdb"
	"github.com/jasonczech/apexload/pkg/internal/localfs"
	"github.com/jasonczech/apexload/pkg/internal/procidentity"
	"github.com/jasonczech/apexload/pkg/storage"
	"gocloud.dev/blob"
	"gocloud.dev/blob/memblob"
)

// Controller A minted generations and died; controller B is the restarted
// controller or HA replica running the tests; controller C is still alive.
var (
	apiWriterA = procidentity.Identity{Machine: "machine-1@host-a", Boot: "boot-1", Namespace: "pid:[1]", PID: 5101, Start: "100"}
	apiWriterB = procidentity.Identity{Machine: "machine-1@host-a", Boot: "boot-1", Namespace: "pid:[1]", PID: 5202, Start: "200"}
	apiWriterC = procidentity.Identity{Machine: "machine-1@host-a", Boot: "boot-1", Namespace: "pid:[1]", PID: 5303, Start: "300"}
)

// apiHostA is the host A, B and C run on.
var apiHostA = procidentity.Host{Machine: "machine-1@host-a", Boot: "boot-1", Namespace: "pid:[1]"}

// actAsWriterB makes server judge as B on host A, proving exactly the exited
// identities. Tests install it with no exits before minting A's generations,
// so the server's own background sweep (started by NewServer) cannot act
// before the test's explicit sweep, and with A's exit just before that sweep.
//
// Its filesystem probe answers "local" without looking: only the locality
// tests (NetworkFilesystemIsNotLocal, ArtifactDirectoryIsNeverCreatedByTheJudge and
// localfs's own) probe the real TMPDIR, so the rest also pass on a runner
// whose TMPDIR is a shared or user-space filesystem.
func actAsWriterB(server *Server, exited ...procidentity.Identity) {
	actAsWriterBWith(server, assumeLocalFilesystem, exited...)
}

// assumeLocalFilesystem is a probe that calls every artifact directory local.
func assumeLocalFilesystem(string) (bool, error) { return true, nil }

func actAsWriterBWith(server *Server, local func(string) (bool, error), exited ...procidentity.Identity) {
	server.analysisWriterOwnership.Store(&analysisWriterOwnershipRuntime{self: apiWriterB, host: apiHostA, known: true, localFS: local, exited: func(id procidentity.Identity) bool {
		return slices.Contains(exited, id)
	}})
}

// analysisNoCloseBucket keeps a shared memblob open across cleanup owners, so
// a regression that deletes reads as a deleted object, not a closed bucket.
type analysisNoCloseBucket struct{ *blob.Bucket }

func (analysisNoCloseBucket) Close() error { return nil }

// claimAsWriter creates a mapped import with UUID files and claims it as the
// given controller, as handleAnalysisCommit does before any artifact I/O.
func claimAsWriter(t *testing.T, store *storage.SQLiteStore, ready *storage.AnalysisImport, writer procidentity.Identity) (*storage.AnalysisImport, *storage.AnalysisAttempt) {
	t.Helper()
	in, err := store.SaveAnalysisImport(storage.AnalysisImport{ID: uuid.NewString(), ProjectID: ready.ProjectID, Name: "writer ownership", State: "uploading", Files: []storage.AnalysisImportFile{{ID: uuid.NewString(), RelativePath: "data.parquet", Size: 12, Uploaded: 12}}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	in.State, in.Mapping = "mapped", ready.Mapping
	if in, err = store.SaveAnalysisImport(*in, in.Revision); err != nil {
		t.Fatal(err)
	}
	claimed, attempt, err := store.ClaimAnalysisImportAttempt(storage.WithAnalysisWriterIncarnation(t.Context(), writer), in.ID, in.ProjectID, in.Revision, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return claimed, attempt
}

func writeAnalysisFixtureObject(t *testing.T, key string) {
	t.Helper()
	if _, err := analysisObjectWrite(t.Context(), key, bytes.NewReader([]byte("PAR1dataPAR1"))); err != nil {
		t.Fatal(err)
	}
}

func analysisFixtureObjectExists(t *testing.T, key string) bool {
	t.Helper()
	_, err := analysisObjectRead(t.Context(), key, io.Discard, 64)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	return err == nil
}

// requireDeletionPending checks the explicit deletion-pending answer, its
// per-reason counts, and that only a resolvable wait is offered as a retry.
func requireDeletionPending(t *testing.T, response *httptest.ResponseRecorder, legacy, unrecorded, remote, unproven, unjudgeable int) {
	t.Helper()
	var body struct {
		Code      string         `json:"code"`
		Retryable bool           `json:"retryable"`
		Pending   map[string]int `json:"pending"`
	}
	// retryable: some writer may still resolve; Retry-After: this host itself
	// will settle one.
	retryable := unproven+unjudgeable+unrecorded > 0
	if response.Code != 503 || json.Unmarshal(response.Body.Bytes(), &body) != nil || body.Code != "analysis_dataset_deletion_pending" || body.Retryable != retryable || (response.Header().Get("Retry-After") != "") != (unproven > 0) || body.Pending["legacy_writers"] != legacy || body.Pending["unrecorded_writers"] != unrecorded || body.Pending["remote_commits_unproven"] != remote || body.Pending["unproven_writers"] != unproven || body.Pending["unjudgeable_writers"] != unjudgeable {
		t.Fatalf("deletion pending (legacy %d, unrecorded %d, remote %d, unproven %d, unjudgeable %d): %d %v %s", legacy, unrecorded, remote, unproven, unjudgeable, response.Code, response.Header(), response.Body.String())
	}
}

func requireAnalysisImportState(t *testing.T, store *storage.SQLiteStore, id, state string) *storage.AnalysisImport {
	t.Helper()
	current, err := store.GetAnalysisImport(id)
	if err != nil || current.State != state {
		t.Fatalf("import %s: want %s, got %+v (%v)", id, state, current, err)
	}
	return current
}

// A restarted controller on the filesystem backend retires the exited
// controller's generations at every boundary it left them, and nothing else:
// a still-running controller's generation and objects are untouched.
func TestAnalysisWriterOwnershipRestartRetiresExitedGenerationsOnFilesystem(t *testing.T) {
	t.Setenv("APEXLOAD_ANALYSIS_PROJECTS", "proj-a")
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", "filesystem")
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	server, store, ready := nativeQueryAuthorityFixture(t)
	actAsWriterB(server)

	// A died mid-normalization (one output written), and A died while its
	// import was being deleted; C is alive mid-normalization.
	interrupted, interruptedAttempt := claimAsWriter(t, store, ready, apiWriterA)
	deleted, deletedAttempt := claimAsWriter(t, store, ready, apiWriterA)
	live, liveAttempt := claimAsWriter(t, store, ready, apiWriterC)
	// A also died mid-upload of a chunk, whose upload was then cancelled.
	upload, err := store.SaveAnalysisImport(storage.AnalysisImport{ID: uuid.NewString(), ProjectID: ready.ProjectID, Name: "interrupted upload", State: "uploading", UploadWriterVersion: storage.AnalysisUploadWriterVersion, Files: []storage.AnalysisImportFile{{ID: uuid.NewString(), RelativePath: "data.parquet", Size: 12}}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	chunk, err := store.BeginAnalysisUploadWriter(storage.WithAnalysisWriterIncarnation(t.Context(), apiWriterA), storage.AnalysisUploadRequest{ProjectID: upload.ProjectID, ImportID: upload.ID, FileID: upload.Files[0].ID, Revision: upload.Revision, Offset: 0, Bytes: 12, SHA256: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{interruptedAttempt.Normalized[0], deletedAttempt.Normalized[0], liveAttempt.Normalized[0], chunk.TemporaryKey} {
		writeAnalysisFixtureObject(t, key)
	}
	requireDeletionPending(t, timeslotRequest(t, server, "DELETE", "/api/v1/analysis/imports/"+deleted.ID, nil), 0, 0, 0, 1, 0)
	queued, err := store.ListAnalysisCleanupJobs(time.Now().Add(time.Hour), 100)
	if err != nil {
		t.Fatal(err)
	}
	// A repeated DELETE re-reads the status without re-queuing cleanup later.
	requireDeletionPending(t, timeslotRequest(t, server, "DELETE", "/api/v1/analysis/imports/"+deleted.ID, nil), 0, 0, 0, 1, 0)
	requeued, err := store.ListAnalysisCleanupJobs(time.Now().Add(time.Hour), 100)
	if err != nil || !slices.Equal(queued, requeued) || len(queued) == 0 {
		t.Fatal("a repeated DELETE re-queued cleanup", queued, requeued, err)
	}
	requireDeletionPending(t, timeslotRequest(t, server, "DELETE", "/api/v1/analysis/imports/"+upload.ID, nil), 0, 0, 0, 1, 0)
	if !analysisFixtureObjectExists(t, deletedAttempt.Normalized[0]) || !analysisFixtureObjectExists(t, chunk.TemporaryKey) {
		t.Fatal("an unproven writer's output was deleted")
	}

	actAsWriterB(server, apiWriterA)
	server.reconcileAnalysisWriterIncarnationsOnce(t.Context())

	failed := requireAnalysisImportState(t, store, interrupted.ID, "failed")
	if !strings.Contains(failed.Error, "controller running it exited") || failed.Owner != "" {
		t.Fatal("interrupted normalization not failed for retry", failed)
	}
	response := timeslotRequest(t, server, "GET", "/api/v1/analysis/imports/"+interrupted.ID, nil)
	if response.Code != 200 || !strings.Contains(response.Body.String(), "controller running it exited") {
		t.Fatalf("interrupted import read: %d %s", response.Code, response.Body.String())
	}
	if _, err := store.PublishAnalysisImportAttempt(t.Context(), storage.AnalysisAttemptPublication{Import: *interrupted, AttemptID: interruptedAttempt.ID, Digest: strings.Repeat("a", 64)}); !errors.Is(err, storage.ErrAnalysisAuthority) {
		t.Fatal("a late result of the exited attempt published", err)
	}
	// The exited attempt's exact cleanup plan is released and removes only
	// its own outputs.
	if err := server.retryAnalysisCleanupJob(t.Context(), store, storage.AnalysisCleanupJob{Kind: "attempt", ProjectID: interrupted.ProjectID, ResourceID: interruptedAttempt.ID}); err != nil {
		t.Fatal(err)
	}
	if analysisFixtureObjectExists(t, interruptedAttempt.Normalized[0]) {
		t.Fatal("exited generation's proven output retained")
	}
	if response = timeslotRequest(t, server, "DELETE", "/api/v1/analysis/imports/"+deleted.ID, nil); response.Code != 204 {
		t.Fatalf("deletion after proven exit: %d %s", response.Code, response.Body.String())
	}
	if analysisFixtureObjectExists(t, deletedAttempt.Normalized[0]) {
		t.Fatal("deleted dataset retained its proven output")
	}
	if response = timeslotRequest(t, server, "DELETE", "/api/v1/analysis/imports/"+upload.ID, nil); response.Code != 204 {
		t.Fatalf("cancelled upload after proven exit: %d %s", response.Code, response.Body.String())
	}
	if analysisFixtureObjectExists(t, chunk.TemporaryKey) {
		t.Fatal("cancelled upload retained the exited writer's object")
	}
	requireAnalysisImportState(t, store, live.ID, "normalizing")
	if !analysisFixtureObjectExists(t, liveAttempt.Normalized[0]) {
		t.Fatal("a live controller's generation lost its output")
	}
	requireDeletionPending(t, timeslotRequest(t, server, "DELETE", "/api/v1/analysis/imports/"+live.ID, nil), 0, 0, 0, 1, 0)
}

// Remote object stores: exit stops new requests, but a commit the exited
// process already sent may still land at the exited generation's own key. The
// import is retried, but cleanup and deletion stay pending, and the memblob
// stand-in for the bucket keeps a late object written after the decision.
func TestAnalysisWriterOwnershipRemoteBackendsRetainUnprovenCommits(t *testing.T) {
	for _, backend := range []string{archiveBackendS3, archiveBackendAzureBlob, archiveBackendGCS} {
		t.Run(backend, func(t *testing.T) {
			t.Setenv("APEXLOAD_ANALYSIS_PROJECTS", "proj-a")
			t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
			t.Setenv("APEXLOAD_ARCHIVE_BACKEND", backend)
			t.Setenv("APEXLOAD_ARCHIVE_BUCKET", "analysis-writer-ownership")
			t.Setenv("APEXLOAD_ARCHIVE_CONTAINER", "analysis-writer-ownership")
			server, store, ready := nativeQueryAuthorityFixture(t)
			// A probe that calls every root local: only the backend kind may
			// keep an object store from being judged like a local filesystem.
			everyRootLocal := func(string) (bool, error) { return true, nil }
			actAsWriterBWith(server, everyRootLocal)
			interrupted, interruptedAttempt := claimAsWriter(t, store, ready, apiWriterA)
			deleted, deletedAttempt := claimAsWriter(t, store, ready, apiWriterA)
			requireDeletionPending(t, timeslotRequest(t, server, "DELETE", "/api/v1/analysis/imports/"+deleted.ID, nil), 0, 0, 0, 1, 0)

			actAsWriterBWith(server, everyRootLocal, apiWriterA)
			if scope, err := server.analysisWriterExitScope(); err != nil || scope.LocalFilesystem {
				t.Fatal("an object store was judged a local filesystem", scope.LocalFilesystem, err)
			}
			server.reconcileAnalysisWriterIncarnationsOnce(t.Context())

			failed := requireAnalysisImportState(t, store, interrupted.ID, "failed")
			if !strings.Contains(failed.Error, "controller running it exited") {
				t.Fatal("remote interrupted normalization not offered a retry", failed)
			}
			bucket := memblob.OpenBucket(nil)
			t.Cleanup(func() { _ = bucket.Close() })
			pinned := currentArchiveBackend()
			late := map[string]bool{}
			for _, key := range []string{interruptedAttempt.Normalized[0], deletedAttempt.Normalized[0]} {
				full := filepath.ToSlash(filepath.Join(pinned.Prefix, "_analysis", key))
				if err := bucket.WriteAll(t.Context(), full, []byte("late commit"), nil); err != nil {
					t.Fatal(err)
				}
				late[full] = true
			}
			ops := defaultAnalysisCleanupPhysicalOps
			ops.openBucket = func(context.Context, archiveBackendRuntime) (analysisCleanupBucket, error) {
				return analysisNoCloseBucket{bucket}, nil
			}
			ctx, _ := cleanupOwnerContext(t, 4, ops)
			for _, job := range []storage.AnalysisCleanupJob{
				{Kind: "attempt", ProjectID: interrupted.ProjectID, ResourceID: interruptedAttempt.ID},
				{Kind: "attempt", ProjectID: deleted.ProjectID, ResourceID: deletedAttempt.ID},
				{Kind: "dataset", ProjectID: deleted.ProjectID, ResourceID: deleted.ID},
			} {
				if err := server.retryAnalysisCleanupJob(ctx, store, job); err != nil {
					t.Fatal(job.Kind, err)
				}
			}
			for key := range late {
				if exists, err := bucket.Exists(t.Context(), key); err != nil || !exists {
					t.Fatal("remote cleanup deleted under an unproven commit", key, exists, err)
				}
			}
			requireDeletionPending(t, timeslotRequest(t, server, "DELETE", "/api/v1/analysis/imports/"+deleted.ID, nil), 0, 0, 1, 0, 0)
			requireAnalysisImportState(t, store, deleted.ID, "cancelled")
			iterator := bucket.List(&blob.ListOptions{})
			count := 0
			for {
				if _, err := iterator.Next(t.Context()); err != nil {
					break
				}
				count++
			}
			if count != len(late) {
				t.Fatal("remote bucket changed", count)
			}
		})
	}
}

// TestAnalysisWriterOwnershipHelperProcess is the exited controller stand-in:
// it lives until its parent closes its stdin or kills it.
func TestAnalysisWriterOwnershipHelperProcess(t *testing.T) {
	if os.Getenv("APEXLOAD_WRITER_OWNERSHIP_HELPER") != "wait" {
		t.Skip("helper process only")
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
}

// With the real process judge: a writer generation of a real process on this
// host is retired once that process is reaped, never while it runs; a
// generation recorded on another machine's boot (the cold HA replica's view
// of a lost pod) stays owned and keeps its dataset deletion-pending.
func TestAnalysisWriterOwnershipRealProcessExitAndColdReplica(t *testing.T) {
	if _, err := procidentity.CurrentHost(); err != nil {
		if runtime.GOOS == "darwin" || runtime.GOOS == "linux" || runtime.GOOS == "windows" {
			t.Fatalf("process identity unavailable on %s: %v", runtime.GOOS, err)
		}
		t.Skipf("process identity unavailable: %v", err)
	}
	t.Setenv("APEXLOAD_ANALYSIS_PROJECTS", "proj-a")
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", "filesystem")
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	server, store, ready := nativeQueryAuthorityFixture(t)
	// The real process identity and exit judge; only the filesystem probe is
	// fixed, so the test does not depend on TMPDIR's filesystem.
	realSelf, err := procidentity.Self()
	if err != nil {
		t.Fatal(err)
	}
	realHost, err := procidentity.CurrentHost()
	if err != nil {
		t.Fatal(err)
	}
	server.analysisWriterOwnership.Store(&analysisWriterOwnershipRuntime{self: realSelf, host: realHost, known: true, exited: procidentity.Exited, localFS: assumeLocalFilesystem})
	child := exec.Command(os.Args[0], "-test.run=^TestAnalysisWriterOwnershipHelperProcess$")
	child.Env = append(os.Environ(), "APEXLOAD_WRITER_OWNERSHIP_HELPER=wait")
	alive, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer alive.Close()
	reaped := false
	t.Cleanup(func() {
		if !reaped {
			_ = child.Process.Kill()
			_ = child.Wait()
		}
	})
	exited, err := procidentity.Of(child.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	host, err := procidentity.CurrentHost()
	if err != nil {
		t.Fatal(err)
	}
	// The common cold case: the same node's machine and boot, another PID
	// namespace (a restarted container, another pod), and a live PID here.
	foreign := procidentity.Identity{Machine: host.Machine, Boot: host.Boot, Namespace: host.Namespace + "-other", PID: os.Getpid(), Start: exited.Start}
	restarted, restartedAttempt := claimAsWriter(t, store, ready, exited)
	cold, _ := claimAsWriter(t, store, ready, foreign)
	writeAnalysisFixtureObject(t, restartedAttempt.Normalized[0])

	server.reconcileAnalysisWriterIncarnationsOnce(t.Context())
	requireAnalysisImportState(t, store, restarted.ID, "normalizing")
	_ = child.Process.Kill()
	_ = child.Wait()
	reaped = true
	server.reconcileAnalysisWriterIncarnationsOnce(t.Context())
	server.reconcileAnalysisWriterIncarnationsOnce(t.Context())

	requireAnalysisImportState(t, store, restarted.ID, "failed")
	requireAnalysisImportState(t, store, cold.ID, "normalizing")
	if response := timeslotRequest(t, server, "DELETE", "/api/v1/analysis/imports/"+restarted.ID, nil); response.Code != 204 {
		t.Fatalf("deletion after a real reaped writer: %d %s", response.Code, response.Body.String())
	}
	if analysisFixtureObjectExists(t, restartedAttempt.Normalized[0]) {
		t.Fatal("proven generation's output retained")
	}
	requireDeletionPending(t, timeslotRequest(t, server, "DELETE", "/api/v1/analysis/imports/"+cold.ID, nil), 0, 0, 0, 0, 1)
}

// An upload chunk writer whose controller exited blocks its offset until a
// retry on a host that proves the exit; then the retry resumes the upload
// with a new writer and only that writer's key enters the cursor.
func TestAnalysisWriterOwnershipUploadRetryResumesAfterExit(t *testing.T) {
	t.Setenv("APEXLOAD_ANALYSIS_PROJECTS", "proj-a")
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", "filesystem")
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	server, store, in := nativeUploadCursorFixture(t)
	data := []byte("PAR1dataPAR1")
	digest := sha256.Sum256(data)
	request := storage.AnalysisUploadRequest{ProjectID: in.ProjectID, ImportID: in.ID, FileID: in.Files[0].ID, Revision: in.Revision, Offset: 0, Bytes: int64(len(data)), SHA256: hex.EncodeToString(digest[:])}
	exited, err := store.BeginAnalysisUploadWriter(storage.WithAnalysisWriterIncarnation(t.Context(), apiWriterA), request)
	if err != nil {
		t.Fatal(err)
	}
	writeAnalysisFixtureObject(t, exited.TemporaryKey)

	// Unproven (a cold replica): the offset stays owned.
	actAsWriterB(server)
	if server.resolveExitedAnalysisUploadOffset(t.Context(), request) {
		t.Fatal("an unproven writer was retired")
	}
	actAsWriterB(server, apiWriterA)
	if analytics.Available() {
		req := httptest.NewRequest("PUT", "/api/v1/analysis/imports/"+in.ID+"/files/"+in.Files[0].ID+"?offset=0", bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/octet-stream")
		response := httptest.NewRecorder()
		server.mux.ServeHTTP(response, req)
		if response.Code != 200 {
			t.Fatalf("upload retry after a proven exit: %d %s", response.Code, response.Body.String())
		}
	} else {
		if !server.resolveExitedAnalysisUploadOffset(t.Context(), request) {
			t.Fatal("a proven exited writer kept its offset")
		}
		replacement, err := store.BeginAnalysisUploadWriter(server.bindAnalysisWriterIncarnation(t.Context()), request)
		if err != nil {
			t.Fatal("upload retry after a proven exit", err)
		}
		if _, err = analysisObjectWrite(t.Context(), replacement.ObjectKey, bytes.NewReader(data)); err != nil {
			t.Fatal(err)
		}
		if _, err = store.CompleteAnalysisUploadWriter(t.Context(), in.ProjectID, replacement.ID, true); err != nil {
			t.Fatal(err)
		}
	}
	current := requireAnalysisImportState(t, store, in.ID, "uploading")
	if current.Files[0].Uploaded != 12 || len(current.Files[0].Chunks) != 1 || current.Files[0].Chunks[0].ObjectKey == exited.ObjectKey {
		t.Fatal("cursor after the resumed upload", current.Files[0])
	}
	if err := server.retryAnalysisCleanupJob(t.Context(), store, storage.AnalysisCleanupJob{Kind: "upload-writer", ProjectID: in.ProjectID, ResourceID: exited.ID}); err != nil {
		t.Fatal(err)
	}
	if analysisFixtureObjectExists(t, exited.TemporaryKey) {
		t.Fatal("exited writer's temporary object retained after proof")
	}
	// The retried Begin recorded this controller (B) as the replacement's writer.
	objectKey := current.Files[0].Chunks[0].ObjectKey
	replacement, err := store.ResolveAnalysisUploadWriter(t.Context(), current.ProjectID, current.ID, objectKey[len(objectKey)-36:])
	if err != nil || replacement.Incarnation == nil || *replacement.Incarnation != apiWriterB {
		t.Fatal("the resumed upload's writer did not record this controller", replacement, err)
	}
	key, err := storage.AnalysisUploadChunkObjectKey(current.ProjectID, current.ID, current.Files[0], current.Files[0].Chunks[0])
	if err != nil || !analysisFixtureObjectExists(t, key) {
		t.Fatal("resumed chunk lost", key, err)
	}
}

// The exit scope is backend-pinned: after the artifact backend configuration
// changes away from the recorded analytical storage, no replica judges any
// generation, so a filesystem proof can never describe another backend.
func TestAnalysisWriterOwnershipRequiresThePinnedBackend(t *testing.T) {
	t.Setenv("APEXLOAD_ANALYSIS_PROJECTS", "proj-a")
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", "filesystem")
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	server, store, ready := nativeQueryAuthorityFixture(t)
	actAsWriterB(server)
	if err := ensureAnalysisBackend(store); err != nil {
		t.Fatal(err)
	}
	interrupted, _ := claimAsWriter(t, store, ready, apiWriterA)
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	actAsWriterB(server, apiWriterA)
	if _, err := server.analysisWriterExitScope(); err == nil {
		t.Fatal("an exit scope was granted on a substituted backend")
	}
	server.reconcileAnalysisWriterIncarnationsOnce(t.Context())
	requireAnalysisImportState(t, store, interrupted.ID, "normalizing")
	// An unknown identity grants nothing, even with a judge that would prove
	// every exit and a valid identity value left in place.
	server.analysisWriterOwnership.Store(&analysisWriterOwnershipRuntime{self: apiWriterB, host: apiHostA, known: false, localFS: localfs.IsLocal, exited: func(procidentity.Identity) bool { return true }})
	if _, err := server.analysisWriterExitScope(); err == nil {
		t.Fatal("an exit scope was granted without this process's identity")
	}
	if bound := server.bindAnalysisWriterIncarnation(t.Context()); bound != t.Context() {
		t.Fatal("an unknown process identity was bound")
	}
	if server.analysisWriterHere()(apiWriterA) {
		t.Fatal("an unknown host judged a writer")
	}
}

// A filesystem backend on a network or FUSE mount (NFS, SMB, EFS, Azure Files,
// blobfuse) can still complete a write after its process is reaped: an exit
// there retires the work like an object store, keeping deletion pending.
func TestAnalysisWriterOwnershipNetworkFilesystemIsNotLocal(t *testing.T) {
	t.Setenv("APEXLOAD_ANALYSIS_PROJECTS", "proj-a")
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", "filesystem")
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	server, store, ready := nativeQueryAuthorityFixture(t)
	network := func(string) (bool, error) { return false, nil }
	actAsWriterBWith(server, network)
	interrupted, interruptedAttempt := claimAsWriter(t, store, ready, apiWriterA)
	writeAnalysisFixtureObject(t, interruptedAttempt.Normalized[0])
	actAsWriterBWith(server, network, apiWriterA)
	scope, err := server.analysisWriterExitScope()
	if err != nil || scope.LocalFilesystem {
		t.Fatal("a network filesystem was judged local", scope.LocalFilesystem, err)
	}
	server.reconcileAnalysisWriterIncarnationsOnce(t.Context())
	requireAnalysisImportState(t, store, interrupted.ID, "failed")
	requireDeletionPending(t, timeslotRequest(t, server, "DELETE", "/api/v1/analysis/imports/"+interrupted.ID, nil), 0, 0, 1, 0, 0)
	if !analysisFixtureObjectExists(t, interruptedAttempt.Normalized[0]) {
		t.Fatal("an output on a network filesystem was deleted after exit alone")
	}
	// A failing probe decides nothing: no scope, so no permanent remote
	// verdict for a filesystem that may well be local.
	actAsWriterBWith(server, func(string) (bool, error) { return true, os.ErrPermission }, apiWriterA)
	if scope, err = server.analysisWriterExitScope(); err == nil {
		t.Fatal("a failed probe granted a scope", scope.LocalFilesystem)
	}
	failedProbe, failedAttempt := claimAsWriter(t, store, ready, apiWriterA)
	server.reconcileAnalysisWriterIncarnationsOnce(t.Context())
	requireAnalysisImportState(t, store, failedProbe.ID, "normalizing")
	if outcome, err := store.ResolveExitedAnalysisAttempt(t.Context(), failedProbe.ProjectID, failedAttempt.ID, storage.AnalysisWriterExitScope{Prover: apiWriterB, Exited: func(procidentity.Identity) bool { return false }}); err != nil || outcome.Proved {
		t.Fatal("a failed probe recorded a verdict", outcome, err)
	}
	// The real probe: this assertion needs a TMPDIR on a local filesystem.
	actAsWriterBWith(server, localfs.IsLocal, apiWriterA)
	if scope, err = server.analysisWriterExitScope(); err != nil || !scope.LocalFilesystem {
		t.Fatal("the local temporary directory was not judged local", scope.LocalFilesystem, err)
	}
	// On this valid, pinned backend an unknown identity still grants nothing.
	server.analysisWriterOwnership.Store(&analysisWriterOwnershipRuntime{self: apiWriterB, host: apiHostA, known: false, localFS: localfs.IsLocal, exited: func(procidentity.Identity) bool { return true }})
	if _, err = server.analysisWriterExitScope(); err == nil {
		t.Fatal("an exit scope was granted without this process's identity")
	}
}

// A controller without a process identity still lists candidates, so its
// refusal to judge them is logged (rate-limited), not silent.
func TestAnalysisWriterOwnershipWarnsWhenItCannotJudge(t *testing.T) {
	t.Setenv("APEXLOAD_ANALYSIS_PROJECTS", "proj-a")
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", "filesystem")
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	server, store, ready := nativeQueryAuthorityFixture(t)
	actAsWriterB(server)
	interrupted, _ := claimAsWriter(t, store, ready, apiWriterA)
	server.analysisWriterOwnership.Store(&analysisWriterOwnershipRuntime{self: apiWriterB, host: apiHostA, known: false, localFS: localfs.IsLocal, exited: func(procidentity.Identity) bool { return true }})
	server.reconcileAnalysisWriterIncarnationsOnce(t.Context())
	server.analysisWriterCursor.mu.Lock()
	warning := server.analysisWriterCursor.lastWarning
	server.analysisWriterCursor.mu.Unlock()
	if warning != "analysis writer exits cannot be judged on this replica" {
		t.Fatalf("no warning for a controller without an identity: %q", warning)
	}
	requireAnalysisImportState(t, store, interrupted.ID, "normalizing")
}

// The judge never creates the filesystem artifact directory. On a fresh
// backend, a writer that died before any artifact write created _analysis
// stays owned: its offset-0 retry stays in flight and DELETE stays pending.
// A later write (or the operator) creates the directory, and the next sweep
// judges the writer.
//
// The probe used here is the real one, so this test needs a TMPDIR on a local
// filesystem.
func TestAnalysisWriterOwnershipArtifactDirectoryIsNeverCreatedByTheJudge(t *testing.T) {
	t.Setenv("APEXLOAD_ANALYSIS_PROJECTS", "proj-a")
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", "filesystem")
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	server, store, in := nativeUploadCursorFixture(t)
	if err := ensureAnalysisBackend(store); err != nil {
		t.Fatal(err)
	}
	directory := analysisArtifactDirectory(currentArchiveBackend())
	// The server's background sweep may probe too, so the record is locked.
	var probedMu sync.Mutex
	var probed []string
	probe := func(path string) (bool, error) {
		probedMu.Lock()
		probed = append(probed, path)
		probedMu.Unlock()
		return localfs.IsLocal(path)
	}
	actAsWriterBWith(server, probe)
	data := []byte("PAR1dataPAR1")
	digest := sha256.Sum256(data)
	request := storage.AnalysisUploadRequest{ProjectID: in.ProjectID, ImportID: in.ID, FileID: in.Files[0].ID, Revision: in.Revision, Offset: 0, Bytes: int64(len(data)), SHA256: hex.EncodeToString(digest[:])}
	exited, err := store.BeginAnalysisUploadWriter(storage.WithAnalysisWriterIncarnation(t.Context(), apiWriterA), request)
	if err != nil {
		t.Fatal(err)
	}
	// A dies before writing anything, so _analysis was never created.
	actAsWriterBWith(server, probe, apiWriterA)
	if _, err = server.analysisWriterExitScope(); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("a missing artifact directory granted a scope", err)
	}
	resetAnalysisWriterWarning(server)
	if server.resolveExitedAnalysisUploadOffset(t.Context(), request) {
		t.Fatal("a writer was judged against a missing artifact directory")
	}
	if warning := lastAnalysisWriterWarning(server); warning != analysisArtifactDirectoryMissingWarning {
		t.Fatalf("no operator warning for the missing artifact directory: %q", warning)
	}
	server.reconcileAnalysisWriterIncarnationsOnce(t.Context())
	if retry, err := store.BeginAnalysisUploadWriter(storage.WithAnalysisWriterIncarnation(t.Context(), apiWriterB), request); retry != nil || !errors.Is(err, storage.ErrAnalysisUploadInFlight) {
		t.Fatal("the stuck first writer's offset was released", retry, err)
	}
	requireDeletionPending(t, timeslotRequest(t, server, "DELETE", "/api/v1/analysis/imports/"+in.ID, nil), 0, 0, 0, 1, 0)
	// DELETE queued the dataset's cleanup, so the sweep now has a candidate:
	// it still cannot judge the writer and creates nothing.
	resetAnalysisWriterWarning(server)
	server.reconcileAnalysisWriterIncarnationsOnce(t.Context())
	if warning := lastAnalysisWriterWarning(server); warning != "analysis writer exits cannot be judged on this replica" {
		t.Fatalf("the sweep with a candidate did not refuse to judge: %q", warning)
	}
	requireDeletionPending(t, timeslotRequest(t, server, "DELETE", "/api/v1/analysis/imports/"+in.ID, nil), 0, 0, 0, 1, 0)
	if _, err = os.Stat(directory); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("the judge, the sweep or DELETE created the artifact directory", err)
	}

	// Another dataset's write creates _analysis as writers always have.
	writeAnalysisFixtureObject(t, analysisObjectKey(in.ProjectID, uuid.NewString(), "other.parquet"))
	info, err := os.Stat(directory)
	if err != nil || !info.IsDir() || runtime.GOOS != "windows" && info.Mode().Perm() != 0700 {
		t.Fatal("the writer did not create the artifact directory with mode 0700", info, err)
	}
	server.reconcileAnalysisWriterIncarnationsOnce(t.Context())
	outcome, err := store.ResolveExitedAnalysisUploadWriter(t.Context(), in.ProjectID, exited.ID, storage.AnalysisWriterExitScope{Prover: apiWriterB, Exited: func(procidentity.Identity) bool { return false }})
	if err != nil || !outcome.Proved || !outcome.Quiesced {
		t.Fatal("the exited writer was not closed once the directory existed", outcome, err)
	}
	if response := timeslotRequest(t, server, "DELETE", "/api/v1/analysis/imports/"+in.ID, nil); response.Code != 204 {
		t.Fatalf("DELETE after the writer was judged: %d %s", response.Code, response.Body.String())
	}
	probedMu.Lock()
	defer probedMu.Unlock()
	if len(probed) == 0 {
		t.Fatal("the artifact directory was never probed")
	}
	for _, path := range probed {
		if path != directory {
			t.Fatalf("probed %s, want the artifact directory %s", path, directory)
		}
	}
}

// Cleanup treats a missing filesystem artifact directory as holding nothing
// to delete and never creates it: neither DELETE's exact deletion nor the
// queued dataset cleanup job.
func TestAnalysisWriterOwnershipCleanupNeverCreatesTheArtifactDirectory(t *testing.T) {
	t.Setenv("APEXLOAD_ANALYSIS_PROJECTS", "proj-a")
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", "filesystem")
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	server, store, in := nativeUploadCursorFixture(t)
	if err := ensureAnalysisBackend(store); err != nil {
		t.Fatal(err)
	}
	directory := analysisArtifactDirectory(currentArchiveBackend())
	if response := timeslotRequest(t, server, "DELETE", "/api/v1/analysis/imports/"+in.ID, nil); response.Code != 204 {
		t.Fatalf("DELETE without writers: %d %s", response.Code, response.Body.String())
	}
	if err := server.retryAnalysisCleanupJob(t.Context(), store, storage.AnalysisCleanupJob{Kind: "dataset", ProjectID: in.ProjectID, ResourceID: in.ID}); err != nil {
		t.Fatal("dataset cleanup on a missing artifact directory", err)
	}
	if _, err := os.Stat(directory); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("cleanup created the artifact directory", err)
	}
}

func resetAnalysisWriterWarning(server *Server) {
	server.analysisWriterCursor.mu.Lock()
	server.analysisWriterCursor.lastWarn = time.Time{}
	server.analysisWriterCursor.lastMissingWarn = time.Time{}
	server.analysisWriterCursor.lastWarning = ""
	server.analysisWriterCursor.mu.Unlock()
}

func lastAnalysisWriterWarning(server *Server) string {
	server.analysisWriterCursor.mu.Lock()
	defer server.analysisWriterCursor.mu.Unlock()
	return server.analysisWriterCursor.lastWarning
}

// A pass that begins mid-inventory (another pass moved the shared cursor)
// wraps once, so it still reaches the normalizing owners it would otherwise
// leave to the next pass.
func TestAnalysisWriterOwnershipSweepWrapsOnce(t *testing.T) {
	t.Setenv("APEXLOAD_ANALYSIS_PROJECTS", "proj-a")
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", "filesystem")
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	server, store, ready := nativeQueryAuthorityFixture(t)
	actAsWriterB(server)
	for _, cursor := range []string{"j:", "i:ffffffff-ffff-ffff-ffff-ffffffffffff", "j:00000000"} {
		interrupted, attempt := claimAsWriter(t, store, ready, apiWriterA)
		writeAnalysisFixtureObject(t, attempt.Normalized[0])
		actAsWriterB(server, apiWriterA)
		server.analysisWriterCursor.mu.Lock()
		server.analysisWriterCursor.after = cursor
		server.analysisWriterCursor.mu.Unlock()
		server.reconcileAnalysisWriterIncarnationsOnce(t.Context())
		requireAnalysisImportState(t, store, interrupted.ID, "failed")
		actAsWriterB(server)
	}
}

// The production runtime, as a server without an override uses it: this
// process's real identity, the real exit judge and the real filesystem probe.
func TestAnalysisWriterOwnershipDefaultRuntimeIsTheRealProcess(t *testing.T) {
	defaults := (*Server)(nil).analysisWriterRuntime()
	supported := runtime.GOOS == "darwin" || runtime.GOOS == "linux" || runtime.GOOS == "windows"
	if !defaults.known {
		if supported {
			t.Fatalf("no process identity by default on %s", runtime.GOOS)
		}
		t.Skip("process identity unavailable on this platform")
	}
	self, err := procidentity.Self()
	if err != nil || defaults.self != self {
		t.Fatal("the default identity is not this process", defaults.self, self, err)
	}
	host, err := procidentity.CurrentHost()
	if err != nil || defaults.host != host {
		t.Fatal("the default host is not this host", defaults.host, host, err)
	}
	if defaults.exited == nil || defaults.exited(self) {
		t.Fatal("the default judge called this live process exited")
	}
	child := exec.Command(os.Args[0], "-test.run=^TestAnalysisWriterOwnershipHelperProcess$")
	child.Env = append(os.Environ(), "APEXLOAD_WRITER_OWNERSHIP_HELPER=wait")
	alive, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = child.Start(); err != nil {
		t.Fatal(err)
	}
	defer alive.Close()
	identity, err := procidentity.Of(child.Process.Pid)
	if err != nil {
		_ = child.Process.Kill()
		_ = child.Wait()
		t.Fatal(err)
	}
	if defaults.exited(identity) {
		_ = child.Process.Kill()
		_ = child.Wait()
		t.Fatal("the default judge called a live child exited")
	}
	_ = child.Process.Kill()
	_ = child.Wait()
	if !defaults.exited(identity) {
		t.Fatal("the default judge called a reaped child alive")
	}
	if defaults.localFS == nil {
		t.Fatal("no default filesystem probe")
	}
	if local, err := defaults.localFS(filepath.Join(t.TempDir(), "missing")); local || !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("the default probe judged a missing path", local, err)
	}
}

func TestAnalysisWriterOwnershipMintTimeVolumeIdentityAndUnmountedShareRefusal(t *testing.T) {
	t.Setenv("APEXLOAD_ANALYSIS_PROJECTS", "proj-a")
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", "filesystem")
	archiveDir := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", archiveDir)
	server, store, in := nativeUploadCursorFixture(t)
	if err := ensureAnalysisBackend(store); err != nil {
		t.Fatal(err)
	}
	// Controller A mints an upload writer via bindAnalysisWriterIncarnation
	// before writing any chunk bytes to disk, then crashes.
	server.analysisWriterOwnership.Store(&analysisWriterOwnershipRuntime{
		self:    apiWriterA,
		host:    apiHostA,
		known:   true,
		localFS: assumeLocalFilesystem,
		exited:  func(procidentity.Identity) bool { return false },
	})
	data := []byte("PAR1dataPAR1")
	digest := sha256.Sum256(data)
	request := storage.AnalysisUploadRequest{
		ProjectID: in.ProjectID,
		ImportID:  in.ID,
		FileID:    in.Files[0].ID,
		Revision:  in.Revision,
		Offset:    0,
		Bytes:     int64(len(data)),
		SHA256:    hex.EncodeToString(digest[:]),
	}
	exited, err := store.BeginAnalysisUploadWriter(server.bindAnalysisWriterIncarnation(t.Context()), request)
	if err != nil {
		t.Fatal(err)
	}
	volID, err := store.GetAnalysisVolumeIdentity()
	if err != nil || volID == "" {
		t.Fatal("mint-time volume identity was not bound in store", volID, err)
	}
	if exited.Volume == nil || exited.Volume.Identity != volID || !exited.Volume.LocalFilesystem {
		t.Fatal("writer did not record mint-time volume identity and locality", exited.Volume)
	}
	sourceFile := filepath.Join(analysisArtifactDirectory(currentArchiveBackend()), analysisVolumeIdentityFileName)
	if info, statErr := os.Stat(sourceFile); statErr != nil || !info.Mode().IsRegular() {
		t.Fatal("_analysis/source.v1 was not materialized at mint time", statErr)
	}
	// Controller B starts after A crashed before writing chunk 0: because
	// source.v1 was materialized at mint time, B immediately resolves A's
	// offset and completes DELETE without needing another dataset write.
	actAsWriterB(server, apiWriterA)
	if !server.resolveExitedAnalysisUploadOffset(t.Context(), request) {
		t.Fatal("mint-time source.v1 did not allow immediate pre-write crash recovery")
	}
	if response := timeslotRequest(t, server, "DELETE", "/api/v1/analysis/imports/"+in.ID, nil); response.Code != 204 {
		t.Fatalf("DELETE after pre-write crash recovery: %d %s", response.Code, response.Body.String())
	}

	// Simulate an unmounted share or replaced directory where _analysis exists
	// as an empty mountpoint without source.v1: exit scope and backend check
	// must fail closed.
	if err := os.Remove(sourceFile); err != nil {
		t.Fatal(err)
	}
	if _, err := server.analysisWriterExitScope(); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("unmounted share without source.v1 granted exit scope", err)
	}
	if err := ensureAnalysisBackend(store); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("unmounted share without source.v1 passed ensureAnalysisBackend", err)
	}
	// Simulate a wrong volume mounted at the same path.
	if err := os.WriteFile(sourceFile, []byte(uuid.NewString()+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := server.analysisWriterExitScope(); err == nil {
		t.Fatal("mismatched volume source.v1 granted exit scope")
	}
}

func TestAnalysisWriterOwnershipStagingDirectoryReclaimedAfterControllerAndWorkerExit(t *testing.T) {
	if _, err := procidentity.CurrentHost(); err != nil {
		t.Skipf("process identity unavailable: %v", err)
	}
	t.Setenv("APEXLOAD_ANALYSIS_PROJECTS", "proj-a")
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", "filesystem")
	archiveDir := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", archiveDir)
	server, _, _ := nativeQueryAuthorityFixture(t)

	child := exec.Command(os.Args[0], "-test.run=^TestAnalysisWriterOwnershipHelperProcess$")
	child.Env = append(os.Environ(), "APEXLOAD_WRITER_OWNERSHIP_HELPER=wait")
	alive, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer alive.Close()
	reaped := false
	t.Cleanup(func() {
		if !reaped {
			_ = child.Process.Kill()
			_ = child.Wait()
		}
	})
	workerID, err := procidentity.Of(child.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}

	cacheRoot, err := nativeAnalysisCacheRoot()
	if err != nil {
		t.Fatal(err)
	}
	datasetID := uuid.NewString()
	attemptID := uuid.NewString()
	jobDir := filepath.Join(cacheRoot, "job-"+datasetID+"-"+attemptID+"-12345")
	attemptDir := filepath.Join(jobDir, "native-"+attemptID+"-67890")
	if err := os.MkdirAll(attemptDir, 0700); err != nil {
		t.Fatal(err)
	}
	stagingOwnerJSON, err := json.Marshal(map[string]any{
		"version":    1,
		"controller": apiWriterA,
		"dataset_id": datasetID,
		"attempt_id": attemptID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(jobDir, ".staging-owner.json"), stagingOwnerJSON, 0600); err != nil {
		t.Fatal(err)
	}
	workerOwnerJSON, err := json.Marshal(map[string]any{
		"version":    1,
		"controller": apiWriterA,
		"attempt_id": attemptID,
		"launching":  false,
		"worker":     workerID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(attemptDir, ".worker-owner.json"), workerOwnerJSON, 0600); err != nil {
		t.Fatal(err)
	}

	// Controller A has exited, but its spawned worker child is still alive:
	// staging directory must NOT be reclaimed yet.
	server.analysisWriterOwnership.Store(&analysisWriterOwnershipRuntime{
		self:    apiWriterB,
		host:    apiHostA,
		known:   true,
		localFS: assumeLocalFilesystem,
		exited: func(id procidentity.Identity) bool {
			if id == apiWriterA {
				return true
			}
			return procidentity.Exited(id)
		},
	})
	server.reconcileAnalysisWriterIncarnationsOnce(t.Context())
	if _, err := os.Stat(jobDir); err != nil {
		t.Fatal("staging directory was reclaimed while worker child was still running", err)
	}

	// Kill and reap the worker child: next sweep proves both controller and
	// worker exited and reclaims the staging directory.
	_ = child.Process.Kill()
	_ = child.Wait()
	reaped = true
	server.reconcileAnalysisWriterIncarnationsOnce(t.Context())
	if _, err := os.Stat(jobDir); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("staging directory was not reclaimed after both controller and worker exited", err)
	}
}

func TestAnalysisWriterOwnershipAdminInterventionResolvesRemoteAndUnjudgeableWriters(t *testing.T) {
	for _, backend := range []string{archiveBackendS3, archiveBackendAzureBlob, archiveBackendGCS} {
		t.Run(backend, func(t *testing.T) {
			t.Setenv("APEXLOAD_ANALYSIS_PROJECTS", "proj-a")
			t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
			t.Setenv("APEXLOAD_ARCHIVE_BACKEND", backend)
			t.Setenv("APEXLOAD_ARCHIVE_BUCKET", "analysis-writer-intervention")
			t.Setenv("APEXLOAD_ARCHIVE_CONTAINER", "analysis-writer-intervention")
			server, store, ready := nativeQueryAuthorityFixture(t)
			actAsWriterB(server)

			deleted, deletedAttempt := claimAsWriter(t, store, ready, apiWriterA)
			requireDeletionPending(t, timeslotRequest(t, server, "DELETE", "/api/v1/analysis/imports/"+deleted.ID, nil), 0, 0, 0, 1, 0)

			// Controller A exits on a remote object store: status transitions to
			// remote_commits_unproven=1 and DELETE stays pending.
			actAsWriterB(server, apiWriterA)
			server.reconcileAnalysisWriterIncarnationsOnce(t.Context())
			requireDeletionPending(t, timeslotRequest(t, server, "DELETE", "/api/v1/analysis/imports/"+deleted.ID, nil), 0, 0, 1, 0, 0)

			bucket := memblob.OpenBucket(nil)
			t.Cleanup(func() { _ = bucket.Close() })
			pinned := currentArchiveBackend()
			lateKey := filepath.ToSlash(filepath.Join(pinned.Prefix, "_analysis", deletedAttempt.Normalized[0]))
			if err := bucket.WriteAll(t.Context(), lateKey, []byte("late remote commit"), nil); err != nil {
				t.Fatal(err)
			}
			// Short reason is rejected with 400.
			badResp := timeslotRequest(t, server, "POST", "/api/v1/analysis/imports/"+deleted.ID+"/attest-writer-stopped", map[string]any{"reason": "no"})
			if badResp.Code != 400 {
				t.Fatalf("expected 400 for short attestation reason, got %d %s", badResp.Code, badResp.Body.String())
			}
			// Admin attests that the writer and any in-flight remote commits have stopped.
			attestResp := timeslotRequest(t, server, "POST", "/api/v1/analysis/imports/"+deleted.ID+"/attest-writer-stopped", map[string]any{
				"reason": "verified pod terminated and remote object store multipart uploads aborted",
			})
			if attestResp.Code != 200 {
				t.Fatalf("attest-writer-stopped failed: %d %s", attestResp.Code, attestResp.Body.String())
			}
			var attested storage.AnalysisImport
			if err := json.Unmarshal(attestResp.Body.Bytes(), &attested); err != nil || attested.WriterAttestation == nil || attested.WriterAttestation.Reason == "" {
				t.Fatalf("attested import payload invalid: %+v (%v)", attested, err)
			}
			// Now AttemptCleanupPlan is unblocked and removes the late remote object.
			ops := defaultAnalysisCleanupPhysicalOps
			ops.openBucket = func(context.Context, archiveBackendRuntime) (analysisCleanupBucket, error) {
				return analysisNoCloseBucket{bucket}, nil
			}
			ctx, _ := cleanupOwnerContext(t, 4, ops)
			if err := server.retryAnalysisCleanupJob(ctx, store, storage.AnalysisCleanupJob{Kind: "attempt", ProjectID: deleted.ProjectID, ResourceID: deletedAttempt.ID}); err != nil {
				t.Fatal(err)
			}
			if exists, err := bucket.Exists(t.Context(), lateKey); err != nil || exists {
				t.Fatal("attested remote attempt cleanup did not delete late object", exists, err)
			}
		})
	}
}
