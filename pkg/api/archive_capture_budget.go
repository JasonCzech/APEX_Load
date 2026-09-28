package api

import (
	"context"
	"errors"
	"io"
	"os"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/internal/diskspace"
)

const (
	envArchiveCaptureMaxBytes     = "APEXLOAD_ARCHIVE_CAPTURE_MAX_BYTES"
	envArchiveCaptureMinFreeBytes = "APEXLOAD_ARCHIVE_CAPTURE_MIN_FREE_BYTES"
)

var errArchiveCaptureLimit = errors.New("controller archive capture byte limit exceeded")
var errArchiveCaptureFreeSpace = errors.New("insufficient free space for controller archive capture")

// Charge serialized records before buffering, so rejecting a record for logical
// capacity does not leave half of its JSONL in the buffer. Physical writes have a
// separate free-space guard: bytes accepted by a buffer are not yet on disk.
// This budget covers raw capture only, not derived reports or retained sources.
type archiveCaptureBudget struct {
	mu        sync.Mutex
	remaining int64
	minFree   int64
	root      string
}

func newArchiveCaptureBudget(root string) (*archiveCaptureBudget, error) {
	limit, err := collectionCacheByteLimit(envArchiveCaptureMaxBytes, 32<<30, 32<<30)
	if err != nil {
		return nil, err
	}
	minFree, err := collectionCacheByteLimit(envArchiveCaptureMinFreeBytes, 64<<20, 1<<50)
	if err != nil {
		return nil, err
	}
	return &archiveCaptureBudget{root: root, remaining: limit, minFree: minFree}, nil
}

func (b *archiveCaptureBudget) reserve(n int64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if n < 0 || n > b.remaining {
		return errArchiveCaptureLimit
	}
	b.remaining -= n
	return nil
}

func (b *archiveCaptureBudget) refund(n int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.remaining += n
}

type archiveCaptureRecordWriter struct {
	destination io.Writer
	budget      *archiveCaptureBudget
}

func (w archiveCaptureRecordWriter) Write(data []byte) (int, error) {
	if err := w.budget.reserve(int64(len(data))); err != nil {
		return 0, err
	}
	n, err := w.destination.Write(data)
	if n < 0 || n > len(data) {
		return 0, errors.New("archive capture writer returned invalid byte count")
	}
	w.budget.refund(int64(len(data) - n))
	if n < len(data) && err == nil {
		err = io.ErrShortWrite
	}
	return n, err
}

type archiveCaptureFileWriter struct {
	file   *os.File
	budget *archiveCaptureBudget
}

func (w archiveCaptureFileWriter) Write(data []byte) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	release, err := lockArchiveNamespaceAt(ctx, w.budget.root, "_capture_locks", "capacity.lock")
	if err != nil {
		return 0, err
	}
	defer release()
	available, err := diskspace.AvailableFile(w.file)
	if err != nil {
		return 0, err
	}
	if available < w.budget.minFree || int64(len(data)) > available-w.budget.minFree {
		return 0, errArchiveCaptureFreeSpace
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return w.file.Write(data)
}
