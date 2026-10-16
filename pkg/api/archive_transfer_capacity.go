package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/internal/diskspace"
)

const (
	envArchiveTransferDir           = "APEXLOAD_ARCHIVE_TRANSFER_DIR"
	envArchiveTransferMaxBytes      = "APEXLOAD_ARCHIVE_TRANSFER_MAX_BYTES"
	envArchiveTransferMinFreeBytes  = "APEXLOAD_ARCHIVE_TRANSFER_MIN_FREE_BYTES"
	archiveTransferReservation      = ".apexload-transfer-v1"
	archiveTransferInventoryEntries = 100000
)

// A reservation charges the full per-run ceiling, including while downloaded
// files are being merged. A crash deliberately leaves that reservation charged:
// elapsed time alone cannot prove that another controller has stopped using it.
// Capacity admission never removes an unknown or abandoned directory.
type archiveTransferLease struct {
	mu               sync.Mutex
	root, path       string
	identity, marker os.FileInfo
	minFree          int64
	closed           bool
}

func reserveArchiveTransfer(ctx context.Context, limit int64) (*archiveTransferLease, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > remoteArchiveTotalMaxBytes {
		return nil, errors.New("invalid archive transfer reservation")
	}
	max, err := collectionCacheByteLimit(envArchiveTransferMaxBytes, 128<<30, 1<<50)
	if err != nil {
		return nil, err
	}
	minFree, err := collectionCacheByteLimit(envArchiveTransferMinFreeBytes, 64<<20, 1<<50)
	if err != nil {
		return nil, err
	}
	root := strings.TrimSpace(os.Getenv(envArchiveTransferDir))
	if root == "" {
		root = filepath.Join(os.TempDir(), "apexload-archive-transfers")
	}
	if !filepath.IsAbs(root) {
		return nil, fmt.Errorf("%s must be absolute", envArchiveTransferDir)
	}
	root, err = resolveArchiveDirectoryRoot(root, true)
	if err != nil {
		return nil, err
	}
	release, err := lockArchiveNamespaceAt(ctx, root, "_transfer_locks", "capacity.lock")
	if err != nil {
		return nil, err
	}
	defer release()
	used, err := archiveTransferUsage(ctx, root, max)
	if err != nil {
		return nil, err
	}
	if limit > max-used {
		return nil, errors.New("archive transfer staging capacity exceeded")
	}
	free, err := diskspace.AvailablePath(root)
	if err != nil {
		return nil, err
	}
	if free < minFree+4096 {
		return nil, errors.New("insufficient free space for archive transfer reservation")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, err := createArchiveTemporaryDirectory(root, "transfer", 0o700)
	if err != nil {
		return nil, err
	}
	identity, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	file, err := createArchiveArtifactExclusive(path, archiveTransferReservation, 0o600, false)
	if err != nil {
		return nil, err
	} // Leave uncertain setup evidence charged by inventory.
	_, writeErr := io.WriteString(file, strconv.FormatInt(limit, 10)+"\n")
	syncErr := file.Sync()
	marker, statErr := file.Stat()
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, statErr, closeErr); err != nil {
		return nil, err
	}
	// Sync the reservation and its parent before a transfer can begin. Match
	// the existing archive publisher's Windows directory-sync compatibility.
	for _, directory := range []string{path, root} {
		if err := syncArchiveTransferDirectory(directory); err != nil {
			return nil, err
		}
	}
	return &archiveTransferLease{root: root, path: path, identity: identity, marker: marker, minFree: minFree}, nil
}

// Bound both traversal work and arithmetic. Descriptor-relative opens do not
// follow symlinks. A malformed reservation or unfamiliar special file fails
// admission instead of silently undercounting retained evidence.
func archiveTransferUsage(ctx context.Context, rootPath string, ceiling int64) (int64, error) {
	root, err := openArchiveDirectoryNoSymlink(rootPath)
	if err != nil {
		return 0, err
	}
	defer root.Close()
	entries := 0
	var walk func(*os.Root, int, bool) (int64, error)
	walk = func(dir *os.Root, depth int, reservationDirectory bool) (int64, error) {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if depth > 8 {
			return 0, errors.New("archive transfer inventory depth exceeded")
		}
		file, err := dir.Open(".")
		if err != nil {
			return 0, err
		}
		defer file.Close()
		var used, reserved int64
		for {
			batch, readErr := file.ReadDir(128)
			for _, entry := range batch {
				entries++
				if entries > archiveTransferInventoryEntries {
					return 0, errors.New("archive transfer inventory entry limit exceeded")
				}
				if err := ctx.Err(); err != nil {
					return 0, err
				}
				info, err := dir.Lstat(entry.Name())
				if err != nil {
					return 0, err
				}
				var size int64
				switch {
				case info.IsDir():
					child, err := openArchiveChildDirectoryAt(dir, entry.Name(), false, 0)
					if err != nil {
						return 0, err
					}
					size, err = walk(child, depth+1, depth == 0)
					_ = child.Close()
					if err != nil {
						return 0, err
					}
				case info.Mode().IsRegular():
					size = info.Size()
					if reservationDirectory && entry.Name() == archiveTransferReservation {
						if size <= 0 || size > 32 {
							return 0, errors.New("invalid archive transfer reservation marker")
						}
						f, _, err := openArchiveArtifactForReadAt(dir, entry.Name())
						if err != nil {
							return 0, err
						}
						opened, statErr := f.Stat()
						data, readErr := io.ReadAll(io.LimitReader(f, 33))
						_ = f.Close()
						if statErr != nil || !os.SameFile(info, opened) || readErr != nil {
							return 0, errors.New("archive transfer reservation changed during read")
						}
						n, parseErr := strconv.ParseInt(strings.TrimSuffix(string(data), "\n"), 10, 64)
						if parseErr != nil || n <= 0 || n > remoteArchiveTotalMaxBytes || string(data) != strconv.FormatInt(n, 10)+"\n" {
							return 0, errors.New("invalid archive transfer reservation marker")
						}
						reserved, size = n, 0
					}
				default:
					return 0, errors.New("archive transfer inventory contains a non-regular entry")
				}
				if size < 0 || size > ceiling-used {
					return 0, errors.New("archive transfer staging capacity exceeded")
				}
				used += size
			}
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				return 0, readErr
			}
		}
		if reserved > used {
			used = reserved
		}
		if used > ceiling {
			return 0, errors.New("archive transfer staging capacity exceeded")
		}
		return used, nil
	}
	return walk(root, 0, false)
}

// The caller owns the lease mutex and capacity fence. Check both identities so
// cleanup cannot delete a replacement operation that reused this pathname.
func (l *archiveTransferLease) unchanged() error {
	info, err := os.Lstat(l.path)
	if err != nil || !info.IsDir() || !os.SameFile(info, l.identity) {
		return errors.New("archive transfer directory identity changed")
	}
	marker, err := os.Lstat(filepath.Join(l.path, archiveTransferReservation))
	if err != nil || !marker.Mode().IsRegular() || !os.SameFile(marker, l.marker) {
		return errors.New("archive transfer reservation identity changed")
	}
	return nil
}

func (l *archiveTransferLease) close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	release, err := lockArchiveNamespaceAt(ctx, l.root, "_transfer_locks", "capacity.lock")
	if err != nil {
		return err
	}
	defer release()
	if err := l.unchanged(); err != nil {
		return err
	}
	if err := removeArchiveSubdirectory(l.root, l.path); err != nil {
		return err
	}
	l.closed = true
	return nil
}

type archiveTransferFileWriter struct {
	ctx   context.Context
	lease *archiveTransferLease
	file  *os.File
}

func (w archiveTransferFileWriter) Write(content []byte) (int, error) {
	l := w.lease
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return 0, errors.New("archive transfer reservation is closed")
	}
	release, err := lockArchiveNamespaceAt(w.ctx, l.root, "_transfer_locks", "capacity.lock")
	if err != nil {
		return 0, err
	}
	defer release()
	if err := l.unchanged(); err != nil {
		return 0, err
	}
	free, err := diskspace.AvailableFile(w.file)
	if err != nil {
		return 0, err
	}
	if free < l.minFree || int64(len(content)) > free-l.minFree {
		return 0, errors.New("insufficient free space for archive transfer")
	}
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	return w.file.Write(content)
}

func syncArchiveTransferDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	syncErr, closeErr := syncArchiveDirectoryHandle(dir), dir.Close()
	if isIgnorableArchiveDirectorySyncError(syncErr) {
		syncErr = nil
	}
	return errors.Join(syncErr, closeErr)
}
