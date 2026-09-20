//go:build !recording_e2e

package browser

import playwright "github.com/mxschmitt/playwright-go"

// Release builds never read or enable the recording E2E debugging capability.
func recordingTestLaunchOptions(_ *playwright.BrowserTypeLaunchOptions) error { return nil }

func recordingTestBeforeStop(_ <-chan struct{}) {}
