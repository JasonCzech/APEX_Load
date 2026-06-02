package api

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/engine"
)

func TestClassifyGeneratorImpact(t *testing.T) {
	tests := []struct {
		name       string
		resources  *engine.SystemResources
		p95Ms      float64
		errorPct   float64
		wantState  string
		wantReason string
	}{
		{
			name:      "missing resources",
			resources: nil,
			wantState: "unknown",
		},
		{
			name:      "clear resources",
			resources: &engine.SystemResources{CPUProcessPct: 25, MemProcessMb: 128},
			wantState: "clear",
		},
		{
			name:       "saturation without degradation",
			resources:  &engine.SystemResources{ContainerCPUThrottledPeriodsDelta: 2},
			p95Ms:      100,
			errorPct:   0,
			wantState:  "warning",
			wantReason: "CPU throttling",
		},
		{
			name:       "saturation with latency degradation",
			resources:  &engine.SystemResources{CPUProcessPct: 96, ContainerMemoryUsagePct: 93},
			p95Ms:      slowTransactionDiagnosticThresholdMs,
			wantState:  "danger",
			wantReason: "CPU saturation",
		},
		{
			name:       "network loss with errors",
			resources:  &engine.SystemResources{TCPRetransmitsPerSec: 1},
			errorPct:   0.5,
			wantState:  "danger",
			wantReason: "Network loss/retransmits",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state, reasons := classifyGeneratorImpact(tt.resources, tt.p95Ms, tt.errorPct)
			if state != tt.wantState {
				t.Fatalf("state = %q, want %q", state, tt.wantState)
			}
			if tt.wantReason != "" && !containsImpactString(reasons, tt.wantReason) {
				t.Fatalf("reasons = %v, want %q", reasons, tt.wantReason)
			}
		})
	}
}

func TestBuildGeneratorResourceSnapshotsMapsDistributedStats(t *testing.T) {
	backend := &distributedExecutionBackend{
		targets: []generatorTargetSpec{{Address: "gen-a:9090", Region: "east"}},
	}
	batches := map[string]generatorrpc.MetricsBatch{
		"gen-a:9090": {
			GeneratorID:     "gen-a",
			TimestampUnixMs: 1_779_999_999_000,
			Stats: generatorrpc.GeneratorStats{
				ActiveVUsers:                     12,
				CPUPercent:                       96,
				MemoryBytes:                      256 * 1024 * 1024,
				ContainerCPUThrottledSecondsRate: 0.4,
				ContainerMemoryUsagePct:          92,
				TCPRetransmitsPerSec:             2,
				DiskIOTimePct:                    75,
			},
			Transactions: []generatorrpc.TransactionMetric{
				{Name: "Checkout", DurationMs: 900, Success: true},
				{Name: "Checkout", DurationMs: 1400, Success: false},
			},
		},
	}

	snapshots := backend.buildGeneratorResourceSnapshots(nil, batches)
	snapshot := snapshots["gen-a"]

	if snapshot == nil {
		t.Fatal("missing generator snapshot for gen-a")
	}
	if snapshot.Region != "east" {
		t.Fatalf("region = %q, want east", snapshot.Region)
	}
	if snapshot.ActiveVUsers != 12 || snapshot.P95Ms != 900 || snapshot.ErrorPct != 50 {
		t.Fatalf("snapshot metrics = %#v, want active users, p95, and error pct", snapshot)
	}
	if snapshot.SaturationState != "danger" {
		t.Fatalf("saturation state = %q, want danger", snapshot.SaturationState)
	}
	if snapshot.Resources == nil || snapshot.Resources.TCPRetransmitsPerSec != 2 || snapshot.Resources.ContainerMemoryUsagePct != 92 {
		t.Fatalf("resources = %#v, want mapped enhanced resource fields", snapshot.Resources)
	}
	if !containsImpactString(snapshot.ImpactReasons, "CPU throttling") || !containsImpactString(snapshot.ImpactReasons, "Memory near limit") {
		t.Fatalf("impact reasons = %v, want CPU throttling and memory pressure", snapshot.ImpactReasons)
	}
}

func containsImpactString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
