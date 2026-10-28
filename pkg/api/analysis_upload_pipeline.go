package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

// This finite retry record lives inside its admitted physical owner. Its exact
// scope and completion decision cannot be changed by a shutdown, metadata outage
// or a replacement Server. It is process-local retention, not restart proof.
var errAnalysisUploadForbidden = errors.New("upload project authority changed")

type analysisUploadReconciliationRecord struct {
	mu       sync.Mutex
	scope    nativeReconciliationScope
	intent   storage.AnalysisUploadWriter
	accepted bool
}

func (s *Server) bindAnalysisUploadContext(ctx context.Context, writers storage.AnalysisUploadWriterStore) (context.Context, nativeReconciliationScope, error) {
	if err := analysisUploadContextError(ctx); err != nil {
		return nil, nativeReconciliationScope{}, err
	}
	store, ok := writers.(storage.Store)
	if !ok || s == nil || !nativeSameStore(store, s.nativeReconciliationStore) {
		return nil, nativeReconciliationScope{}, errNativeReconciliationRetained
	}
	scope, err := s.nativeReconciliationScope(ctx)
	if err != nil {
		return nil, nativeReconciliationScope{}, err
	}
	if err = ensureAnalysisBackendFor(store, scope.artifacts); err != nil {
		return nil, nativeReconciliationScope{}, err
	}
	if err = analysisUploadContextError(ctx); err != nil {
		return nil, nativeReconciliationScope{}, err
	}
	return context.WithValue(ctx, nativeAnalysisArtifactBackendKey{}, scope.artifacts), scope, nil
}

func (s *Server) writeAnalysisUploadChunk(w http.ResponseWriter, r *http.Request, writers storage.AnalysisUploadWriterStore, in storage.AnalysisImport, file storage.AnalysisImportFile, offset int64, data []byte, digest string) {
	owner, _ := r.Context().Value(analysisUploadOwnerKey{}).(*analysisUploadPhysicalOwner)
	if owner == nil {
		writeError(w, 503, "upload parent ownership unavailable")
		return
	}
	ctx, _, err := s.bindAnalysisUploadContext(r.Context(), writers)
	if err != nil {
		writeError(w, 503, "upload storage authority unavailable")
		return
	}
	r = r.WithContext(ctx)
	end, err := owner.beginOperation()
	if err != nil {
		writeError(w, 503, "upload parent is closing")
		return
	}
	workDone := make(chan struct{})
	if !s.launchBackground(func() {
		returned := false
		defer close(workDone)
		defer end()
		defer func() {
			if recover() != nil || !returned {
				owner.markPhysicalUnknown()
			}
		}()
		s.runAnalysisUploadChunk(ctx, writers, owner, in, file, offset, data, digest)
		returned = true
	}) {
		end()
		writeError(w, 503, "upload writer admission stopped")
		return
	}
	if err = waitAnalysisUploadContext(r.Context(), workDone, analysisUploadOperationTimeout); err != nil {
		writeError(w, 503, "upload write remains owned; reload durable cursor before retry")
		return
	}
	if !owner.physicalJoined() {
		writeError(w, 503, "upload writer closure remains unproven")
		return
	}
	if err = waitAnalysisUploadContext(r.Context(), owner.done, 10*time.Second); err != nil {
		writeError(w, 503, "upload completion reconciliation pending")
		return
	}
	receipt, err := owner.outcome()
	if err != nil || receipt == nil {
		writeError(w, 409, "upload cursor was not committed; reload durable cursor")
		return
	}
	if receipt.State != "accepted" {
		if receipt.Rejection == "metadata_limit" {
			writeError(w, 413, "upload metadata limit exceeded; create a smaller import session")
		} else {
			writeError(w, 409, "upload was revoked or failed; reload durable cursor")
		}
		return
	}
	if err = s.writeAnalysisUploadCursor(w, r, in, file.ID, receipt.Uploaded, receipt.AppliedRevision); err != nil {
		writeAnalysisUploadCursorError(w, err)
	}
}

// The current project/session check is separate from private outliving receipts.
// Return authority is rechecked after each context-free metadata call.
func (s *Server) writeAnalysisUploadCursor(w http.ResponseWriter, r *http.Request, snapshot storage.AnalysisImport, fileID string, uploaded, revision int64) error {
	if project, scoped := authorizedProjectFromRequest(r); scoped && normalizeProjectID(snapshot.ProjectID) != project {
		return errAnalysisUploadForbidden
	}
	if !analysisQueryContextLive(r.Context()) || !nativeSameStore(s.store, s.nativeReconciliationStore) {
		return errNativeReconciliationRetained
	}
	store := s.nativeReconciliationStore
	imports, ok := store.(storage.AnalysisImportStore)
	if !ok {
		return errNativeReconciliationRetained
	}
	project, err := store.GetProject(snapshot.ProjectID)
	if !analysisQueryContextLive(r.Context()) || err != nil || project == nil || project.ID != snapshot.ProjectID {
		return errNativeReconciliationRetained
	}
	current, err := imports.GetAnalysisImport(snapshot.ID)
	if !analysisQueryContextLive(r.Context()) || err != nil || current == nil || current.ID != snapshot.ID || current.ProjectID != snapshot.ProjectID || current.RunID != snapshot.RunID || current.State != "uploading" && current.State != "mapped" || current.Revision != revision {
		return errNativeReconciliationRetained
	}
	for _, file := range current.Files {
		if file.ID == fileID && file.Uploaded == uploaded {
			if _, err = nativeAnalysisArtifactBackend(r.Context()); err != nil || !analysisQueryContextLive(r.Context()) {
				return errNativeReconciliationRetained
			}
			if project, scoped := authorizedProjectFromRequest(r); scoped && normalizeProjectID(snapshot.ProjectID) != project {
				return errAnalysisUploadForbidden
			}
			writeJSON(w, 200, map[string]any{"uploaded": uploaded, "revision": revision})
			return nil
		}
	}
	return errNativeReconciliationRetained
}

func writeAnalysisUploadCursorError(w http.ResponseWriter, err error) {
	if errors.Is(err, errAnalysisUploadForbidden) {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	writeError(w, 409, "upload session authority changed; reload durable cursor")
}

func (s *Server) runAnalysisUploadChunk(ctx context.Context, writers storage.AnalysisUploadWriterStore, owner *analysisUploadPhysicalOwner, in storage.AnalysisImport, file storage.AnalysisImportFile, offset int64, data []byte, digest string) {
	ctx, scope, err := s.bindAnalysisUploadContext(ctx, writers)
	if err != nil {
		owner.finish(nil, err)
		return
	}
	fileDigest := ""
	if offset+int64(len(data)) == file.Size && offset > 0 {
		fileDigest, err = computeAnalysisAssembledFileSHA256(ctx, in.ProjectID, in.ID, file, offset, data)
		if err != nil {
			owner.finish(nil, err)
			return
		}
	}
	// Begin durably records this process as the writer before any I/O. A writer
	// of an exited controller that still blocks this offset is retired first
	// when this host can prove the exit; otherwise the offset stays owned.
	request := storage.AnalysisUploadRequest{ProjectID: in.ProjectID, ImportID: in.ID, FileID: file.ID, Revision: in.Revision, Offset: offset, Bytes: int64(len(data)), SHA256: digest, FileSHA256: fileDigest}
	intent, err := writers.BeginAnalysisUploadWriter(s.bindAnalysisWriterIncarnation(ctx), request)
	if intent == nil && errors.Is(err, storage.ErrAnalysisUploadInFlight) && s.resolveExitedAnalysisUploadOffset(ctx, request) {
		intent, err = writers.BeginAnalysisUploadWriter(s.bindAnalysisWriterIncarnation(ctx), request)
	}
	if intent != nil {
		owner.setIntent(intent)
	}
	if errors.Is(err, storage.ErrAnalysisPublicationUnknown) && intent != nil {
		resolveCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		resolved, resolveErr := writers.ResolveAnalysisUploadWriter(resolveCtx, in.ProjectID, in.ID, intent.ID)
		stop()
		if !analysisQueryContextLive(ctx) || !scope.matches(s) {
			s.retainAnalysisUploadCompletion(owner, scope, false)
			return
		}
		if errors.Is(resolveErr, storage.ErrAnalysisUploadNotCommitted) {
			owner.finish(nil, err)
			return
		}
		if resolveErr != nil || !analysisSameUploadIntent(intent, resolved) || resolved.State != "pending" {
			s.retainAnalysisUploadCompletion(owner, scope, false)
			return
		}
		err = nil
	}
	if err != nil {
		if intent == nil {
			owner.finish(nil, err)
		} else {
			s.retainAnalysisUploadCompletion(owner, scope, false)
		}
		return
	}
	if intent == nil {
		owner.finish(nil, storage.ErrAnalysisAuthority)
		return
	}
	if err = analysisUploadContextError(ctx); err != nil {
		s.retainAnalysisUploadCompletion(owner, scope, false)
		return
	}
	actualDigest, writeErr := analysisObjectWrite(ctx, intent.ObjectKey, bytes.NewReader(data))
	accepted := writeErr == nil && actualDigest == digest && analysisUploadContextError(ctx) == nil
	s.retainAnalysisUploadCompletion(owner, scope, accepted)
}

func computeAnalysisAssembledFileSHA256(ctx context.Context, projectID, importID string, file storage.AnalysisImportFile, tailOffset int64, tail []byte) (string, error) {
	if tailOffset < 0 || tailOffset+int64(len(tail)) != file.Size {
		return "", storage.ErrAnalysisAuthority
	}
	// Each prior chunk is read sequentially and closed before the next opens,
	// so detach from the per-writer descriptor cap while keeping context cancellation.
	readCtx := context.WithValue(ctx, analysisUploadOwnerKey{}, (*analysisUploadPhysicalOwner)(nil))
	hash := sha256.New()
	var cursor int64
	for _, chunk := range file.Chunks {
		if chunk.Offset != cursor || chunk.Size <= 0 || chunk.Offset+chunk.Size > tailOffset {
			return "", storage.ErrAnalysisAuthority
		}
		key, err := storage.AnalysisUploadChunkObjectKey(projectID, importID, file, chunk)
		if err != nil {
			return "", err
		}
		chunkDigest, err := analysisObjectRead(readCtx, key, hash, chunk.Size)
		if err != nil {
			return "", err
		}
		if chunkDigest != chunk.SHA256 {
			return "", storage.ErrAnalysisAuthority
		}
		cursor += chunk.Size
	}
	if cursor != tailOffset {
		return "", storage.ErrAnalysisAuthority
	}
	if len(tail) > 0 {
		_, _ = hash.Write(tail)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func analysisSameUploadIntent(a, b *storage.AnalysisUploadWriter) bool {
	return a != nil && b != nil && a.ID == b.ID && a.ProjectID == b.ProjectID && a.ImportID == b.ImportID && a.FileID == b.FileID && a.Revision == b.Revision && a.Offset == b.Offset && a.Bytes == b.Bytes && a.SHA256 == b.SHA256 && a.FileSHA256 == b.FileSHA256 && a.ObjectKey == b.ObjectKey && a.TemporaryKey == b.TemporaryKey && a.CreatedAt.Equal(b.CreatedAt)
}

func (s *Server) retainAnalysisUploadCompletion(owner *analysisUploadPhysicalOwner, scope nativeReconciliationScope, accepted bool) {
	owner.mu.Lock()
	if owner.intent == nil || owner.reconciliation != nil {
		owner.mu.Unlock()
		return
	}
	owner.reconciliation = &analysisUploadReconciliationRecord{scope: scope, intent: *owner.intent, accepted: accepted}
	owner.mu.Unlock()
	owner.afterPhysicalJoin(func() {
		// A stopped Server can refuse this task. The typed record remains reachable
		// by the next same-namespace Server's bounded startup/periodic inventory.
		s.launchBackground(func() {
			ctx, stop := context.WithTimeout(s.startupContext, 10*time.Second)
			defer stop()
			s.reconcileAnalysisUploadOwner(ctx, owner)
		})
	})
}

func (s *Server) reconcileAnalysisUploadOwner(ctx context.Context, owner *analysisUploadPhysicalOwner) {
	if !analysisQueryContextLive(ctx) || s == nil || owner == nil {
		return
	}
	writers, ok := s.nativeReconciliationStore.(storage.AnalysisUploadWriterStore)
	if !ok {
		return
	}
	owner.mu.Lock()
	record, resolved := owner.reconciliation, owner.resolved
	owner.mu.Unlock()
	if resolved || record == nil || !record.scope.matches(s) || !record.mu.TryLock() {
		return
	}
	defer record.mu.Unlock()
	// Permanent physical admission seal precedes the first SQL completion,
	// including an ambiguous SQL outcome. Metadata retries never reopen I/O.
	if !owner.sealPhysical() || !analysisQueryContextLive(ctx) {
		return
	}
	intent := &record.intent
	receipt, err := writers.ResolveAnalysisUploadWriter(ctx, intent.ProjectID, intent.ImportID, intent.ID)
	if !analysisQueryContextLive(ctx) || !record.scope.matches(s) {
		return
	}
	if errors.Is(err, storage.ErrAnalysisUploadNotCommitted) {
		owner.finish(nil, storage.ErrAnalysisAuthority)
		return
	}
	if err != nil || !analysisSameUploadIntent(intent, receipt) || !analysisQueryContextLive(ctx) || !record.scope.matches(s) {
		return
	}
	if !receipt.PhysicallyClosed {
		receipt, err = writers.CompleteAnalysisUploadWriter(ctx, intent.ProjectID, intent.ID, record.accepted)
	}
	if err == nil && analysisQueryContextLive(ctx) && record.scope.matches(s) && analysisSameUploadIntent(intent, receipt) && receipt.PhysicallyClosed && (record.accepted || receipt.State != "accepted") {
		owner.finish(receipt, nil)
	}
}

func (s *Server) reconcileAnalysisUploadWritersOnce(ctx context.Context) {
	if s == nil || !analysisQueryContextLive(ctx) {
		return
	}
	analysisUploadOwners.Range(func(_, value any) bool {
		owner := value.(*analysisUploadPhysicalOwner)
		if owner.physicalJoined() {
			call, stop := context.WithTimeout(ctx, 5*time.Second)
			s.reconcileAnalysisUploadOwner(call, owner)
			stop()
		}
		return analysisQueryContextLive(ctx)
	})
}
