package api

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/harsynth"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/protocol/browser"
)

// Browser replay HARs (D7). A browser replay records a native HAR when the
// replay API asks for one (record_har, opt-in) or when its script's own
// capture policy does, through the browser's own capture, owner and scrub
// path; when the browser closes, the manager copies the published HAR into
// its own private directory and retires the original, so the HAR directory
// keeps nothing for replays. The copy belongs to that session record: it is
// kept only for a session that still holds its lease and integrity fence, is
// never served to another session reusing the ID, and is removed when the
// record leaves the manager. A replay without a native HAR (none recorded, or
// the copy failed) still has a synthetic HAR built from its report's retained
// network entries. Downloads need editor access to the replay's project;
// native HARs are streamed from the copy.

const (
	replayHARReadTimeout = 30 * time.Second
	// replayHARRootName is the managed directory under the system temporary
	// directory; each manager uses its own subdirectory, and one left by a
	// process that ended (older than replayHARStaleAfter) is removed.
	replayHARRootName   = "apexload-replay-har"
	replayHARStaleAfter = 24 * time.Hour
	// replayHARMaxDownloads bounds concurrent replay HAR and evidence
	// downloads.
	replayHARMaxDownloads = 4
)

var errReplayHARStoreClosed = errors.New("the replay HAR store is closed")

// replayHARStore holds the native HAR copies of a manager's replay sessions.
type replayHARStore struct {
	mu     sync.Mutex
	dir    string
	closed bool
	owners map[string]*BrowserReplaySession // copy path → owning session record
}

// retainReplayHAR copies a replay's native HAR (the first; a replay drives one
// browser context) out of the HAR directory for session when keep is set,
// and retires every published original. It returns the copy's path, or "".
func (m *BrowserReplayManager) retainReplayHAR(session *BrowserReplaySession, artifacts []protocol.BrowserHARArtifact, keep bool) string {
	if m == nil || len(artifacts) == 0 {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), replayHARReadTimeout)
	defer cancel()
	kept := ""
	for k, artifact := range artifacts {
		var path string
		identity, err := browser.ReadRuntimeHARArtifact(ctx, artifact.Path, func(r io.Reader, size int64) error {
			if k > 0 || !keep {
				return nil
			}
			// A copy that cannot be kept still lets the original be retired:
			// the replay then serves a synthetic HAR.
			copied, err := m.har.write(session, r, size)
			if err != nil {
				slog.Warn("browser replay HAR copy failed; serving a synthetic HAR", "session_id", session.ID, "err", err)
				return nil
			}
			path = copied
			return nil
		})
		if err != nil {
			slog.Warn("browser replay HAR was not retained", "session_id", session.ID, "err", err)
			continue
		}
		if path != "" {
			kept = path
		}
		if err := browser.RetireRuntimeHARArtifact(ctx, artifact.Path, identity); err != nil {
			slog.Warn("browser replay HAR stays in its HAR directory until retention", "session_id", session.ID, "err", err)
		}
	}
	return kept
}

// discardReplayHAR retires HARs that no session keeps.
func (m *BrowserReplayManager) discardReplayHAR(artifacts []protocol.BrowserHARArtifact) {
	m.retainReplayHAR(&BrowserReplaySession{}, artifacts, false)
}

// retireReplayHARArtifacts retires HARs a replay published that nothing
// serves (a script debug session's or a classic replay's, recorded by the
// script's own capture policy), so the HAR directory keeps nothing for them.
func retireReplayHARArtifacts(artifacts []protocol.BrowserHARArtifact) {
	if len(artifacts) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), replayHARReadTimeout)
	defer cancel()
	for _, artifact := range artifacts {
		identity, err := browser.ReadRuntimeHARArtifact(ctx, artifact.Path, func(io.Reader, int64) error { return nil })
		if err != nil {
			slog.Warn("replay HAR was not retired", "err", err)
			continue
		}
		if err := browser.RetireRuntimeHARArtifact(ctx, artifact.Path, identity); err != nil {
			slog.Warn("replay HAR stays in its HAR directory until retention", "err", err)
		}
	}
}

// openLocked makes the store's directory, removing ones left idle for a day
// by processes that ended. A directory that disappeared (another process
// took it for stale) is made again.
func (s *replayHARStore) openLocked() error {
	if s.closed {
		return errReplayHARStoreClosed
	}
	if s.dir != "" {
		if _, err := os.Lstat(s.dir); !os.IsNotExist(err) {
			return nil
		}
		s.dir = ""
	}
	dir, err := openManagedReplayHARDir()
	if err != nil {
		// Not this process's to manage (another user's, a link, other
		// permissions): a private directory of its own, without the sweep.
		if dir, err = os.MkdirTemp("", "apexload-replay-har-"); err != nil {
			return err
		}
	}
	s.dir, s.owners = dir, map[string]*BrowserReplaySession{}
	return nil
}

// openManagedReplayHARDir makes this manager's directory under the managed
// root and removes ones idle for a day (a live manager keeps its own fresh).
// The root must be a real, private directory this process's user owns: never
// a link (which would steer the sweep elsewhere) or another user's.
func openManagedReplayHARDir() (string, error) {
	root := filepath.Join(os.TempDir(), replayHARRootName)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	info, err := os.Lstat(root)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o700 || !replayHAROwnedByProcess(info) {
		return "", errors.New("the managed replay HAR root is not a private directory")
	}
	dir, err := os.MkdirTemp(root, "manager-")
	if err != nil {
		return "", err
	}
	if entries, err := os.ReadDir(root); err == nil {
		for _, entry := range entries {
			if entry.Name() == filepath.Base(dir) || entry.Type()&os.ModeSymlink != 0 || !entry.IsDir() {
				continue
			}
			if info, err := entry.Info(); err == nil && time.Since(info.ModTime()) > replayHARStaleAfter {
				_ = os.RemoveAll(filepath.Join(root, entry.Name()))
			}
		}
	}
	return dir, nil
}

func (s *replayHARStore) write(owner *BrowserReplaySession, r io.Reader, size int64) (string, error) {
	// The copy (up to the HAR size limit) runs outside the lock, so a slow
	// volume never stalls the sweeps on replay request paths or the abandon
	// backstop; the copy is registered only if the store is still the one it
	// was written into.
	s.mu.Lock()
	if err := s.openLocked(); err != nil {
		s.mu.Unlock()
		return "", err
	}
	dir := s.dir
	file, err := os.CreateTemp(dir, "replay-*.har")
	s.mu.Unlock()
	if err != nil {
		return "", err
	}
	copied, err := io.Copy(file, io.LimitReader(r, size+1))
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err == nil && copied != size {
		err = io.ErrUnexpectedEOF
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err == nil && s.closed {
		err = errReplayHARStoreClosed
	}
	if err == nil && s.dir != dir {
		err = errors.New("the replay HAR directory was removed during the copy")
	}
	if err == nil {
		_, err = os.Lstat(file.Name())
	}
	if err != nil {
		_ = os.Remove(file.Name())
		return "", err
	}
	// Touch the directory so a live manager's is never taken for stale.
	now := time.Now()
	_ = os.Chtimes(dir, now, now)
	s.owners[file.Name()] = owner
	return file.Name(), nil
}

// remove drops one copy.
func (s *replayHARStore) remove(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.owners[path]; ok {
		_ = os.Remove(path)
		delete(s.owners, path)
	}
}

// open opens a session's copy, if the store still holds it.
func (s *replayHARStore) open(path string) (*os.File, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.owners[path]; !ok || path == "" {
		return nil, os.ErrNotExist
	}
	return os.Open(path)
}

// sweep removes the copies whose session record the manager no longer holds,
// and keeps the live manager's directory fresh so no other process takes it
// for stale.
func (s *replayHARStore) sweep(live func(*BrowserReplaySession) bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dir != "" {
		now := time.Now()
		_ = os.Chtimes(s.dir, now, now)
	}
	for path, owner := range s.owners {
		if !live(owner) {
			_ = os.Remove(path)
			delete(s.owners, path)
		}
	}
}

// close removes every copy; later writes are refused.
func (s *replayHARStore) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dir != "" {
		_ = os.RemoveAll(s.dir)
	}
	s.dir, s.owners, s.closed = "", nil, true
}

// sweepReplayHARs drops the HAR copies of session records the manager no
// longer holds (pruned, evicted, retired, abandoned or replaced).
func (m *BrowserReplayManager) sweepReplayHARs() {
	m.har.sweep(func(owner *BrowserReplaySession) bool {
		m.mu.RLock()
		defer m.mu.RUnlock()
		return owner != nil && m.sessions[owner.ID] == owner
	})
}

// replayHAR is a replay's downloadable HAR: a native copy (streamed from
// file) or a synthetic one (bounded by the report's retained entries).
type replayHAR struct {
	file     *os.File
	size     int64
	data     []byte
	fidelity string
	summary  *harsynth.Summary
}

func (h replayHAR) close() {
	if h.file != nil {
		_ = h.file.Close()
	}
}

func (h replayHAR) reader() io.Reader {
	if h.file != nil {
		return h.file
	}
	return bytes.NewReader(h.data)
}

func (h replayHAR) length() int64 {
	if h.file != nil {
		return h.size
	}
	return int64(len(h.data))
}

// replayHARFor returns a terminal replay's native HAR copy, or a synthetic
// one from its report. ok is false while it runs or without a report. The
// caller closes the result.
func (m *BrowserReplayManager) replayHARFor(session *BrowserReplaySession) (replayHAR, bool, error) {
	if session == nil || !replaySessionStatusTerminal(session.Status) {
		return replayHAR{}, false, nil
	}
	if session.harCopy != "" {
		file, err := m.har.open(session.harCopy)
		if err == nil {
			info, statErr := file.Stat()
			if statErr != nil {
				_ = file.Close()
				return replayHAR{}, false, statErr
			}
			return replayHAR{file: file, size: info.Size(), fidelity: "native"}, true, nil
		}
		if !os.IsNotExist(err) {
			return replayHAR{}, false, err
		}
	}
	if session.FinalReport == nil {
		return replayHAR{}, false, nil
	}
	var out bytes.Buffer
	summary, err := writeReplaySyntheticHAR(session.FinalReport.Events, &out)
	if err != nil {
		return replayHAR{}, false, err
	}
	return replayHAR{data: out.Bytes(), fidelity: harsynth.Fidelity, summary: &summary}, true, nil
}

// acquireHARDownload takes one of the bounded replay HAR download slots.
func (m *BrowserReplayManager) acquireHARDownload() (func(), bool) {
	if m.harDownloads == nil {
		return func() {}, true
	}
	select {
	case m.harDownloads <- struct{}{}:
		return func() { <-m.harDownloads }, true
	default:
		return nil, false
	}
}

// readBrowserReplayHAR resolves a request's replay HAR and takes a
// download slot; the caller calls the returned release.
func (s *Server) readBrowserReplayHAR(w http.ResponseWriter, r *http.Request) (*BrowserReplaySession, replayHAR, func(), bool) {
	sessionID := r.PathValue("session_id")
	authorizedProjectID, _ := authorizedProjectFromRequest(r)
	session, ok := s.browserReplay.GetForProject(sessionID, authorizedProjectID)
	if !ok {
		writeError(w, http.StatusNotFound, "browser replay session not found")
		return nil, replayHAR{}, nil, false
	}
	if !replaySessionStatusTerminal(session.Status) {
		writeError(w, http.StatusConflict, "the replay is still running; its HAR is available when it ends")
		return nil, replayHAR{}, nil, false
	}
	// A stopped replay is terminal while its worker is still closing the
	// browser and retaining the HAR: that is "retry shortly", not "no HAR".
	// Once the worker has recorded its final state the HAR is final.
	if !session.workerSettled {
		w.Header().Set("Retry-After", "2")
		writeError(w, http.StatusConflict, "the replay is still closing; its HAR is available when it has closed")
		return nil, replayHAR{}, nil, false
	}
	release, admitted := s.browserReplay.acquireHARDownload()
	if !admitted {
		w.Header().Set("Retry-After", "5")
		writeError(w, http.StatusServiceUnavailable, "too many replay HAR downloads are in progress; retry shortly")
		return nil, replayHAR{}, nil, false
	}
	har, ok, err := s.browserReplay.replayHARFor(session)
	if err != nil {
		release()
		writeError(w, http.StatusInternalServerError, "failed to read the replay HAR")
		return nil, replayHAR{}, nil, false
	}
	if !ok {
		release()
		writeError(w, http.StatusNotFound, "this replay has no HAR")
		return nil, replayHAR{}, nil, false
	}
	return session, har, func() { har.close(); release() }, true
}

// OpenAPI-Responses: 404, 409, 500, 503
func (s *Server) handleDownloadBrowserReplayHAR(w http.ResponseWriter, r *http.Request) {
	// A replay HAR may take longer than the server's write timeout.
	extendArtifactWriteDeadline(w)
	session, har, release, ok := s.readBrowserReplayHAR(w, r)
	if !ok {
		return
	}
	defer release()
	writeBrowserHARHeaders(w, "replay-"+session.ID+".har", har.fidelity)
	if har.summary != nil {
		w.Header().Set("X-APEXLoad-HAR-Completeness", har.summary.Completeness)
	}
	w.Header().Set("Content-Length", strconv.FormatInt(har.length(), 10))
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, har.reader())
}

// browserReplayHARMetadata describes the HAR in a replay evidence ZIP.
type browserReplayHARMetadata struct {
	Fidelity     string           `json:"fidelity"`
	CaptureMode  string           `json:"capture_mode"`
	Completeness string           `json:"completeness,omitempty"`
	Entries      *int64           `json:"entries,omitempty"`
	Omitted      map[string]int64 `json:"omitted_entries,omitempty"`
	SHA256       string           `json:"sha256"`
	Bytes        int64            `json:"bytes"`
}

// handleDownloadBrowserReplayEvidence is a terminal replay's evidence ZIP:
// the session (report.json), its HAR (replay.har) and what the HAR is
// (har.json, written last with the digest of the HAR as streamed). The ZIP
// is streamed; a failure after it started aborts the response.
//
// OpenAPI-Responses: 404, 409, 500, 503
func (s *Server) handleDownloadBrowserReplayEvidence(w http.ResponseWriter, r *http.Request) {
	extendArtifactWriteDeadline(w)
	session, har, release, ok := s.readBrowserReplayHAR(w, r)
	if !ok {
		return
	}
	defer release()
	report, err := json.MarshalIndent(BrowserReplaySessionResponse{Session: session}, "", "  ")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to encode the replay report")
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", "attachment; filename=\"replay-"+session.ID+"-evidence.zip\"")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	archive := zip.NewWriter(w)
	create := func(name string) io.Writer {
		entry, err := archive.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: time.Now().UTC()})
		if err != nil {
			panic(http.ErrAbortHandler)
		}
		return entry
	}
	if _, err := create("report.json").Write(report); err != nil {
		panic(http.ErrAbortHandler)
	}
	digest := sha256.New()
	written, err := io.Copy(io.MultiWriter(create("replay.har"), digest), har.reader())
	if err != nil || written != har.length() {
		slog.Warn("browser replay evidence was cut short", "session_id", session.ID, "err", err)
		panic(http.ErrAbortHandler)
	}
	metadata := browserReplayHARMetadata{Fidelity: har.fidelity, CaptureMode: "native_recording", SHA256: hex.EncodeToString(digest.Sum(nil)), Bytes: written}
	if har.summary != nil {
		entries := har.summary.Entries
		metadata.CaptureMode, metadata.Completeness, metadata.Entries, metadata.Omitted = harsynth.CaptureReplayNetworkEntries, har.summary.Completeness, &entries, har.summary.Omitted
	}
	metadataJSON, _ := json.MarshalIndent(metadata, "", "  ")
	if _, err := create("har.json").Write(metadataJSON); err != nil {
		panic(http.ErrAbortHandler)
	}
	if err := archive.Close(); err != nil {
		panic(http.ErrAbortHandler)
	}
}
