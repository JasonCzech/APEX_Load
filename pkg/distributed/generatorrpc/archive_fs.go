package generatorrpc

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// generatorArchiveNamespace is an opened, stable generator directory. All
// archive operations are relative to root; the path is metadata only and is
// never reopened for security-sensitive I/O.
type generatorArchiveNamespace struct {
	path string
	root *os.Root
}

func (n *generatorArchiveNamespace) close() error {
	if n == nil || n.root == nil {
		return nil
	}
	err := n.root.Close()
	n.root = nil
	return err
}

func prepareGeneratorArchiveNamespace(generatorID string, create bool) (*generatorArchiveNamespace, error) {
	namespace := ""
	if generatorID != "" {
		namespace = sanitizeArchiveNamespace(generatorID)
		if err := validateArchiveNamespace(namespace); err != nil {
			return nil, fmt.Errorf("invalid generator archive namespace: %w", err)
		}
	}
	basePath, baseRoot, err := openCanonicalArchiveDirectory(resolveArchiveRoot(), create)
	if err != nil {
		return nil, err
	}
	if namespace == "" {
		return &generatorArchiveNamespace{path: basePath, root: baseRoot}, nil
	}
	child, err := openArchiveChildDirectory(baseRoot, namespace, create, 0o755)
	if err != nil {
		_ = baseRoot.Close()
		return nil, err
	}
	_ = baseRoot.Close()
	return &generatorArchiveNamespace{path: filepath.Join(basePath, namespace), root: child}, nil
}

// openCanonicalArchiveDirectory resolves existing operator-owned ancestors
// once, then traverses and creates every canonical component through held
// os.Root descriptors. A concurrent rename/symlink swap cannot redirect later
// components outside the directory already opened.
func openCanonicalArchiveDirectory(path string, create bool) (string, *os.Root, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", nil, fmt.Errorf("archive root is empty")
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", nil, err
	}
	absPath = filepath.Clean(absPath)

	cursor := absPath
	missing := make([]string, 0)
	for {
		info, statErr := os.Lstat(cursor)
		if statErr == nil {
			if cursor == absPath && info.Mode()&os.ModeSymlink != 0 {
				return "", nil, fmt.Errorf("configured archive root %q is a symbolic link", path)
			}
			if info.Mode()&os.ModeSymlink != 0 && cursor != absPath {
				resolved, resolveErr := filepath.EvalSymlinks(cursor)
				if resolveErr != nil {
					return "", nil, resolveErr
				}
				cursor = resolved
			} else if !info.IsDir() {
				return "", nil, fmt.Errorf("archive root component %q is not a directory", cursor)
			}
			break
		}
		if !os.IsNotExist(statErr) {
			return "", nil, statErr
		}
		parent := filepath.Dir(cursor)
		if parent == cursor {
			return "", nil, statErr
		}
		missing = append(missing, filepath.Base(cursor))
		cursor = parent
	}

	resolvedExisting, err := filepath.EvalSymlinks(cursor)
	if err != nil {
		return "", nil, err
	}
	currentPath, currentRoot, err := openCanonicalExistingDirectory(resolvedExisting)
	if err != nil {
		return "", nil, err
	}
	for index := len(missing) - 1; index >= 0; index-- {
		component := missing[index]
		child, childErr := openArchiveChildDirectory(currentRoot, component, create, 0o755)
		if childErr != nil {
			_ = currentRoot.Close()
			return "", nil, childErr
		}
		_ = currentRoot.Close()
		currentRoot = child
		currentPath = filepath.Join(currentPath, component)
	}
	return currentPath, currentRoot, nil
}

func openCanonicalExistingDirectory(path string) (string, *os.Root, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", nil, err
	}
	volume := filepath.VolumeName(absPath)
	anchor := string(filepath.Separator)
	if volume != "" {
		anchor = volume + string(filepath.Separator)
	}
	relative, err := filepath.Rel(anchor, absPath)
	if err != nil {
		return "", nil, err
	}
	root, err := os.OpenRoot(anchor)
	if err != nil {
		return "", nil, err
	}
	currentPath := anchor
	if relative == "." {
		return currentPath, root, nil
	}
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		child, childErr := openArchiveChildDirectory(root, component, false, 0)
		if childErr != nil {
			_ = root.Close()
			return "", nil, childErr
		}
		_ = root.Close()
		root = child
		currentPath = filepath.Join(currentPath, component)
	}
	return currentPath, root, nil
}

func openArchiveChildDirectory(parent *os.Root, name string, create bool, perm os.FileMode) (*os.Root, error) {
	if parent == nil {
		return nil, fmt.Errorf("archive parent root is required")
	}
	if err := validateArchiveNamespace(name); err != nil {
		return nil, err
	}
	info, err := parent.Lstat(name)
	if os.IsNotExist(err) && create {
		if mkdirErr := parent.Mkdir(name, perm); mkdirErr != nil && !os.IsExist(mkdirErr) {
			return nil, mkdirErr
		}
		info, err = parent.Lstat(name)
	}
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, fmt.Errorf("archive directory %q is not a non-symlink directory", name)
	}
	child, err := parent.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	opened, err := child.Stat(".")
	if err != nil {
		_ = child.Close()
		return nil, err
	}
	after, err := parent.Lstat(name)
	if err != nil || after.Mode()&os.ModeSymlink != 0 || !after.IsDir() || !os.SameFile(info, opened) || !os.SameFile(after, opened) {
		_ = child.Close()
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("archive directory %q changed while opening", name)
	}
	return child, nil
}

func validateArchiveNamespace(value string) error {
	if value == "" || value != strings.TrimSpace(value) || value == "." || value == ".." || strings.ContainsAny(value, `/\\`) || filepath.IsAbs(value) || filepath.VolumeName(value) != "" {
		return fmt.Errorf("archive namespace %q is not safe", value)
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("archive namespace %q contains a control character", value)
		}
	}
	return nil
}

func normalizeGeneratorArchiveArtifactName(name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" || trimmed != name {
		return "", fmt.Errorf("artifact name is empty or padded")
	}
	if strings.ContainsRune(trimmed, 0) || strings.ContainsRune(trimmed, '\\') || filepath.IsAbs(trimmed) || filepath.VolumeName(trimmed) != "" {
		return "", fmt.Errorf("artifact name is not a safe relative path")
	}
	for _, component := range strings.Split(trimmed, "/") {
		if component == "" || component == "." || component == ".." {
			return "", fmt.Errorf("artifact name contains an unsafe path component")
		}
	}
	cleaned := filepath.ToSlash(filepath.Clean(filepath.FromSlash(trimmed)))
	if cleaned == "." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("artifact name escapes archive root")
	}
	return cleaned, nil
}

func archiveRandomName(base string) (string, error) {
	random := make([]byte, 12)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return "." + base + ".tmp-" + hex.EncodeToString(random), nil
}

func inspectGeneratorArchiveFileTarget(root *os.Root, name string, allowMissing bool) (os.FileInfo, error) {
	info, err := root.Lstat(filepath.FromSlash(name))
	if os.IsNotExist(err) && allowMissing {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("archive artifact %q is not a regular file", name)
	}
	return info, nil
}

func replaceGeneratorArchiveFile(root *os.Root, name string, perm os.FileMode) (*os.File, error) {
	name, err := normalizeGeneratorArchiveArtifactName(name)
	if err != nil {
		return nil, err
	}
	if strings.Contains(name, "/") {
		return nil, fmt.Errorf("generator archive producer artifacts must be direct children")
	}
	if _, err := inspectGeneratorArchiveFileTarget(root, name, true); err != nil {
		return nil, err
	}
	temp, err := archiveRandomName(filepath.Base(name))
	if err != nil {
		return nil, err
	}
	file, err := root.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
	if err != nil {
		return nil, err
	}
	if err := root.Rename(temp, name); err != nil {
		_ = file.Close()
		_ = root.Remove(temp)
		return nil, err
	}
	return file, nil
}

func writeGeneratorArchiveFileDurable(root *os.Root, name string, data []byte, perm os.FileMode) error {
	return writeGeneratorArchiveFileDurableWithWriter(root, name, data, perm, func(file *os.File, content []byte) (int, error) {
		return file.Write(content)
	})
}

func writeGeneratorArchiveFileDurableWithWriter(root *os.Root, name string, data []byte, perm os.FileMode, write func(*os.File, []byte) (int, error)) error {
	if write == nil {
		return fmt.Errorf("generator archive durable writer is required")
	}
	name, err := normalizeGeneratorArchiveArtifactName(name)
	if err != nil {
		return err
	}
	if strings.Contains(name, "/") {
		return fmt.Errorf("generator archive producer artifacts must be direct children")
	}
	if _, err := inspectGeneratorArchiveFileTarget(root, name, true); err != nil {
		return err
	}
	temp, err := archiveRandomName(filepath.Base(name))
	if err != nil {
		return err
	}
	file, err := root.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	keep := true
	defer func() {
		if keep {
			_ = root.Remove(temp)
		}
	}()
	written, err := write(file, data)
	if err != nil {
		_ = file.Close()
		return err
	}
	if written != len(data) {
		_ = file.Close()
		return io.ErrShortWrite
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if _, err := inspectGeneratorArchiveFileTarget(root, name, true); err != nil {
		return err
	}
	if err := root.Rename(temp, name); err != nil {
		return err
	}
	keep = false
	return syncGeneratorArchiveDirectory(root)
}

func openGeneratorArchiveRegularFile(root *os.Root, name string) (*os.File, os.FileInfo, error) {
	name, err := normalizeGeneratorArchiveArtifactName(name)
	if err != nil {
		return nil, nil, err
	}
	parentRoot := root
	closeParent := false
	components := strings.Split(filepath.FromSlash(name), string(filepath.Separator))
	for _, component := range components[:len(components)-1] {
		child, openErr := openArchiveChildDirectory(parentRoot, component, false, 0)
		if openErr != nil {
			if closeParent {
				_ = parentRoot.Close()
			}
			return nil, nil, openErr
		}
		if closeParent {
			_ = parentRoot.Close()
		}
		parentRoot = child
		closeParent = true
	}
	if closeParent {
		defer parentRoot.Close()
	}
	base := components[len(components)-1]
	info, err := inspectGeneratorArchiveFileTarget(parentRoot, base, false)
	if err != nil {
		return nil, nil, err
	}
	file, err := parentRoot.Open(base)
	if err != nil {
		return nil, nil, err
	}
	opened, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, nil, err
	}
	after, err := parentRoot.Lstat(base)
	if err != nil || after.Mode()&os.ModeSymlink != 0 || !after.Mode().IsRegular() || !os.SameFile(info, opened) || !os.SameFile(after, opened) {
		_ = file.Close()
		if err != nil {
			return nil, nil, err
		}
		return nil, nil, fmt.Errorf("archive artifact %q changed while opening", name)
	}
	return file, opened, nil
}

func syncGeneratorArchiveDirectory(root *os.Root) error {
	if root == nil {
		return fmt.Errorf("generator archive root is unavailable")
	}
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func openGeneratorArchiveRun(namespace *generatorArchiveNamespace, testID string, create bool) (*os.Root, os.FileInfo, error) {
	if namespace == nil || namespace.root == nil {
		return nil, nil, fmt.Errorf("generator archive namespace is unavailable")
	}
	if err := validateArchiveTestID(testID); err != nil {
		return nil, nil, err
	}
	run, err := openArchiveChildDirectory(namespace.root, testID, create, 0o755)
	if err != nil {
		return nil, nil, err
	}
	info, err := run.Stat(".")
	if err != nil {
		_ = run.Close()
		return nil, nil, err
	}
	return run, info, nil
}

func verifyGeneratorArchiveRun(namespace *generatorArchiveNamespace, testID string, identity os.FileInfo) error {
	if namespace == nil || namespace.root == nil {
		return fmt.Errorf("generator archive namespace is unavailable")
	}
	if identity == nil {
		return fmt.Errorf("generator archive run identity is unavailable")
	}
	if err := validateArchiveTestID(testID); err != nil {
		return err
	}
	info, err := namespace.root.Lstat(testID)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || !os.SameFile(identity, info) {
		return fmt.Errorf("generator archive run %q was replaced", testID)
	}
	return nil
}

func removeGeneratorArchiveRun(namespace *generatorArchiveNamespace, testID string, identity os.FileInfo) error {
	if namespace == nil || namespace.root == nil {
		return fmt.Errorf("generator archive namespace is unavailable")
	}
	if err := validateArchiveTestID(testID); err != nil {
		return err
	}
	info, err := namespace.root.Lstat(testID)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("generator archive run %q is not a non-symlink directory", testID)
	}
	if identity != nil && !os.SameFile(identity, info) {
		return fmt.Errorf("generator archive run %q was replaced before deletion", testID)
	}
	tombstone, err := archiveRandomName(testID + ".deleting")
	if err != nil {
		return err
	}
	if err := namespace.root.Rename(testID, tombstone); err != nil {
		return err
	}
	tombstoneInfo, err := namespace.root.Lstat(tombstone)
	if err != nil || tombstoneInfo.Mode()&os.ModeSymlink != 0 || !tombstoneInfo.IsDir() || (identity != nil && !os.SameFile(identity, tombstoneInfo)) {
		if _, currentErr := namespace.root.Lstat(testID); os.IsNotExist(currentErr) {
			_ = namespace.root.Rename(tombstone, testID)
		}
		if err != nil {
			return err
		}
		return fmt.Errorf("generator archive run %q changed during deletion", testID)
	}
	return namespace.root.RemoveAll(tombstone)
}

func readGeneratorArchiveDirectories(namespace *generatorArchiveNamespace) ([]fs.DirEntry, error) {
	if namespace == nil || namespace.root == nil {
		return nil, fmt.Errorf("generator archive namespace is unavailable")
	}
	return fs.ReadDir(namespace.root.FS(), ".")
}
