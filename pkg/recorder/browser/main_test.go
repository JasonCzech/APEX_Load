package browser

import (
	"os"
	"testing"
	"time"

	browserprotocol "github.com/jasonczech/apexload/pkg/protocol/browser"
)

// TestMain closes the replay browsers this package's tests left in the
// browser protocol's process-wide pool before the Playwright driver exits
// with the test binary. A browser whose driver exits first aborts, and macOS
// records a crash.
func TestMain(m *testing.M) {
	code := m.Run()
	browserprotocol.ShutdownPool(10 * time.Second)
	os.Exit(code)
}
