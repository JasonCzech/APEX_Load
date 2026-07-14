package fileasset

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const MaxBytes int64 = 50 << 20

var (
	rootsMu      sync.RWMutex
	allowedRoots = make(map[string]struct{})
)

func init() {
	_ = RegisterRoot(DefaultImportAssetDir())
	// Runtime-generated fixtures such as descriptor sets and browser uploads are
	// commonly staged under the process temp directory. Keep that scope confined
	// to the OS temp root rather than permitting arbitrary absolute paths.
	_ = RegisterRoot(os.TempDir())
}

func DefaultImportAssetDir() string {
	return filepath.Join(os.TempDir(), "apexload-import-assets")
}

func RegisterRoot(root string) error {
	normalized, err := normalizeRoot(root)
	if err != nil {
		return err
	}
	rootsMu.Lock()
	allowedRoots[normalized] = struct{}{}
	rootsMu.Unlock()
	return nil
}

func ValidateStorageRef(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", fmt.Errorf("storage_ref is required")
	}
	parsed, err := url.Parse(ref)
	if err != nil {
		return "", fmt.Errorf("parse storage_ref: %w", err)
	}
	if parsed.Scheme != "file" {
		return "", fmt.Errorf("unsupported storage_ref scheme %q", parsed.Scheme)
	}
	if parsed.Host != "" && parsed.Host != "localhost" {
		return "", fmt.Errorf("file storage_ref host must be empty or localhost")
	}
	if strings.TrimSpace(parsed.Path) == "" {
		return "", fmt.Errorf("file storage_ref path is required")
	}
	return ValidatePath(parsed.Path)
}

func ValidatePath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("path is required")
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("file asset path must be absolute")
	}
	abs := filepath.Clean(path)
	if !pathWithinAllowedRoots(abs, allowedRootPaths()) {
		return "", fmt.Errorf("file asset path %q is outside allowed roots", abs)
	}
	realPath, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolve file asset path: %w", err)
	}
	realPath = filepath.Clean(realPath)
	if !pathWithinAllowedRoots(realPath, allowedRootPaths()) {
		return "", fmt.Errorf("file asset path %q resolves outside allowed roots", abs)
	}
	info, err := os.Stat(realPath)
	if err != nil {
		return "", fmt.Errorf("stat file asset %s: %w", abs, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("file asset %s is not a regular file", abs)
	}
	if info.Size() > MaxBytes {
		return "", fmt.Errorf("file asset %s exceeds %d byte limit", abs, MaxBytes)
	}
	return realPath, nil
}

func OpenPath(path string) (io.ReadCloser, error) {
	realPath, err := ValidatePath(path)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(realPath)
	if err != nil {
		return nil, fmt.Errorf("open file asset %s: %w", path, err)
	}
	return f, nil
}

func OpenStorageRef(ref string) (io.ReadCloser, error) {
	realPath, err := ValidateStorageRef(ref)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(realPath)
	if err != nil {
		return nil, fmt.Errorf("open file asset %s: %w", realPath, err)
	}
	return f, nil
}

func ReadAllLimited(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > MaxBytes {
		return nil, fmt.Errorf("file asset exceeds %d byte limit", MaxBytes)
	}
	return data, nil
}

func normalizeRoot(root string) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", fmt.Errorf("file asset root is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve file asset root: %w", err)
	}
	abs = filepath.Clean(abs)
	info, err := os.Stat(abs)
	if err == nil {
		if !info.IsDir() {
			return "", fmt.Errorf("file asset root %s is not a directory", abs)
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("stat file asset root %s: %w", abs, err)
	}
	return abs, nil
}

func allowedRootPaths() []string {
	rootsMu.RLock()
	roots := make([]string, 0, len(allowedRoots)*2)
	for root := range allowedRoots {
		roots = append(roots, root)
		if realRoot, err := filepath.EvalSymlinks(root); err == nil {
			roots = append(roots, filepath.Clean(realRoot))
		}
	}
	rootsMu.RUnlock()

	seen := make(map[string]struct{}, len(roots))
	unique := roots[:0]
	for _, root := range roots {
		root = filepath.Clean(root)
		if _, ok := seen[root]; ok {
			continue
		}
		seen[root] = struct{}{}
		unique = append(unique, root)
	}
	return unique
}

func pathWithinAllowedRoots(path string, roots []string) bool {
	for _, root := range roots {
		if pathWithinRoot(path, root) {
			return true
		}
	}
	return false
}

func pathWithinRoot(path string, root string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
