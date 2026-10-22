package rca

import (
	"testing"
	"time"
)

func TestBrowserSourcePopulationCannotBridgeSeparateSteadySpans(t *testing.T) {
	s := documentSeries("proof", "lcp", "ms", "family", chromium141, []float64{100, 200}, 1)
	for i, p := range s.Points {
		w := s.Document.Windows[p.At]
		w.MonotonicVerified = true
		w.MonotonicVUsers = 10
		w.MonotonicSpanID = "first"
		if i == 1 {
			w.MonotonicSpanID = "second"
		}
		s.Document.Windows[p.At] = w
	}
	level := LoadLevel{Start: browserBaselineStart, End: browserBaselineStart.Add(time.Minute), VUsers: 10}
	if browserSourceWindowsWithin(s, s.Points, level) {
		t.Fatal("equal load hid pause")
	}
	w := s.Document.Windows[s.Points[1].At]
	w.MonotonicSpanID = "first"
	s.Document.Windows[s.Points[1].At] = w
	if !browserSourceWindowsWithin(s, s.Points, level) {
		t.Fatal("one proven span refused")
	}
	w.MonotonicVerified = false
	s.Document.Windows[s.Points[1].At] = w
	if browserSourceWindowsWithin(s, s.Points, level) {
		t.Fatal("mixed raw and proved population")
	}
}
