package config

import (
	"os"
	"path/filepath"
	"strings"
)

// EnvArchiveDir is the operator-owned override for the filesystem location of
// run archives. When unset, archives default to DefaultArchiveDir().
const EnvArchiveDir = "APEXLOAD_ARCHIVE_DIR"

// DefaultArchiveDir returns the filesystem path used for run archives when the
// operator has not set APEXLOAD_ARCHIVE_DIR.
//
// It is deliberately an ABSOLUTE path *outside* the Go module/source tree —
// under the user cache directory, falling back to the OS temp directory. The
// archive subsystem writes one directory per run (UUID-named). The previous
// default ("artifacts/archives", resolved relative to the process working
// directory) meant `go test ./pkg/api/...` and locally-run controllers
// accumulated thousands of these directories *inside* the repository. Anything
// that walks the module tree — `go list all`, `wails build` from desktop/ —
// then either slowed to a crawl or raced against concurrent archive
// create/delete and failed outright ("Repository not found", "no such file or
// directory"). Keeping the default out of the tree removes both failure modes;
// real deployments set APEXLOAD_ARCHIVE_DIR explicitly (e.g.
// /var/lib/apexload/archives, /data/archives).
func DefaultArchiveDir() string {
	base, err := os.UserCacheDir()
	if err != nil || strings.TrimSpace(base) == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "apexload", "archives")
}

// ArchiveDir resolves the filesystem archive root, honoring the
// APEXLOAD_ARCHIVE_DIR operator override and otherwise using DefaultArchiveDir.
func ArchiveDir() string {
	if dir := os.Getenv(EnvArchiveDir); strings.TrimSpace(dir) != "" {
		return dir
	}
	return DefaultArchiveDir()
}
