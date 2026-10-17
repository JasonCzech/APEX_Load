package api

import (
	"errors"
	"net/http"
	"runtime"

	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/protocol/browser"
	"github.com/jasonczech/apexload/pkg/recordbridge"
)

var browserNativeHARSupported = browser.NativeHARSupported
var errReplayHARPlatformUnsupported = errors.New("record_har is not available: native HAR capture is not supported on " + runtime.GOOS + "; start the replay without record_har to get the synthetic HAR built from its network entries")

func retireReplayHARArtifacts(artifacts []protocol.BrowserHARArtifact) {
	recordbridge.RetireReplayHARArtifacts(artifacts)
}

type browserReplayHARMetadata = recordbridge.BrowserReplayHARMetadata

// OpenAPI-Responses: 404, 409, 500, 503
func (s *Server) handleDownloadBrowserReplayHAR(w http.ResponseWriter, r *http.Request) {
	s.recordingHTTPService().HandleDownloadBrowserReplayHAR(w, r)
}

// OpenAPI-Responses: 404, 409, 500, 503
func (s *Server) handleDownloadBrowserReplayEvidence(w http.ResponseWriter, r *http.Request) {
	s.recordingHTTPService().HandleDownloadBrowserReplayEvidence(w, r)
}
