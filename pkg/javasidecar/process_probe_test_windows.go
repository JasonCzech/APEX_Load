//go:build windows

package javasidecar

// The only caller is a POSIX process-group regression that skips on Windows.
// Keeping the platform-specific probe out of the common test file lets the
// package's complete test binary continue to cross-compile.
func testProcessExists(int) bool {
	return false
}
