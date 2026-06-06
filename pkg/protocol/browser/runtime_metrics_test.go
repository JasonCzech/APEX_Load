package browser

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestApplyCDPPerformanceMetrics(t *testing.T) {
	var metrics protocol.ClientMetrics
	b := &Browser{}

	b.applyCDPPerformanceMetrics(map[string]any{
		"metrics": []any{
			map[string]any{"name": "JSHeapUsedSize", "value": float64(2 * 1024 * 1024)},
			map[string]any{"name": "JSHeapTotalSize", "value": float64(4 * 1024 * 1024)},
			map[string]any{"name": "LayoutCount", "value": float64(7)},
			map[string]any{"name": "RecalcStyleCount", "value": float64(3)},
			map[string]any{"name": "ScriptDuration", "value": 0.12},
			map[string]any{"name": "LayoutDuration", "value": 0.03},
			map[string]any{"name": "TaskDuration", "value": 0.5},
		},
	}, &metrics)

	if metrics.JSHeapUsedMB != 2 {
		t.Fatalf("JSHeapUsedMB = %v, want 2", metrics.JSHeapUsedMB)
	}
	if metrics.JSHeapTotalMB != 4 {
		t.Fatalf("JSHeapTotalMB = %v, want 4", metrics.JSHeapTotalMB)
	}
	if metrics.LayoutCount != 7 || metrics.RecalcStyleCount != 3 {
		t.Fatalf("layout counts = %d/%d, want 7/3", metrics.LayoutCount, metrics.RecalcStyleCount)
	}
	if metrics.ScriptDurationMs != 120 || metrics.LayoutDurationMs != 30 || metrics.TaskDurationMs != 500 {
		t.Fatalf("durations = %v/%v/%v, want 120/30/500", metrics.ScriptDurationMs, metrics.LayoutDurationMs, metrics.TaskDurationMs)
	}
}

func TestApplyCDPHeapMetrics(t *testing.T) {
	metrics := protocol.ClientMetrics{
		JSHeapUsedMB:  1,
		JSHeapTotalMB: 2,
	}
	b := &Browser{}

	b.applyCDPHeapMetrics(map[string]any{
		"usedSize":  float64(3 * 1024 * 1024),
		"totalSize": float64(6 * 1024 * 1024),
	}, &metrics)

	if metrics.JSHeapUsedMB != 3 {
		t.Fatalf("JSHeapUsedMB = %v, want 3", metrics.JSHeapUsedMB)
	}
	if metrics.JSHeapTotalMB != 6 {
		t.Fatalf("JSHeapTotalMB = %v, want 6", metrics.JSHeapTotalMB)
	}
}

func TestApplyBrowserPageMetrics(t *testing.T) {
	var metrics protocol.ClientMetrics

	applyBrowserPageMetrics(browserPageMetrics{
		FCPMs:                 12.5,
		LCPMs:                 34.5,
		CLSScore:              0.04,
		INPMs:                 56.5,
		DOMContentLoadedMs:    78.5,
		DOMCompleteMs:         90.5,
		LoadEventMs:           101.5,
		BlockingResourceCount: 2,
		TotalResourceCount:    9,
		ResourceTransferKB:    128.25,
		LongTaskCount:         3,
	}, &metrics)

	if metrics.FCPMs != 12.5 || metrics.LCPMs != 34.5 || metrics.CLSScore != 0.04 || metrics.INPMs != 56.5 {
		t.Fatalf("paint/interactivity metrics = %v/%v/%v/%v", metrics.FCPMs, metrics.LCPMs, metrics.CLSScore, metrics.INPMs)
	}
	if metrics.DOMContentLoadedMs != 78.5 || metrics.DOMCompleteMs != 90.5 || metrics.LoadEventMs != 101.5 {
		t.Fatalf("navigation metrics = %v/%v/%v", metrics.DOMContentLoadedMs, metrics.DOMCompleteMs, metrics.LoadEventMs)
	}
	if metrics.BlockingResourceCount != 2 || metrics.TotalResourceCount != 9 || metrics.ResourceTransferKB != 128.25 || metrics.LongTaskCount != 3 {
		t.Fatalf("resource/task metrics = %d/%d/%v/%d", metrics.BlockingResourceCount, metrics.TotalResourceCount, metrics.ResourceTransferKB, metrics.LongTaskCount)
	}
}

func TestNumberFromAny(t *testing.T) {
	tests := []struct {
		name   string
		value  any
		want   float64
		wantOK bool
	}{
		{name: "float64", value: float64(1.5), want: 1.5, wantOK: true},
		{name: "float32", value: float32(2.5), want: 2.5, wantOK: true},
		{name: "int", value: 3, want: 3, wantOK: true},
		{name: "int64", value: int64(4), want: 4, wantOK: true},
		{name: "json number", value: json.Number("5.75"), want: 5.75, wantOK: true},
		{name: "bad json number", value: json.Number("not-a-number"), wantOK: false},
		{name: "string", value: "6", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := numberFromAny(tt.value)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && got != tt.want {
				t.Fatalf("value = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBytesToMegabytes(t *testing.T) {
	if got := bytesToMegabytes(2 * 1024 * 1024); got != 2 {
		t.Fatalf("bytesToMegabytes = %v, want 2", got)
	}
	if got := bytesToMegabytes(-1); got != 0 {
		t.Fatalf("bytesToMegabytes(-1) = %v, want 0", got)
	}
}

func TestBrowserProcessResourceMetrics(t *testing.T) {
	started := time.Date(2026, time.May, 16, 10, 0, 0, 0, time.UTC)
	cpuPct, memoryMB := browserProcessResourceMetrics(
		browserProcessSnapshot{CPUTimeSeconds: 1.25, MemoryMB: 128, CapturedAt: started},
		browserProcessSnapshot{CPUTimeSeconds: 1.75, MemoryMB: 256, CapturedAt: started.Add(2 * time.Second)},
	)
	if cpuPct != 25 {
		t.Fatalf("cpuPct = %v, want 25", cpuPct)
	}
	if memoryMB != 256 {
		t.Fatalf("memoryMB = %v, want 256", memoryMB)
	}
}
