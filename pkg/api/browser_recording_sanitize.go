package api

import "github.com/jasonczech/apexload/pkg/recordbridge"

func sanitizeBrowserRecordingSessionForResponse(session *BrowserRecordingSession) *BrowserRecordingSession {
	return recordbridge.SanitizeBrowserRecordingSessionForResponse(session)
}
