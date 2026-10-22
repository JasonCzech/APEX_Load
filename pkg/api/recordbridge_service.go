package api

import "github.com/jasonczech/apexload/pkg/recordbridge"

// recordingHTTPService binds the shared recording workflow to controller policy
// and durable adapters. Managers and install state outlive this request view.
func (s *Server) recordingHTTPService() *recordbridge.Service {
	service := &recordbridge.Service{Recording: s.browserRecording, Recorder: s.recorder, Retention: defaultBrowserRecordingRetention, Availability: s.browserRecordingAvailability, AuthorizedProject: authorizedProjectOr, CanExportRaw: s.canExportRawRecording, Audit: s.recordAuditEvent, WriteError: writeError, DecodeJSON: decodeJSONBody, DecodeOptionalJSON: decodeOptionalJSONBody, LogServerError: logServerError, ExtendWriteDeadline: extendArtifactWriteDeadline}
	if s.replayArtifacts != nil {
		service.RetainedReplay = s.replayArtifacts
	}
	if s.browserReplay != nil {
		service.Replay = s.browserReplay.BrowserReplayManager
	}
	if s.browserInstall != nil {
		service.Install = s.browserInstall.InstallState
	}
	s.populateRecordingConversionPorts(service)
	return service
}
