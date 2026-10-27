package engine

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

type sourceJournalTestSink struct {
	journal *protocol.BrowserSourceJournal
}

func (s sourceJournalTestSink) BrowserDocumentSourceJournal() (*protocol.BrowserSourceJournal, error) {
	return s.journal, nil
}
func (s sourceJournalTestSink) RecordResult(*protocol.Result) error { return nil }

type sourceLeaseTestRuntime struct {
	lease          *protocol.BrowserSourceLease
	init, teardown bool
	events         *[]string
}

func (p *sourceLeaseTestRuntime) SetBrowserDocumentSourceLease(l *protocol.BrowserSourceLease) {
	p.lease = l
	*p.events = append(*p.events, "lease")
}
func (p *sourceLeaseTestRuntime) BrowserDocumentSourceLease() *protocol.BrowserSourceLease {
	return p.lease
}
func (p *sourceLeaseTestRuntime) Init(context.Context, map[string]any) error {
	p.init = true
	*p.events = append(*p.events, "init")
	if p.lease == nil {
		return errors.New("missing lease")
	}
	return nil
}
func (p *sourceLeaseTestRuntime) Teardown(context.Context) error {
	p.teardown = true
	*p.events = append(*p.events, "teardown")
	return nil
}
func TestBrowserSourceEngineAdmissionOrdering(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{true: "failed write", false: "successful lifecycle"}[fail], func(t *testing.T) {
			var events []string
			j, err := protocol.NewBrowserSourceJournal("run", "controller", func(e *protocol.BrowserDocumentReceiptEvidence) error {
				events = append(events, e.Source.Kind)
				if fail && e.Source.Kind == "slot_expected" {
					return errors.New("disk full")
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			p := &sourceLeaseTestRuntime{events: &events}
			err = callProtocolInit(context.Background(), p, map[string]any{"browser_document_source_journal": sourceJournalTestSink{j}}, nil)
			if fail {
				if err == nil || p.init || p.lease != nil {
					t.Fatal("native operation preceded persistence")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = callProtocolTeardownWithMode(context.Background(), p, nil, make(chan struct{}, 1), true); err != nil {
				t.Fatal(err)
			}
			want := []string{"owner_expected", "slot_expected", "lease", "init", "teardown", "slot_terminal"}
			if len(events) != len(want) {
				t.Fatal(events)
			}
			for i := range want {
				if events[i] != want[i] {
					t.Fatal(events)
				}
			}
		})
	}
}

type zeroRampOrderProtocol struct {
	mu      *sync.Mutex
	lease   *protocol.BrowserSourceLease
	journal *protocol.BrowserSourceJournal
	events  *[]string
}

func (p *zeroRampOrderProtocol) Name() string        { return "bRoWsEr" }
func (p *zeroRampOrderProtocol) Description() string { return "test" }
func (p *zeroRampOrderProtocol) SetBrowserDocumentSourceLease(l *protocol.BrowserSourceLease) {
	p.lease = l
}
func (p *zeroRampOrderProtocol) BrowserDocumentSourceLease() *protocol.BrowserSourceLease {
	return p.lease
}
func (p *zeroRampOrderProtocol) Init(context.Context, map[string]any) error { return nil }
func (p *zeroRampOrderProtocol) Execute(context.Context, *protocol.Action) (*protocol.Result, error) {
	p.mu.Lock()
	*p.events = append(*p.events, "execute")
	p.mu.Unlock()
	return &protocol.Result{Success: true, Duration: time.Millisecond}, nil
}
func (p *zeroRampOrderProtocol) Teardown(context.Context) error          { return nil }
func (p *zeroRampOrderProtocol) HealthCheck(context.Context) error { return nil }

func TestBrowserSourceSteadyPhasePrecedesZeroRampVUserExecution(t *testing.T) {
	var mu sync.Mutex
	var events []string
	j, err := protocol.NewBrowserSourceJournal("run", "controller", func(e *protocol.BrowserDocumentReceiptEvidence) error {
		if e.Source != nil && e.Source.Kind == "steady" {
			mu.Lock()
			events = append(events, "steady")
			mu.Unlock()
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	protocol.Register("bRoWsEr", func() protocol.Protocol {
		return &zeroRampOrderProtocol{mu: &mu, journal: j, events: &events}
	})
	yes := true
	script := &dsl.Script{
		ID:       "zero-ramp-order",
		Name:     "zero-ramp-order",
		Protocol: "bRoWsEr",
		Actions:  []dsl.Action{{Type: "browser_navigate", Target: "https://example.com"}},
	}
	script.Config.Browser.CaptureDocumentMetrics = &yes
	script.Config.RunLogic.Iterations = 1
	mge, err := NewMultiGroup(MultiGroupConfig{
		Duration: 200 * time.Millisecond,
		Groups: []GroupConfig{{
			Name:                        "browser",
			Script:                      script,
			VUsers:                      4,
			RampUp:                      0,
			ResultSink:                  sourceJournalTestSink{j},
			RunID:                       "run",
			BrowserDocumentProofVersion: 3,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := mge.Run(ctx); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(events) == 0 || events[0] != "steady" {
		t.Fatalf("expected steady phase to be emitted before any VUser execute when RampUp=0, got %v", events)
	}
}
