package recordbridge

import (
	"os"
	"runtime"
	"strings"
)

type BrowserRecordingAvailability struct {
	Available bool   `json:"available"`
	Code      string `json:"code,omitempty"`
	Reason    string `json:"reason,omitempty"`
}
type browserRecordingAvailability = BrowserRecordingAvailability

const (
	browserRecordingUnavailableDisabled             = "disabled"
	browserRecordingUnavailableProductionDisabled   = "production_disabled"
	browserRecordingUnavailableDisplayMissing       = "display_missing"
	browserRecordingUnavailableBrowsersNotInstalled = "browsers_not_installed"
)

func RecordingAvailability(local, production bool, probe func() error) BrowserRecordingAvailability {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("APEXLOAD_BROWSER_RECORDER"))) {
	case "off", "disabled", "false", "0":
		return BrowserRecordingAvailability{Code: browserRecordingUnavailableDisabled, Reason: "browser recording is disabled via APEXLOAD_BROWSER_RECORDER"}
	case "on", "enabled", "true", "1":
	default:
		if production && !local {
			return BrowserRecordingAvailability{Code: browserRecordingUnavailableProductionDisabled, Reason: "browser recording launches a headed browser on the controller host and is disabled in production mode (set APEXLOAD_BROWSER_RECORDER=on to override)"}
		}
	}
	if runtime.GOOS == "linux" && strings.TrimSpace(os.Getenv("DISPLAY")) == "" && strings.TrimSpace(os.Getenv("WAYLAND_DISPLAY")) == "" {
		return BrowserRecordingAvailability{Code: browserRecordingUnavailableDisplayMissing, Reason: "no display detected on the controller host for a headed browser"}
	}
	if probe != nil {
		if err := probe(); err != nil {
			if availability, ok := browserRecordingAvailabilityFromStartError(err); ok {
				return availability
			}
			return BrowserRecordingAvailability{Code: browserRecordingUnavailableBrowsersNotInstalled, Reason: "Playwright Chromium is not available on the controller host; install the recording browser and retry"}
		}
	}
	return BrowserRecordingAvailability{Available: true}
}
