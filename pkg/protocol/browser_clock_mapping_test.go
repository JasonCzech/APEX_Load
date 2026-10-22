package protocol

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func causalMappingFixture() (*BrowserDocumentReceiptEvidence, BrowserDocumentObservation, BrowserCausalEnvelope) {
	g := strings.Repeat("a", 32)
	at := time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)
	o := BrowserDocumentObservation{SourceGeneration: g, DocumentID: g + "-d1", LifecycleID: g + "-l1", FrameID: g + "-f1", MeasurementID: g + "-d1/" + g + "-l1/lcp", Revision: 2, State: "final", Metric: "lcp", LifecyclePhase: "navigation", CapturedAt: at.Add(2), WindowStart: time.UnixMilli(1).UTC(), WindowEnd: time.UnixMilli(2).UTC()}
	e := &BrowserDocumentReceiptEvidence{Version: 2, SourceGeneration: g, StartedAt: at, Lifetime: &BrowserNativeLifetime{CreationBracketed: true, Closed: true, EndNS: 3}}
	for i, kind := range []string{"begin", "metric", "end"} {
		r := BrowserDocumentReceipt{DocumentID: o.DocumentID, LifecycleID: o.LifecycleID, FrameID: o.FrameID, Kind: kind, BrowserTimeOriginMS: 1, BrowserOffsetMS: float64(i), ReceivedAt: at.Add(time.Duration(i + 1)), HostElapsedNS: int64(i + 1)}
		if kind == "metric" {
			r.MeasurementID = o.MeasurementID
			r.Revision = o.Revision
		}
		e.Receipts = append(e.Receipts, r)
	}
	envelope := BrowserCausalEnvelope{Version: 1, Authority: BrowserClockDomain{RunID: "run", Incarnation: g, DomainID: strings.Repeat("b", 32)}, SourceGeneration: g, BeforeNS: 10, AfterNS: 30, SourceUpperNS: 3, NativeCreationBracketed: true, TerminalAcknowledged: true}
	return e, o, envelope
}
func TestBrowserCausalWholeWindowMapping(t *testing.T) {
	e, o, c := causalMappingFixture()
	w, err := MapBrowserDocumentLifetime(e, o, c)
	if err != nil || w.StartNS != 10 || w.EndNS != 30 {
		t.Fatal(w, err)
	}
	for _, tc := range []struct {
		name  string
		spans []BrowserMonotonicSpan
		want  bool
	}{
		{"contained", []BrowserMonotonicSpan{{c.Authority, 10, 30}}, true},
		{"starts before steady", []BrowserMonotonicSpan{{c.Authority, 11, 40}}, false},
		{"ends after steady", []BrowserMonotonicSpan{{c.Authority, 0, 29}}, false},
		{"pause between equal loads", []BrowserMonotonicSpan{{c.Authority, 0, 20}, {c.Authority, 21, 40}}, false},
		{"no proof", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if ContainedInBrowserSteadySpan(w, tc.spans) != tc.want {
				t.Fatal(tc)
			}
		})
	}
	foreign := c.Authority
	foreign.Incarnation = strings.Repeat("c", 32)
	if ContainedInBrowserSteadySpan(w, []BrowserMonotonicSpan{{foreign, 0, 100}}) {
		t.Fatal("restart domain equated")
	}
	// Even enormous receipt wall-clock movement cannot narrow the causal bounds.
	e.WallStepObserved = true
	for i := range e.Receipts {
		e.Receipts[i].WallDriftNS = int64(time.Hour)
		e.Receipts[i].ReceivedAt = e.Receipts[i].ReceivedAt.Add(time.Hour)
	}
	o.CapturedAt = o.CapturedAt.Add(time.Hour)
	got, err := MapBrowserDocumentLifetime(e, o, c)
	if err != nil || got != w {
		t.Fatal("causal bound depended on wall time", got, err)
	}
}
func TestBrowserCausalMappingRefusals(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*BrowserDocumentReceiptEvidence, *BrowserDocumentObservation, *BrowserCausalEnvelope)
	}{
		{"pre-origin start", func(_ *BrowserDocumentReceiptEvidence, o *BrowserDocumentObservation, _ *BrowserCausalEnvelope) {
			o.WindowStart = o.WindowStart.Add(-time.Hour)
		}},
		{"unknown phase", func(_ *BrowserDocumentReceiptEvidence, o *BrowserDocumentObservation, _ *BrowserCausalEnvelope) {
			o.LifecyclePhase = "unknown"
		}},
		{"restore start mismatch", func(e *BrowserDocumentReceiptEvidence, o *BrowserDocumentObservation, _ *BrowserCausalEnvelope) {
			o.LifecyclePhase = "bfcache_restore"
			e.Receipts[0].BrowserOffsetMS = 0.5
		}},
		{"missing end", func(e *BrowserDocumentReceiptEvidence, _ *BrowserDocumentObservation, _ *BrowserCausalEnvelope) {
			e.Receipts = e.Receipts[:2]
		}},
		{"revision mismatch", func(_ *BrowserDocumentReceiptEvidence, o *BrowserDocumentObservation, _ *BrowserCausalEnvelope) {
			o.Revision++
		}},
		{"foreign generation", func(_ *BrowserDocumentReceiptEvidence, _ *BrowserDocumentObservation, c *BrowserCausalEnvelope) {
			c.SourceGeneration = strings.Repeat("c", 32)
		}},
		{"late creation anchor", func(_ *BrowserDocumentReceiptEvidence, _ *BrowserDocumentObservation, c *BrowserCausalEnvelope) {
			c.NativeCreationBracketed = false
		}},
		{"missing terminal ack", func(_ *BrowserDocumentReceiptEvidence, _ *BrowserDocumentObservation, c *BrowserCausalEnvelope) {
			c.TerminalAcknowledged = false
		}},
		{"late receipt", func(_ *BrowserDocumentReceiptEvidence, _ *BrowserDocumentObservation, c *BrowserCausalEnvelope) {
			c.SourceUpperNS = 2
		}},
		{"loss", func(e *BrowserDocumentReceiptEvidence, _ *BrowserDocumentObservation, _ *BrowserCausalEnvelope) {
			e.Incomplete = true
		}},
		{"censored", func(_ *BrowserDocumentReceiptEvidence, o *BrowserDocumentObservation, _ *BrowserCausalEnvelope) {
			o.State = "censored"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, o, c := causalMappingFixture()
			tc.mutate(e, &o, &c)
			if _, err := MapBrowserDocumentLifetime(e, o, c); err == nil {
				t.Fatal("unproven mapping admitted")
			}
		})
	}
}

func TestBrowserReceiptLifetimeVersionProjection(t *testing.T) {
	e, _, _ := causalMappingFixture()
	if e.Version != 2 {
		t.Fatal("fixture must exercise v2")
	}
	legacy := BrowserDocumentReceiptProjection(e, 1)
	if legacy == nil || legacy.Version != 1 || legacy.Lifetime != nil {
		t.Fatal("v1 received lifetime")
	}
	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	for name := range fields {
		switch name {
		case "version", "source_generation", "started_at", "wall_step_observed", "incomplete", "receipts":
		default:
			t.Fatal("strict v1 peer would reject", name)
		}
	}
	if _, err = DecodeBrowserDocumentReceiptEvidence(raw); err != nil {
		t.Fatal(err)
	}
	if BrowserDocumentReceiptProjection(legacy, 2) != nil {
		t.Fatal("v1 upgraded into lifetime evidence")
	}
	e.Version = 1
	if e.Clone() != nil {
		t.Fatal("lifetime smuggled through v1")
	}
}
