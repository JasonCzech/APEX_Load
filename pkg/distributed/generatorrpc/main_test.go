package generatorrpc

import (
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jasonczech/apexload/pkg/config"
)

var isolatedServiceSequence atomic.Uint64

// TestMain pins APEXLOAD_ARCHIVE_DIR to a throwaway temp dir for the whole test
// binary when the operator has not set it, so generator-side archive tests
// never write per-run directories into the module tree. See the package api
// TestMain and config.DefaultArchiveDir for the full rationale. Individual
// tests may still override APEXLOAD_ARCHIVE_DIR via t.Setenv.
func TestMain(m *testing.M) {
	os.Exit(runArchiveSandboxedTests(m))
}

func runArchiveSandboxedTests(m *testing.M) int {
	if strings.TrimSpace(os.Getenv(config.EnvArchiveDir)) == "" {
		dir, err := os.MkdirTemp("", "apexload-generatorrpc-archive-*")
		if err != nil {
			fmt.Fprintln(os.Stderr, "archive test sandbox:", err)
			return 1
		}
		defer os.RemoveAll(dir)
		os.Setenv(config.EnvArchiveDir, dir)
	}
	return m.Run()
}

// newIsolatedService gives ordinary unit tests their own durable generator
// namespace. TestMain intentionally supplies one archive root for the package,
// so reusing a production-like generator ID across shuffled tests would allow a
// completed archive from one test to fence an unrelated test that happens to
// reuse the same test ID. Tests that exercise restart/rehydration semantics call
// NewService directly so they can deliberately share a namespace.
func newIsolatedService(t testing.TB, maxVUsers int, opts ...ServiceOption) *Service {
	t.Helper()
	digest := sha256.Sum256([]byte(t.Name()))
	sequence := isolatedServiceSequence.Add(1)
	return NewService(fmt.Sprintf("generator-test-%x-%d", digest[:8], sequence), maxVUsers, opts...)
}
