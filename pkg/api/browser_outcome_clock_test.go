package api

import (
	"github.com/jasonczech/apexload/pkg/protocol"
	"testing"
	"time"
)

func TestBrowserOutcomeClockDoesNotMoveDispatchPatternWindows(t *testing.T) {
	rows := []protocol.BrowserInteraction{v3Interaction(1, 0), v3Interaction(2, 500*time.Millisecond), v3Interaction(3, time.Second)}
	for n := range rows {
		i := &rows[n]
		i.Version = protocol.BrowserInteractionVersion3
		start := e4Start.Add(time.Duration(n+1) * time.Hour)
		i.Outcome = protocol.BrowserInteractionOutcome{Declared: "duration", DeadlineMS: 1000, Observed: protocol.InteractionOutcomeMet, Clock: &protocol.BrowserOutcomeClock{Version: 1, StartedAt: start, DeadlineAt: start.Add(time.Second), ObservedAt: start.Add(10 * time.Millisecond), BudgetMS: 1000, ElapsedMS: 10}}
		i.CapturedAt = start.Add(time.Minute)
		if !i.Valid() {
			t.Fatal("invalid clock fixture")
		}
	}
	got := analyzeV3(rows...)
	patterns := e4Patterns(got, interactionPatternScriptedRepetition)
	if got.Coverage.PatternEligible != 3 || len(patterns) != 1 || patterns[0].Count != 3 || !patterns[0].Start.Equal(rows[0].Dispatch.At) || !patterns[0].End.Equal(rows[2].Dispatch.At) {
		t.Fatal("wait clock changed gesture semantics", got)
	}
}
