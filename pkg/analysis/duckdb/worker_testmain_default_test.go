//go:build !(analytics_duckdb && cgo)

package duckdb

import (
	"os"
	"testing"
)

// Builds without the native engine have no worker dispatch, but their snapshot
// tests still leave a macOS executable template, which this removes.
func TestMain(m *testing.M) {
	code := m.Run()
	ReleaseNativeWorkerTemplate()
	os.Exit(code)
}
