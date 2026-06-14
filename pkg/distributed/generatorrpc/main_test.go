package generatorrpc

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/config"
)

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
