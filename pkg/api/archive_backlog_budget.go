package api

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/internal/archivequota"
	"github.com/jasonczech/apexload/pkg/storage"
)

const envArchiveBacklogMaxBytes = "APEXLOAD_ARCHIVE_BACKLOG_MAX_BYTES"

// These are independently admitted collection copies, object-backend namespaces
// below the filesystem root, and imported library assets. They are not workload
// producer/publication output. Readers and transfer scratch use separate roots.
var controllerArchiveBacklogExclusions = []string{"_collection", "_object_staging", "library-imports", archiveReplayDirectory}

type archiveBacklogBudget struct {
	mu      sync.Mutex
	path    string
	maximum int64
	root    *os.Root
	quota   *archivequota.Budget
	checked map[*os.File]struct{}
	closed  bool
}

func newArchiveBacklogBudget(root string) (*archiveBacklogBudget, error) {
	limit, err := collectionCacheByteLimit(envArchiveBacklogMaxBytes, 256<<30, 1<<50)
	if err != nil {
		return nil, err
	}
	return &archiveBacklogBudget{path: root, maximum: limit}, nil
}

// Initialization is lazy so constructors that fail before writing do not retain
// descriptors. A held root and descriptor identity prevent charging one root
// while a path replacement redirects the actual output into another namespace.
func (b *archiveBacklogBudget) write(ctx context.Context, file *os.File, data []byte) (int, error) {
	if b == nil {
		return file.Write(data)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return 0, os.ErrClosed
	}
	if b.root == nil {
		path, err := resolveArchiveDirectoryRoot(b.path, false)
		if err != nil {
			return 0, err
		}
		root, err := openArchiveDirectoryNoSymlink(path)
		if err != nil {
			return 0, err
		}
		b.root = root
		b.path = path
		b.checked = make(map[*os.File]struct{})
	}
	if _, ok := b.checked[file]; !ok {
		if err := b.checkFile(ctx, file); err != nil {
			return 0, err
		}
		b.checked[file] = struct{}{}
	}
	if b.quota == nil {
		quota, err := archivequota.New(b.root, archivequota.Options{MaxBytes: b.maximum, Exclude: controllerArchiveBacklogExclusions})
		if err != nil {
			return 0, err
		}
		b.quota = quota
	}
	n, err := b.quota.Write(ctx, file, data)
	if errors.Is(err, archivequota.ErrCapacity) {
		return n, &archiveOutputAdmissionError{cause: err}
	}
	return n, err
}

func (b *archiveBacklogBudget) checkFile(ctx context.Context, file *os.File) error {
	if file == nil {
		return errors.New("archive backlog output file is missing")
	}
	parent, err := resolveArchiveDirectoryRoot(filepath.Dir(file.Name()), false)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(b.path, filepath.Join(parent, filepath.Base(file.Name())))
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return errors.New("archive output lies outside its admitted namespace")
	}
	first := strings.Split(filepath.ToSlash(relative), "/")[0]
	if archivequota.IsReservedName(first) {
		return errors.New("archive output aliases backlog bookkeeping")
	}
	for _, excluded := range controllerArchiveBacklogExclusions {
		if strings.EqualFold(first, excluded) {
			return fmt.Errorf("archive output belongs to separately admitted namespace %q", excluded)
		}
	}
	// Atomic replacement may have renamed the descriptor's temporary name.
	// Find the actual regular inode in its anchored parent, with bounded work.
	if err := inspectArchiveRelativeParents(b.root, filepath.ToSlash(relative), false, 0); err != nil {
		return err
	}
	parentName := filepath.Dir(relative)
	before, err := b.root.Lstat(parentName)
	if err != nil {
		return err
	}
	if !before.IsDir() {
		return errors.New("archive output parent is not a directory")
	}
	parentRoot, err := b.root.OpenRoot(parentName)
	if err != nil {
		return err
	}
	defer parentRoot.Close()
	openedParent, statErr := parentRoot.Stat(".")
	after, afterErr := b.root.Lstat(parentName)
	if statErr != nil || afterErr != nil || !after.IsDir() || !os.SameFile(before, openedParent) || !os.SameFile(after, openedParent) {
		return errors.New("archive output parent identity changed")
	}
	actual, err := file.Stat()
	if err != nil {
		return err
	}
	if !actual.Mode().IsRegular() {
		return errors.New("archive output is not a regular file")
	}
	if candidate, err := parentRoot.Lstat(filepath.Base(file.Name())); err == nil && candidate.Mode().IsRegular() && os.SameFile(candidate, actual) {
		return nil
	}
	directory, err := parentRoot.Open(".")
	if err != nil {
		return err
	}
	defer directory.Close()
	entries := 0
	for {
		batch, readErr := directory.ReadDir(128)
		for _, entry := range batch {
			entries++
			if entries > 10000 {
				return errors.New("archive output identity inventory limit exceeded")
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			candidate, err := parentRoot.Lstat(entry.Name())
			if err != nil {
				return err
			}
			if candidate.Mode().IsRegular() && os.SameFile(candidate, actual) {
				return nil
			}
		}
		if readErr == io.EOF {
			return errors.New("archive output inode is not present in its admitted namespace")
		}
		if readErr != nil {
			return readErr
		}
	}
}

// Close follows joined writes. Uncertain reservation cleanup remains charged and
// is logged; it cannot turn a durable source into a deletion or failed execution.
func (b *archiveBacklogBudget) close() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	if b.quota != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := b.quota.Close(ctx)
		cancel()
		if err != nil {
			slog.Warn("controller archive backlog reservation retained", "error", err)
		}
	}
	if b.root != nil {
		_ = b.root.Close()
	}
	b.checked = nil
}

func rejectArchiveBacklogDirectoryAlias(root, run string) error {
	ledger, err := os.Lstat(filepath.Join(root, archivequota.DirectoryName))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	target, err := os.Lstat(run)
	if err != nil {
		return err
	}
	if os.SameFile(ledger, target) {
		return errors.New("archive run aliases backlog bookkeeping")
	}
	return nil
}

// Finalization can resume in a retained namespace after a backend change. Pick
// the existing namespace from its admitted layout, never an arbitrary per-run
// directory (which would give every run an independent aggregate quota).
func archiveBacklogRootForRecord(record *storage.RunArchiveRecord) (string, error) {
	if record == nil {
		return "", errors.New("archive record is required")
	}
	path, err := resolveArchiveDirectoryRoot(record.ArchivePath, false)
	if err != nil {
		return "", err
	}
	roots := []string{resolveArchiveRoot()}
	bases := []string{filepath.Join(resolveFilesystemArchiveRoot(), "_object_staging")}
	if custom := strings.TrimSpace(os.Getenv("APEXLOAD_ARCHIVE_STAGING_DIR")); custom != "" {
		bases = append(bases, custom)
	}
	for _, base := range bases {
		for _, backend := range []string{archiveBackendS3, archiveBackendAzureBlob, archiveBackendGCS} {
			parent, err := resolveArchiveDirectoryRoot(filepath.Join(base, backend), false)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return "", err
			}
			relative, err := filepath.Rel(parent, path)
			if err != nil || !filepath.IsLocal(relative) || relative == "." {
				continue
			}
			parts := strings.Split(relative, string(filepath.Separator))
			if len(parts) < 2 {
				continue
			}
			decoded, err := hex.DecodeString(parts[0])
			if err == nil && len(decoded) == 32 {
				roots = append(roots, filepath.Join(parent, parts[0]))
			}
		}
	}
	roots = append(roots, resolveFilesystemArchiveRoot())
	for _, candidate := range roots {
		root, err := resolveArchiveDirectoryRoot(candidate, false)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil || relative == "." || !filepath.IsLocal(relative) {
			continue
		}
		first := strings.Split(relative, string(filepath.Separator))[0]
		excluded := archivequota.IsReservedName(first)
		for _, name := range controllerArchiveBacklogExclusions {
			excluded = excluded || strings.EqualFold(first, name)
		}
		if !excluded {
			return root, nil
		}
	}
	return "", errors.New("archive finalization path is outside admitted workload namespaces")
}
