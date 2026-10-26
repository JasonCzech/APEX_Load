package recordbridge

import (
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
)

func DebugRunIdentity(requested, sessionID string) string {
	if id := strings.TrimSpace(requested); id != "" {
		return id
	}
	return "debug-" + sessionID
}
func DebugContinueOnError(requested *bool, script *dsl.Script) bool {
	if requested != nil {
		return *requested
	}
	if script != nil && script.Config.ErrorHandling.ContinueOnErrorSet() {
		return script.Config.ErrorHandling.ContinueOnErrorEnabled()
	}
	return true
}
func DebugFailurePolicy(continueOnError bool) string {
	if continueOnError {
		return "continue_on_error"
	}
	return "stop_on_error"
}
func ReplayRuntimeProfileForBrowser(script *dsl.Script, req BrowserReplayStartRequest, ignoreThinkTime bool) engine.ReplayRuntimeProfile {
	profile := engine.ReplayRuntimeProfile{
		Name:            "browser canary",
		CaptureMode:     "browser_evidence",
		LogLevel:        "info",
		IgnoreThinkTime: ignoreThinkTime,
	}
	if script != nil {
		profile.BrowserEngine = strings.TrimSpace(script.Config.Browser.Engine)
		profile.DeviceProfile = strings.TrimSpace(script.Config.Browser.DeviceProfile)
		if script.Config.Browser.Headless != nil {
			headless := *script.Config.Browser.Headless
			profile.Headless = &headless
		}
		profile.FailurePolicy = DebugFailurePolicy(DebugContinueOnError(req.ContinueOnError, script))
	}
	if req.Browser != nil {
		if req.Browser.Engine != "" {
			profile.BrowserEngine = strings.TrimSpace(req.Browser.Engine)
		}
		if req.Browser.DeviceProfile != "" {
			profile.DeviceProfile = strings.TrimSpace(req.Browser.DeviceProfile)
		}
		if req.Browser.Headless != nil {
			headless := *req.Browser.Headless
			profile.Headless = &headless
		}
	}
	return profile
}
func CloneEngineReplayRuntimeProfile(profile *engine.ReplayRuntimeProfile) engine.ReplayRuntimeProfile {
	if profile == nil {
		return engine.ReplayRuntimeProfile{}
	}
	cloned := *profile
	cloned.HAROptions = profile.HAROptions.Clone()
	if profile.Headless != nil {
		headless := *profile.Headless
		cloned.Headless = &headless
	}
	return cloned
}
