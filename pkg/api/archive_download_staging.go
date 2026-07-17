package api

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/jasonczech/apexload/pkg/internal/diskspace"
)

const (
	envArchiveDownloadMaxBytes       = "APEXLOAD_ARCHIVE_DOWNLOAD_MAX_BYTES"
	envArchiveDownloadMinFreeBytes   = "APEXLOAD_ARCHIVE_DOWNLOAD_MIN_FREE_BYTES"
	envArchiveDownloadMaxConcurrent  = "APEXLOAD_ARCHIVE_DOWNLOAD_MAX_CONCURRENT"
	envArchiveDownloadStagingDir     = "APEXLOAD_ARCHIVE_DOWNLOAD_STAGING_DIR"
	defaultArchiveDownloadMaxBytes   = int64(32 << 30)
	defaultArchiveDownloadMinFree    = int64(1 << 30)
	defaultArchiveDownloadConcurrent = 2
	maxArchiveDownloadConcurrent     = 32
	archiveDownloadFreeCheckBytes    = int64(1 << 20)
)

type archiveDownloadLimiter struct {
	mu             sync.Mutex
	active         int
	changed        chan struct{}
	diskMu         sync.Mutex
	diskGeneration uint64
}

func newArchiveDownloadLimiter() *archiveDownloadLimiter {
	return &archiveDownloadLimiter{changed: make(chan struct{})}
}

func (l *archiveDownloadLimiter) noteDiskChange() {
	if l == nil {
		return
	}
	l.diskMu.Lock()
	l.diskGeneration++
	l.diskMu.Unlock()
}

func (l *archiveDownloadLimiter) acquire(ctx context.Context, limit int) (func(), error) {
	if l == nil {
		return nil, fmt.Errorf("archive download limiter is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if limit <= 0 {
		limit = defaultArchiveDownloadConcurrent
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		l.mu.Lock()
		if l.changed == nil {
			l.changed = make(chan struct{})
		}
		if l.active < limit {
			l.active++
			l.mu.Unlock()
			var once sync.Once
			return func() {
				once.Do(func() {
					l.mu.Lock()
					if l.active > 0 {
						l.active--
					}
					close(l.changed)
					l.changed = make(chan struct{})
					l.mu.Unlock()
				})
			}, nil
		}
		changed := l.changed
		l.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-changed:
		}
	}
}

var archiveDownloadStages = newArchiveDownloadLimiter()

type stagedArchiveDownload struct {
	File    *os.File
	root    *os.Root
	name    string
	release func()
	limiter *archiveDownloadLimiter
	once    sync.Once
}

type boundedArchiveDownloadWriter struct {
	ctx            context.Context
	file           *os.File
	maxBytes       int64
	minFreeBytes   int64
	written        int64
	nextFreeCheck  int64
	knownFree      int64
	freeKnown      bool
	limiter        *archiveDownloadLimiter
	diskGeneration uint64
}

func (w *boundedArchiveDownloadWriter) Write(content []byte) (int, error) {
	if w == nil || w.file == nil {
		return 0, fmt.Errorf("archive download staging writer is unavailable")
	}
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	if w.limiter == nil {
		return 0, fmt.Errorf("archive download disk budget is unavailable")
	}
	w.limiter.diskMu.Lock()
	defer w.limiter.diskMu.Unlock()
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	length := int64(len(content))
	if w.written > w.maxBytes || length > w.maxBytes-w.written {
		return 0, fmt.Errorf("archive download exceeds byte limit %d", w.maxBytes)
	}
	if !w.freeKnown || w.diskGeneration != w.limiter.diskGeneration || w.written >= w.nextFreeCheck {
		free, err := archiveDownloadFreeBytes(w.file)
		if err != nil {
			return 0, err
		}
		w.knownFree = free
		w.freeKnown = true
		w.diskGeneration = w.limiter.diskGeneration
		if w.written > int64(^uint64(0)>>1)-archiveDownloadFreeCheckBytes {
			w.nextFreeCheck = int64(^uint64(0) >> 1)
		} else {
			w.nextFreeCheck = w.written + archiveDownloadFreeCheckBytes
		}
	}
	if w.knownFree < w.minFreeBytes || length > w.knownFree-w.minFreeBytes {
		return 0, fmt.Errorf("archive download free space would fall below reserve %d", w.minFreeBytes)
	}
	n, err := w.file.Write(content)
	if n < 0 || n > len(content) {
		return 0, fmt.Errorf("archive download writer returned invalid byte count %d", n)
	}
	w.written += int64(n)
	if n > 0 {
		w.limiter.diskGeneration++
		w.diskGeneration = w.limiter.diskGeneration
	}
	if int64(n) >= w.knownFree {
		w.knownFree = 0
	} else {
		w.knownFree -= int64(n)
	}
	if err == nil && n != len(content) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = w.ctx.Err()
	}
	return n, err
}

func archiveDownloadFreeBytes(file *os.File) (int64, error) {
	if file == nil {
		return 0, fmt.Errorf("archive download staging file is unavailable")
	}
	return diskspace.AvailableFile(file)
}

func stageArchiveDownload(ctx context.Context, write func(io.Writer) error) (*stagedArchiveDownload, error) {
	return stageArchiveDownloadWithLimiter(ctx, archiveDownloadStages, write)
}

func stageArchiveDownloadWithLimiter(ctx context.Context, limiter *archiveDownloadLimiter, write func(io.Writer) error) (*stagedArchiveDownload, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if write == nil {
		return nil, fmt.Errorf("archive download writer is required")
	}
	release, err := limiter.acquire(ctx, configuredArchiveDownloadConcurrency())
	if err != nil {
		return nil, err
	}
	rootPath, err := resolveArchiveDirectoryRoot(configuredArchiveDownloadStagingRoot(), true)
	if err != nil {
		release()
		return nil, err
	}
	root, err := openArchiveDirectoryNoSymlink(rootPath)
	if err != nil {
		release()
		return nil, err
	}
	name, file, err := createArchiveDownloadStagingFile(root)
	if err != nil {
		_ = root.Close()
		release()
		return nil, err
	}
	staged := &stagedArchiveDownload{File: file, root: root, name: name, release: release, limiter: limiter}
	fail := func(stageErr error) (*stagedArchiveDownload, error) {
		removeStagedArchiveDownload(staged)
		return nil, stageErr
	}
	bounded := &boundedArchiveDownloadWriter{
		ctx:          ctx,
		file:         file,
		maxBytes:     configuredArchiveDownloadMaxBytes(),
		minFreeBytes: configuredArchiveDownloadMinFreeBytes(),
		limiter:      limiter,
	}
	if err := write(bounded); err != nil {
		return fail(err)
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	limiter.diskMu.Lock()
	syncErr := file.Sync()
	limiter.diskGeneration++
	limiter.diskMu.Unlock()
	if syncErr != nil {
		return fail(syncErr)
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return fail(err)
	}
	return staged, nil
}

func createArchiveDownloadStagingFile(root *os.Root) (string, *os.File, error) {
	if root == nil {
		return "", nil, fmt.Errorf("archive download staging root is unavailable")
	}
	for attempt := 0; attempt < 100; attempt++ {
		name, err := archiveTemporaryName(".", "apexload-archive-download.zip")
		if err != nil {
			return "", nil, err
		}
		file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
		if os.IsExist(err) {
			continue
		}
		if err != nil {
			return "", nil, err
		}
		return name, file, nil
	}
	return "", nil, fmt.Errorf("create unique archive download staging file")
}

func removeStagedArchiveDownload(staged *stagedArchiveDownload) {
	if staged == nil {
		return
	}
	staged.once.Do(func() {
		if staged.File != nil {
			_ = staged.File.Close()
			staged.File = nil
		}
		if staged.root != nil {
			_ = staged.root.Remove(staged.name)
			if staged.limiter != nil {
				staged.limiter.noteDiskChange()
			}
			_ = syncOpenedArchiveDirectory(staged.root)
			_ = staged.root.Close()
			staged.root = nil
		}
		if staged.release != nil {
			staged.release()
			staged.release = nil
		}
	})
}

func configuredArchiveDownloadStagingRoot() string {
	if root := strings.TrimSpace(os.Getenv(envArchiveDownloadStagingDir)); root != "" {
		return root
	}
	return filepath.Join(os.TempDir(), "apexload-archive-downloads")
}

func configuredArchiveDownloadMaxBytes() int64 {
	return positiveArchiveDownloadInt64Env(envArchiveDownloadMaxBytes, defaultArchiveDownloadMaxBytes)
}

func configuredArchiveDownloadMinFreeBytes() int64 {
	return positiveArchiveDownloadInt64Env(envArchiveDownloadMinFreeBytes, defaultArchiveDownloadMinFree)
}

func positiveArchiveDownloadInt64Env(name string, fallback int64) int64 {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func configuredArchiveDownloadConcurrency() int {
	raw := strings.TrimSpace(os.Getenv(envArchiveDownloadMaxConcurrent))
	if raw == "" {
		return defaultArchiveDownloadConcurrent
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return defaultArchiveDownloadConcurrent
	}
	if value > maxArchiveDownloadConcurrent {
		return maxArchiveDownloadConcurrent
	}
	return value
}
