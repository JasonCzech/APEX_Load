package recordbridge

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
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
func RetireReplayHARArtifacts(artifacts []protocol.BrowserHARArtifact) {
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
		if dir, err = privateReplayHARTempDir(); err != nil {
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
	if err := ensurePrivateReplayHARRoot(root); err != nil {
		return "", err
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
type ReplayHAR struct {
	stream      io.Reader
	closeStream func()
	file        *os.File
	size        int64
	data        []byte
	Fidelity    string
	Summary     *harsynth.Summary
}

func (h ReplayHAR) Close() {
	if h.closeStream != nil {
		h.closeStream()
	}
	if h.file != nil {
		_ = h.file.Close()
	}
}

func (h ReplayHAR) Reader() io.Reader {
	if h.stream != nil {
		return h.stream
	}
	if h.file != nil {
		return h.file
	}
	return bytes.NewReader(h.data)
}

func (h ReplayHAR) Length() int64 {
	if h.file != nil || h.stream != nil {
		return h.size
	}
	return int64(len(h.data))
}

// replayHARFor returns a terminal replay's native HAR copy, or a synthetic
// one from its report. ok is false while it runs or without a report. The
// caller closes the result.
func (m *BrowserReplayManager) ReplayHARFor(session *BrowserReplaySession) (ReplayHAR, bool, error) {
	if session == nil || !ReplaySessionStatusTerminal(session.Status) {
		return ReplayHAR{}, false, nil
	}
	if session.harCopy != "" {
		file, err := m.har.open(session.harCopy)
		if err == nil {
			info, statErr := file.Stat()
			if statErr != nil {
				_ = file.Close()
				return ReplayHAR{}, false, statErr
			}
			return ReplayHAR{file: file, size: info.Size(), Fidelity: "native"}, true, nil
		}
		if !os.IsNotExist(err) {
			return ReplayHAR{}, false, err
		}
	}
	if session.ArtifactDurability() == "durable" {
		return ReplayHAR{}, false, ErrRetainedReplayUnavailable
	}
	if session.FinalReport == nil {
		return ReplayHAR{}, false, nil
	}
	var out bytes.Buffer
	summary, err := WriteReplaySyntheticHAR(session.FinalReport.Events, &out)
	if err != nil {
		return ReplayHAR{}, false, err
	}
	return ReplayHAR{data: out.Bytes(), Fidelity: harsynth.Fidelity, Summary: &summary}, true, nil
}

// acquireHARDownload takes one of the bounded replay HAR download slots.
func (m *BrowserReplayManager) AcquireHARDownload() (func(), bool) {
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
