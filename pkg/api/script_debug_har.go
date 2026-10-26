package api

import (
	"bytes"
	"io"
	"net/http"
	"strings"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/recordbridge"
)

// ScriptDebugHARAvailability describes a synthetic export of retained traffic.
type ScriptDebugHARAvailability struct {
	Options              *protocol.ReplayHAROptions `json:"options,omitempty"`
	Eligible             bool                       `json:"eligible"`
	Available            bool                       `json:"available"`
	RetainedRequestCount int64                      `json:"retained_request_count"`
	Reason               string                     `json:"reason,omitempty"`
	Limitations          string                     `json:"limitations"`
}

func scriptDebugHAREvents(session *ScriptDebugSession) []engine.ReplayActionEvent {
	if session.FinalReport != nil {
		return session.FinalReport.Events
	}
	return nil
}

func scriptDebugHARAvailability(session *ScriptDebugSession) ScriptDebugHARAvailability {
	protocol := strings.ToLower(session.Protocol)
	info := ScriptDebugHARAvailability{
		Options:     session.RuntimeProfile.HAROptions.Clone(),
		Eligible:    protocol == "browser" || protocol == "http" || strings.HasPrefix(protocol, "http/") || protocol == "soap" || protocol == "java_http",
		Limitations: "Contains retained traffic only. Bodies, redirect hops, and measured timing phases follow the run’s capture options and may be unavailable. Capture and retention limits apply.",
	}
	switch {
	case !info.Eligible:
		info.Reason = "HAR downloads are available for HTTP and browser scripts."
	case session.CompletedAt == nil || !replaySessionStatusTerminal(session.Status):
		info.Reason = "HAR is available after debugging finishes."
	case session.RuntimeEvidenceUnavailable || session.FinalReport == nil || session.FinalReport.RuntimeEvidenceUnavailable:
		info.Reason = "Runtime evidence unavailable."
	default:
		summary, err := recordbridge.WriteReplaySyntheticHARWithOptions(scriptDebugHAREvents(session), io.Discard, info.Options)
		if err != nil {
			info.Reason = "Retained traffic could not be exported."
			break
		}
		info.RetainedRequestCount = summary.Entries
		info.Available = summary.Entries > 0
		if !info.Available {
			info.Reason = "No network traffic was retained for this run."
		}
	}
	return info
}

// ExportHARForProject uses the same authorized, expiry-checked and disclosure-safe
// snapshot as session reads. The bool distinguishes an inaccessible session from
// a session whose evidence is unavailable.
func (m *ScriptDebugManager) ExportHARForProject(sessionID, projectID string) ([]byte, ScriptDebugHARAvailability, bool) {
	session, ok := m.getForProject(sessionID, projectID)
	if !ok {
		return nil, ScriptDebugHARAvailability{}, false
	}
	info := session.HAR
	if !info.Available {
		return nil, info, true
	}
	var out bytes.Buffer
	if _, err := recordbridge.WriteReplaySyntheticHARWithOptions(scriptDebugHAREvents(session), &out, info.Options); err != nil {
		info.Available = false
		info.Reason = "Retained traffic could not be exported."
		return nil, info, true
	}
	return out.Bytes(), info, true
}

// hideHARAccessDenials keeps authorization and its audit trail intact while
// making missing and inaccessible HAR sessions indistinguishable to callers.
// Existing JSON/log exports keep their established authorization responses.
func hideHARAccessDenials(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("format")), "har") {
			next.ServeHTTP(w, r)
			return
		}
		masked := &scriptDebugHARAccessWriter{ResponseWriter: w}
		next.ServeHTTP(masked, r)
		if masked.status == http.StatusForbidden {
			writeError(w, http.StatusNotFound, "script debug session not found")
		}
	})
}

type scriptDebugHARAccessWriter struct {
	http.ResponseWriter
	status int
}

func (w *scriptDebugHARAccessWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	if status != http.StatusForbidden {
		w.ResponseWriter.WriteHeader(status)
	}
}
func (w *scriptDebugHARAccessWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if w.status == http.StatusForbidden {
		return len(body), nil
	}
	return w.ResponseWriter.Write(body)
}
