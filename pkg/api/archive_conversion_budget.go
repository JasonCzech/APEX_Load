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

const (
	envArchiveConversionMaxBytes     = "APEXLOAD_ARCHIVE_CONVERSION_MAX_BYTES"
	envArchiveConversionMinFreeBytes = "APEXLOAD_ARCHIVE_CONVERSION_MIN_FREE_BYTES"
)

var errArchiveConversionLimit = errors.New("archive conversion byte limit exceeded")
var errArchiveConversionFreeSpace = errors.New("insufficient free space for archive conversion")

// One frozen budget covers all Parquet and declared CSV outputs in a conversion.
// Written bytes stay charged even if an incomplete output is later removed. This
// is not a quota for source retention, distributed merge or publication copies.
type archiveConversionBudget struct {
	mu                 sync.Mutex
	root               string
	remaining, minFree int64
	err                error
}

func newArchiveConversionBudget(root string) (*archiveConversionBudget, error) {
	limit, err := collectionCacheByteLimit(envArchiveConversionMaxBytes, 32<<30, 128<<30)
	if err != nil {
		return nil, err
	}
	minFree, err := collectionCacheByteLimit(envArchiveConversionMinFreeBytes, 64<<20, 1<<50)
	if err != nil {
		return nil, err
	}
	return &archiveConversionBudget{root: root, remaining: limit, minFree: minFree}, nil
}

func conversionBudget(root string, budgets []*archiveConversionBudget) (*archiveConversionBudget, error) {
	if len(budgets) == 0 {
		return newArchiveConversionBudget(root)
	}
	if len(budgets) != 1 || budgets[0] == nil {
		return nil, errors.New("exactly one archive conversion budget is required")
	}
	return budgets[0], nil
}

func (b *archiveConversionBudget) failure() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.err
}

type archiveConversionFile struct {
	file   *os.File
	budget *archiveConversionBudget
}

func (w *archiveConversionFile) Write(data []byte) (n int, err error) {
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
		return 0, errArchiveConversionLimit
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Use the capture namespace too: conversions and raw capture under a shared
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
		return 0, errArchiveConversionFreeSpace
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	n, err = w.file.Write(data)
	b.remaining -= int64(n)
	if n < len(data) && err == nil {
		err = io.ErrShortWrite
	}
	return n, err
}

func (w *archiveConversionFile) Close() error { return w.file.Close() }

// parquet-go's writer only needs Write and Close. Reject the other interface
// operations so a library change cannot bypass byte admission or open a path.
func (w *archiveConversionFile) Read([]byte) (int, error) {
	return 0, errors.New("archive conversion file is write-only")
}
func (w *archiveConversionFile) Seek(int64, int) (int64, error) {
	return 0, errors.New("archive conversion file is append-only")
}
func (w *archiveConversionFile) Open(string) (source.ParquetFile, error) {
	return nil, errors.New("archive conversion cannot open another file")
}
func (w *archiveConversionFile) Create(string) (source.ParquetFile, error) {
	return nil, errors.New("archive conversion cannot create another file")
}
