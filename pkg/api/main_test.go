package api

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis/duckdb"
	"github.com/jasonczech/apexload/pkg/config"
	browserprotocol "github.com/jasonczech/apexload/pkg/protocol/browser"
)

const generatorArchiveMinFreeEnv = "APEXLOAD_GENERATOR_ARCHIVE_MIN_FREE_BYTES"

// TestMain ensures this package's tests never write run archives into the
// module tree. Without an explicit APEXLOAD_ARCHIVE_DIR the archive subsystem
// falls back to config.DefaultArchiveDir(); pinning it to a throwaway temp dir
// for the whole test binary keeps even archive tests that forget to override it
// from polluting the repository — which previously accumulated thousands of
// UUID-named directories under pkg/api/artifacts/archives and raced
// `go list all` / wails builds that walk the tree. Individual tests may still
// override APEXLOAD_ARCHIVE_DIR via t.Setenv. It also closes the browsers the
// tests left in the browser protocol's process-wide pool before the Playwright
// driver exits with the test binary: a browser whose driver exits first
// aborts, and macOS records a crash.
func TestMain(m *testing.M) {
	if handled, code := duckdb.DispatchNativeWorker(); handled {
		os.Exit(code)
	}
	code := runArchiveSandboxedTests(m)
	browserprotocol.ShutdownPool(10 * time.Second)
	duckdb.ReleaseNativeWorkerTemplate()
	os.Exit(code)
}

func runArchiveSandboxedTests(m *testing.M) int {
	// Generator archives share the host filesystem with the controller archive
	// sandbox in integration tests. Keep production's 1 GiB reserve intact while
	// making tests independent of the developer or CI worker's current free space.
	if strings.TrimSpace(os.Getenv(generatorArchiveMinFreeEnv)) == "" {
		os.Setenv(generatorArchiveMinFreeEnv, "1")
	}
	if strings.TrimSpace(os.Getenv(config.EnvArchiveDir)) == "" {
		dir, err := os.MkdirTemp("", "apexload-api-archive-*")
		if err != nil {
			fmt.Fprintln(os.Stderr, "archive test sandbox:", err)
			return 1
		}
		defer os.RemoveAll(dir)
		os.Setenv(config.EnvArchiveDir, dir)
	}
	if strings.TrimSpace(os.Getenv(envArchiveTransferDir)) == "" {
		dir, err := os.MkdirTemp("", "apexload-api-transfer-*")
		if err != nil {
			fmt.Fprintln(os.Stderr, "transfer test sandbox:", err)
			return 1
		}
		defer os.RemoveAll(dir)
		os.Setenv(envArchiveTransferDir, dir)
	}
	return m.Run()
}
