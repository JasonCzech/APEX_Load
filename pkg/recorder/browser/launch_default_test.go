//go:build !recording_e2e

package browser

import (
	"reflect"
	"testing"

	playwright "github.com/mxschmitt/playwright-go"
)

func TestReleaseRecorderIgnoresE2EDebugPort(t *testing.T) {
	t.Setenv("APEXLOAD_RECORDING_E2E_CDP_PORT", "19222")
	opts := playwright.BrowserTypeLaunchOptions{Args: []string{"--window-size=1280,900"}}
	if err := recordingTestLaunchOptions(&opts); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(opts.Args, []string{"--window-size=1280,900"}) {
		t.Fatalf("release enabled debugging: %v", opts.Args)
	}
}

func TestReleaseRecorderIgnoresStopGate(t *testing.T) {
	t.Setenv("APEXLOAD_RECORDING_E2E_STOP_GATE", "/")
	recordingTestBeforeStop(nil)
}
