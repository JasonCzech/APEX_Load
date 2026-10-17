package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
	playwright "github.com/mxschmitt/playwright-go"
)

type browserPageMetrics struct {
	// SameDocument: the document is the one the previous capture on this page
	// measured. TimeOrigin identifies the document and Now is when it was
	// measured, in its own clock.
	SameDocument          bool    `json:"same_document"`
	TimeOrigin            float64 `json:"time_origin"`
	Now                   float64 `json:"now"`
	FCPMs                 float64 `json:"fcp_ms"`
	LCPMs                 float64 `json:"lcp_ms"`
	CLSScore              float64 `json:"cls_score"`
	INPMs                 float64 `json:"inp_ms"`
	TTFBMs                float64 `json:"ttfb_ms"`
	DOMContentLoadedMs    float64 `json:"dom_content_loaded_ms"`
	DOMCompleteMs         float64 `json:"dom_complete_ms"`
	LoadEventMs           float64 `json:"load_event_ms"`
	BlockingResourceCount int     `json:"blocking_resource_count"`
	TotalResourceCount    int     `json:"total_resource_count"`
	ResourceTransferKB    float64 `json:"resource_transfer_kb"`
	LongTaskCount         int     `json:"long_task_count"`
	// DocumentCLS is the document's cumulative layout shift.
	DocumentCLS float64 `json:"document_cls"`
}

func (b *Browser) captureClientMetrics(ctx context.Context, duration time.Duration, processBefore browserProcessSnapshot) *protocol.ClientMetrics {
	if !b.clientMetricsEnabled {
		return nil
	}

	metrics := &protocol.ClientMetrics{
		DocumentEvidence:    b.BrowserDocumentEvidence(),
		RuntimeAvailability: b.runtimeMetricAvailability(),
		DeviceProfile:       b.deviceProfile,
		DevicePlatform:      b.devicePlatform,
		DeviceTier:          b.deviceTier,
		BrowserEngine:       b.engine,
		ActionDurationMs:    float64(duration) / float64(time.Millisecond),
	}

	if err := b.capturePageMetrics(ctx, metrics); err != nil {
		// Keep metrics collection non-fatal for the browser action.
	}
	if err := b.captureRuntimeMetrics(ctx, metrics); err != nil {
		// Keep metrics collection non-fatal for the browser action.
	}
	// WebKit exposes no browser process CPU or RSS; the driver's process tree
	// never stands in for it.
	if b.processMetricsAttributionEnabled() && b.engine != "webkit" {
		if err := b.captureBrowserProcessMetrics(ctx, metrics, processBefore); err != nil {
			// Keep browser-process attribution non-fatal because CDP support varies by runtime.
		}
	} else {
		// Keep browser-process attribution non-fatal because CDP support varies by runtime.
	}

	if *metrics == (protocol.ClientMetrics{ActionDurationMs: metrics.ActionDurationMs}) {
		return metrics
	}
	return metrics
}

func (b *Browser) processMetricsAttributionEnabled() bool {
	return envFlagEnabled("APEXLOAD_BROWSER_PROCESS_METRICS")
}

// pageMetricsMark is where the previous capture left a page's document. A
// single-page app (Salesforce Lightning) keeps one document across a whole
// journey: the next capture on that document reports what its step did, not
// the page load again.
type pageMetricsMark struct {
	page       playwright.Page
	timeOrigin float64
	measuredAt float64
	cls        float64
}

func (b *Browser) capturePageMetrics(ctx context.Context, metrics *protocol.ClientMetrics) error {
	page := b.currentPage()
	b.mu.Lock()
	mark := b.metricsMark
	b.mu.Unlock()
	if mark.page != page {
		mark = pageMetricsMark{}
	}
	var snapshot browserPageMetrics
	// A document is the one measured before when its time origin is. Its
	// step-scoped measures come from the observer's step counters, which each
	// capture reads and resets, else from the entries since that capture.
	expression := fmt.Sprintf(`((previousOrigin, since) => {
		const sameDocument = previousOrigin > 0 && performance.timeOrigin === previousOrigin;
		const after = (entries) => sameDocument ? entries.filter((entry) => entry.startTime >= since) : entries;
		const paints = performance.getEntriesByType('paint');
		const fcp = paints.find((entry) => entry.name === 'first-contentful-paint');
		const nav = performance.getEntriesByType('navigation')[0] || {};
		const resources = after(performance.getEntriesByType('resource'));
		const lcpEntries = performance.getEntriesByType('largest-contentful-paint');
		const lcp = lcpEntries.length ? lcpEntries[lcpEntries.length - 1].startTime : 0;
		const shifts = performance.getEntriesByType('layout-shift');
		const cls = shifts.reduce((sum, entry) => sum + (entry.hadRecentInput ? 0 : (entry.value || 0)), 0);
		const eventEntries = after(performance.getEntriesByType('event'));
		const inp = eventEntries.reduce((max, entry) => Math.max(max, entry.duration || entry.processingEnd || 0), 0);
		const longTasks = after(performance.getEntriesByType('longtask'));
		const observerMetrics = window.__apexloadMetrics || {};
		// The resource timing buffer stops growing once full, as a single-page
		// app's load leaves it: a step in that document counts the resources
		// the observer saw instead, when the page has one.
		const observedResources = typeof observerMetrics.takeResources === 'function';
		if (observedResources) {
			observerMetrics.takeResources();
		}
		const step = observerMetrics.step;
		if (step) {
			observerMetrics.step = { cls: 0, inp: 0, longTaskCount: 0, resourceCount: 0, transferKB: 0 };
		}
		const stepResources = sameDocument && observedResources && step;
		const blocking = resources.filter((entry) => entry.renderBlockingStatus === 'blocking');
		const navigationCount = nav.name && !sameDocument ? 1 : 0;
		const documentCLS = observerMetrics.cls || cls;
		return {
			same_document: sameDocument,
			time_origin: performance.timeOrigin,
			now: performance.now(),
			fcp_ms: fcp ? fcp.startTime : 0,
			lcp_ms: observerMetrics.lcp || lcp,
			cls_score: sameDocument ? (step ? step.cls : -1) : documentCLS,
			document_cls: documentCLS,
			inp_ms: sameDocument ? (step ? step.inp : inp) : (observerMetrics.inp || inp),
			ttfb_ms: nav.responseStart && nav.requestStart ? Math.max(0, nav.responseStart - nav.requestStart) : 0,
			dom_content_loaded_ms: nav.domContentLoadedEventEnd || 0,
			dom_complete_ms: nav.domComplete || 0,
			load_event_ms: nav.loadEventEnd || 0,
			blocking_resource_count: blocking.length,
			total_resource_count: stepResources ? step.resourceCount : resources.length + navigationCount,
			resource_transfer_kb: stepResources ? step.transferKB : resources.reduce((sum, entry) => sum + ((entry.transferSize || 0) / 1024), navigationCount ? (nav.transferSize || 0) / 1024 : 0),
			long_task_count: sameDocument ? (step ? step.longTaskCount : longTasks.length) : (observerMetrics.longTaskCount || longTasks.length),
		};
	})(%s, %s)`, strconv.FormatFloat(mark.timeOrigin, 'f', -1, 64), strconv.FormatFloat(mark.measuredAt, 'f', -1, 64))
	if err := b.evaluatePageExpression(ctx, expression, &snapshot); err != nil {
		return err
	}
	if snapshot.SameDocument {
		snapshot = sameDocumentPageMetrics(snapshot, mark)
	}
	b.mu.Lock()
	b.metricsMark = pageMetricsMark{page: page, timeOrigin: snapshot.TimeOrigin, measuredAt: snapshot.Now, cls: snapshot.DocumentCLS}
	b.mu.Unlock()
	applyBrowserPageMetrics(snapshot, metrics)
	return nil
}

// sameDocumentPageMetrics keeps a step's own measures for a document an
// earlier step measured: its page-load measures belong to that load and are
// left out, and its layout shift is the step's (the change since that
// capture when the page has no step counter).
func sameDocumentPageMetrics(snapshot browserPageMetrics, mark pageMetricsMark) browserPageMetrics {
	snapshot.FCPMs, snapshot.LCPMs, snapshot.TTFBMs = 0, 0, 0
	snapshot.DOMContentLoadedMs, snapshot.DOMCompleteMs, snapshot.LoadEventMs = 0, 0, 0
	snapshot.BlockingResourceCount = 0
	if snapshot.CLSScore < 0 {
		snapshot.CLSScore = max(0, snapshot.DocumentCLS-mark.cls)
	}
	return snapshot
}

func applyBrowserPageMetrics(snapshot browserPageMetrics, metrics *protocol.ClientMetrics) {
	metrics.SameDocument = snapshot.SameDocument
	metrics.FCPMs = snapshot.FCPMs
	metrics.LCPMs = snapshot.LCPMs
	metrics.CLSScore = snapshot.CLSScore
	metrics.INPMs = snapshot.INPMs
	metrics.TTFBMs = snapshot.TTFBMs
	metrics.DOMContentLoadedMs = snapshot.DOMContentLoadedMs
	metrics.DOMCompleteMs = snapshot.DOMCompleteMs
	metrics.LoadEventMs = snapshot.LoadEventMs
	metrics.BlockingResourceCount = snapshot.BlockingResourceCount
	metrics.TotalResourceCount = snapshot.TotalResourceCount
	metrics.ResourceTransferKB = snapshot.ResourceTransferKB
	metrics.LongTaskCount = snapshot.LongTaskCount
}

func (b *Browser) captureRuntimeMetrics(ctx context.Context, metrics *protocol.ClientMetrics) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// Snapshot pwCDP under b.mu: the OnPage dispatch goroutine can swap it via
	// installCDPSession, and close() nils it, concurrently with this action path.
	b.mu.Lock()
	cdp := b.pwCDP
	b.mu.Unlock()
	if cdp != nil {
		_, _ = cdp.Send("Performance.enable", map[string]any{})
		if raw, err := cdp.Send("Performance.getMetrics", map[string]any{}); err == nil {
			b.applyCDPPerformanceMetrics(raw, metrics)
		}
		if raw, err := cdp.Send("Runtime.getHeapUsage", map[string]any{}); err == nil {
			b.applyCDPHeapMetrics(raw, metrics)
		}
	}
	if metrics.JSHeapTotalMB <= 0 {
		var snapshot struct {
			Available bool    `json:"available"`
			Used      float64 `json:"used"`
			Total     float64 `json:"total"`
		}
		if err := b.evaluatePageExpression(ctx, `(() => {
			const memory = performance.memory || {};
			return {
				available:typeof memory.usedJSHeapSize==='number'&&typeof memory.totalJSHeapSize==='number',
 used: memory.usedJSHeapSize || 0,
				total: memory.totalJSHeapSize || memory.jsHeapSizeLimit || 0,
			};
		})()`, &snapshot); err == nil {
			if snapshot.Available && browserMetricNumber(snapshot.Used) && browserMetricNumber(snapshot.Total) {
				setBrowserRuntimeAvailability(metrics, "js_heap_used_mb", "page_performance_memory", true)
				setBrowserRuntimeAvailability(metrics, "js_heap_total_mb", "page_performance_memory", true)
			}
			metrics.JSHeapUsedMB = bytesToMegabytes(snapshot.Used)
			metrics.JSHeapTotalMB = bytesToMegabytes(snapshot.Total)
		}
	}
	if metrics.JSHeapTotalMB <= 0 && metrics.JSHeapUsedMB > 0 {
		metrics.JSHeapTotalMB = metrics.JSHeapUsedMB
	}
	return nil
}

func (b *Browser) applyCDPPerformanceMetrics(raw any, metrics *protocol.ClientMetrics) {
	payload, ok := raw.(map[string]any)
	if !ok {
		return
	}
	items, ok := payload["metrics"].([]any)
	if !ok {
		return
	}
	values := make(map[string]float64, len(items))
	for _, item := range items {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name, _ := entry["name"].(string)
		value, ok := numberFromAny(entry["value"])
		if name == "" || !ok {
			continue
		}
		values[name] = value
	}
	for name, metric := range map[string]string{"JSHeapUsedSize": "js_heap_used_mb", "JSHeapTotalSize": "js_heap_total_mb", "LayoutCount": "layout_count", "RecalcStyleCount": "recalc_style_count", "ScriptDuration": "script_duration_ms", "LayoutDuration": "layout_duration_ms", "TaskDuration": "task_duration_ms"} {
		if value, present := values[name]; present && browserMetricNumber(value) {
			setBrowserRuntimeAvailability(metrics, metric, "cdp", true)
		}
	}
	metrics.JSHeapUsedMB = bytesToMegabytes(values["JSHeapUsedSize"])
	metrics.JSHeapTotalMB = bytesToMegabytes(values["JSHeapTotalSize"])
	metrics.LayoutCount = int(values["LayoutCount"])
	metrics.RecalcStyleCount = int(values["RecalcStyleCount"])
	metrics.ScriptDurationMs = values["ScriptDuration"] * 1000
	metrics.LayoutDurationMs = values["LayoutDuration"] * 1000
	metrics.TaskDurationMs = values["TaskDuration"] * 1000
}

func (b *Browser) applyCDPHeapMetrics(raw any, metrics *protocol.ClientMetrics) {
	payload, ok := raw.(map[string]any)
	if !ok {
		return
	}
	if used, ok := numberFromAny(payload["usedSize"]); ok {
		if browserMetricNumber(used) {
			setBrowserRuntimeAvailability(metrics, "js_heap_used_mb", "cdp", true)
		}
		metrics.JSHeapUsedMB = bytesToMegabytes(used)
	}
	if total, ok := numberFromAny(payload["totalSize"]); ok {
		if browserMetricNumber(total) {
			setBrowserRuntimeAvailability(metrics, "js_heap_total_mb", "cdp", true)
		}
		metrics.JSHeapTotalMB = bytesToMegabytes(total)
	}
}

func numberFromAny(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

func bytesToMegabytes(value float64) float64 {
	if value <= 0 {
		return 0
	}
	return value / (1024 * 1024)
}

func (b *Browser) captureBrowserProcessMetrics(ctx context.Context, metrics *protocol.ClientMetrics, before browserProcessSnapshot) error {
	after, err := b.captureBrowserProcessSnapshot(ctx)
	if err != nil {
		return err
	}
	cpuPct, memoryMB := browserProcessResourceMetrics(before, after)
	if cpuPct > 0 {
		metrics.BrowserProcessCPUPct = cpuPct
	}
	if memoryMB > 0 {
		metrics.BrowserProcessMemMB = memoryMB
	}
	return nil
}

func (b *Browser) captureBrowserProcessSnapshot(ctx context.Context) (browserProcessSnapshot, error) {
	rootPID := playwrightDriverPID()
	if rootPID <= 0 {
		return browserProcessSnapshot{}, nil
	}
	return processTreeSnapshot(ctx, rootPID)
}

func playwrightDriverPID() int {
	globalMutex.Lock()
	defer globalMutex.Unlock()
	if globalPlaywright == nil {
		return 0
	}
	return globalPlaywright.Pid()
}

type processSample struct {
	PID            int
	PPID           int
	RSSKB          float64
	CPUTimeSeconds float64
}

// processTreeEntry is one process read for walkProcessTree: its sample and
// its creation time, in any unit that orders creations.
type processTreeEntry struct {
	sample  processSample
	created uint64
}

// walkProcessTree reads rootPID and then, breadth first, each listed child of
// a process already in the tree; children lists, per PID, the processes that
// name it as their creator. Windows keeps a creator's PID after the creator
// exits and reuses PIDs, so a listed child created before its listed parent
// was not started by it: it and its descendants are left out, which also
// keeps the parent links acyclic. Each sample's parent is the process it was
// reached from, and the root's is 0. A process that cannot be read is left
// out; a root that cannot be read gives no samples. Only the Windows sampler
// uses it; it is portable so its tests run everywhere.
func walkProcessTree(ctx context.Context, rootPID int, children map[int][]int, read func(pid int) (processTreeEntry, bool)) ([]processSample, error) {
	root, ok := read(rootPID)
	if !ok {
		return nil, nil
	}
	root.sample.PPID = 0
	samples, queue, seen := []processSample{root.sample}, []processTreeEntry{root}, map[int]bool{rootPID: true}
	for len(queue) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		parent := queue[0]
		queue = queue[1:]
		for _, pid := range children[parent.sample.PID] {
			if seen[pid] {
				continue
			}
			child, ok := read(pid)
			if !ok || child.created < parent.created {
				continue
			}
			seen[pid] = true
			child.sample.PPID = parent.sample.PID
			samples = append(samples, child.sample)
			queue = append(queue, child)
		}
	}
	return samples, nil
}

func processTreeSnapshot(ctx context.Context, rootPID int) (browserProcessSnapshot, error) {
	samples, err := listProcessTreeSamples(ctx, rootPID)
	if err != nil {
		return browserProcessSnapshot{}, err
	}
	childrenByParent := map[int][]processSample{}
	byPID := map[int]processSample{}
	for _, sample := range samples {
		byPID[sample.PID] = sample
		childrenByParent[sample.PPID] = append(childrenByParent[sample.PPID], sample)
	}
	queue := append([]processSample(nil), childrenByParent[rootPID]...)
	if len(queue) == 0 {
		if root, ok := byPID[rootPID]; ok {
			queue = append(queue, root)
		}
	}
	var totalRSSKB float64
	var totalCPU float64
	for len(queue) > 0 {
		sample := queue[0]
		queue = queue[1:]
		totalRSSKB += sample.RSSKB
		totalCPU += sample.CPUTimeSeconds
		queue = append(queue, childrenByParent[sample.PID]...)
	}
	if totalRSSKB <= 0 && totalCPU <= 0 {
		return browserProcessSnapshot{}, nil
	}
	return browserProcessSnapshot{
		CPUTimeSeconds: totalCPU,
		MemoryMB:       totalRSSKB / 1024,
		CapturedAt:     time.Now(),
	}, nil
}

func parseProcessSamples(output string) []processSample {
	lines := strings.Split(output, "\n")
	samples := make([]processSample, 0, len(lines))
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		pid, pidErr := strconv.Atoi(fields[0])
		ppid, ppidErr := strconv.Atoi(fields[1])
		rssKB, rssErr := strconv.ParseFloat(fields[2], 64)
		cpuSeconds := parseProcessCPUTime(fields[3])
		if pidErr != nil || ppidErr != nil || rssErr != nil {
			continue
		}
		samples = append(samples, processSample{
			PID:            pid,
			PPID:           ppid,
			RSSKB:          rssKB,
			CPUTimeSeconds: cpuSeconds,
		})
	}
	return samples
}

func parseProcessCPUTime(value string) float64 {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	days := 0
	if parts := strings.SplitN(value, "-", 2); len(parts) == 2 {
		if parsed, err := strconv.Atoi(parts[0]); err == nil {
			days = parsed
		}
		value = parts[1]
	}
	parts := strings.Split(value, ":")
	total := float64(days * 24 * 60 * 60)
	switch len(parts) {
	case 3:
		hours, _ := strconv.Atoi(parts[0])
		minutes, _ := strconv.Atoi(parts[1])
		seconds, _ := strconv.ParseFloat(parts[2], 64)
		total += float64(hours*3600+minutes*60) + seconds
	case 2:
		minutes, _ := strconv.Atoi(parts[0])
		seconds, _ := strconv.ParseFloat(parts[1], 64)
		total += float64(minutes*60) + seconds
	case 1:
		seconds, _ := strconv.ParseFloat(parts[0], 64)
		total += seconds
	}
	return total
}

func browserProcessResourceMetrics(before, after browserProcessSnapshot) (float64, float64) {
	memoryMB := after.MemoryMB
	if before.CapturedAt.IsZero() || after.CapturedAt.IsZero() || !after.CapturedAt.After(before.CapturedAt) {
		return 0, memoryMB
	}
	cpuDelta := after.CPUTimeSeconds - before.CPUTimeSeconds
	if cpuDelta <= 0 {
		return 0, memoryMB
	}
	elapsedSeconds := after.CapturedAt.Sub(before.CapturedAt).Seconds()
	if elapsedSeconds <= 0 {
		return 0, memoryMB
	}
	return cpuDelta / elapsedSeconds * 100, memoryMB
}

const browserMetricsBootstrapScript = `(() => {
	if (window.__apexloadMetrics) {
		return;
	}
	// step holds what happened since the last metrics capture, which reads
	// and resets it.
	window.__apexloadMetrics = {
		lcp: 0,
		cls: 0,
		inp: 0,
		longTaskCount: 0,
		step: { cls: 0, inp: 0, longTaskCount: 0, resourceCount: 0, transferKB: 0 },
	};
	try {
		new PerformanceObserver((list) => {
			for (const entry of list.getEntries()) {
				window.__apexloadMetrics.lcp = entry.startTime || window.__apexloadMetrics.lcp;
			}
		}).observe({ type: 'largest-contentful-paint', buffered: true });
	} catch (e) {}
	try {
		new PerformanceObserver((list) => {
			for (const entry of list.getEntries()) {
				if (!entry.hadRecentInput) {
					window.__apexloadMetrics.cls += entry.value || 0;
					window.__apexloadMetrics.step.cls += entry.value || 0;
				}
			}
		}).observe({ type: 'layout-shift', buffered: true });
	} catch (e) {}
	try {
		new PerformanceObserver((list) => {
			window.__apexloadMetrics.longTaskCount += list.getEntries().length;
			window.__apexloadMetrics.step.longTaskCount += list.getEntries().length;
		}).observe({ type: 'longtask', buffered: true });
	} catch (e) {}
	try {
		new PerformanceObserver((list) => {
			for (const entry of list.getEntries()) {
				const duration = entry.duration || entry.processingEnd || 0;
				if (duration > window.__apexloadMetrics.inp) {
					window.__apexloadMetrics.inp = duration;
				}
				if (duration > window.__apexloadMetrics.step.inp) {
					window.__apexloadMetrics.step.inp = duration;
				}
			}
		}).observe({ type: 'event', buffered: true, durationThreshold: 16 });
	} catch (e) {}
	// The page's resource timing buffer holds 250 entries by default, which a
	// single-page app fills as it loads; an observer sees every resource. A
	// capture first takes the entries not yet delivered to it.
	try {
		const countResources = (entries) => {
			for (const entry of entries) {
				window.__apexloadMetrics.step.resourceCount += 1;
				window.__apexloadMetrics.step.transferKB += (entry.transferSize || 0) / 1024;
			}
		};
		const resourceObserver = new PerformanceObserver((list) => countResources(list.getEntries()));
		resourceObserver.observe({ type: 'resource', buffered: true });
		window.__apexloadMetrics.takeResources = () => countResources(resourceObserver.takeRecords());
	} catch (e) {}
})();`

func browserMetricNumber(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 }
func (b *Browser) runtimeMetricAvailability() *protocol.BrowserRuntimeMetricAvailability {
	out := &protocol.BrowserRuntimeMetricAvailability{Version: 1}
	for _, metric := range []string{"js_heap_used_mb", "js_heap_total_mb", "layout_count", "recalc_style_count", "script_duration_ms", "layout_duration_ms", "task_duration_ms"} {
		reason := "not_exposed"
		if !b.supportsCDP() {
			reason = "unsupported"
		}
		out.Metrics = append(out.Metrics, protocol.BrowserRuntimeMetricCapability{Metric: metric, Source: "cdp", Scope: "active_page", UnavailableReason: reason})
	}
	for _, metric := range []string{"browser_process_cpu_pct", "browser_process_mem_mb"} {
		reason := "scope_unqualified"
		if b.engine == "webkit" {
			reason = "unsupported"
		}
		out.Metrics = append(out.Metrics, protocol.BrowserRuntimeMetricCapability{Metric: metric, Source: "driver_process_tree", Scope: "driver_process_tree", UnavailableReason: reason})
	}
	return out
}
func setBrowserRuntimeAvailability(metrics *protocol.ClientMetrics, metric, source string, available bool) {
	if metrics.RuntimeAvailability == nil {
		return
	}
	for i, row := range metrics.RuntimeAvailability.Metrics {
		if row.Metric == metric {
			metrics.RuntimeAvailability.Metrics[i].Source = source
			metrics.RuntimeAvailability.Metrics[i].Available = available
			if available {
				metrics.RuntimeAvailability.Metrics[i].UnavailableReason = ""
			}
			return
		}
	}
}
