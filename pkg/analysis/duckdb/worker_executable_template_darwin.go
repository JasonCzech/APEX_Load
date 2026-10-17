//go:build darwin

package duckdb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// macOS assesses every new executable file on its first exec: Gatekeeper's
// scan, an online notarization lookup and an XProtect analysis, about 15 ms per
// MB on an M4 and slower on hosted Intel runners. A per-attempt copy is a new
// file every time, so each native worker paid that scan (three seconds for a
// 442 MB test binary). Each process instead copies and verifies one private
// template, then hard-links it into every attempt's scratch. The attempt still
// launches its own 0500 path in its private directory, re-hashed against the
// startup approval, and macOS does not assess an inode it has already run.
const (
	workerExecutableTemplatePrefix   = "apexload-native-image-"
	workerExecutableTemplateLockName = "owner.lock"
	// A template directory without its lock file is either being created or
	// was left by a crash during creation; only the latter is this old.
	workerExecutableTemplateStaleAge = time.Minute
	// After a template could not be built (a full disk, an unwritable temp
	// directory), attempts copy without retrying it for this long, so each one
	// does not also write a partial template first.
	workerExecutableTemplateRetryAfter = time.Minute
)

type workerExecutableTemplate struct {
	dir    string
	lock   *os.File // flock'd for the process lifetime; marks the template live
	digest string
	info   os.FileInfo
}

var workerExecutableTemplates struct {
	mu       sync.Mutex
	current  *workerExecutableTemplate
	failedAt time.Time
	// root is where templates live. It is the per-user temporary directory, on
	// the same volume as the scratch roots, so links to any parent work.
	root func() string
}

func init() { workerExecutableTemplates.root = os.TempDir }

// workerLinkedExecutableSnapshot gives the attempt its snapshot as a link to
// this process's verified template. handled is false when no template could be
// prepared or linked, and the caller then copies the executable as before.
func workerLinkedExecutableSnapshot(a *workerAttempt, sourcePath, expected string) (path string, handled bool, err error) {
	template := workerExecutableTemplateFor(a.ctx, sourcePath, expected)
	if template == nil {
		return "", false, nil
	}
	name := workerExecutableSnapshotName()
	if err := os.Link(filepath.Join(template.dir, name), filepath.Join(a.path, name)); err != nil {
		// Scratch on another volume cannot link to the template, which stays
		// valid for other parents; any other failure retires it.
		if !errors.Is(err, unix.EXDEV) {
			workerDropExecutableTemplate(template)
		}
		return "", false, nil
	}
	linked, err := a.root.Open(name)
	if err != nil {
		return "", true, errWorkerOwnership
	}
	a.trackDescriptor(linked)
	info, statErr := linked.Stat()
	digest := sha256.New()
	var n int64
	var copyErr error
	if statErr == nil {
		n, copyErr = workerCopyContext(a.ctx, digest, io.LimitReader(linked, info.Size()+1))
	}
	closeErr := a.closeDescriptor(linked)
	if errors.Join(statErr, copyErr, closeErr) != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0500 || !os.SameFile(info, template.info) || n != info.Size() || info.Size() > workerExecutableMaxBytes || hex.EncodeToString(digest.Sum(nil)) != expected {
		// The shared inode no longer holds the approved bytes: fail this
		// attempt closed and let the next one build a fresh template.
		workerDropExecutableTemplate(template)
		return "", true, errWorkerOwnership
	}
	if workerContextError(a.ctx) != nil {
		return "", true, errWorkerOwnership
	}
	return filepath.Join(a.path, name), true, nil
}

// workerExecutableTemplateFor returns the live template for expected, building
// it from sourcePath when there is none. It returns nil when it cannot.
func workerExecutableTemplateFor(ctx context.Context, sourcePath, expected string) *workerExecutableTemplate {
	workerExecutableTemplates.mu.Lock()
	defer workerExecutableTemplates.mu.Unlock()
	if current := workerExecutableTemplates.current; current != nil {
		if current.digest == expected {
			return current
		}
		workerExecutableTemplates.current = nil
		current.remove()
	}
	if time.Since(workerExecutableTemplates.failedAt) < workerExecutableTemplateRetryAfter {
		return nil
	}
	root := workerExecutableTemplates.root()
	workerSweepExecutableTemplates(root)
	template, err := workerCreateExecutableTemplate(ctx, root, sourcePath, expected)
	if err != nil {
		// A cancelled attempt says nothing about the next one.
		if workerContextError(ctx) == nil {
			workerExecutableTemplates.failedAt = time.Now()
		}
		return nil
	}
	workerExecutableTemplates.current = template
	return template
}

func workerCreateExecutableTemplate(ctx context.Context, root, sourcePath, expected string) (_ *workerExecutableTemplate, err error) {
	if workerContextError(ctx) != nil {
		return nil, errWorkerOwnership
	}
	dir, err := os.MkdirTemp(root, workerExecutableTemplatePrefix)
	if err != nil {
		return nil, err
	}
	template := &workerExecutableTemplate{dir: dir, digest: expected}
	defer func() {
		if err != nil {
			template.remove()
		}
	}()
	// O_EXLOCK takes the lock as the file appears, so a concurrent sweep never
	// sees this template unlocked.
	if template.lock, err = os.OpenFile(filepath.Join(dir, workerExecutableTemplateLockName), os.O_CREATE|os.O_EXCL|os.O_RDWR|unix.O_EXLOCK, 0600); err != nil {
		return nil, err
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		return nil, err
	}
	defer source.Close()
	before, err := source.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Size() < 1 || before.Size() > workerExecutableMaxBytes {
		return nil, errWorkerResourceLimit
	}
	path := filepath.Join(dir, workerExecutableSnapshotName())
	output, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0500)
	if err != nil {
		return nil, err
	}
	digest := sha256.New()
	n, copyErr := workerCopyContext(ctx, io.MultiWriter(output, digest), io.LimitReader(source, before.Size()+1))
	after, statErr := source.Stat()
	if err = errors.Join(copyErr, statErr, output.Sync(), output.Close()); err != nil {
		return nil, err
	}
	if n != before.Size() || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) || hex.EncodeToString(digest.Sum(nil)) != expected {
		return nil, errWorkerOwnership
	}
	if template.info, err = os.Lstat(path); err != nil {
		return nil, err
	}
	return template, nil
}

// remove deletes the template's directory and releases its lock. Attempts that
// already linked it keep their own names for the same inode.
func (t *workerExecutableTemplate) remove() {
	_ = os.RemoveAll(t.dir)
	if t.lock != nil {
		_ = t.lock.Close()
	}
}

func workerDropExecutableTemplate(template *workerExecutableTemplate) {
	workerExecutableTemplates.mu.Lock()
	defer workerExecutableTemplates.mu.Unlock()
	if workerExecutableTemplates.current == template {
		workerExecutableTemplates.current = nil
		template.remove()
	}
}

// workerSweepExecutableTemplates removes templates whose process is gone: its
// lock is free once it exits, however it exits.
func workerSweepExecutableTemplates(root string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), workerExecutableTemplatePrefix) {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		// Non-blocking, so a lock path that is not a regular file cannot stall
		// the sweep.
		lock, err := os.OpenFile(filepath.Join(dir, workerExecutableTemplateLockName), os.O_RDONLY|unix.O_NONBLOCK, 0)
		if os.IsNotExist(err) {
			if info, statErr := entry.Info(); statErr == nil && time.Since(info.ModTime()) > workerExecutableTemplateStaleAge {
				_ = os.RemoveAll(dir)
			}
			continue
		}
		if err != nil {
			continue
		}
		if info, err := lock.Stat(); err == nil && info.Mode().IsRegular() && unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB) == nil {
			_ = os.RemoveAll(dir)
		}
		_ = lock.Close()
	}
}

// ReleaseNativeWorkerTemplate removes this process's worker executable
// template. Processes call it as they exit normally; a later process sweeps
// the template of one that crashed.
func ReleaseNativeWorkerTemplate() {
	workerExecutableTemplates.mu.Lock()
	defer workerExecutableTemplates.mu.Unlock()
	if current := workerExecutableTemplates.current; current != nil {
		workerExecutableTemplates.current = nil
		current.remove()
	}
}
