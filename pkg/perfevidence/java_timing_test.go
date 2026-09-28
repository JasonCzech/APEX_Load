package perfevidence

import (
	"github.com/jasonczech/apexload/pkg/engine"
	"testing"
	"time"
)

func TestJavaTimingIncludesSlowDrainedTransactions(t *testing.T) {
	start := time.Now()
	end := start.Add(time.Second)
	s := JavaEvidenceSample{Complete: true}
	var durations []float64
	for _, r := range []engine.ResultTiming{
		{StartTime: start.Add(-100 * time.Millisecond), Duration: 200 * time.Millisecond, Success: true, BusinessTransaction: true},
		{StartTime: start.Add(100 * time.Millisecond), Duration: 100 * time.Millisecond, Success: true, BusinessTransaction: true},
		{StartTime: start.Add(900 * time.Millisecond), Duration: 5 * time.Second, Success: false, BusinessTransaction: true},
		{StartTime: end, Duration: time.Millisecond, Success: true, BusinessTransaction: true},
		{StartTime: start, Duration: time.Millisecond, Success: false},
	} {
		recordJavaTiming(&s, &durations, start, end, r)
	}
	if s.Transactions != 2 || len(durations) != 2 || durations[0] != 100 || durations[1] != 5000 || s.Errors != 1 || s.HarnessErrors != 1 || !s.Complete {
		t.Fatalf("sample=%+v durations=%v", s, durations)
	}
}
