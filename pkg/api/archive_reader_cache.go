package api

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const envArchiveReaderCacheDir = "APEXLOAD_ARCHIVE_READER_CACHE_DIR"

// Reader leases and eviction are process-local. Deployments sharing publication
// staging must give each controller a private reader cache, even though all
// controllers retain the same durable object identities and source paths.
func archiveReaderRuntime(backend archiveBackendRuntime) (archiveBackendRuntime, error) {
	root := strings.TrimSpace(os.Getenv(envArchiveReaderCacheDir))
	if root == "" {
		return backend, nil
	}
	if !filepath.IsAbs(root) {
		return archiveBackendRuntime{}, fmt.Errorf("%s must be an absolute pod-private directory", envArchiveReaderCacheDir)
	}
	root = filepath.Clean(root)
	canonical, err := archiveReaderCacheCanonicalRoot(root)
	if err != nil {
		return archiveBackendRuntime{}, err
	}
	for _, source := range []string{resolveFilesystemArchiveRoot(), os.Getenv("APEXLOAD_ARCHIVE_STAGING_DIR"), backend.LocalRoot} {
		source = strings.TrimSpace(source)
		if source == "" {
			continue
		}
		shared, err := archiveReaderCacheCanonicalRoot(source)
		if err != nil {
			return archiveBackendRuntime{}, err
		}
		if archiveReaderPathsOverlap(canonical, shared) {
			return archiveBackendRuntime{}, fmt.Errorf("%s must not overlap archive source staging", envArchiveReaderCacheDir)
		}
	}
	backend.LocalRoot = archiveObjectNamespaceRoot(filepath.Join(canonical, backend.Backend), backend)
	return backend, nil
}

// Resolve existing ancestors without creating directories. This catches a cache
// alias of shared staging even before its namespace directories have been made.
func archiveReaderCacheCanonicalRoot(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	cursor := filepath.Clean(abs)
	var missing []string
	for {
		resolved, err := filepath.EvalSymlinks(cursor)
		if err == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return resolved, nil
		}
		if !os.IsNotExist(err) || filepath.Dir(cursor) == cursor {
			return "", err
		}
		missing = append(missing, filepath.Base(cursor))
		cursor = filepath.Dir(cursor)
	}
}

func archiveReaderPathsOverlap(a, b string) bool {
	for _, pair := range [][2]string{{a, b}, {b, a}} {
		rel, err := filepath.Rel(pair[0], pair[1])
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
