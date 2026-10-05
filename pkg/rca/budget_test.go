package rca

import (
	"encoding/json"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fakeClock advances only when told to.
type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

func truncationDetails(a Assessment) []string {
	var details []string
	for _, l := range a.Limitations {
		if l.Code == LimitationBudgetTruncated {
			details = append(details, l.Detail)
		}
	}
	return details
}

// Every limit, once exceeded, surfaces as a budget_truncated limitation on
// the assessment, never as an error or a silent cut.
func TestEachBudgetLimitBecomesALimitation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		exceed func(*Budgeter, *fakeClock, *uint64) int
		kept   int
		detail string
	}{
		{"series", func(b *Budgeter, _ *fakeClock, _ *uint64) int { return b.Series(2500) }, 2000, "series: 2000 of 2500"},
		{"series points", func(b *Budgeter, _ *fakeClock, _ *uint64) int { return b.SeriesPoints(86400) }, MaxSeriesPoints, "series_points: 10000 of 86400"},
		{"exemplars", func(b *Budgeter, _ *fakeClock, _ *uint64) int { return b.Exemplars(25000) }, 20000, "exemplars: 20000 of 25000"},
		{"graph nodes", func(b *Budgeter, _ *fakeClock, _ *uint64) int { return b.GraphNodes(5001) }, 5000, "graph_nodes: 5000 of 5001"},
		{"graph edges", func(b *Budgeter, _ *fakeClock, _ *uint64) int { return b.GraphEdges(30000) }, 20000, "graph_edges: 20000 of 30000"},
		{"wall time", func(b *Budgeter, c *fakeClock, _ *uint64) int {
			c.now = c.now.Add(121 * time.Second)
			return 0
		}, 0, "wall_time: 120.0 s allowed, 121.0 s used"},
		{"heap growth", func(b *Budgeter, _ *fakeClock, heap *uint64) int {
			*heap += 600 << 20
			return 0
		}, 0, "heap: 512 MiB allowed, 600 MiB used"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clock := &fakeClock{now: validityStart}
			heap := uint64(2 << 30) // the controller's own heap does not count
			budgeter := NewBudgeter(DefaultBudget, clock.Now, func() uint64 { return heap })
			if kept := tc.exceed(budgeter, clock, &heap); kept != tc.kept {
				t.Fatalf("kept %d, want %d", kept, tc.kept)
			}
			got := Analyze(AnalysisInput{Validity: validityRun(20)}, budgeter)
			if details := truncationDetails(got); !reflect.DeepEqual(details, []string{tc.detail}) {
				t.Fatalf("limitations %v, want %q", details, tc.detail)
			}
		})
	}
}

func TestBudgetWithinLimitsKeepsEverything(t *testing.T) {
	clock := &fakeClock{now: validityStart}
	budgeter := NewBudgeter(DefaultBudget, clock.Now, func() uint64 { return 1 << 20 })
	if budgeter.Series(2000) != 2000 || budgeter.SeriesPoints(10) != 10 || budgeter.Exemplars(0) != 0 || budgeter.Exhausted() {
		t.Fatal("a budget within limits cut evidence")
	}
	unlimitedClock := &fakeClock{now: validityStart}
	unlimited := NewBudgeter(Budget{}, unlimitedClock.Now, nil)
	unlimitedClock.now = unlimitedClock.now.Add(24 * time.Hour)
	if unlimited.Series(1<<20) != 1<<20 || unlimited.Exhausted() || len(unlimited.Truncations()) != 0 {
		t.Fatal("a zero budget limited the analysis")
	}
	if got := Analyze(AnalysisInput{Validity: validityRun(5)}, budgeter); len(truncationDetails(got)) != 0 {
		t.Fatalf("%+v", got.Limitations)
	}
}

func TestExhaustedBudgetSkipsValidity(t *testing.T) {
	clock := &fakeClock{now: validityStart}
	budgeter := NewBudgeter(DefaultBudget, clock.Now, nil)
	clock.now = clock.now.Add(5 * time.Minute)
	got := Analyze(AnalysisInput{Validity: validityRun(20)}, budgeter)
	if got.Validity.Verdict != ValidityUnknown || !contains(got.Validity.Limitations, LimitationBudgetTruncated) || len(truncationDetails(got)) != 1 {
		t.Fatalf("%+v", got)
	}
}

func TestPhase3aAssessment(t *testing.T) {
	input := AnalysisInput{
		Acceptance: Acceptance{Status: AcceptanceBreached, Details: []string{"checkout p95 1200 ms > 800 ms"}},
		Validity:   validityRun(20),
		Coverage:   []Coverage{{Evidence: "interval_distributions", Status: CoverageComplete}, {Evidence: "generator_stats", Status: CoverageComplete}},
	}
	for i := 3; i < 6; i++ {
		sampleAt(&input.Validity, i, "g1").CPUPct = 90
	}
	clock := &fakeClock{now: validityStart}
	got := Analyze(input, NewBudgeter(DefaultBudget, clock.Now, nil))
	if got.SchemaVersion != AssessmentSchemaVersion || got.Acceptance.Status != AcceptanceBreached || got.Validity.Verdict != ValiditySuspect ||
		got.Outcome != OutcomeInsufficientEvidence || len(got.Anomalies) != 0 || len(got.Hypotheses) != 0 ||
		got.Coverage[0].Evidence != "generator_stats" || !hasLimitation(got.Limitations, LimitationLoadLevelsUnavailable) {
		t.Fatalf("%+v", got)
	}
	// Identical frozen inputs give byte-identical assessments.
	again := Analyze(input, NewBudgeter(DefaultBudget, (&fakeClock{now: validityStart}).Now, nil))
	first, _ := json.Marshal(got)
	second, _ := json.Marshal(again)
	if string(first) != string(second) {
		t.Fatal("assessments differ")
	}
	// The contract keeps empty lists as lists.
	for _, key := range []string{`"anomalies":[]`, `"hypotheses":[]`, `"capacity":[]`, `"littles_law":[]`, `"details":["checkout`} {
		if !strings.Contains(string(first), key) {
			t.Fatalf("%s missing from %s", key, first)
		}
	}
	if empty := Analyze(AnalysisInput{}, nil); empty.Acceptance.Status != AcceptanceUnknown || empty.Validity.Verdict != ValidityUnknown {
		t.Fatalf("empty input: %+v", empty)
	}
	// The live heap is measured by garbage collection, so it reads zero
	// before the first one.
	runtime.GC()
	if LiveHeapBytes() == 0 {
		t.Fatal("live heap unreadable")
	}
}
