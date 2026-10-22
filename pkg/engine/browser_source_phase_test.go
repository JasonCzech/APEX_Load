package engine

import (
	"context"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	"testing"
)

type phaseJournalSink struct{ sourceJournalTestSink }

func (phaseJournalSink) RecordResult(*protocol.Result) error { return nil }
func TestBrowserSourcePhaseRequiresPositiveStablePopulation(t *testing.T) {
	for _, name := range []string{"steady", "paused", "ramping", "missing worker", "dynamic", "shaped"} {
		t.Run(name, func(t *testing.T) {
			var events []protocol.BrowserSourceEvent
			j, err := protocol.NewBrowserSourceJournal("run", "controller", func(e *protocol.BrowserDocumentReceiptEvidence) error { events = append(events, *e.Source); return nil })
			if err != nil {
				t.Fatal(err)
			}
			yes := true
			s := &dsl.Script{Protocol: "browser"}
			s.Config.Browser.CaptureDocumentMetrics = &yes
			m := &MultiGroupEngine{controlsReady: true, vuserRunning: map[int]struct{}{1: {}}, groups: []GroupConfig{{Script: s, VUsers: 1, ResultSink: phaseJournalSink{sourceJournalTestSink{j}}}}}
			switch name {
			case "paused":
				m.paused = true
			case "ramping":
				m.initialRampInProgress = true
			case "missing worker":
				m.vuserRunning = nil
			case "dynamic":
				m.browserProofDynamic = true
			case "shaped":
				m.groups[0].ThroughputCapEnabled = true
			}
			m.browserSourcePhaseLocked(true)
			if (len(events) == 2) != (name == "steady") {
				t.Fatal("unsafe steady phase", events)
			}
			if name == "steady" {
				m.browserSourcePhaseLocked(false)
				if len(events) != 3 || events[2].Kind != "unknown" {
					t.Fatal(events)
				}
			}
		})
	}
}
func TestBrowserSourcePauseSeparatesEqualLoadSpans(t *testing.T) {
	var events []protocol.BrowserSourceEvent
	j, err := protocol.NewBrowserSourceJournal("run", "controller", func(e *protocol.BrowserDocumentReceiptEvidence) error { events = append(events, *e.Source); return nil })
	if err != nil {
		t.Fatal(err)
	}
	yes := true
	s := &dsl.Script{Protocol: "browser"}
	s.Config.Browser.CaptureDocumentMetrics = &yes
	m := &MultiGroupEngine{baseCtx: context.Background(), controlsReady: true, vuserRunning: map[int]struct{}{1: {}}, groups: []GroupConfig{{Script: s, VUsers: 1, ResultSink: phaseJournalSink{sourceJournalTestSink{j}}}}}
	m.browserSourcePhaseLocked(true)
	if err = m.Pause(); err != nil {
		t.Fatal(err)
	}
	if err = m.Resume(); err != nil {
		t.Fatal(err)
	}
	if len(events) != 4 || events[1].Kind != "steady" || events[2].Kind != "unknown" || events[3].Kind != "steady" {
		t.Fatal(events)
	}
	m.browserDocumentLoadLeaving(true)
	if events[len(events)-1].Successful {
		t.Fatal("unexpected exit concealed")
	}
}

func TestBrowserSourceRejectedControlsPreservePhase(t *testing.T) {
	m := &MultiGroupEngine{browserProofPhaseActive: true}
	if err := m.ScaleVUsers("missing", 1); err == nil {
		t.Fatal("unknown group admitted")
	}
	if err := m.SetGroupThroughputCap("missing", 1, true); err == nil {
		t.Fatal("unknown cap admitted")
	}
	if !m.browserProofPhaseActive || m.browserProofDynamic {
		t.Fatal("rejected control changed proof")
	}
}
func TestBrowserSourceExternalCancellationCannotCloseCleanSpan(t *testing.T) {
	var events []protocol.BrowserSourceEvent
	j, err := protocol.NewBrowserSourceJournal("run", "controller", func(e *protocol.BrowserDocumentReceiptEvidence) error { events = append(events, *e.Source); return nil })
	if err != nil {
		t.Fatal(err)
	}
	m := &MultiGroupEngine{browserProofPhaseActive: true, controlsReady: true, groups: []GroupConfig{{ResultSink: phaseJournalSink{sourceJournalTestSink{j}}}}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m.closeRuntimeAdmissionOnDone(ctx)
	m.browserDocumentLoadLeaving(false)
	if len(events) != 2 || events[1].Successful {
		t.Fatal("cancellation inferred clean boundary", events)
	}
}

func TestBrowserSourceCancelledBeforePlannedStopCannotCloseCleanSpan(t *testing.T) {
	for _, hard := range []bool{false, true} {
		var events []protocol.BrowserSourceEvent
		j, err := protocol.NewBrowserSourceJournal("run", "controller", func(e *protocol.BrowserDocumentReceiptEvidence) error { events = append(events, *e.Source); return nil })
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		m := &MultiGroupEngine{baseCtx: ctx, loopCtx: ctx, browserProofPhaseActive: true, controlsReady: true, groups: []GroupConfig{{ResultSink: phaseJournalSink{sourceJournalTestSink{j}}}}}
		cancel()
		if hard {
			m.Stop()
		} else {
			m.GracefulStop()
		}
		m.closeRuntimeAdmissionOnDone(ctx)
		m.browserDocumentLoadLeaving(false)
		if len(events) != 2 || events[1].Successful {
			t.Fatal("stop hid prior cancellation", events)
		}
	}
}
