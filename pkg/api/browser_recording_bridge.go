package api

import (
	"github.com/jasonczech/apexload/pkg/recordbridge"
	"net/http"
	"time"
)

type BrowserRecorderBridgeConfig = recordbridge.BrowserRecorderBridgeConfig
type BrowserRecordingBridge = recordbridge.BrowserRecordingBridge

func GenerateBrowserRecorderBridgeToken() (string, error) {
	return recordbridge.GenerateBrowserRecorderBridgeToken()
}
func ValidateBrowserRecorderBridgePairingToken(token string) error {
	return recordbridge.ValidateBrowserRecorderBridgePairingToken(token)
}

// NewBrowserRecorderBridgeServer preserves the controller embedding API while
// the standalone agent uses the same recording runtime without the controller.
func NewBrowserRecorderBridgeServer(cfg BrowserRecorderBridgeConfig) *Server {
	if cfg.DependencyProbe == nil {
		probe := browserRecordingDependencyProbe
		cfg.DependencyProbe = func() error {
			if probe != nil {
				return probe()
			}
			return nil
		}
	}
	runtime := recordbridge.NewBrowserRecorderBridgeServer(cfg)
	mux := http.NewServeMux()
	mux.Handle("/", runtime.Handler())
	return &Server{recordingRuntime: runtime, bridgeMode: true, mux: mux}
}
func (s *Server) BridgePairingToken() string {
	if s == nil || s.recordingRuntime == nil {
		return ""
	}
	return s.recordingRuntime.BridgePairingToken()
}
func (s *Server) BridgePairingExpiresAt() time.Time {
	if s == nil || s.recordingRuntime == nil {
		return time.Time{}
	}
	return s.recordingRuntime.BridgePairingExpiresAt()
}

// Header and path aliases keep the controller's generated route contract stable.
const (
	bridgeTokenHeader   = recordbridge.BridgeTokenHeader
	bridgeSessionHeader = recordbridge.BridgeSessionHeader
	bridgePairPath      = recordbridge.BridgePairPath
	bridgeStatusPath    = recordbridge.BridgeStatusPath
)
