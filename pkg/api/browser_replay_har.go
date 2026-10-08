package api

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/harsynth"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/protocol/browser"
)

// Browser replay HARs (D7). A browser replay records a native HAR through the
// browser's own capture, owner and scrub path; when it closes, the manager
// copies the published HAR into its own private directory and retires the
// original, so the HAR directory keeps nothing for replays. A replay without
// a native HAR (recording off, or it failed) still has a synthetic HAR built
// from its report's retained network entries. Downloads need editor access to
// the replay's project.

const replayHARReadTimeout = 30 * time.Second

// replayHARStore holds the native HAR copies of a manager's replay sessions.
type replayHARStore struct {
	mu    sync.Mutex
	dir   string
	files map[string]string // session ID → copy
}

// retainReplayHAR copies a replay's native HAR (the first; a replay drives one
// browser context) out of the HAR directory and retires every published
// original. It returns false when no copy was kept.
func (m *BrowserReplayManager) retainReplayHAR(sessionID string, artifacts []protocol.BrowserHARArtifact) bool {
	if m == nil || len(artifacts) == 0 {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), replayHARReadTimeout)
	defer cancel()
	kept := false
	for k, artifact := range artifacts {
		var path string
		identity, err := browser.ReadRuntimeHARArtifact(ctx, artifact.Path, func(r io.Reader, size int64) error {
			if k > 0 {
				return nil
			}
			copied, err := m.har.write(sessionID, r, size)
			path = copied
			return err
		})
		if err != nil {
			slog.Warn("browser replay HAR was not retained", "session_id", sessionID, "err", err)
			continue
		}
		if k == 0 && path != "" {
			kept = true
		}
		if err := browser.RetireRuntimeHARArtifact(ctx, artifact.Path, identity); err != nil {
			slog.Warn("browser replay HAR stays in its HAR directory until retention", "session_id", sessionID, "err", err)
		}
	}
	return kept
}

func (s *replayHARStore) write(sessionID string, r io.Reader, size int64) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dir == "" {
		dir, err := os.MkdirTemp("", "apexload-replay-har-")
		if err != nil {
			return "", err
		}
		s.dir, s.files = dir, map[string]string{}
	}
	file, err := os.CreateTemp(s.dir, "replay-*.har")
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
	if err != nil {
		_ = os.Remove(file.Name())
		return "", err
	}
	if previous := s.files[sessionID]; previous != "" {
		_ = os.Remove(previous)
	}
	s.files[sessionID] = file.Name()
	return file.Name(), nil
}

func (s *replayHARStore) path(sessionID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.files[sessionID]
}

// sweep removes the copies of sessions the manager no longer holds.
func (s *replayHARStore) sweep(live func(string) bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, path := range s.files {
		if !live(id) {
			_ = os.Remove(path)
			delete(s.files, id)
		}
	}
}

func (s *replayHARStore) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dir != "" {
		_ = os.RemoveAll(s.dir)
	}
	s.dir, s.files = "", nil
}

// sweepReplayHARs drops the HAR copies of pruned sessions.
func (m *BrowserReplayManager) sweepReplayHARs() {
	m.har.sweep(func(id string) bool {
		m.mu.RLock()
		defer m.mu.RUnlock()
		_, ok := m.sessions[id]
		return ok
	})
}

// replayHAR is a replay's downloadable HAR.
type replayHAR struct {
	data     []byte
	fidelity string
	summary  *harsynth.Summary
}

// replayHARFor returns a terminal replay's native HAR copy, or a synthetic
// one from its report. ok is false while it runs or without a report.
func (m *BrowserReplayManager) replayHARFor(session *BrowserReplaySession) (replayHAR, bool, error) {
	if session == nil || !replaySessionStatusTerminal(session.Status) {
		return replayHAR{}, false, nil
	}
	if path := m.har.path(session.ID); path != "" {
		data, err := os.ReadFile(path)
		if err == nil {
			return replayHAR{data: data, fidelity: "native"}, true, nil
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

func (s *Server) browserReplayHARForRequest(w http.ResponseWriter, r *http.Request) (*BrowserReplaySession, replayHAR, bool) {
	sessionID := r.PathValue("session_id")
	authorizedProjectID, _ := authorizedProjectFromRequest(r)
	session, ok := s.browserReplay.GetForProject(sessionID, authorizedProjectID)
	if !ok {
		writeError(w, http.StatusNotFound, "browser replay session not found")
		return nil, replayHAR{}, false
	}
	if !replaySessionStatusTerminal(session.Status) {
		writeError(w, http.StatusConflict, "the replay is still running; its HAR is available when it ends")
		return nil, replayHAR{}, false
	}
	har, ok, err := s.browserReplay.replayHARFor(session)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read the replay HAR")
		return nil, replayHAR{}, false
	}
	if !ok {
		writeError(w, http.StatusNotFound, "this replay has no HAR")
		return nil, replayHAR{}, false
	}
	return session, har, true
}

func (s *Server) handleDownloadBrowserReplayHAR(w http.ResponseWriter, r *http.Request) {
	session, har, ok := s.browserReplayHARForRequest(w, r)
	if !ok {
		return
	}
	writeBrowserHARHeaders(w, "replay-"+session.ID+".har", har.fidelity)
	if har.summary != nil {
		w.Header().Set("X-APEXLoad-HAR-Completeness", har.summary.Completeness)
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(har.data)
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
// (har.json).
func (s *Server) handleDownloadBrowserReplayEvidence(w http.ResponseWriter, r *http.Request) {
	session, har, ok := s.browserReplayHARForRequest(w, r)
	if !ok {
		return
	}
	report, err := json.MarshalIndent(BrowserReplaySessionResponse{Session: session}, "", "  ")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to encode the replay report")
		return
	}
	sum := sha256.Sum256(har.data)
	metadata := browserReplayHARMetadata{Fidelity: har.fidelity, CaptureMode: "native_recording", SHA256: hex.EncodeToString(sum[:]), Bytes: int64(len(har.data))}
	if har.summary != nil {
		entries := har.summary.Entries
		metadata.CaptureMode, metadata.Completeness, metadata.Entries, metadata.Omitted = harsynth.CaptureReplayNetworkEntries, har.summary.Completeness, &entries, har.summary.Omitted
	}
	metadataJSON, _ := json.MarshalIndent(metadata, "", "  ")
	var out bytes.Buffer
	archive := zip.NewWriter(&out)
	for _, file := range []struct {
		name string
		data []byte
	}{{"report.json", report}, {"replay.har", har.data}, {"har.json", metadataJSON}} {
		entry, err := archive.CreateHeader(&zip.FileHeader{Name: file.name, Method: zip.Deflate, Modified: time.Now().UTC()})
		if err == nil {
			_, err = entry.Write(file.data)
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to build the replay evidence")
			return
		}
	}
	if err := archive.Close(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build the replay evidence")
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", "attachment; filename=\"replay-"+session.ID+"-evidence.zip\"")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out.Bytes())
}
