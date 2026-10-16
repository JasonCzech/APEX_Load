package browser

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/jasonczech/apexload/pkg/internal/filelock"
)

const runtimeHARWorkingDirectory = ".apexload-har-work"

func harRandomGeneration() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func validHARGeneration(s string) bool {
	if len(s) != 32 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			if c < 'a' || c > 'f' {
				return false
			}
		}
	}
	return true
}

func openHARPublishedRoot(dir string) (string, *os.Root, error) {
	if strings.TrimSpace(dir) == "" {
		dir = os.TempDir()
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", nil, err
	}
	before, err := os.Lstat(abs)
	if err == nil {
		before, err = pinHARPathIdentity(abs, before)
	}
	if err != nil {
		return "", nil, err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.IsDir() {
		return "", nil, fmt.Errorf("HAR artifact root must be a non-symlink directory")
	}
	canonical, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", nil, err
	}
	if err := validateHARVolume(canonical); err != nil {
		return "", nil, err
	}
	root, err := os.OpenRoot(canonical)
	if err != nil {
		return "", nil, err
	}
	opened, statErr := root.Stat(".")
	after, afterErr := os.Lstat(abs)
	if afterErr == nil {
		after, afterErr = pinHARPathIdentity(abs, after)
	}
	if statErr != nil || afterErr != nil || !os.SameFile(before, opened) || !os.SameFile(after, opened) || after.Mode()&os.ModeSymlink != 0 {
		_ = root.Close()
		return "", nil, fmt.Errorf("HAR artifact root changed while opening")
	}
	return canonical, root, nil
}

// validHARComponent admits one plain path component: no separator, no
// alternate data stream (':') and no dot names.
func validHARComponent(name string) bool {
	return name != "" && name != "." && name != ".." && name == filepath.Base(name) && !strings.ContainsAny(name, `/\\:`)
}

func openHARPrivateChild(parent *os.Root, name string, create bool) (*os.Root, error) {
	if !validHARComponent(name) {
		return nil, fmt.Errorf("invalid HAR directory component")
	}
	info, err := parent.Lstat(name)
	if os.IsNotExist(err) && create {
		if err = createHARPrivateDir(parent, name); err != nil && !os.IsExist(err) {
			return nil, err
		}
		info, err = parent.Lstat(name)
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("HAR staging directory is not private")
	}
	child, err := parent.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	opened, openErr := child.Stat(".")
	after, afterErr := parent.Lstat(name)
	if openErr != nil || afterErr != nil || !os.SameFile(info, opened) || !os.SameFile(after, opened) || after.Mode()&os.ModeSymlink != 0 {
		_ = child.Close()
		return nil, fmt.Errorf("HAR staging directory changed while opening")
	}
	if err := validateHARPrivateRoot(child); err != nil {
		_ = child.Close()
		return nil, errors.Join(fmt.Errorf("HAR staging directory is not private"), err)
	}
	return child, nil
}

// validateHARPrivateRoot proves the directory root holds open private.
func validateHARPrivateRoot(root *os.Root) error {
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	return errors.Join(validateHARPrivate(directory, false), directory.Close())
}

func openHARRegular(root *os.Root, name string, create bool) (*os.File, error) {
	if !validHARComponent(name) {
		return nil, fmt.Errorf("invalid HAR file name")
	}
	info, err := root.Lstat(name)
	flags := os.O_RDWR
	if os.IsNotExist(err) && create {
		file, createErr := root.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
		if createErr == nil {
			if err := validateHARPrivate(file, true); err != nil {
				return nil, errors.Join(fmt.Errorf("HAR file is not private and regular"), err, file.Close())
			}
			return file, nil
		}
		if !os.IsExist(createErr) {
			return nil, createErr
		}
		info, err = root.Lstat(name)
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("HAR file is not private and regular")
	}
	file, err := root.OpenFile(name, flags, 0)
	if err != nil {
		return nil, err
	}
	opened, openErr := file.Stat()
	after, afterErr := root.Lstat(name)
	if openErr != nil || afterErr != nil || !os.SameFile(info, opened) || !os.SameFile(after, opened) || after.Mode()&os.ModeSymlink != 0 {
		_ = file.Close()
		return nil, fmt.Errorf("HAR file changed while opening")
	}
	if err := validateHARPrivate(file, true); err != nil {
		return nil, errors.Join(fmt.Errorf("HAR file is not private and regular"), err, file.Close())
	}
	return file, nil
}

func verifyHARLease(root *os.Root, name string, file *os.File) error {
	entry, err := root.Lstat(name)
	if err != nil {
		return err
	}
	opened, err := file.Stat()
	if err != nil || !os.SameFile(entry, opened) {
		return fmt.Errorf("HAR lease identity changed")
	}
	return validateHARPrivate(file, true)
}

var harNamespaceOwners = struct {
	sync.Mutex
	owners map[*harNamespace]bool
}{owners: make(map[*harNamespace]bool)}

type harNamespace struct {
	path       string
	root, work *os.Root
	file       *os.File
	unlock     func() error
	once       sync.Once
	err        error
}

func lockHARNamespace(ctx context.Context, dir string) (*harNamespace, error) {
	if !NativeHARSupported() {
		// Refused before anything is opened, so no staging is created here.
		return nil, errHARPlatformUnsupported
	}
	n := &harNamespace{}
	harNamespaceOwners.Lock()
	if len(harNamespaceOwners.owners) >= 128 {
		harNamespaceOwners.Unlock()
		return nil, fmt.Errorf("HAR namespace ownership limit reached")
	}
	harNamespaceOwners.owners[n] = true
	harNamespaceOwners.Unlock()
	path, root, err := openHARPublishedRoot(dir)
	if err != nil {
		return n, errors.Join(err, n.close())
	}
	n.path, n.root = path, root
	n.work, err = openHARPrivateChild(root, runtimeHARWorkingDirectory, true)
	if err == nil {
		n.file, err = openHARRegular(n.work, "namespace.lock", true)
	}
	if err == nil {
		err = n.acquire(ctx)
	}
	if err != nil {
		return n, errors.Join(err, n.close())
	}
	return n, nil
}

func (n *harNamespace) acquire(ctx context.Context) error {
	var err error
	n.unlock, err = filelock.LockContextOwned(ctx, n.file)
	if err != nil {
		return err
	}
	return n.verify()
}

func (n *harNamespace) verify() error {
	if n == nil || n.root == nil || n.work == nil || n.file == nil || n.path == "" {
		return errHAROwnershipUnproven
	}
	parent, parentErr := n.root.Stat(".")
	currentParent, pathErr := os.Lstat(n.path)
	if pathErr == nil {
		currentParent, pathErr = pinHARPathIdentity(n.path, currentParent)
	}
	openedWork, workErr := n.work.Stat(".")
	currentWork, childErr := n.root.Lstat(runtimeHARWorkingDirectory)
	if parentErr != nil || pathErr != nil || workErr != nil || childErr != nil {
		return errors.Join(parentErr, pathErr, workErr, childErr, errHAROwnershipUnproven)
	}
	if currentParent.Mode()&os.ModeSymlink != 0 || !os.SameFile(parent, currentParent) ||
		currentWork.Mode()&os.ModeSymlink != 0 || !os.SameFile(openedWork, currentWork) || validateHARPrivateRoot(n.work) != nil {
		return errHAROwnershipUnproven
	}
	return errors.Join(verifyHARLease(n.work, "namespace.lock", n.file))
}

func (n *harNamespace) close() error {
	if n == nil {
		return nil
	}
	n.once.Do(func() {
		if n.unlock != nil {
			n.err = errors.Join(n.err, n.unlock())
		}
		if n.file != nil {
			n.err = errors.Join(n.err, n.file.Close())
		}
		if n.work != nil {
			n.err = errors.Join(n.err, n.work.Close())
		}
		if n.root != nil {
			n.err = errors.Join(n.err, n.root.Close())
		}
		if n.err == nil {
			harNamespaceOwners.Lock()
			delete(harNamespaceOwners.owners, n)
			harNamespaceOwners.Unlock()
		}
	})
	return n.err
}

// WithRuntimeHARArtifactNamespace serializes completed HAR publication and
// pruning through one existing-descriptor cross-process fence. The callback
// must not retain the root after return or recursively acquire this fence.
func WithRuntimeHARArtifactNamespace(ctx context.Context, dir string, action func(*os.Root) error) (err error) {
	if action == nil {
		return fmt.Errorf("HAR namespace callback is required")
	}
	n, err := lockHARNamespace(ctx, dir)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, n.close()) }()
	if err := n.verify(); err != nil {
		return err
	}
	return action(n.root)
}

func writeHARPrivateDurable(root *os.Root, name string, data []byte) error {
	f, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if err := validateHARPrivate(f, true); err != nil {
		return errors.Join(err, f.Close())
	}
	count, err := f.Write(data)
	if err == nil && count != len(data) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = f.Sync()
	}
	return errors.Join(err, f.Close())
}
