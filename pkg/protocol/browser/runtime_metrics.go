package browser

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

type browserPageMetrics struct {
	FCPMs                 float64 `json:"fcp_ms"`
	LCPMs                 float64 `json:"lcp_ms"`
	CLSScore              float64 `json:"cls_score"`
	INPMs                 float64 `json:"inp_ms"`
	DOMContentLoadedMs    float64 `json:"dom_content_loaded_ms"`
	DOMCompleteMs         float64 `json:"dom_complete_ms"`
	LoadEventMs           float64 `json:"load_event_ms"`
	BlockingResourceCount int     `json:"blocking_resource_count"`
	TotalResourceCount    int     `json:"total_resource_count"`
	ResourceTransferKB    float64 `json:"resource_transfer_kb"`
	LongTaskCount         int     `json:"long_task_count"`
}

func (b *Browser) captureClientMetrics(ctx context.Context, duration time.Duration, processBefore browserProcessSnapshot) *protocol.ClientMetrics {
	if !b.clientMetricsEnabled {
		return nil
	}

	metrics := &protocol.ClientMetrics{
		DeviceProfile:    b.deviceProfile,
		DevicePlatform:   b.devicePlatform,
		DeviceTier:       b.deviceTier,
		BrowserEngine:    b.engine,
		ActionDurationMs: float64(duration) / float64(time.Millisecond),
	}

	if err := b.capturePageMetrics(ctx, metrics); err != nil {
		// Keep metrics collection non-fatal for the browser action.
	}
	if err := b.captureRuntimeMetrics(ctx, metrics); err != nil {
		// Keep metrics collection non-fatal for the browser action.
	}
	if err := b.captureBrowserProcessMetrics(ctx, metrics, processBefore); err != nil {
		// Keep browser-process attribution non-fatal because CDP support varies by runtime.
	}

	if *metrics == (protocol.ClientMetrics{ActionDurationMs: metrics.ActionDurationMs}) {
		return metrics
	}
	return metrics
}

func (b *Browser) capturePageMetrics(ctx context.Context, metrics *protocol.ClientMetrics) error {
	var snapshot browserPageMetrics
	expression := `(() => {
		const paints = performance.getEntriesByType('paint');
		const fcp = paints.find((entry) => entry.name === 'first-contentful-paint');
		const nav = performance.getEntriesByType('navigation')[0] || {};
		const resources = performance.getEntriesByType('resource');
		const lcpEntries = performance.getEntriesByType('largest-contentful-paint');
		const lcp = lcpEntries.length ? lcpEntries[lcpEntries.length - 1].startTime : 0;
		const shifts = performance.getEntriesByType('layout-shift');
		const cls = shifts.reduce((sum, entry) => sum + (entry.hadRecentInput ? 0 : (entry.value || 0)), 0);
		const eventEntries = performance.getEntriesByType('event');
		const inp = eventEntries.reduce((max, entry) => Math.max(max, entry.duration || entry.processingEnd || 0), 0);
		const longTasks = performance.getEntriesByType('longtask');
		const observerMetrics = window.__apexloadMetrics || {};
		const blocking = resources.filter((entry) => entry.renderBlockingStatus === 'blocking');
		const navigationCount = nav.name ? 1 : 0;
		return {
			fcp_ms: fcp ? fcp.startTime : 0,
			lcp_ms: observerMetrics.lcp || lcp,
			cls_score: observerMetrics.cls || cls,
			inp_ms: observerMetrics.inp || inp,
			dom_content_loaded_ms: nav.domContentLoadedEventEnd || 0,
			dom_complete_ms: nav.domComplete || 0,
			load_event_ms: nav.loadEventEnd || 0,
			blocking_resource_count: blocking.length,
			total_resource_count: resources.length + navigationCount,
			resource_transfer_kb: resources.reduce((sum, entry) => sum + ((entry.transferSize || 0) / 1024), (nav.transferSize || 0) / 1024),
			long_task_count: observerMetrics.longTaskCount || longTasks.length,
		};
	})()`
	if err := b.evaluatePageExpression(ctx, expression, &snapshot); err != nil {
		return err
	}
	applyBrowserPageMetrics(snapshot, metrics)
	return nil
}

func applyBrowserPageMetrics(snapshot browserPageMetrics, metrics *protocol.ClientMetrics) {
	metrics.FCPMs = snapshot.FCPMs
	metrics.LCPMs = snapshot.LCPMs
	metrics.CLSScore = snapshot.CLSScore
	metrics.INPMs = snapshot.INPMs
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
	if b.pwCDP != nil {
		_, _ = b.pwCDP.Send("Performance.enable", map[string]any{})
		if raw, err := b.pwCDP.Send("Performance.getMetrics", map[string]any{}); err == nil {
			b.applyCDPPerformanceMetrics(raw, metrics)
		}
		if raw, err := b.pwCDP.Send("Runtime.getHeapUsage", map[string]any{}); err == nil {
			b.applyCDPHeapMetrics(raw, metrics)
		}
	}
	if metrics.JSHeapTotalMB <= 0 {
		var snapshot struct {
			Used  float64 `json:"used"`
			Total float64 `json:"total"`
		}
		if err := b.evaluatePageExpression(ctx, `(() => {
			const memory = performance.memory || {};
			return {
				used: memory.usedJSHeapSize || 0,
				total: memory.totalJSHeapSize || memory.jsHeapSizeLimit || 0,
			};
		})()`, &snapshot); err == nil {
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
		metrics.JSHeapUsedMB = bytesToMegabytes(used)
	}
	if total, ok := numberFromAny(payload["totalSize"]); ok {
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
	return nil
}

func (b *Browser) captureBrowserProcessSnapshot(ctx context.Context) (browserProcessSnapshot, error) {
	return browserProcessSnapshot{}, nil
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
	window.__apexloadMetrics = {
		lcp: 0,
		cls: 0,
		inp: 0,
		longTaskCount: 0,
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
				}
			}
		}).observe({ type: 'layout-shift', buffered: true });
	} catch (e) {}
	try {
		new PerformanceObserver((list) => {
			window.__apexloadMetrics.longTaskCount += list.getEntries().length;
		}).observe({ type: 'longtask', buffered: true });
	} catch (e) {}
	try {
		new PerformanceObserver((list) => {
			for (const entry of list.getEntries()) {
				const duration = entry.duration || entry.processingEnd || 0;
				if (duration > window.__apexloadMetrics.inp) {
					window.__apexloadMetrics.inp = duration;
				}
			}
		}).observe({ type: 'event', buffered: true, durationThreshold: 16 });
	} catch (e) {}
})();`
