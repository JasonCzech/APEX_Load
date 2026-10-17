package api

import "github.com/jasonczech/apexload/pkg/recordbridge"

// Compatibility aliases keep controller callers on the shared recording lifecycle.
type browserRecorderBackend = recordbridge.RecorderBackend
type browserRecorderFactory = recordbridge.RecorderFactory
type BrowserRecordingStartRequest = recordbridge.BrowserRecordingStartRequest
type BrowserRecordingSession = recordbridge.BrowserRecordingSession
type BrowserRecordingManager = recordbridge.BrowserRecordingManager

const BrowserRecordingActive = recordbridge.BrowserRecordingActive
const BrowserRecordingPaused = recordbridge.BrowserRecordingPaused
const BrowserRecordingStopping = recordbridge.BrowserRecordingStopping
const BrowserRecordingStopped = recordbridge.BrowserRecordingStopped
const BrowserRecordingFailed = recordbridge.BrowserRecordingFailed
const browserRecordingStopRequested = "requested"
const browserRecordingStopBrowserClosed = "browser_closed"
const browserRecordingStopForced = "forced"

const defaultBrowserRecordingStopTimeout = recordbridge.DefaultBrowserRecordingStopTimeout
const defaultBrowserRecordingRetention = recordbridge.DefaultBrowserRecordingRetention

var errBrowserRecordingActive = recordbridge.ErrBrowserRecordingActive

func NewBrowserRecordingManager(hub *Hub, options ...recordbridge.RecordingManagerOptions) *BrowserRecordingManager {
	if hub == nil {
		return recordbridge.NewBrowserRecordingManager(nil, options...)
	}
	return recordbridge.NewBrowserRecordingManager(hub, options...)
}
func validBrowserRecordingQUICMode(mode string) bool {
	return recordbridge.ValidBrowserRecordingQUICMode(mode)
}
