package browser

import (
	"container/heap"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
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
	if maxArtifacts <= 0 || maxBytes <= 0 || maxBytes > maxRetainedRuntimeHARBytes {
		return fmt.Errorf("runtime HAR retention limits must be positive")
	}
	dir = strings.TrimSpace(dir)
	if dir == "" {
		dir = os.TempDir()
	}

	ctx, cancel := context.WithTimeout(context.Background(), runtimeHARCloseWait)
	defer cancel()
	return WithRuntimeHARArtifactNamespace(ctx, dir, func(root *os.Root) error {
		return enforceRuntimeHARRetentionAtContext(ctx, root, maxArtifacts, maxBytes)
	})
}

const maxRuntimeHARRetentionScanEntries = 1024
const maxRuntimeHARRetentionErrors = 16

func enforceRuntimeHARRetentionAt(root *os.Root, maxArtifacts int, maxBytes int64) error {
	ctx, cancel := context.WithTimeout(context.Background(), runtimeHARCloseWait)
	defer cancel()
	return enforceRuntimeHARRetentionAtContext(ctx, root, maxArtifacts, maxBytes)
}

func enforceRuntimeHARRetentionAtContext(ctx context.Context, root *os.Root, maxArtifacts int, maxBytes int64) (err error) {
	if ctx == nil || root == nil || maxArtifacts <= 0 || maxArtifacts > maxRetainedRuntimeHARArtifacts || maxBytes <= 0 || maxBytes > maxRetainedRuntimeHARBytes {
		return fmt.Errorf("HAR retention requires bounded limits and an owned context/root")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, directory.Close()) }()
	kept := &retainedHARHeap{}
	heap.Init(kept)
	var keptBytes int64
	var cleanupErrors []error
	suppressed, scanned := 0, 0
	addError := func(e error) {
		if e == nil {
			return
		}
		if len(cleanupErrors) < maxRuntimeHARRetentionErrors {
			cleanupErrors = append(cleanupErrors, e)
		} else {
			suppressed++
		}
	}
	result := func(e error) error {
		addError(e)
		if suppressed > 0 {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("%d additional HAR retention errors", suppressed))
		}
		return errors.Join(cleanupErrors...)
	}
	for {
		if err := ctx.Err(); err != nil {
			return result(err)
		}
		entries, readErr := directory.ReadDir(64)
		for _, entry := range entries {
			scanned++
			if scanned > maxRuntimeHARRetentionScanEntries {
				return result(fmt.Errorf("HAR retention scan limit reached"))
			}
			if err := ctx.Err(); err != nil {
				return result(err)
			}
			if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !isRetainedRuntimeHARArtifact(entry.Name()) {
				continue
			}
			info, infoErr := root.Lstat(entry.Name())
			if infoErr != nil {
				addError(infoErr)
				continue
			}
			if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
				continue
			}
			candidate := retainedHARArtifact{path: entry.Name(), info: info, modifiedAt: info.ModTime(), size: info.Size()}
			if candidate.size > maxBytes {
				addError(removeRetainedRuntimeHARAt(root, candidate))
				continue
			}
			heap.Push(kept, candidate)
			keptBytes += candidate.size
			for kept.Len() > maxArtifacts || keptBytes > maxBytes {
				oldest := heap.Pop(kept).(retainedHARArtifact)
				keptBytes -= oldest.size
				addError(removeRetainedRuntimeHARAt(root, oldest))
			}
		}
		if errors.Is(readErr, io.EOF) {
			return result(nil)
		}
		if readErr != nil {
			return result(readErr)
		}
	}
}

func removeRetainedRuntimeHARAt(root *os.Root, candidate retainedHARArtifact) error {
	current, err := root.Lstat(candidate.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if !current.Mode().IsRegular() || current.Mode()&os.ModeSymlink != 0 || !os.SameFile(candidate.info, current) || current.Size() != candidate.size || !current.ModTime().Equal(candidate.modifiedAt) {
		return fmt.Errorf("runtime HAR artifact changed during retention")
	}
	return RemoveRuntimeHARArtifactAt(root, candidate.path, candidate.info)
}
