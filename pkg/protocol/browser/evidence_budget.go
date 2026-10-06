package browser

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jasonczech/apexload/pkg/internal/diskspace"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/process"
)

// Generator-wide budgets for browser evidence. Every native HAR writer
// reserves its full per-HAR allowance before its context opens, against an
// aggregate allowance and free scratch space, and every browser context is
// admitted against measured driver, browser and generator memory. Playwright
// buffers a recording until the context closes, so a watchdog samples that
// memory while writers are active instead of waiting for the file to grow.
// A writer or context that does not fit is refused up front; a recording that
// outgrows the budget closes its context as an explicit resource-limit failure.

const (
	harAggregateReservationBytes = maxRetainedRuntimeHARBytes
	harScratchHeadroomBytes      = int64(512 << 20)
	// browserContextMarginalBytes covers one more context: about 98 MB was
	// measured per additional browser VUser (2026-09-15), rounded up.
	browserContextMarginalBytes = int64(128 << 20)
	browserMemoryLimitFraction  = 0.9
	browserMemorySampleTTL      = time.Second
	envBrowserMemoryLimitMB     = "APEXLOAD_BROWSER_MEMORY_LIMIT_MB"

	resourceLimitHARCapture    = "har_capture"
	resourceLimitBrowserMemory = "browser_memory"
)

// errBrowserResourceLimit marks a session refused or closed to protect the
// generator. Its text is what result classification keys on.
var errBrowserResourceLimit = errors.New("browser resource limit")

// BrowserEvidenceStats counts generator-wide evidence admission and overflow.
type BrowserEvidenceStats struct {
	HARWritersAdmitted      int64 `json:"har_writers_admitted"`
	HARWritersRefused       int64 `json:"har_writers_refused"`
	HARScratchRefusals      int64 `json:"har_scratch_refusals"`
	HAROverflowClosures     int64 `json:"har_overflow_closures"`
	MemoryWatchdogClosures  int64 `json:"memory_watchdog_closures"`
	ContextsRefusedByMemory int64 `json:"contexts_refused_by_memory"`
	NetworkTasksDropped     int64 `json:"network_tasks_dropped"`
}

type evidenceBudgetCounters struct {
	harAdmitted, harRefused, harScratch, harOverflow, watchdog, contextsRefused, networkDropped atomic.Int64
}

type evidenceBudget struct {
	mu                  sync.Mutex
	writers             map[*Browser]struct{}
	reserved            int64
	sampledAt           time.Time
	sampledBytes        int64
	admittedSinceSample int64
	watchdogRunning     bool
	counters            evidenceBudgetCounters

	// Measurements; tests replace them on their own budget.
	scratchAvailable func(string) (int64, error)
	memoryUsage      func(context.Context) (int64, error)
	memoryLimit      func() int64
	watchdogInterval time.Duration
}

var browserEvidenceBudget = newEvidenceBudget()

func newEvidenceBudget() *evidenceBudget {
	return &evidenceBudget{writers: map[*Browser]struct{}{}, scratchAvailable: diskspace.AvailablePath, memoryUsage: measureBrowserMemoryUsage, memoryLimit: resolvedBrowserMemoryLimit, watchdogInterval: time.Second}
}

// BrowserEvidenceStatsSnapshot returns the process-wide evidence counters.
func BrowserEvidenceStatsSnapshot() BrowserEvidenceStats {
	c := &browserEvidenceBudget.counters
	return BrowserEvidenceStats{
		HARWritersAdmitted: c.harAdmitted.Load(), HARWritersRefused: c.harRefused.Load(), HARScratchRefusals: c.harScratch.Load(),
		HAROverflowClosures: c.harOverflow.Load(), MemoryWatchdogClosures: c.watchdog.Load(), ContextsRefusedByMemory: c.contextsRefused.Load(),
		NetworkTasksDropped: c.networkDropped.Load(),
	}
}

// reserveHARWriter reserves one full per-HAR allowance for b. It returns the
// refusal reason, or "" when the writer may record.
func (e *evidenceBudget) reserveHARWriter(b *Browser, dir string) string {
	if strings.TrimSpace(dir) == "" {
		dir = os.TempDir()
	}
	free, freeErr := e.scratchAvailable(dir)
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, held := e.writers[b]; held {
		return ""
	}
	if len(e.writers) >= maxRetainedRuntimeHARArtifacts || e.reserved+maxRuntimeHARBytes > harAggregateReservationBytes {
		e.counters.harRefused.Add(1)
		return "har_budget_exhausted"
	}
	if freeErr != nil || free < e.reserved+maxRuntimeHARBytes+harScratchHeadroomBytes {
		e.counters.harScratch.Add(1)
		return "har_scratch_headroom"
	}
	e.writers[b] = struct{}{}
	e.reserved += maxRuntimeHARBytes
	e.counters.harAdmitted.Add(1)
	if !e.watchdogRunning {
		e.watchdogRunning = true
		go e.watch()
	}
	return ""
}

func (e *evidenceBudget) releaseHARWriter(b *Browser) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, held := e.writers[b]; held {
		delete(e.writers, b)
		e.reserved -= maxRuntimeHARBytes
	}
}

// admitBrowserContext refuses a new context when measured memory plus one
// context's marginal footprint would exceed the generator's browser memory
// limit. Contexts admitted since the last sample count as already used, so a
// burst cannot pass on one stale measurement.
func (e *evidenceBudget) admitBrowserContext(ctx context.Context) error {
	limit := e.memoryLimit()
	if limit <= 0 {
		return nil
	}
	e.mu.Lock()
	stale := time.Since(e.sampledAt) > browserMemorySampleTTL
	e.mu.Unlock()
	if stale {
		if usage, err := e.memoryUsage(ctx); err == nil {
			e.recordSample(usage)
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.sampledAt.IsZero() {
		// Without a measurement there is nothing to admit against.
		return nil
	}
	projected := e.sampledBytes + (e.admittedSinceSample+1)*browserContextMarginalBytes
	if projected > limit {
		e.counters.contextsRefused.Add(1)
		return fmt.Errorf("%w: measured browser and generator memory %d MiB plus a new context would exceed %d MiB", errBrowserResourceLimit, e.sampledBytes>>20, limit>>20)
	}
	e.admittedSinceSample++
	return nil
}

func (e *evidenceBudget) recordSample(usage int64) {
	e.mu.Lock()
	e.sampledAt, e.sampledBytes, e.admittedSinceSample = time.Now(), usage, 0
	e.mu.Unlock()
}

// watch samples memory while HAR writers are active and closes the writer
// holding the most observed bytes whenever the limit is exceeded.
func (e *evidenceBudget) watch() {
	ticker := time.NewTicker(e.watchdogInterval)
	defer ticker.Stop()
	for range ticker.C {
		e.mu.Lock()
		if len(e.writers) == 0 {
			e.watchdogRunning = false
			e.mu.Unlock()
			return
		}
		writers := make([]*Browser, 0, len(e.writers))
		for b := range e.writers {
			writers = append(writers, b)
		}
		e.mu.Unlock()
		limit := e.memoryLimit()
		if limit <= 0 {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), e.watchdogInterval)
		usage, err := e.memoryUsage(ctx)
		cancel()
		if err != nil {
			continue
		}
		e.recordSample(usage)
		if usage <= limit {
			continue
		}
		var victim *Browser
		most := int64(-1)
		for _, b := range writers {
			if observed := b.harObservedBytesSnapshot(); observed > most {
				victim, most = b, observed
			}
		}
		if victim != nil && victim.closeForResourceLimit(resourceLimitBrowserMemory) {
			e.counters.watchdog.Add(1)
		}
	}
}

func (b *Browser) harObservedBytesSnapshot() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.harObservedBytes
}

// closeForResourceLimit records why the session is being closed, censors its
// unfinished document observations as resource_limit and closes the context.
// It reports whether this call closed a recording session.
func (b *Browser) closeForResourceLimit(reason string) bool {
	b.mu.Lock()
	owner, active := b.harOwner, b.pwContext != nil || b.harOwner != nil
	if !active || b.resourceLimit != "" {
		b.mu.Unlock()
		return false
	}
	b.resourceLimit = reason
	b.mu.Unlock()
	b.closeOwnedOversizedHARContext(owner)
	return true
}

func (b *Browser) resourceLimitReason() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.resourceLimit
}

// applyEvidenceOutcome makes a resource-limit close an explicit failure of the
// affected VUser's action and reports, once per context, evidence that could
// not be recorded and network tasks dropped before evidence was discarded.
func (b *Browser) applyEvidenceOutcome(result *protocol.Result) {
	b.mu.Lock()
	reason := b.resourceLimit
	unavailable := ""
	if b.harUnavailableReason != "" && !b.harUnavailableReported {
		unavailable, b.harUnavailableReported = b.harUnavailableReason, true
	}
	dropped := b.networkTasksDropped > b.networkTasksDroppedReported
	b.networkTasksDroppedReported = b.networkTasksDropped
	b.mu.Unlock()
	if unavailable != "" {
		result.WarningCodes = append(result.WarningCodes, "browser_har_unavailable_"+unavailable)
	}
	if dropped {
		result.WarningCodes = append(result.WarningCodes, "browser_network_tasks_dropped")
	}
	if reason != "" {
		result.Success = false
		result.ErrorMessage = fmt.Sprintf("%v (%s): the browser context was closed to protect the generator", errBrowserResourceLimit, reason)
		result.WarningCodes = append(result.WarningCodes, "browser_resource_limit_"+reason)
	}
}

// measureBrowserMemoryUsage is the resident memory of the Playwright driver,
// every browser process it started and this generator process.
func measureBrowserMemoryUsage(ctx context.Context) (int64, error) {
	self, err := process.NewProcessWithContext(ctx, int32(os.Getpid()))
	if err != nil {
		return 0, err
	}
	info, err := self.MemoryInfoWithContext(ctx)
	if err != nil {
		return 0, err
	}
	total := int64(info.RSS)
	if driver := playwrightDriverPID(); driver > 0 {
		tree, err := processTreeRSSBytes(ctx, driver)
		if err != nil {
			return 0, err
		}
		total += tree
	}
	return total, nil
}

// processTreeRSSBytes sums the resident memory of rootPID and its descendants.
func processTreeRSSBytes(ctx context.Context, rootPID int) (int64, error) {
	out, err := exec.CommandContext(ctx, "ps", "-eo", "pid=,ppid=,rss=,time=").Output()
	if err != nil {
		return 0, err
	}
	children := map[int][]processSample{}
	var root *processSample
	for _, sample := range parseProcessSamples(string(out)) {
		if sample.PID == rootPID {
			s := sample
			root = &s
		}
		children[sample.PPID] = append(children[sample.PPID], sample)
	}
	if root == nil {
		return 0, nil
	}
	total, queue, seen := 0.0, []processSample{*root}, map[int]bool{}
	for len(queue) > 0 {
		sample := queue[0]
		queue = queue[1:]
		if seen[sample.PID] {
			continue
		}
		seen[sample.PID] = true
		total += sample.RSSKB
		queue = append(queue, children[sample.PID]...)
	}
	return int64(total * 1024), nil
}

var browserMemoryLimitOnce struct {
	sync.Once
	value int64
}

// resolvedBrowserMemoryLimit is the configured browser memory limit, else 90%
// of the cgroup memory limit, else 90% of physical memory. Zero disables
// admission.
func resolvedBrowserMemoryLimit() int64 {
	browserMemoryLimitOnce.Do(func() {
		if text := strings.TrimSpace(os.Getenv(envBrowserMemoryLimitMB)); text != "" {
			if mb, err := strconv.ParseInt(text, 10, 64); err == nil && mb >= 0 && mb < 1<<40 {
				browserMemoryLimitOnce.value = mb << 20
				return
			}
		}
		limit := cgroupMemoryLimit()
		if limit <= 0 {
			if vm, err := mem.VirtualMemory(); err == nil {
				limit = int64(vm.Total)
			}
		}
		browserMemoryLimitOnce.value = int64(float64(limit) * browserMemoryLimitFraction)
	})
	return browserMemoryLimitOnce.value
}

func cgroupMemoryLimit() int64 {
	for _, path := range []string{"/sys/fs/cgroup/memory.max", "/sys/fs/cgroup/memory/memory.limit_in_bytes"} {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		text := strings.TrimSpace(string(data))
		if text == "max" {
			return 0
		}
		if value, err := strconv.ParseInt(text, 10, 64); err == nil && value > 0 && value < 1<<60 {
			return value
		}
	}
	return 0
}
