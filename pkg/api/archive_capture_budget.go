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
	backlog   *archiveBacklogBudget
	mu        sync.Mutex
	remaining int64
	minFree   int64
	root      string
	// optional is the optional evidence (HARs, interaction records)
	// admitted so far, within optionalCap: a quarter of the capture limit
	// and of the free space above the floor, fixed at the first optional
	// admission, so required capture always keeps the rest.
	optional, optionalCap int64
	optionalSet           bool
}

// optionalEvidenceFreeBytes reads the free space an optional-evidence
// allowance is fixed against (tests replace it).
var optionalEvidenceFreeBytes = diskspace.AvailablePath

// admitOptional admits n bytes of optional evidence within the run's
// optional allowance. A refusal omits the evidence; it never fails capture.
func (b *archiveCaptureBudget) admitOptional(n int64) bool {
	if b == nil {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if n < 0 || n > b.remaining {
		return false
	}
	if !b.optionalSet {
		free, err := optionalEvidenceFreeBytes(b.root)
		if err != nil {
			return false
		}
		b.optionalCap, b.optionalSet = min(b.remaining, max(free-b.minFree, 0))/4, true
	}
	if n > b.optionalCap-b.optional {
		return false
	}
	b.optional += n
	return true
}

// releaseOptional returns an admitted optional length that was not written.
func (b *archiveCaptureBudget) releaseOptional(n int64) {
	if b == nil || n <= 0 {
		return
	}
	b.mu.Lock()
	b.optional = max(b.optional-n, 0)
	b.mu.Unlock()
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
	backlog, err := newArchiveBacklogBudget(root)
	if err != nil {
		return nil, err
	}
	return &archiveCaptureBudget{backlog: backlog, root: root, remaining: limit, minFree: minFree}, nil
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
	return w.budget.backlog.write(ctx, w.file, data)
}

func (b *archiveCaptureBudget) close() {
	if b != nil {
		b.backlog.close()
	}
}
