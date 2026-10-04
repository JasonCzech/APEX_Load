//go:build !linux

package duckdb

import "os"

// Keep the prior platform behavior until a qualified loaded-image authority is
// integrated for its packaged launcher. This pathname is not a kernel witness
// of the mapped image on Windows/macOS, so this delta does not qualify those
// platforms or their DLL/rpath/code-signing and parent-death requirements.
func workerRunningExecutableSourcePath() (string, error) {
	return os.Executable()
}
