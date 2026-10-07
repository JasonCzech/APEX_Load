package api

import (
	"context"
	"errors"
	"io"
	"os"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/internal/diskspace"
	"github.com/xitongsys/parquet-go/source"
)

var errArchiveOutputLimit = errors.New("archive output byte limit exceeded")
var errArchiveOutputFreeSpace = errors.New("insufficient free space for archive output")

// A frozen operation budget charges every physical write across its output files.
// Failed or removed outputs remain charged; the first write failure is latched.
// Callers provide their own limits, error labels and optional operation context.
type archiveOutputBudget struct {
	backlog                *archiveBacklogBudget
	mu                     sync.Mutex
	root                   string
	remaining, minFree     int64
	err                    error
	limitErr, freeSpaceErr error
	ctx                    context.Context
	// optional is what optional evidence may still take from this budget;
	// it is fixed at the first optional admission (optionalSet).
	optional    int64
	optionalSet bool
}

// Admission rejection is an archive-availability failure, distinct from an
// invalid or missing required generator source. Preserve that distinction at
// distributed terminalization without matching remote error strings.
type archiveOutputAdmissionError struct{ cause error }

func (e *archiveOutputAdmissionError) Error() string { return e.cause.Error() }
func (e *archiveOutputAdmissionError) Unwrap() error { return e.cause }

func isArchiveOutputAdmissionError(err error) bool {
	var rejected *archiveOutputAdmissionError
	return errors.As(err, &rejected)
}

func (b *archiveOutputBudget) limitError() error {
	cause := b.limitErr
	if cause == nil {
		cause = errArchiveOutputLimit
	}
	return &archiveOutputAdmissionError{cause: cause}
}
func (b *archiveOutputBudget) freeSpaceError() error {
	cause := b.freeSpaceErr
	if cause == nil {
		cause = errArchiveOutputFreeSpace
	}
	return &archiveOutputAdmissionError{cause: cause}
}

// admits reports whether n more bytes fit the byte allowance, without
// latching a failure, so a writer can stop cleanly before the limit.
func (b *archiveOutputBudget) admits(n int) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.err == nil && int64(n) <= b.remaining
}

func (b *archiveOutputBudget) failure() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.err
}

type archiveOutputFile struct {
	file   *os.File
	budget *archiveOutputBudget
}

func (w *archiveOutputFile) Write(data []byte) (n int, err error) {
	b := w.budget
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.err != nil {
		return 0, b.err
	}
	defer func() {
		if err != nil {
			b.err = err
		}
	}()
	if int64(len(data)) > b.remaining {
		return 0, b.limitError()
	}
	parent := b.ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	// Share the capture namespace: raw capture, merge and conversion under one
	// archive root must not independently spend the same observed headroom.
	release, err := lockArchiveNamespaceAt(ctx, b.root, "_capture_locks", "capacity.lock")
	if err != nil {
		return 0, err
	}
	defer release()
	available, err := diskspace.AvailableFile(w.file)
	if err != nil {
		return 0, err
	}
	if available < b.minFree || int64(len(data)) > available-b.minFree {
		return 0, b.freeSpaceError()
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	n, err = b.backlog.write(ctx, w.file, data)
	b.remaining -= int64(n)
	if n < len(data) && err == nil {
		err = io.ErrShortWrite
	}
	return n, err
}

func (w *archiveOutputFile) Close() error { return w.file.Close() }
func (w *archiveOutputFile) Sync() error  { return w.file.Sync() }

// parquet-go's writer only needs Write and Close. Reject the other interface
// operations so a library change cannot bypass byte admission or open a path.
func (w *archiveOutputFile) Read([]byte) (int, error) {
	return 0, errors.New("archive output file is write-only")
}
func (w *archiveOutputFile) Seek(int64, int) (int64, error) {
	return 0, errors.New("archive output file is append-only")
}
func (w *archiveOutputFile) Open(string) (source.ParquetFile, error) {
	return nil, errors.New("archive output cannot open another file")
}
func (w *archiveOutputFile) Create(string) (source.ParquetFile, error) {
	return nil, errors.New("archive output cannot create another file")
}

// admitOptional charges n bytes of optional evidence written into the
// archive root (HARs copied across filesystems, the merged interactions
// sidecar). All optional evidence of one budget shares one allowance, fixed
// at its first admission: a quarter of the byte allowance and of the free
// space above the floor then left, so the required outputs written later
// (provider evidence, conversion, the manifest) keep the rest. A refusal is
// not latched: optional evidence is omitted, never fatal to the archive.
func (b *archiveOutputBudget) admitOptional(n int64) bool {
	if b == nil {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.err != nil || n < 0 || n > b.remaining || b.optionalSet && n > b.optional {
		return false
	}
	parent := b.ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	release, err := lockArchiveNamespaceAt(ctx, b.root, "_capture_locks", "capacity.lock")
	if err != nil {
		return false
	}
	defer release()
	available, err := diskspace.AvailablePath(b.root)
	if err != nil || available < b.minFree || n > available-b.minFree {
		return false
	}
	if !b.optionalSet {
		b.optional, b.optionalSet = min(b.remaining, available-b.minFree)/4, true
	}
	if n > b.optional {
		return false
	}
	b.optional -= n
	b.remaining -= n
	return true
}

func (b *archiveOutputBudget) close() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.backlog.close()
}
