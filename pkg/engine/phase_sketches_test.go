package engine

import (
	"math"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

type phaseSample struct{ dns, connect, tls, ttfb, duration float64 }

func recordPhaseSamples(samples []phaseSample) (*PhaseSketches, PhaseObservations) {
	var recorder PhaseSketchRecorder
	var observed PhaseObservations
	for _, s := range samples {
		recorder.ObserveMs(s.dns, s.connect, s.tls, s.ttfb, s.duration)
		observed.ObserveMs(s.dns, s.connect, s.tls, s.ttfb, s.duration)
	}
	return recorder.Snapshot(), observed
}

func TestPhaseSketchRecorderKeepsOnlyObservedPhases(t *testing.T) {
	samples := []phaseSample{
		{dns: 4, connect: 10, tls: 30, ttfb: 100, duration: 120}, // a new TLS connection
		{ttfb: 20, duration: 25},                                 // reused connection
		{connect: 8, duration: 3000},                             // timed out before a first byte
		{dns: 2, ttfb: 1, duration: 1},                           // TTFB below setup: server clamps at 0
	}
	sketches, observed := recordPhaseSamples(samples)
	counts := map[string]uint64{}
	for _, phase := range NetworkPhases {
		if s := sketches.Phase(phase); s != nil {
			counts[phase] = s.Count
		}
	}
	want := map[string]uint64{PhaseDNS: 2, PhaseConnect: 2, PhaseTLS: 1, PhaseServer: 3, PhaseDownload: 3}
	for phase, n := range want {
		if counts[phase] != n {
			t.Fatalf("%s samples = %d, want %d (all %v)", phase, counts[phase], n, counts)
		}
	}
	if !sketches.ConsistentWith(&observed, int64(len(samples))) {
		t.Fatalf("sketches should agree with the observations %+v", observed)
	}
	// Server = TTFB - setup: 100-44, 20, max(0, 1-2).
	set := sketches.Percentiles()
	if set == nil || set.Server == nil || set.Server.Samples != 3 {
		t.Fatalf("server percentiles = %+v", set)
	}
	if sketches.Server.Max != 56*float64(time.Millisecond) || sketches.Server.Min != 0 {
		t.Fatalf("server range = %v..%v ns, want 0..56 ms", sketches.Server.Min, sketches.Server.Max)
	}
	// Interpolated between the order statistics 20 and 56 ms, as an exact
	// percentile is.
	if got := set.Server.P99Ms; got < 55 || got > 56*(1+DurationSketchRelativeAccuracy) {
		t.Fatalf("server P99 = %v, want about 55.3", got)
	}
	if set.Server.P50Ms > 20*(1+2*DurationSketchRelativeAccuracy) || set.Server.P50Ms < 0 {
		t.Fatalf("server P50 = %v, want at most 20", set.Server.P50Ms)
	}
	if got, ok := set.TLS.Value(95); !ok || math.Abs(got-30) > 0.5 {
		t.Fatalf("TLS P95 = %v %v", got, ok)
	}
	if _, ok := set.Phase("nope").Value(95); ok {
		t.Fatal("an unknown phase has no value")
	}
	if _, ok := set.TLS.Value(42); ok {
		t.Fatal("an unsupported percentile has no value")
	}
}

func TestPhaseSketchesNoSamplesAreKnownButEmpty(t *testing.T) {
	sketches, observed := recordPhaseSamples([]phaseSample{{duration: 5}}) // e.g. gRPC: no TTFB split
	if sketches == nil || sketches.Bins() != 0 {
		t.Fatalf("sketches = %+v", sketches)
	}
	if !sketches.ConsistentWith(&observed, 1) {
		t.Fatal("empty sketches agree with no observations")
	}
	set := sketches.Percentiles()
	if set == nil || set.Version != PhaseSketchVersion || set.DNS != nil || set.Server != nil {
		t.Fatalf("percentiles = %+v", set)
	}
}

func TestPhaseSketchesConsistencyRefusesMismatchedCounts(t *testing.T) {
	sketches, observed := recordPhaseSamples([]phaseSample{{dns: 3, ttfb: 10, duration: 12}, {ttfb: 5, duration: 6}})
	bad := observed
	bad.DNS = 2
	if sketches.ConsistentWith(&bad, 2) {
		t.Fatal("a DNS sketch with fewer samples than observed must be refused")
	}
	bad = observed
	bad.NoTTFB = 1
	if sketches.ConsistentWith(&bad, 2) {
		t.Fatal("server samples must equal the samples with a first byte")
	}
	if sketches.ConsistentWith(nil, 2) {
		t.Fatal("sketches without observations cannot be checked")
	}
	var nilSketches *PhaseSketches
	if nilSketches.ConsistentWith(&observed, 2) {
		t.Fatal("nil sketches are unknown")
	}
	tampered := sketches.Clone()
	tampered.DNS.Bins[0].Count++
	if tampered.ConsistentWith(&observed, 2) {
		t.Fatal("a malformed sketch must be refused")
	}
	if sketches.DNS.Bins[0].Count != 1 {
		t.Fatal("Clone must not share buckets")
	}
}

func TestPhaseSketchMergeIsExactAndUnknownWins(t *testing.T) {
	first := []phaseSample{{dns: 1, connect: 5, tls: 9, ttfb: 40, duration: 60}, {ttfb: 12, duration: 13}}
	second := []phaseSample{{connect: 7, tls: 11, ttfb: 70, duration: 90}, {duration: 2000, connect: 3}}
	a, _ := recordPhaseSamples(first)
	b, _ := recordPhaseSamples(second)
	all, observed := recordPhaseSamples(append(append([]phaseSample(nil), first...), second...))

	var merge PhaseSketchMerge
	merge.Add(a, 2)
	merge.Add(b, 2)
	merge.Add(nil, 0) // a part with no samples changes nothing
	merged := merge.Result()
	if merged == nil || !merged.ConsistentWith(&observed, 4) {
		t.Fatalf("merged = %+v", merged)
	}
	for _, phase := range NetworkPhases {
		got, want := merged.Phase(phase), all.Phase(phase)
		if (got == nil) != (want == nil) || got != nil && (got.Count != want.Count || got.Sum != want.Sum || len(got.Bins) != len(want.Bins)) {
			t.Fatalf("%s merged %+v, recorded together %+v", phase, got, want)
		}
	}

	var mixed PhaseSketchMerge
	mixed.Add(a, 2)
	mixed.Add(nil, 3) // an older generator's part
	if got := mixed.Result(); got != nil {
		t.Fatalf("a part without sketches must make the merge unknown, got %+v", got)
	}
	var none PhaseSketchMerge
	if none.Result() != nil {
		t.Fatal("a merge with no samples is unknown")
	}
	var invalid PhaseSketchMerge
	broken := a.Clone()
	broken.Server.Count++
	invalid.Add(broken, 2)
	if invalid.Result() != nil {
		t.Fatal("an invalid part must make the merge unknown")
	}
}

func TestApplyPhaseSketchBudgetKeepsPriorityThenBusiest(t *testing.T) {
	wide := func(n int) *PhaseSketches {
		var r PhaseSketchRecorder
		for i := 1; i <= n; i++ {
			r.ObserveMs(float64(i), 0, 0, 0, 0) // n distinct DNS buckets
		}
		return r.Snapshot()
	}
	fleet, busy, quiet := wide(30), wide(30), wide(30)
	ApplyPhaseSketchBudget(fleet.Bins()+busy.Bins(),
		[]PhaseSketchBudgetEntry{{Key: "fleet", Sketches: &fleet}},
		[]PhaseSketchBudgetEntry{{Key: "quiet", Count: 1, Sketches: &quiet}, {Key: "busy", Count: 9, Sketches: &busy}})
	if fleet == nil || busy == nil || quiet != nil {
		t.Fatalf("fleet %v busy %v quiet %v", fleet != nil, busy != nil, quiet != nil)
	}
}

func TestMetricsSnapshotRecordsPhaseSketchesForFleetTransactionsAndActions(t *testing.T) {
	mc := NewMetricsCollector(16)
	results := []*protocol.Result{
		{ActionName: "checkout", BusinessTransaction: true, Success: true, Group: "g", Duration: 120 * time.Millisecond, DNSTime: 4 * time.Millisecond, ConnectTime: 10 * time.Millisecond, TLSTime: 30 * time.Millisecond, TTFB: 100 * time.Millisecond},
		{ActionName: "checkout", BusinessTransaction: true, Success: true, Group: "g", Duration: 25 * time.Millisecond, TTFB: 20 * time.Millisecond},
		{ActionName: "GET /cart", Success: true, Group: "g", Duration: 40 * time.Millisecond, ConnectTime: 5 * time.Millisecond, TTFB: 30 * time.Millisecond, NetworkProfile: "4g"},
	}
	snap := mc.computeSnapshot(results, time.Second)
	if snap.PhaseSketches == nil || snap.PhaseSketches.DNS == nil || snap.PhaseSketches.DNS.Count != 1 || snap.PhaseSketches.Server.Count != 2 {
		t.Fatalf("fleet sketches = %+v", snap.PhaseSketches)
	}
	if !snap.PhaseSketches.ConsistentWith(snap.PhaseObserved, 2) {
		t.Fatal("fleet sketches should agree with the fleet observations")
	}
	tx := snap.ByTransaction["checkout"]
	if tx == nil || tx.PhaseSketches == nil || tx.PhaseSketches.TLS == nil || !tx.PhaseSketches.ConsistentWith(tx.PhaseObserved, tx.Count) {
		t.Fatalf("transaction sketches = %+v", tx)
	}
	var action *ActionMetrics
	for _, value := range snap.ByAction {
		action = value
	}
	if action == nil || action.PhaseSketches == nil || action.PhaseSketches.Connect == nil || action.PhaseSketches.TLS != nil {
		t.Fatalf("action sketches = %+v", action)
	}
	for name, value := range snap.ByNetworkProfile {
		if value.PhaseSketches != nil {
			t.Fatalf("network profile %q should not carry phase sketches", name)
		}
	}
	without := snap
	without.PhaseSketches = nil
	if estimateMetricsSnapshotBytes(snap) <= estimateMetricsSnapshotBytes(without) {
		t.Fatal("the snapshot size estimate should count phase sketches")
	}
}
