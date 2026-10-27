package protocol

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func sourceJournalFixture(t *testing.T) ([]*BrowserDocumentReceiptEvidence, string) {
	t.Helper()
	var records []*BrowserDocumentReceiptEvidence
	journal, err := NewBrowserSourceJournal("run", "controller", func(e *BrowserDocumentReceiptEvidence) error { records = append(records, e.Clone()); return nil })
	if err != nil {
		t.Fatal(err)
	}
	lease, err := journal.Begin()
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	g := strings.Repeat("a", 32)
	e := &BrowserDocumentReceiptEvidence{Version: 3, SourceGeneration: g, StartedAt: start.UTC(), Lifetime: &BrowserNativeLifetime{CreationBracketed: true}, Receipts: []BrowserDocumentReceipt{}}
	if err = lease.ExpectContext(e, start); err != nil {
		t.Fatal(err)
	}
	if err = journal.Phase(true, 1); err != nil {
		t.Fatal(err)
	}
	e.Lifetime.Closed = true
	e.Lifetime.EndNS = time.Since(start).Nanoseconds()
	e.Closure = &BrowserNativeSourceClosure{Method: "chromium_cdp_commits_v1", DiscoveryComplete: true, Documents: []BrowserNativeDocumentExpectation{{NativeID: strings.Repeat("b", 64), DocumentID: g + "-d1", LifecycleID: g + "-l1", Terminal: true}}}
	if err = lease.CloseContext(e, true); err != nil {
		t.Fatal(err)
	}
	if err = journal.Phase(false, 1); err != nil {
		t.Fatal(err)
	}
	if err = lease.Finish(true); err != nil {
		t.Fatal(err)
	}
	if err = journal.Seal(true); err != nil {
		t.Fatal(err)
	}
	return records, g
}
func TestBrowserSourceJournalReconcilesIndependentInventory(t *testing.T) {
	records, g := sourceJournalFixture(t)
	r := NewBrowserSourceReconciler("run", []string{"controller"})
	for _, e := range records {
		if e == nil {
			t.Fatal("invalid journal record")
		}
		if err := r.Observe(e); err != nil {
			t.Fatal(err)
		}
	}
	proof, spans, ok := r.Context(g)
	if !ok || !r.Complete() || len(spans) != 1 || proof.SourceOriginNS <= 0 || proof.BeforeNS < proof.SourceOriginNS || proof.AfterNS < proof.BeforeNS {
		t.Fatalf("invalid reconciled proof: %+v %+v", proof, spans)
	}
	proof.Receipts.Closure.DiscoveryComplete = false
	if next, _, _ := r.Context(g); !next.Receipts.Closure.DiscoveryComplete {
		t.Fatal("proof aliases reconciler")
	}
}
func TestBrowserSourceJournalRefusesIncompletePopulation(t *testing.T) {
	for _, name := range []string{"missing owner", "missing context", "missing terminal", "failed context", "failed slot", "late context", "changed origin", "reordered", "duplicate", "unknown discovery", "missing document", "empty slots", "future lifetime", "document conflict"} {
		t.Run(name, func(t *testing.T) {
			records, _ := sourceJournalFixture(t)
			owners := []string{"controller"}
			switch name {
			case "missing owner":
				owners = append(owners, "unobserved-generator")
			case "missing context":
				records = append(records[:2], records[3:]...)
			case "missing terminal":
				records = records[:len(records)-1]
			case "failed context":
				records[4].Source.Successful = false
			case "failed slot":
				records[6].Source.Successful = false
			case "late context":
				records[2].Lifetime.Closed = true
			case "changed origin":
				records[4].Source.SourceOriginNS++
			case "reordered":
				records[3], records[4] = records[4], records[3]
			case "duplicate":
				records = append(records, records[len(records)-1])
			case "unknown discovery":
				records[4].Closure.DiscoveryComplete = false
			case "missing document":
				records[4].Closure.Documents[0].Terminal = false
			case "empty slots":
				records[4].Closure.Documents = nil
			case "future lifetime":
				records[4].Lifetime.EndNS = records[4].Source.AtNS - records[4].Source.SourceOriginNS + 1
			case "document conflict":
				e := records[4]
				g := e.SourceGeneration
				e.Receipts = []BrowserDocumentReceipt{{DocumentID: g + "-d2", LifecycleID: g + "-l1", FrameID: g + "-f1", Kind: "begin", BrowserTimeOriginMS: 1, ReceivedAt: e.StartedAt}}

			}
			r := NewBrowserSourceReconciler("run", owners)
			for _, e := range records {
				_ = r.Observe(e)
			}
			if r.Complete() {
				t.Fatal("incomplete source admitted")
			}
		})
	}
}
func TestBrowserSourceJournalDurabilityFailureSticky(t *testing.T) {
	for _, panicWriter := range []bool{false, true} {
		calls := 0
		j, err := NewBrowserSourceJournal("run", "controller", func(*BrowserDocumentReceiptEvidence) error {
			calls++
			if calls == 2 {
				if panicWriter {
					panic("disk")
				}
				return errors.New("disk")
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if l, err := j.Begin(); err == nil || l != nil {
			t.Fatal("operation admitted without durable expectation")
		}
		if err := j.Seal(true); err == nil {
			t.Fatal("failed expectation forgotten")
		}
	}
}
func TestBrowserSourceV3Projection(t *testing.T) {
	records, _ := sourceJournalFixture(t)
	for _, version := range []int{1, 2} {
		e := BrowserDocumentReceiptProjection(records[4], version)
		if e == nil || e.Source != nil || e.Closure != nil || e.Version != version {
			t.Fatal("invalid projection", version, e)
		}
	}
}

func TestBrowserSourceWholeWindowMap(t *testing.T) {
	for _, name := range []string{"valid", "revision", "raw start", "raw end", "capture", "metric identity", "missing begin", "late lower", "restored", "censored"} {
		t.Run(name, func(t *testing.T) {
			records, g := sourceJournalFixture(t)
			source, o, _ := causalMappingFixture()
			e := records[4]
			e.Receipts = source.Receipts
			for i := range e.Receipts {
				e.Receipts[i].ReceivedAt = e.StartedAt.Add(time.Duration(e.Receipts[i].HostElapsedNS))
			}
			o.CapturedAt = e.Receipts[1].ReceivedAt
			switch name {
			case "revision":
				o.Revision++
			case "raw start":
				o.WindowStart = o.WindowStart.Add(-1)
			case "raw end":
				o.WindowEnd = o.WindowEnd.Add(1)
			case "capture":
				o.CapturedAt = o.CapturedAt.Add(1)
			case "metric identity":
				o.Metric = "fcp"
			case "missing begin":
				e.Receipts = e.Receipts[1:]
			case "late lower":
				e.Closure.Documents[0].LowerNS = 2
			case "restored":
				o.LifecyclePhase = "bfcache_restore"
			case "censored":
				o.State = "censored"
			}
			r := NewBrowserSourceReconciler("run", []string{"controller"})
			for _, e := range records {
				if err := r.Observe(e); err != nil {
					t.Fatal(err)
				}
			}
			w, _, err := r.Map(o)
			if name == "valid" {
				p, _, _ := r.Context(g)
				if err != nil || w.StartNS != p.SourceOriginNS || w.EndNS != p.SourceOriginNS+2 {
					t.Fatal("wrong causal envelope", w, err)
				}
			} else if err == nil {
				t.Fatal("invalid measurement mapped")
			}
		})
	}
}

func TestBrowserSourceUnboundSnapshotsCannotDisappear(t *testing.T) {
	for _, version := range []int{1, 2, 3} {
		for _, foreign := range []bool{false, true} {
			records, _ := sourceJournalFixture(t)
			snapshot := BrowserDocumentReceiptProjection(records[4], version)
			snapshot.Source = nil
			if foreign {
				snapshot.SourceGeneration = strings.Repeat("c", 32)
				snapshot.Closure = nil
			}
			r := NewBrowserSourceReconciler("run", []string{"controller"})
			if err := r.Observe(snapshot); err != nil {
				t.Fatal(err)
			}
			for _, e := range records {
				if err := r.Observe(e); err != nil {
					t.Fatal(err)
				}
			}
			if r.Complete() == foreign {
				t.Fatalf("version=%d foreign=%v complete=%v", version, foreign, r.Complete())
			}
		}
	}
}

func TestBrowserSourceControlEventCannotHideForeignReceipts(t *testing.T) {
	for _, index := range []int{1, 3, 6, 7} {
		for _, field := range []string{"generation", "receipt", "lifetime", "closure", "origin"} {
			records, _ := sourceJournalFixture(t)
			e := records[index]
			switch field {
			case "generation":
				e.SourceGeneration = strings.Repeat("c", 32)
			case "receipt":
				q := receiptContractFixture()
				e.SourceGeneration = q.SourceGeneration
				e.StartedAt = q.StartedAt
				e.Receipts = q.Receipts
			case "lifetime":
				e.Lifetime = &BrowserNativeLifetime{CreationBracketed: true}
			case "closure":
				e.Closure = &BrowserNativeSourceClosure{Method: "unavailable"}
			case "origin":
				e.Source.SourceOriginNS = 1
			}
			r := NewBrowserSourceReconciler("run", []string{"controller"})
			for _, record := range records {
				_ = r.Observe(record)
			}
			if r.Complete() {
				t.Fatalf("control %d concealed %s", index, field)
			}
		}
	}
}

func TestBrowserSourceCompletedLookupCacheInvalidatedByNewEvidence(t *testing.T) {
	records, g := sourceJournalFixture(t)
	r := NewBrowserSourceReconciler("run", []string{"controller"})
	for _, e := range records {
		if err := r.Observe(e); err != nil {
			t.Fatal(err)
		}
	}
	if !r.Complete() {
		t.Fatal("valid journal incomplete")
	}
	for i := 0; i < 1000; i++ {
		if _, _, ok := r.Context(g); !ok {
			t.Fatal("immutable lookup failed")
		}
	}
	extra := receiptContractFixture()
	extra.SourceGeneration = strings.Repeat("c", 32)
	extra.Receipts = nil
	if err := r.Observe(extra); err != nil {
		t.Fatal(err)
	}
	if r.Complete() {
		t.Fatal("new source reused cached closure")
	}
}

func TestBrowserSourceDistributedClockBracketsAndMultiGeneratorReconciliation(t *testing.T) {
	controllerDomain, err := NewBrowserClockDomain("run")
	if err != nil {
		t.Fatal(err)
	}
	startedAt := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	buildOwner := func(ownerID, genChar string, steadyStart, steadyEnd, navStart, navEnd, terminalAt time.Duration, vusers int, wallShift time.Duration) ([]*BrowserDocumentReceiptEvidence, BrowserClockDomain, BrowserDocumentObservation) {
		authority := BrowserClockDomain{
			RunID:       "run",
			Incarnation: strings.Repeat(genChar, 32),
			DomainID:    strings.Repeat(genChar, 32),
		}
		slot := strings.Repeat(genChar, 32)
		gen := strings.Repeat(genChar, 32)
		ownerStart := startedAt.Add(wallShift)
		beginElapsed := navStart - 10*time.Millisecond
		endElapsed := navEnd - 10*time.Millisecond
		rawStart := ownerStart.Add(navStart).Truncate(time.Millisecond)
		rawEnd := rawStart.Add(navEnd - navStart)
		capturedAt := ownerStart.Add(endElapsed)
		value := 120.0
		obs := BrowserDocumentObservation{
			SourceGeneration: gen,
			DocumentID:       gen + "-d1",
			LifecycleID:      gen + "-l1",
			FrameID:          gen + "-f1",
			MeasurementID:    gen + "-d1/" + gen + "-l1/lcp",
			Metric:           "lcp",
			Unit:             "ms",
			Value:            &value,
			Available:        true,
			State:            "final",
			LifecyclePhase:   "navigation",
			Revision:         1,
			WindowStart:      rawStart,
			WindowEnd:        rawEnd,
			CapturedAt:       capturedAt,
		}
		begin := BrowserDocumentReceipt{
			DocumentID:          obs.DocumentID,
			LifecycleID:         obs.LifecycleID,
			FrameID:             obs.FrameID,
			Kind:                "begin",
			BrowserTimeOriginMS: float64(rawStart.UnixMilli()),
			HostElapsedNS:       int64(beginElapsed),
			ReceivedAt:          ownerStart.Add(beginElapsed),
		}
		metric := begin
		metric.Kind = "metric"
		metric.MeasurementID = obs.MeasurementID
		metric.Revision = 1
		metric.BrowserOffsetMS = float64((navEnd - navStart).Milliseconds())
		metric.HostElapsedNS = int64(endElapsed)
		metric.ReceivedAt = capturedAt
		terminalEvidence := &BrowserDocumentReceiptEvidence{
			Version:          3,
			SourceGeneration: gen,
			StartedAt:        ownerStart,
			Lifetime: &BrowserNativeLifetime{
				CreationBracketed: true,
				Closed:            true,
				EndNS:             int64(terminalAt - 50*time.Millisecond),
			},
			Closure: &BrowserNativeSourceClosure{
				Method:            "chromium_cdp_commits_v1",
				DiscoveryComplete: true,
				Documents: []BrowserNativeDocumentExpectation{{
					NativeID:    strings.Repeat(genChar, 64),
					DocumentID:  obs.DocumentID,
					LifecycleID: obs.LifecycleID,
					Terminal:    true,
					LowerNS:     int64(navStart - 10*time.Millisecond),
				}},
			},
			Receipts: []BrowserDocumentReceipt{begin, metric},
		}
		var records []*BrowserDocumentReceiptEvidence
		seq := uint64(0)
		emit := func(kind, slotID string, at, origin time.Duration, expected uint64, ev *BrowserDocumentReceiptEvidence) {
			seq++
			if ev == nil {
				ev = &BrowserDocumentReceiptEvidence{Version: 3, SourceGeneration: authority.DomainID, StartedAt: ownerStart, Receipts: []BrowserDocumentReceipt{}}
			} else {
				ev = ev.Clone()
			}
			ev.Source = &BrowserSourceEvent{
				Authority:      authority,
				OwnerID:        ownerID,
				Kind:           kind,
				SlotID:         slotID,
				Sequence:       seq,
				AtNS:           int64(at),
				SourceOriginNS: int64(origin),
				ExpectedSlots:  expected,
				Successful:     true,
				VUsers:         vusers,
			}
			records = append(records, ev)
		}
		emit("owner_expected", "", 0, 0, 0, nil)
		emit("slot_expected", slot, 5*time.Millisecond, 0, 1, nil)
		emit("context_expected", slot, 15*time.Millisecond, 10*time.Millisecond, 1, &BrowserDocumentReceiptEvidence{
			Version:          3,
			SourceGeneration: gen,
			StartedAt:        ownerStart,
			Lifetime:         &BrowserNativeLifetime{CreationBracketed: true},
			Receipts:         []BrowserDocumentReceipt{},
		})
		emit("steady", "", steadyStart, 0, 1, nil)
		emit("unknown", "", steadyEnd, 0, 1, nil)
		emit("context_terminal", slot, terminalAt-20*time.Millisecond, 10*time.Millisecond, 1, terminalEvidence)
		emit("slot_terminal", slot, terminalAt-10*time.Millisecond, 0, 1, nil)
		emit("owner_terminal", "", terminalAt, 0, 1, nil)
		return records, authority, obs
	}

	rec1, auth1, obs1 := buildOwner("gen-1", "a", 100*time.Millisecond, 4*time.Second, 500*time.Millisecond, 700*time.Millisecond, 5*time.Second, 2, 0)
	rec2, auth2, obs2 := buildOwner("gen-2", "b", 150*time.Millisecond, 3900*time.Millisecond, 600*time.Millisecond, 800*time.Millisecond, 5*time.Second, 3, 10*time.Minute)

	reconciler := func() *BrowserSourceReconciler {
		r := NewBrowserSourceReconciler("run", []string{"gen-1", "gen-2"})
		for _, e := range append(append([]*BrowserDocumentReceiptEvidence(nil), rec1...), rec2...) {
			if err := r.Observe(e); err != nil {
				t.Fatal(err)
			}
		}
		if !r.Complete() {
			t.Fatal("expected complete source closure")
		}
		return r
	}

	validEnvelope := func() *BrowserDistributedClockEnvelope {
		return &BrowserDistributedClockEnvelope{
			Version:            1,
			Controller:         controllerDomain,
			AuthorityStartedAt: startedAt,
			MaxSkewPPM:         DefaultBrowserClockMaxSkewPPM,
			MaxBracketRTTNS:    DefaultBrowserClockMaxBracketRTTNS,
			MaxBracketGapNS:    DefaultBrowserClockMaxBracketGapNS,
			Owners: map[string][]BrowserClockBracket{
				"gen-1": {
					{Authority: auth1, OwnerID: "gen-1", ControllerSendNS: int64(1 * time.Millisecond), ControllerRecvNS: int64(3 * time.Millisecond), OwnerMonoNS: int64(2 * time.Millisecond)},
					{Authority: auth1, OwnerID: "gen-1", ControllerSendNS: int64(5001 * time.Millisecond), ControllerRecvNS: int64(5003 * time.Millisecond), OwnerMonoNS: int64(5002 * time.Millisecond)},
				},
				"gen-2": {
					{Authority: auth2, OwnerID: "gen-2", ControllerSendNS: int64(1 * time.Millisecond), ControllerRecvNS: int64(4 * time.Millisecond), OwnerMonoNS: int64(2 * time.Millisecond)},
					{Authority: auth2, OwnerID: "gen-2", ControllerSendNS: int64(5001 * time.Millisecond), ControllerRecvNS: int64(5004 * time.Millisecond), OwnerMonoNS: int64(5002 * time.Millisecond)},
				},
			},
		}
	}

	r := reconciler()
	r.SetDistributedClockEnvelope(validEnvelope())
	if ok, reason := r.ValidateDistributedClocks(); !ok || reason != "" {
		t.Fatalf("expected valid distributed clocks, got ok=%v reason=%q", ok, reason)
	}
	w1, u1, spans1, err := r.MapDistributed(obs1)
	if err != nil {
		t.Fatal(err)
	}
	w2, u2, spans2, err := r.MapDistributed(obs2)
	if err != nil {
		t.Fatal(err)
	}
	if len(spans1) != 1 || len(spans2) != 1 || spans1[0] != spans2[0] || spans1[0].VUsers != 5 {
		t.Fatalf("expected intersected 5-VUser steady span across 2 generators, got %+v and %+v", spans1, spans2)
	}
	if !ContainedInBrowserSteadySpan(w1, []BrowserMonotonicSpan{spans1[0].BrowserMonotonicSpan}) || !ContainedInBrowserSteadySpan(w2, []BrowserMonotonicSpan{spans2[0].BrowserMonotonicSpan}) {
		t.Fatalf("mapped windows not contained in intersected steady span: w1=%+v w2=%+v span=%+v", w1, w2, spans1[0])
	}
	if spans1[0].StartNS < int64(150*time.Millisecond) || spans1[0].EndNS > int64(3900*time.Millisecond) {
		t.Fatalf("expected steady span to be conservative intersection [>=150ms, <=3900ms], got %+v", spans1[0])
	}
	earlyWindow := BrowserMonotonicSpan{Authority: controllerDomain, StartNS: int64(110 * time.Millisecond), EndNS: int64(130 * time.Millisecond)}
	if ContainedInBrowserSteadySpan(earlyWindow, []BrowserMonotonicSpan{spans1[0].BrowserMonotonicSpan}) {
		t.Fatalf("window during gen-1-only steady phase [110ms, 130ms] must not be contained in fleet intersected steady span %+v", spans1[0])
	}
	if u1 <= 0 || u1 > 10*time.Millisecond || u2 <= 0 || u2 > 10*time.Millisecond {
		t.Fatalf("expected ms-scale clock uncertainty, got u1=%v u2=%v", u1, u2)
	}

	for _, tc := range []struct {
		name   string
		mutate func(*BrowserDistributedClockEnvelope)
		reason string
	}{
		{
			name: "rtt exceeded",
			mutate: func(env *BrowserDistributedClockEnvelope) {
				b := env.Owners["gen-1"][1]
				b.ControllerRecvNS = b.ControllerSendNS + int64(1500*time.Millisecond)
				env.Owners["gen-1"][1] = b
			},
			reason: "browser_clock_bracket_rtt_exceeded",
		},
		{
			name: "gap exceeded",
			mutate: func(env *BrowserDistributedClockEnvelope) {
				env.MaxBracketGapNS = int64(2 * time.Second)
			},
			reason: "browser_clock_bracket_gap_exceeded",
		},
		{
			name: "unbounded initial bracket",
			mutate: func(env *BrowserDistributedClockEnvelope) {
				env.Owners["gen-1"][0].OwnerMonoNS = int64(20 * time.Millisecond)
			},
			reason: "browser_clock_brackets_unbounded",
		},
		{
			name: "inconsistent drift cone",
			mutate: func(env *BrowserDistributedClockEnvelope) {
				b := env.Owners["gen-2"][1]
				b.ControllerSendNS = int64(6000 * time.Millisecond)
				b.ControllerRecvNS = int64(6002 * time.Millisecond)
				env.Owners["gen-2"][1] = b
			},
			reason: "browser_clock_brackets_inconsistent",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := reconciler()
			env := validEnvelope()
			tc.mutate(env)
			r.SetDistributedClockEnvelope(env)
			ok, reason := r.ValidateDistributedClocks()
			if ok || reason != tc.reason {
				t.Fatalf("expected (false, %q), got (%v, %q)", tc.reason, ok, reason)
			}
			if !r.Complete() {
				t.Fatal("clock qualification failure must not invalidate source coverage closure")
			}
		})
	}
}
