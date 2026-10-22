package engine

import (
	"errors"
	"github.com/jasonczech/apexload/pkg/protocol"
	"sync"
	"testing"
)

type receiptUnitProvider struct {
	*browserEvidenceUnitProtocol
	receipts *protocol.BrowserDocumentReceiptEvidence
	version  int
}

func (p *receiptUnitProvider) BrowserDocumentReceiptEvidence() *protocol.BrowserDocumentReceiptEvidence {
	return p.receipts
}
func (p *receiptUnitProvider) BrowserDocumentReceiptTransportVersion() int {
	if p.version == 99 {
		panic("wiring")
	}
	return p.version
}

type receiptUnitSink struct {
	mu               sync.Mutex
	calls            int
	fail, incomplete bool
}

func (s *receiptUnitSink) RecordResult(*protocol.Result) error { return nil }
func (s *receiptUnitSink) RecordBrowserDocumentReceiptEvidence(e *protocol.BrowserDocumentReceiptEvidence) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.fail {
		return errors.New("write failed")
	}
	return nil
}
func (s *receiptUnitSink) MarkBrowserDocumentReceiptIncomplete() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.incomplete = true
}
func receiptUnitFixture() *receiptUnitProvider {
	o := browserEngineTestObservation()
	return &receiptUnitProvider{browserEvidenceUnitProtocol: browserEngineTestProvider(o), version: 1, receipts: &protocol.BrowserDocumentReceiptEvidence{Version: 1, SourceGeneration: o.SourceGeneration, StartedAt: o.WindowStart, Receipts: []protocol.BrowserDocumentReceipt{}}}
}
func TestBrowserReceiptPublicationRetainsLossAfterRetirement(t *testing.T) {
	for _, tc := range []struct {
		name                               string
		version                            int
		missing, malformed, fail, wantLoss bool
	}{{"success", 1, false, false, false, false}, {"legacy", -1, false, false, true, false}, {"wiring panic", 99, false, false, false, true}, {"missing sink", 1, true, false, false, true}, {"malformed", 1, false, true, false, true}, {"failed write", 1, false, false, true, true}} {
		t.Run(tc.name, func(t *testing.T) {
			mc := NewMetricsCollector(4)
			p := receiptUnitFixture()
			p.version = tc.version
			if tc.malformed {
				p.receipts = nil
			}
			entry := mc.registerBrowserDocuments(p, "browser")
			sink := &receiptUnitSink{fail: tc.fail}
			if !tc.missing {
				entry.publish(sink, nil, "")
			}
			entry.capture(true)
			mc.browserDocuments.sample(true)
			if (tc.version != 1 || tc.malformed) && sink.calls != 0 {
				t.Fatal("unsupported or malformed provider published receipts")
			}
			if mc.BrowserDocumentReceiptsIncomplete() != tc.wantLoss {
				t.Fatal("loss state", mc.BrowserDocumentReceiptsIncomplete())
			}
			if len(mc.browserDocuments.entries) != 0 {
				t.Fatal("provider retained")
			}
			if !tc.missing && sink.incomplete != tc.wantLoss {
				t.Fatal("archive loss marker", sink.incomplete)
			}
			if mc.browserDocuments.incomplete {
				t.Fatal("receipt failure changed raw population")
			}
		})
	}
}
func TestBrowserReceiptPublicationDeduplicatesAndRetries(t *testing.T) {
	mc := NewMetricsCollector(4)
	p := receiptUnitFixture()
	entry := mc.registerBrowserDocuments(p, "browser")
	s := &receiptUnitSink{fail: true}
	entry.publish(s, nil, "")
	entry.publishReceipts()
	s.fail = false
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(entry.publishReceipts)
	}
	wg.Wait()
	if s.calls != 2 {
		t.Fatal("publication not serialized or deduplicated", s.calls)
	}
	if !mc.BrowserDocumentReceiptsIncomplete() || !s.incomplete {
		t.Fatal("retry cleared loss history")
	}
}
