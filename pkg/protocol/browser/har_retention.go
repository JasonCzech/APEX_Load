package browser

import (
	"container/heap"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/scrub"
)

// Runtime HAR capture is opt-in, but a long-running test can still create one
// retained artifact per browser session (notably with new_user_per_iteration).
// Keep a hard process-level safety ceiling even when the controller's
// time-based archive-retention policy is disabled or has not run yet.
const (
	maxRetainedRuntimeHARArtifacts = 128
	maxRetainedRuntimeHARBytes     = int64(1 << 30)
)

var runtimeHARRetentionMu sync.Mutex

type retainedHARArtifact struct {
	path       string
	info       os.FileInfo
	modifiedAt time.Time
	size       int64
}

type retainedHARHeap []retainedHARArtifact

func (h retainedHARHeap) Len() int { return len(h) }
func (h retainedHARHeap) Less(i, j int) bool {
	if h[i].modifiedAt.Equal(h[j].modifiedAt) {
		return h[i].path < h[j].path
	}
	return h[i].modifiedAt.Before(h[j].modifiedAt)
}
func (h retainedHARHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *retainedHARHeap) Push(value any) {
	*h = append(*h, value.(retainedHARArtifact))
}
func (h *retainedHARHeap) Pop() any {
	old := *h
	last := len(old) - 1
	value := old[last]
	old[last] = retainedHARArtifact{}
	*h = old[:last]
	return value
}

func isRetainedRuntimeHARArtifact(name string) bool {
	return strings.HasPrefix(name, "apexload_browser_") &&
		strings.HasSuffix(name, "_"+scrub.SensitivityScrubbed+".har")
}

func enforceRuntimeHARRetention(dir string) error {
	return enforceRuntimeHARRetentionLimit(dir, maxRetainedRuntimeHARArtifacts, maxRetainedRuntimeHARBytes)
}

// enforceRuntimeHARRetentionLimit keeps the newest completed, scrubbed runtime
// HARs within both limits. Its heap is itself bounded by maxArtifacts, so a
// pre-existing directory with many artifacts cannot turn cleanup into another
// unbounded-memory path.
func enforceRuntimeHARRetentionLimit(dir string, maxArtifacts int, maxBytes int64) error {
	if maxArtifacts <= 0 || maxBytes <= 0 {
		return fmt.Errorf("runtime HAR retention limits must be positive")
	}
	dir = strings.TrimSpace(dir)
	if dir == "" {
		dir = os.TempDir()
	}

	runtimeHARRetentionMu.Lock()
	defer runtimeHARRetentionMu.Unlock()

	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer directory.Close()

	kept := &retainedHARHeap{}
	heap.Init(kept)
	var keptBytes int64
	var cleanupErrors []error
	for {
		entries, readErr := directory.ReadDir(256)
		for _, entry := range entries {
			if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !isRetainedRuntimeHARArtifact(entry.Name()) {
				continue
			}
			info, infoErr := entry.Info()
			if infoErr != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
				continue
			}
			candidate := retainedHARArtifact{
				path:       filepath.Join(dir, entry.Name()),
				info:       info,
				modifiedAt: info.ModTime(),
				size:       info.Size(),
			}
			heap.Push(kept, candidate)
			keptBytes += candidate.size
			for kept.Len() > maxArtifacts || keptBytes > maxBytes {
				oldest := heap.Pop(kept).(retainedHARArtifact)
				keptBytes -= oldest.size
				if removeErr := removeRetainedRuntimeHAR(oldest); removeErr != nil {
					cleanupErrors = append(cleanupErrors, removeErr)
				}
			}
		}
		if errors.Is(readErr, io.EOF) {
			return errors.Join(cleanupErrors...)
		}
		if readErr != nil {
			cleanupErrors = append(cleanupErrors, readErr)
			return errors.Join(cleanupErrors...)
		}
	}
}

func removeRetainedRuntimeHAR(candidate retainedHARArtifact) error {
	current, err := os.Lstat(candidate.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() || !os.SameFile(candidate.info, current) {
		return fmt.Errorf("runtime HAR artifact %q changed during retention", filepath.Base(candidate.path))
	}
	return os.Remove(candidate.path)
}
