// Package recordbridge owns the local recording workflow shared by controller
// routes and the standalone recording agent. Durable controller state is supplied
// through narrow ports; the service never imports the controller API or assets.
package recordbridge

import (
	"net/http"
	"time"

	"github.com/jasonczech/apexload/pkg/recorder"
)

// Service is a request-facing view of long-lived recording managers. Controller
// adapters supply project, audit and response policies; the local bridge supplies
// its paired-session policies. Constructing this view does not start goroutines.
type Service struct {
	Conversion          ConversionPorts
	Library             *MemoryScriptLibrary
	Recording           *BrowserRecordingManager
	Replay              *BrowserReplayManager
	Recorder            *recorder.Recorder
	Install             *InstallState
	Device              *DeviceCapture
	Retention           time.Duration
	Availability        func() BrowserRecordingAvailability
	AuthorizedProject   func(*http.Request, string) string
	CanExportRaw        func(*http.Request, string) bool
	Audit               func(*http.Request, string, string, string, string, string, map[string]any)
	WriteError          func(http.ResponseWriter, int, string)
	DecodeJSON          func(http.ResponseWriter, *http.Request, any) bool
	DecodeOptionalJSON  func(http.ResponseWriter, *http.Request, any) bool
	LogServerError      func(http.ResponseWriter, int, error, ...any) string
	ExtendWriteDeadline func(http.ResponseWriter)
}

func (s *Service) availability() BrowserRecordingAvailability {
	if s.Availability != nil {
		return s.Availability()
	}
	return BrowserRecordingAvailability{Code: browserRecordingUnavailableDisabled, Reason: "browser recording is unavailable"}
}
func (s *Service) authorizedProject(r *http.Request, fallback string) string {
	if s.AuthorizedProject != nil {
		return s.AuthorizedProject(r, fallback)
	}
	return fallback
}
func (s *Service) canExportRaw(r *http.Request, project string) bool {
	return s.CanExportRaw != nil && s.CanExportRaw(r, project)
}
func (s *Service) audit(r *http.Request, action, outcome, kind, id, project string, metadata map[string]any) {
	if s.Audit != nil {
		s.Audit(r, action, outcome, kind, id, project, metadata)
	}
}
func (s *Service) retention() time.Duration { return s.Retention }
func (s *Service) writeError(w http.ResponseWriter, status int, message string) {
	if s.WriteError != nil {
		s.WriteError(w, status, message)
		return
	}
	WriteError(w, status, message)
}
func (s *Service) decodeJSONBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	if s.DecodeJSON != nil {
		return s.DecodeJSON(w, r, dst)
	}
	return DecodeJSONBody(w, r, dst)
}
func (s *Service) decodeOptionalJSONBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	if s.DecodeOptionalJSON != nil {
		return s.DecodeOptionalJSON(w, r, dst)
	}
	return DecodeOptionalJSONBody(w, r, dst)
}
func (s *Service) logServerError(w http.ResponseWriter, status int, cause error, attrs ...any) string {
	if s.LogServerError != nil {
		return s.LogServerError(w, status, cause, attrs...)
	}
	return LogServerError(w, status, cause, attrs...)
}
func (s *Service) extendWriteDeadline(w http.ResponseWriter) {
	if s.ExtendWriteDeadline != nil {
		s.ExtendWriteDeadline(w)
		return
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(ArtifactWriteTimeout()))
}
