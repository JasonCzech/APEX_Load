package engine

import (
	"context"
	"errors"
	"github.com/jasonczech/apexload/pkg/protocol"
	"testing"
)

type sourceJournalTestSink struct {
	journal *protocol.BrowserSourceJournal
}

func (s sourceJournalTestSink) BrowserDocumentSourceJournal() (*protocol.BrowserSourceJournal, error) {
	return s.journal, nil
}

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
