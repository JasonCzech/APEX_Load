package browser

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHARMonitorStopsOnActualFileSize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "capture.har.tmp")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	browser := &Browser{harWorkingPath: path}
	browser.startHARMonitor()
	if err := os.Truncate(path, maxRuntimeHARBytes+1); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		browser.harMonitorWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("HAR monitor did not detect oversized file")
	}
	browser.mu.Lock()
	errorSeen := browser.harErrorSeen
	browser.mu.Unlock()
	if !errorSeen {
		t.Fatal("oversized HAR did not mark the session as failed")
	}
}
