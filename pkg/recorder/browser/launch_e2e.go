//go:build recording_e2e

package browser

import (
	"fmt"
	"os"
	"strconv"
	"time"

	playwright "github.com/mxschmitt/playwright-go"
)

// Only explicitly built test binaries admit a loopback debugging port. The
// dashboard still starts the real recorder; tests attach to drive its browser.
func recordingTestLaunchOptions(options *playwright.BrowserTypeLaunchOptions) error {
	raw := os.Getenv("APEXLOAD_RECORDING_E2E_CDP_PORT")
	if raw == "" {
		return nil
	}
	port, err := strconv.Atoi(raw)
	if err != nil || port < 1024 || port > 65535 {
		return fmt.Errorf("APEXLOAD_RECORDING_E2E_CDP_PORT must be a port between 1024 and 65535")
	}
	options.Args = append(options.Args, "--remote-debugging-address=127.0.0.1", "--remote-debugging-port="+strconv.Itoa(port))
	return nil
}

// A file gate makes stuck-stop recovery reproducible without changing REST
// contracts. ForceStop releases done. A deadline also bounds an abandoned test.
func recordingTestBeforeStop(done <-chan struct{}) {
	path := os.Getenv("APEXLOAD_RECORDING_E2E_STOP_GATE")
	if path == "" {
		return
	}
	timeout := time.NewTimer(30 * time.Second)
	defer timeout.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(path); err != nil {
			return
		}
		select {
		case <-done:
			return
		case <-timeout.C:
			return
		case <-ticker.C:
		}
	}
}
