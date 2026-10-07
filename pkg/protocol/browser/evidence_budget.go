package browser

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"slices"
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
	// browserMemorySampleMaxAge is how long a sample may stand in when new
	// measurements fail; past it the budget has no measurement at all.
	browserMemorySampleMaxAge = 10 * browserMemorySampleTTL
	envBrowserMemoryLimitMB   = "APEXLOAD_BROWSER_MEMORY_LIMIT_MB"

	resourceLimitHARCapture    = "har_capture"
	resourceLimitBrowserMemory = "browser_memory"
	// resourceLimitAdmission marks a session whose context was refused at
	// open; every action of it fails as a resource-limit result.
	resourceLimitAdmission = "browser_memory_admission"
)

// errBrowserResourceLimit marks a session refused or closed to protect the
// generator. Its text is what result classification keys on.
var errBrowserResourceLimit = errors.New("browser resource limit")

// BrowserEvidenceStats counts generator-wide evidence admission and overflow.
type BrowserEvidenceStats struct {
	HARWritersAdmitted      int64 `json:"har_writers_admitted"`
	HARWritersRefused       int64 `json:"har_writers_refused"`
	HARScratchRefusals      int64 `json:"har_scratch_refusals"`
	HARScratchUnavailable   int64 `json:"har_scratch_unavailable"`
	HAROverflowClosures     int64 `json:"har_overflow_closures"`
	MemoryWatchdogClosures  int64 `json:"memory_watchdog_closures"`
	ContextsRefusedByMemory int64 `json:"contexts_refused_by_memory"`
	ContextsUnmeasured      int64 `json:"contexts_admitted_unmeasured"`
	MemorySampleFailures    int64 `json:"memory_sample_failures"`
	NetworkTasksDropped     int64 `json:"network_tasks_dropped"`
}

type evidenceBudgetCounters struct {
	harAdmitted, harRefused, harScratch, harScratchUnavailable, harOverflow, watchdog, contextsRefused, contextsUnmeasured, sampleFailures, networkDropped atomic.Int64
}

type evidenceBudget struct {
	mu       sync.Mutex
	writers  map[*Browser]struct{}
	reserved int64
	// A sample is the measured memory and the admission count when its
	// measurement started: contexts admitted after that start are not in
	// it, and only one measurement runs at a time.
	sampledAt         time.Time
	sampledBytes      int64
	admissions        int64
	sampledAdmissions int64
	measuring         bool
	measured          chan struct{}
	// live holds every admitted context until it closes. A fresh context is
	// nearly blank when sampled, so live contexts keep a footprint floor.
	live            map[*Browser]struct{}
	watchdogRunning bool
	// sampleFailing is set while measurements fail, so each run of failures
	// is logged once rather than once per process.
	sampleFailing bool
	counters      evidenceBudgetCounters

	// Measurements; tests replace them on their own budget.
	scratchAvailable func(string) (int64, error)
	memoryUsage      func(context.Context) (int64, error)
	memoryLimit      func() int64
	watchdogInterval time.Duration
}

var browserEvidenceBudget = newEvidenceBudget()

func newEvidenceBudget() *evidenceBudget {
	return &evidenceBudget{writers: map[*Browser]struct{}{}, live: map[*Browser]struct{}{}, scratchAvailable: diskspace.AvailablePath, memoryUsage: measureBrowserMemoryUsage, memoryLimit: resolvedBrowserMemoryLimit, watchdogInterval: time.Second}
}

// BrowserEvidenceStatsSnapshot returns the process-wide evidence counters.
func BrowserEvidenceStatsSnapshot() BrowserEvidenceStats {
	c := &browserEvidenceBudget.counters
	return BrowserEvidenceStats{
		HARWritersAdmitted: c.harAdmitted.Load(), HARWritersRefused: c.harRefused.Load(), HARScratchRefusals: c.harScratch.Load(),
		HARScratchUnavailable: c.harScratchUnavailable.Load(), HAROverflowClosures: c.harOverflow.Load(), MemoryWatchdogClosures: c.watchdog.Load(),
		ContextsRefusedByMemory: c.contextsRefused.Load(), ContextsUnmeasured: c.contextsUnmeasured.Load(), MemorySampleFailures: c.sampleFailures.Load(),
		NetworkTasksDropped: c.networkDropped.Load(),
	}
}

// logEvidenceEvent reports a refusal or closure with the generator-wide
// counters, so an operator can see evidence pressure in generator logs.
func logEvidenceEvent(message string, attrs ...any) {
	stats := BrowserEvidenceStatsSnapshot()
	slog.Warn(message, append(attrs, "stats", stats)...)
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
	// The aggregate allowance admits harAggregateReservationBytes /
	// maxRuntimeHARBytes concurrent writers (10 by default), well below the
	// artifact cap.
	if len(e.writers) >= maxRetainedRuntimeHARArtifacts || e.reserved+maxRuntimeHARBytes > harAggregateReservationBytes {
		e.counters.harRefused.Add(1)
		return "har_budget_exhausted"
	}
	if freeErr != nil {
		// A missing or unreadable scratch directory is a configuration fault,
		// not a lack of headroom.
		e.counters.harScratchUnavailable.Add(1)
		return "har_scratch_unavailable"
	}
	if free < e.reserved+maxRuntimeHARBytes+harScratchHeadroomBytes {
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

// retireHARWriter releases b's reservation once its HAR owner has retired.
// A writer whose finish outlived the close wait is still producing its raw
// and scrubbed copies, so its bytes stay reserved until the finish completes.
func (e *evidenceBudget) retireHARWriter(b *Browser, owner *harCaptureOwner) {
	if owner == nil || owner.retiredSuccessfully() {
		e.releaseHARWriter(b)
		return
	}
	e.mu.Lock()
	_, held := e.writers[b]
	delete(e.writers, b)
	e.mu.Unlock()
	if !held {
		return
	}
	go func() {
		<-owner.finishDone
		e.mu.Lock()
		e.reserved -= maxRuntimeHARBytes
		e.mu.Unlock()
	}()
}

// admitBrowserContext refuses a new context for b when projected memory plus
// one context's marginal footprint would exceed the generator's browser
// memory limit. The projection is the larger of the latest sample plus every
// context admitted since that measurement started, and the footprint floor of
// every live context. Without a usable measurement only the floor applies;
// such admissions are counted and the measurement failure is logged.
func (e *evidenceBudget) admitBrowserContext(ctx context.Context, b *Browser) error {
	limit := e.memoryLimit()
	if limit <= 0 {
		e.mu.Lock()
		e.live[b] = struct{}{}
		e.mu.Unlock()
		return nil
	}
	e.refreshSample(ctx)
	e.mu.Lock()
	defer e.mu.Unlock()
	live := int64(len(e.live))
	if _, already := e.live[b]; already {
		live--
	}
	projected := live * browserContextMarginalBytes
	measured := !e.sampledAt.IsZero() && time.Since(e.sampledAt) <= browserMemorySampleMaxAge
	if measured {
		pending := e.admissions - e.sampledAdmissions
		projected = max(e.sampledBytes+pending*browserContextMarginalBytes, projected)
	}
	projected += browserContextMarginalBytes
	if !measured && projected <= limit {
		e.counters.contextsUnmeasured.Add(1)
		e.admissions++
		e.live[b] = struct{}{}
		return nil
	}
	if projected > limit {
		e.counters.contextsRefused.Add(1)
		return fmt.Errorf("%w: projected browser and generator memory %d MiB plus a new context would exceed %d MiB", errBrowserResourceLimit, (projected-browserContextMarginalBytes)>>20, limit>>20)
	}
	e.admissions++
	e.live[b] = struct{}{}
	return nil
}

// releaseBrowserContext ends b's live-context floor.
func (e *evidenceBudget) releaseBrowserContext(b *Browser) {
	e.mu.Lock()
	delete(e.live, b)
	e.mu.Unlock()
}

// refreshSample measures when the sample is stale; only one measurement runs
// at a time. Admitters that find no usable sample while another measures
// wait for it rather than being admitted unmeasured. A failed measurement
// keeps the previous sample until it ages out.
func (e *evidenceBudget) refreshSample(ctx context.Context) {
	e.mu.Lock()
	if time.Since(e.sampledAt) <= browserMemorySampleTTL {
		e.mu.Unlock()
		return
	}
	if e.measuring {
		done := e.measured
		usable := !e.sampledAt.IsZero() && time.Since(e.sampledAt) <= browserMemorySampleMaxAge
		e.mu.Unlock()
		if !usable {
			select {
			case <-done:
			case <-ctx.Done():
			}
		}
		return
	}
	e.measuring, e.measured = true, make(chan struct{})
	started := e.admissions
	e.mu.Unlock()
	usage, err := e.memoryUsage(ctx)
	e.mu.Lock()
	// The sample is stored before waiters wake, so none of them finds the
	// budget still unmeasured.
	logFailure := e.recordSampleLocked(usage, started, err)
	e.measuring = false
	close(e.measured)
	e.mu.Unlock()
	if logFailure {
		logEvidenceEvent("browser memory measurement failed; admission falls back to the last sample until it ages out", "error", err)
	}
}

func (e *evidenceBudget) recordSample(usage, admissionsAtStart int64, err error) {
	e.mu.Lock()
	logFailure := e.recordSampleLocked(usage, admissionsAtStart, err)
	e.mu.Unlock()
	if logFailure {
		logEvidenceEvent("browser memory measurement failed; admission falls back to the last sample until it ages out", "error", err)
	}
}

// recordSampleLocked keeps the newer of the stored and the new measurement.
// It reports whether this failure starts a new run of failures to log.
func (e *evidenceBudget) recordSampleLocked(usage, admissionsAtStart int64, err error) bool {
	if err != nil {
		e.counters.sampleFailures.Add(1)
		first := !e.sampleFailing
		e.sampleFailing = true
		return first
	}
	e.sampleFailing = false
	if admissionsAtStart >= e.sampledAdmissions {
		e.sampledAt, e.sampledBytes, e.sampledAdmissions = time.Now(), usage, admissionsAtStart
	}
	return false
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
		started := e.admissions
		e.mu.Unlock()
		limit := e.memoryLimit()
		if limit <= 0 {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), e.watchdogInterval)
		usage, err := e.memoryUsage(ctx)
		cancel()
		e.recordSample(usage, started, err)
		if err != nil || usage <= limit {
			continue
		}
		// Only fully open recording sessions are candidates: a closed one
		// keeps its observed bytes until Teardown, and one still opening has
		// no bound writer to close.
		var victim *Browser
		most := int64(-1)
		for _, b := range writers {
			if observed, eligible := b.watchdogCandidate(); eligible && observed > most {
				victim, most = b, observed
			}
		}
		if victim != nil && victim.closeForResourceLimit(resourceLimitBrowserMemory) {
			e.counters.watchdog.Add(1)
			logEvidenceEvent("browser memory watchdog closed the largest recording session", "usage_mib", usage>>20, "limit_mib", limit>>20)
		}
	}
}

// watchdogCandidate reports the session's observed HAR bytes and whether it
// is fully open: its context and HAR monitor are running and nothing has
// closed it for a resource limit.
func (b *Browser) watchdogCandidate() (int64, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.harObservedBytes, b.resourceLimit == "" && b.pwContext != nil && b.harMonitorStop != nil
}

// closeForResourceLimit records why the session is being closed, censors its
// unfinished document observations as resource_limit and closes the context.
// It reports whether this call closed a recording session; a session that
// is not fully open is left alone.
func (b *Browser) closeForResourceLimit(reason string) bool {
	b.mu.Lock()
	owner := b.harOwner
	if b.resourceLimit != "" || b.pwContext == nil || b.harMonitorStop == nil {
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
		effect := "the browser context was closed to protect the generator"
		if reason == resourceLimitAdmission {
			effect = "the browser context was refused to protect the generator"
		}
		result.ErrorMessage = fmt.Sprintf("%v (%s): %s", errBrowserResourceLimit, reason, effect)
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
// A root missing from the process list is a measurement failure, never zero.
func processTreeRSSBytes(ctx context.Context, rootPID int) (int64, error) {
	samples, err := listProcessSamples(ctx)
	if err != nil {
		return 0, err
	}
	if !slices.ContainsFunc(samples, func(sample processSample) bool { return sample.PID == rootPID }) {
		return 0, fmt.Errorf("process %d is not in the process list", rootPID)
	}
	return processTreeRSS(samples, rootPID), nil
}

// listProcessSamples lists processes with their parent, resident memory and
// CPU time. Linux reads /proc directly: container images ship BusyBox ps,
// whose RSS column is abbreviated (292m), or no ps at all.
func listProcessSamples(ctx context.Context) ([]processSample, error) {
	if runtime.GOOS != "linux" {
		out, err := exec.CommandContext(ctx, "ps", "-eo", "pid=,ppid=,rss=,time=").Output()
		if err != nil {
			return nil, err
		}
		return parseProcessSamples(string(out)), nil
	}
	return readProcProcessSamples(ctx, "/proc")
}

// readProcProcessSamples reads each process's stat file once. A process that
// exits, or whose file cannot be read or parsed, meanwhile is left out.
func readProcProcessSamples(ctx context.Context, root string) ([]processSample, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	pageKB := float64(os.Getpagesize()) / 1024
	samples := make([]processSample, 0, len(entries))
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		data, err := os.ReadFile(root + "/" + entry.Name() + "/stat")
		if err != nil {
			continue
		}
		if sample, ok := parseProcStat(pid, string(data), pageKB); ok {
			samples = append(samples, sample)
		}
	}
	return samples, nil
}

// procClockTicks is USER_HZ, the unit of utime and stime in /proc/<pid>/stat;
// Linux reports it as 100 on every supported architecture.
const procClockTicks = 100

// parseProcStat reads the parent, CPU time and resident pages of one
// /proc/<pid>/stat line. The command name is parenthesized and may itself
// hold spaces or parentheses, so fields are counted from its last ')'.
func parseProcStat(pid int, line string, pageKB float64) (processSample, bool) {
	end := strings.LastIndexByte(line, ')')
	if end < 0 {
		return processSample{}, false
	}
	// After the name: state(3) ppid(4) ... utime(14) stime(15) ... rss(24).
	fields := strings.Fields(line[end+1:])
	if len(fields) < 22 {
		return processSample{}, false
	}
	ppid, ppidErr := strconv.Atoi(fields[1])
	utime, utimeErr := strconv.ParseUint(fields[11], 10, 64)
	stime, stimeErr := strconv.ParseUint(fields[12], 10, 64)
	rss, rssErr := strconv.ParseInt(fields[21], 10, 64)
	if ppidErr != nil || utimeErr != nil || stimeErr != nil || rssErr != nil || rss < 0 {
		return processSample{}, false
	}
	return processSample{PID: pid, PPID: ppid, RSSKB: float64(rss) * pageKB, CPUTimeSeconds: float64(utime+stime) / procClockTicks}, true
}

// processTreeRSS sums the resident memory of rootPID and every descendant.
func processTreeRSS(samples []processSample, rootPID int) int64 {
	children := map[int][]processSample{}
	var root *processSample
	for _, sample := range samples {
		if sample.PID == rootPID {
			s := sample
			root = &s
		}
		children[sample.PPID] = append(children[sample.PPID], sample)
	}
	if root == nil {
		return 0
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
	return int64(total * 1024)
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
			slog.Warn("ignoring invalid browser memory limit; using 90% of the memory limit", "env", envBrowserMemoryLimitMB, "value", text)
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
