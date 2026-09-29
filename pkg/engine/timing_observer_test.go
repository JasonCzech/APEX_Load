package engine

import (
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestTimingObserverDrainsBeforeStop(t *testing.T) {
	collector := NewMetricsCollector(32)
	var seen []ResultTiming
	collector.timingObserver = func(timing ResultTiming) { seen = append(seen, timing) }
	collector.Start(time.Hour)
	start := time.Now()
	collector.Record(&protocol.Result{StartTime: start, Duration: 3 * time.Millisecond, Success: true, BusinessTransaction: true})
	collector.Record(&protocol.Result{StartTime: start, Duration: 4 * time.Millisecond, Success: false})
	collector.Stop()
	if len(seen) != 2 || seen[0].StartTime != start || seen[0].Duration != 3*time.Millisecond || !seen[0].BusinessTransaction || !seen[0].Success || seen[1].Success {
		t.Fatalf("timings = %#v", seen)
	}
}
