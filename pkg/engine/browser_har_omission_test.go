package engine

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

type harOmissionProtocol struct {
	harUnitProtocol
	counts      map[string]int64
	panicStatus bool
}

func (p *harOmissionProtocol) TakeBrowserHARCaptureOmissions() map[string]int64 {
	if p.panicStatus {
		panic("private provider detail")
	}
	out := p.counts
	p.counts = nil
	return out
}

type harOmissionSink struct {
	harUnitSink
	counts map[string]int64
}

func (s *harOmissionSink) RecordBrowserHARCaptureOmissions(counts map[string]int64) error {
	var err error
	s.counts, err = protocol.MergeBrowserHARCaptureOmissions(s.counts, counts)
	return err
}
func TestBrowserHARCaptureOmissionsReachArchiveWithoutArtifacts(t *testing.T) {
	p := &harOmissionProtocol{counts: map[string]int64{protocol.BrowserHAROmittedCaptureFailed: 2}}
	sink := &harOmissionSink{}
	handOffBrowserHARArtifacts(p, &concurrentCaptureSink{})
	if len(p.counts) == 0 {
		t.Fatal("unavailable sink consumed losses")
	}
	handOffBrowserSessionEvidence(p, sink, nil)
	handOffBrowserSessionEvidence(p, sink, nil)
	if sink.counts[protocol.BrowserHAROmittedCaptureFailed] != 2 || len(sink.recorded) != 0 {
		t.Fatalf("counts=%v artifacts=%v", sink.counts, sink.recorded)
	}
	for _, bad := range []*harOmissionProtocol{{panicStatus: true}, {counts: map[string]int64{"secret error detail": 1}}} {
		next := &harOmissionSink{}
		handOffBrowserHARArtifacts(bad, next)
		if len(next.counts) != 1 || next.counts[protocol.BrowserHAROmittedCaptureStatus] != 1 {
			t.Fatal(next.counts)
		}
	}
}
