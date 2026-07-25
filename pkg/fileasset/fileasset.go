package fileasset

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

const (
	MaxBytes           int64 = 50 << 20
	MaxCredentialBytes int64 = 4 << 20
	// MaxJarBytes bounds a single user-uploaded Java Vuser classpath JAR. It is
	// deliberately larger than MaxBytes (which fences ordinary data-source
	// reads) and is used only by the classpath materializer and distributed
	// stager via the *Limited helpers. The global MaxBytes is unchanged.
	MaxJarBytes int64 = 256 << 20
)

var (
	rootsMu      sync.RWMutex
	allowedRoots = make(map[string]struct{})
	projectRoots = make(map[string]struct{})
)

func init() {
	_ = RegisterProjectRoot(DefaultImportAssetDir())
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

// RegisterProjectRoot registers a file-asset root whose contents are partitioned
// by project. Paths under one of these roots remain ordinary file:// references,
// but callers validating a script must use ValidatePathForProject or
// ValidateStorageRefForProject so a reference cannot cross project boundaries.
func RegisterProjectRoot(root string) error {
	normalized, err := normalizeRoot(root)
	if err != nil {
		return err
	}
	rootsMu.Lock()
	allowedRoots[normalized] = struct{}{}
	projectRoots[normalized] = struct{}{}
	rootsMu.Unlock()
	return nil
}

// ProjectAssetDir returns the collision-free namespace used for one project's
// imported assets. The project identifier is hashed so arbitrary identifiers
// cannot introduce path separators or collide after lossy sanitization.
func ProjectAssetDir(root, projectID string) (string, error) {
	normalized, err := normalizeRoot(root)
	if err != nil {
		return "", err
	}
	namespace, err := projectAssetNamespace(projectID)
	if err != nil {
		return "", err
	}
	return filepath.Join(normalized, "projects", namespace), nil
}

func ValidateStorageRef(ref string) (string, error) {
	path, err := storageRefPath(ref)
	if err != nil {
		return "", err
	}
	return ValidatePath(path)
}

// ValidateStorageRefForProject validates a file:// reference and, when the file
// lives below a project-scoped root, verifies that it belongs to projectID.
func ValidateStorageRefForProject(ref, projectID string) (string, error) {
	path, err := storageRefPath(ref)
	if err != nil {
		return "", err
	}
	return ValidatePathForProject(path, projectID)
}

func storageRefPath(ref string) (string, error) {
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
	return parsed.Path, nil
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

// ValidatePathForProject performs the ordinary root/symlink/size checks and
// additionally fences paths below a registered project root to projectID.
func ValidatePathForProject(path, projectID string) (string, error) {
	realPath, err := ValidatePath(path)
	if err != nil {
		return "", err
	}
	root := matchingProjectRoot(realPath)
	if root == "" {
		return realPath, nil
	}
	namespace, err := projectAssetNamespace(projectID)
	if err != nil {
		return "", fmt.Errorf("project-scoped file asset: %w", err)
	}
	expectedRoot := filepath.Join(root, "projects", namespace)
	if !pathWithinRoot(realPath, expectedRoot) {
		return "", fmt.Errorf("file asset does not belong to project %q", strings.TrimSpace(projectID))
	}
	return realPath, nil
}

func OpenPath(path string) (io.ReadCloser, error) {
	return OpenPathLimited(path, MaxBytes)
}

// OpenPathLimited is OpenPath with a caller-supplied maximum size. It is used
// for asset classes whose ceiling differs from the default MaxBytes (e.g.
// user-uploaded classpath JARs, which use MaxJarBytes). maxBytes must be
// positive; the root/symlink fencing is identical to OpenPath.
func OpenPathLimited(path string, maxBytes int64) (io.ReadCloser, error) {
	if maxBytes <= 0 {
		return nil, fmt.Errorf("file asset read limit must be positive")
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("path is required")
	}
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("file asset path must be absolute")
	}
	abs := filepath.Clean(path)
	matched := false
	var lastErr error
	for _, rootPath := range allowedRootPaths() {
		if !pathWithinRoot(abs, rootPath) {
			continue
		}
		matched = true
		root, err := os.OpenRoot(rootPath)
		if err != nil {
			lastErr = err
			continue
		}
		rel, err := filepath.Rel(rootPath, abs)
		if err != nil {
			_ = root.Close()
			lastErr = err
			continue
		}
		f, err := root.Open(rel)
		_ = root.Close()
		if err != nil {
			lastErr = err
			continue
		}
		info, err := f.Stat()
		if err != nil {
			_ = f.Close()
			return nil, fmt.Errorf("stat file asset %s: %w", abs, err)
		}
		if !info.Mode().IsRegular() {
			_ = f.Close()
			return nil, fmt.Errorf("file asset %s is not a regular file", abs)
		}
		if info.Size() > maxBytes {
			_ = f.Close()
			return nil, fmt.Errorf("file asset %s exceeds %d byte limit", abs, maxBytes)
		}
		return f, nil
	}
	if !matched {
		return nil, fmt.Errorf("file asset path %q is outside allowed roots", abs)
	}
	return nil, fmt.Errorf("open file asset %s: %w", abs, lastErr)
}

func OpenStorageRef(ref string) (io.ReadCloser, error) {
	path, err := storageRefPath(ref)
	if err != nil {
		return nil, err
	}
	return OpenPath(path)
}

// OpenStorageRefLimited resolves a file:// storage_ref and opens it with a
// caller-supplied maximum size (see OpenPathLimited).
func OpenStorageRefLimited(ref string, maxBytes int64) (io.ReadCloser, error) {
	path, err := storageRefPath(ref)
	if err != nil {
		return nil, err
	}
	return OpenPathLimited(path, maxBytes)
}

func ReadPathLimited(path string, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 || maxBytes > MaxBytes {
		return nil, fmt.Errorf("file asset read limit must be between 1 and %d bytes", MaxBytes)
	}
	reader, err := OpenPath(path)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read file asset %s: %w", path, err)
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("file asset %s exceeds %d byte limit", path, maxBytes)
	}
	return data, nil
}

func ReadCredential(path string) ([]byte, error) {
	return ReadPathLimited(path, MaxCredentialBytes)
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

func projectRootPaths() []string {
	rootsMu.RLock()
	roots := make([]string, 0, len(projectRoots)*2)
	for root := range projectRoots {
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
	// Prefer the most specific root when operators intentionally nest roots.
	sort.Slice(unique, func(i, j int) bool { return len(unique[i]) > len(unique[j]) })
	return unique
}

func matchingProjectRoot(path string) string {
	for _, root := range projectRootPaths() {
		if pathWithinRoot(path, root) {
			return root
		}
	}
	return ""
}

func projectAssetNamespace(projectID string) (string, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return "", fmt.Errorf("project_id is required")
	}
	sum := sha256.Sum256([]byte(projectID))
	return hex.EncodeToString(sum[:]), nil
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
