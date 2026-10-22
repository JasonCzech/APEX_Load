package recordbridge

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/jasonczech/apexload/pkg/harsynth"
)

// readBrowserReplayHAR resolves a request's replay HAR and takes a
// download slot; the caller calls the returned release.
func (s *Service) readBrowserReplayHAR(w http.ResponseWriter, r *http.Request) (*BrowserReplaySession, ReplayHAR, func(), bool) {
	id := r.PathValue("session_id")
	project := s.authorizedProject(r, "")
	var session *BrowserReplaySession
	var local bool
	if s.Replay != nil {
		session, local = s.Replay.GetForProject(id, project)
	}
	if local {
		if !ReplaySessionStatusTerminal(session.Status) {
			s.writeError(w, http.StatusConflict, "the replay is still running; its HAR is available when it ends")
			return nil, ReplayHAR{}, nil, false
		}
		if !session.WorkerSettled() {
			w.Header().Set("Retry-After", "2")
			s.writeError(w, http.StatusConflict, "the replay is still closing; its HAR is available when it has closed")
			return nil, ReplayHAR{}, nil, false
		}
	}
	retained := !local || session.ArtifactDurability() == "durable"
	if retained && s.RetainedReplay == nil {
		if local {
			s.WriteRetainedReplayError(w, ErrRetainedReplayUnavailable)
		} else {
			s.WriteRetainedReplayError(w, ErrRetainedReplayNotFound)
		}
		return nil, ReplayHAR{}, nil, false
	}
	release := func() {}
	if s.Replay != nil {
		var admitted bool
		release, admitted = s.Replay.AcquireHARDownload()
		if !admitted {
			s.WriteRetainedReplayError(w, ErrRetainedReplayBusy)
			return nil, ReplayHAR{}, nil, false
		}
	}
	var har ReplayHAR
	if retained {
		var err error
		session, har, err = s.RetainedReplay.HAR(r.Context(), id, project)
		if err != nil {
			release()
			s.WriteRetainedReplayError(w, err)
			return nil, ReplayHAR{}, nil, false
		}
	} else {
		var ok bool
		var err error
		har, ok, err = s.Replay.ReplayHARFor(session)
		if err != nil {
			release()
			s.writeError(w, http.StatusInternalServerError, "failed to read the replay HAR")
			return nil, ReplayHAR{}, nil, false
		}
		if !ok {
			release()
			s.writeError(w, http.StatusNotFound, "this replay has no HAR")
			return nil, ReplayHAR{}, nil, false
		}
	}
	writeReplayDurabilityHeader(w, session)
	return session, har, func() { har.Close(); release() }, true
}

// OpenAPI-Responses: 404, 409, 500, 503
func (s *Service) HandleDownloadBrowserReplayHAR(w http.ResponseWriter, r *http.Request) {
	// A replay HAR may take longer than the server's write timeout.
	s.extendWriteDeadline(w)
	session, har, release, ok := s.readBrowserReplayHAR(w, r)
	if !ok {
		return
	}
	defer release()
	WriteBrowserHARHeaders(w, "replay-"+session.ID+".har", har.Fidelity)
	if har.Summary != nil {
		w.Header().Set("X-APEXLoad-HAR-Completeness", har.Summary.Completeness)
	}
	w.Header().Set("Content-Length", strconv.FormatInt(har.Length(), 10))
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, har.Reader())
}

// BrowserReplayHARMetadata describes the HAR in a replay evidence ZIP.
type BrowserReplayHARMetadata struct {
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
func (s *Service) HandleDownloadBrowserReplayEvidence(w http.ResponseWriter, r *http.Request) {
	s.extendWriteDeadline(w)
	session, har, release, ok := s.readBrowserReplayHAR(w, r)
	if !ok {
		return
	}
	defer release()
	report, err := json.MarshalIndent(struct {
		Session *BrowserReplaySession `json:"session"`
	}{Session: session}, "", "  ")
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "failed to encode the replay report")
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
	written, err := io.Copy(io.MultiWriter(create("replay.har"), digest), har.Reader())
	if err != nil || written != har.Length() {
		slog.Warn("browser replay evidence was cut short", "session_id", session.ID, "err", err)
		panic(http.ErrAbortHandler)
	}
	metadata := BrowserReplayHARMetadata{Fidelity: har.Fidelity, CaptureMode: "native_recording", SHA256: hex.EncodeToString(digest.Sum(nil)), Bytes: written}
	if har.Summary != nil {
		entries := har.Summary.Entries
		metadata.CaptureMode, metadata.Completeness, metadata.Entries, metadata.Omitted = harsynth.CaptureReplayNetworkEntries, har.Summary.Completeness, &entries, har.Summary.Omitted
	}
	metadataJSON, _ := json.MarshalIndent(metadata, "", "  ")
	if _, err := create("har.json").Write(metadataJSON); err != nil {
		panic(http.ErrAbortHandler)
	}
	if err := archive.Close(); err != nil {
		panic(http.ErrAbortHandler)
	}
}

func WriteBrowserHARHeaders(w http.ResponseWriter, filename, fidelity string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+"\"")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-APEXLoad-HAR-Fidelity", fidelity)
}
