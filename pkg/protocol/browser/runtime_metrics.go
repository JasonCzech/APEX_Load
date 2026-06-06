package browser

import (
	"context"
	"encoding/json"

	"github.com/jasonczech/apexload/pkg/protocol"
)

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
