package engine

import "testing"

func TestRunMetricsRecordProbeAccountsForEveryIteration(t *testing.T) {
	const iterations = 10_000
	result, err := RunMetricsRecordProbe(iterations)
	if err != nil {
		t.Fatalf("RunMetricsRecordProbe: %v", err)
	}
	if result.Iterations != iterations {
		t.Fatalf("iterations = %d, want %d", result.Iterations, iterations)
	}
	if result.Processed+result.Dropped != iterations {
		t.Fatalf("processed + dropped = %d, want %d", result.Processed+result.Dropped, iterations)
	}
	if result.Dropped != 0 {
		t.Fatalf("dropped = %d, want zero", result.Dropped)
	}
	if result.Elapsed <= 0 {
		t.Fatalf("elapsed = %s, want positive", result.Elapsed)
	}
}

func TestRunMetricsRecordProbeRejectsNonPositiveIterations(t *testing.T) {
	if _, err := RunMetricsRecordProbe(0); err == nil {
		t.Fatal("RunMetricsRecordProbe(0) succeeded, want validation error")
	}
}
