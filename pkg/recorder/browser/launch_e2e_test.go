//go:build recording_e2e

package browser

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	playwright "github.com/mxschmitt/playwright-go"
)

func TestRecordingE2ELaunchOptions(t *testing.T) {
	for _, tc := range []struct {
		port  string
		valid bool
	}{{"", true}, {"19222", true}, {"0", false}, {"1023", false}, {"65536", false}, {"127.0.0.1:9222", false}, {"9222 --remote-debugging-address=0.0.0.0", false}} {
		t.Run(tc.port, func(t *testing.T) {
			t.Setenv("APEXLOAD_RECORDING_E2E_CDP_PORT", tc.port)
			opts := playwright.BrowserTypeLaunchOptions{Args: []string{"--window-size=1280,900"}}
			err := recordingTestLaunchOptions(&opts)
			if (err == nil) != tc.valid {
				t.Fatalf("error = %v", err)
			}
			want := []string{"--window-size=1280,900"}
			if tc.valid && tc.port != "" {
				want = append(want, "--remote-debugging-address=127.0.0.1", "--remote-debugging-port="+tc.port)
			}
			if !reflect.DeepEqual(opts.Args, want) {
				t.Fatalf("args = %v, want %v", opts.Args, want)
			}
		})
	}
}

func TestRecordingE2EStopGateReleasesOnForceStop(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stop-gate")
	if err := os.WriteFile(path, []byte("hold"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APEXLOAD_RECORDING_E2E_STOP_GATE", path)
	done := make(chan struct{})
	returned := make(chan struct{})
	go func() { recordingTestBeforeStop(done); close(returned) }()
	select {
	case <-returned:
		t.Fatal("gate failed to hold graceful stop")
	case <-time.After(30 * time.Millisecond):
	}
	close(done)
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("force stop did not release gate")
	}
}
