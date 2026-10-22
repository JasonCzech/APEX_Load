package protocol

import (
	"encoding/json"
	"math"
	"testing"
	"time"
)

func TestBrowserOutcomeClockValidationAndVersionIsolation(t *testing.T) {
	now := time.Now().UTC()
	clock := &BrowserOutcomeClock{Version: 1, StartedAt: now, DeadlineAt: now.Add(time.Second), ObservedAt: now.Add(10 * time.Millisecond), BudgetMS: 1000, ElapsedMS: 10}
	if !clock.Valid() {
		t.Fatal("valid local wait refused")
	}
	for name, mutate := range map[string]func(*BrowserOutcomeClock){
		"version":         func(c *BrowserOutcomeClock) { c.Version = 2 },
		"zero":            func(c *BrowserOutcomeClock) { c.StartedAt = time.Time{} },
		"reversed":        func(c *BrowserOutcomeClock) { c.ObservedAt = now.Add(-time.Second) },
		"wall-jump":       func(c *BrowserOutcomeClock) { c.ObservedAt = now.Add(time.Second) },
		"bad-deadline":    func(c *BrowserOutcomeClock) { c.DeadlineAt = now.Add(-time.Second) },
		"extended-budget": func(c *BrowserOutcomeClock) { c.DeadlineAt = now.Add(2 * time.Second) },
		"nonfinite":       func(c *BrowserOutcomeClock) { c.ElapsedMS = math.NaN() },
		"too-large":       func(c *BrowserOutcomeClock) { c.BudgetMS = 86400001 },
	} {
		t.Run(name, func(t *testing.T) {
			c := *clock
			mutate(&c)
			if c.Valid() || c.Clone() != nil {
				t.Fatal("invalid clock admitted")
			}
		})
	}
	i := interactionFixture()
	i.Version = BrowserInteractionVersion3
	i.DispatchUnavailableReason = "not_observed"
	i.CapturedAt = now.Add(2 * time.Second)
	i.Outcome = BrowserInteractionOutcome{Declared: "duration", DeadlineMS: 1000, Observed: InteractionOutcomeMet, Clock: clock}
	evidence := &BrowserInteractionEvidence{Version: BrowserInteractionVersion3, SourceGeneration: i.SourceGeneration, Interactions: []BrowserInteraction{i}}
	raw, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeBrowserInteractionEvidence(raw)
	if err != nil {
		t.Fatal(err)
	}
	copied := evidence.Clone()
	if copied == nil {
		t.Fatal("clock evidence not cloned")
	}
	copied.Interactions[0].Outcome.Clock.BudgetMS = 99
	if clock.BudgetMS != 1000 || decoded.Interactions[0].Outcome.Clock.BudgetMS != 1000 {
		t.Fatal("clock aliases its source")
	}
	for _, version := range []int{BrowserInteractionVersion, BrowserInteractionVersion2} {
		legacy := i
		legacy.Version = version
		if legacy.Valid() {
			t.Fatal("clock leaked into legacy version", version)
		}
	}
	i.Outcome.Observed = InteractionOutcomeMissed
	if i.Valid() {
		t.Fatal("early timeout qualified a missed authored deadline")
	}
	i.Outcome.Observed = InteractionOutcomeMet
	i.Outcome.Clock = nil
	if i.Valid() {
		t.Fatal("raw v3 qualified an outcome without its observation clock")
	}
	late := *clock
	late.ObservedAt = now.Add(2 * time.Second)
	late.ElapsedMS = 2000
	i.Outcome.Clock = &late
	if i.Valid() {
		t.Fatal("raw v3 qualified a late success before its deadline")
	}
	i.Outcome.Clock = nil
	i.Version = BrowserInteractionVersion2
	if !i.Valid() {
		t.Fatal("historical v2 requires a fabricated clock")
	}
}
