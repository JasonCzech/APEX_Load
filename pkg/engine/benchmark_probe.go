package engine

import (
	"fmt"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// MetricsRecordProbeResult describes one controlled measurement of the real
// MetricsCollector.Record hot path. It is intentionally narrow: the probe does
// not represent all work performed by a VUser transaction.
type MetricsRecordProbeResult struct {
	Iterations int
	Elapsed    time.Duration
	Processed  int64
	Dropped    int64
}

// RunMetricsRecordProbe measures the production metrics-recording hot path
// while a dedicated consumer drains the collector channel. Keeping the drain
// in the engine package lets the opt-in benchmark harness measure successful
// channel handoffs without exposing MetricsCollector internals or retaining a
// channel large enough to hold every compacted Result.
//
// This function is benchmark support, not a target-attainment assertion. The
// caller is responsible for recording the host, build, repetitions, and scope.
func RunMetricsRecordProbe(iterations int) (MetricsRecordProbeResult, error) {
	if iterations <= 0 {
		return MetricsRecordProbeResult{}, fmt.Errorf("iterations must be positive")
	}

	collector := NewMetricsCollector(1 << 16)
	stop := make(chan struct{})
	drained := make(chan int64, 1)
	go func() {
		var processed int64
		for {
			select {
			case <-collector.ch:
				processed++
			case <-stop:
				for {
					select {
					case <-collector.ch:
						processed++
					default:
						drained <- processed
						return
					}
				}
			}
		}
	}()

	result := &protocol.Result{
		ActionName: "benchmark-transaction",
		Group:      "benchmark",
		StartTime:  time.Now(),
		Duration:   5 * time.Millisecond,
		Success:    true,
	}

	started := time.Now()
	for range iterations {
		collector.Record(result)
	}
	elapsed := time.Since(started)
	close(stop)
	processed := <-drained
	dropped := collector.current.droppedResults.Load()

	measurement := MetricsRecordProbeResult{
		Iterations: iterations,
		Elapsed:    elapsed,
		Processed:  processed,
		Dropped:    dropped,
	}
	if processed+dropped != int64(iterations) {
		return measurement, fmt.Errorf(
			"metrics record probe accounted for %d of %d iterations",
			processed+dropped,
			iterations,
		)
	}
	if dropped != 0 {
		return measurement, fmt.Errorf("metrics record probe dropped %d of %d iterations", dropped, iterations)
	}
	return measurement, nil
}
