package recordbridge

import (
	"errors"
	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/dsl"
	"strings"
)

func ApplyBrowserReplayOverrides(script *dsl.Script, req BrowserReplayStartRequest) *dsl.Script {
	if script == nil || req.Browser == nil {
		return script
	}
	clone := *script
	clone.Config = script.Config
	browserCfg := clone.Config.Browser
	if req.Browser.Engine != "" {
		browserCfg.Engine = req.Browser.Engine
	}
	if req.Browser.Headless != nil {
		browserCfg.Headless = req.Browser.Headless
	}
	if req.Browser.SlowMoMs > 0 {
		browserCfg.SlowMoMs = req.Browser.SlowMoMs
	}
	if req.Browser.UserAgent != "" {
		browserCfg.UserAgent = req.Browser.UserAgent
	}
	if req.Browser.DeviceProfile != "" {
		browserCfg.DeviceProfile = req.Browser.DeviceProfile
	}
	if req.Browser.TouchMode != nil {
		browserCfg.TouchMode = req.Browser.TouchMode
	}
	if req.Browser.Orientation != "" {
		browserCfg.Orientation = req.Browser.Orientation
	}
	if req.Browser.ClientMetrics != nil {
		browserCfg.ClientMetrics = req.Browser.ClientMetrics
	}
	if req.Browser.CaptureScreenshots != "" {
		browserCfg.CaptureScreenshots = req.Browser.CaptureScreenshots
	}
	if req.Browser.Viewport.Width > 0 {
		browserCfg.Viewport.Width = req.Browser.Viewport.Width
	}
	if req.Browser.Viewport.Height > 0 {
		browserCfg.Viewport.Height = req.Browser.Viewport.Height
	}
	if req.Browser.ReviewPolicy.AllowReviewedJavaScript || req.Browser.ReviewPolicy.AllowReviewRequired {
		browserCfg.ReviewPolicy = req.Browser.ReviewPolicy
	}
	clone.Config.Browser = browserCfg
	return &clone
}

func ValidateExternalReplaySessionID(value string) error {
	if value == "" {
		return nil
	}
	if strings.TrimSpace(value) != value {
		return ErrReplaySessionIDInvalid
	}
	parsed, err := uuid.Parse(value)
	if err != nil || parsed.Version() != uuid.Version(4) || parsed.Variant() != uuid.RFC4122 || parsed.String() != value {
		return ErrReplaySessionIDInvalid
	}
	return nil
}

var ErrReplaySessionIDInvalid = errors.New("replay_session_id must be a canonical UUID v4")
