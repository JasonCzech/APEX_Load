//go:build analytics_duckdb && cgo

package duckdb

import (
	"os"
	"testing"
)

// Production always launches the current controller/desktop executable. Tagged
// package-native equivalence tests use their own exact test executable's first
// TestMain dispatch; no PATH-selected executable or application startup occurs.
func TestMain(m *testing.M) {
	if handled, code := DispatchNativeWorker(); handled {
		os.Exit(code)
	}
	os.Exit(m.Run())
}
