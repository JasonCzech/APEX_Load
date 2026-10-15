package browser

import (
	"os"
	"testing"
	"time"
)

// TestMain closes the browsers this package's tests left in the
// process-wide pool before the Playwright driver exits with the test binary.
// A browser whose driver exits first aborts, and macOS records a crash.
func TestMain(m *testing.M) {
	code := m.Run()
	ShutdownPool(10 * time.Second)
	os.Exit(code)
}
