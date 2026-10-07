package engine

import (
	"errors"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

type harUnitProtocol struct {
	protocol.Protocol
	published []protocol.BrowserHARArtifact
	panics    bool
}

func (p *harUnitProtocol) Name() string { return "browser" }
func (p *harUnitProtocol) TakeBrowserHARArtifacts() []protocol.BrowserHARArtifact {
	if p.panics {
		panic("provider failure")
	}
	out := p.published
	p.published = nil
	return out
}

type harUnitSink struct {
	concurrentCaptureSink
	recorded []protocol.BrowserHARArtifact
	fail     bool
}

func (s *harUnitSink) RecordBrowserHARArtifact(artifact protocol.BrowserHARArtifact) error {
	s.recorded = append(s.recorded, artifact)
	if s.fail {
		return errors.New("archive full")
	}
	return nil
}

func TestBrowserHARArtifactsMoveToTheArchiveSinkOnce(t *testing.T) {
	p := &harUnitProtocol{published: []protocol.BrowserHARArtifact{{Path: "/a.har", Generation: "g1"}, {Path: "/b.har", Generation: "g2"}}}
	sink := &harUnitSink{}
	handOffBrowserHARArtifacts(p, sink)
	handOffBrowserHARArtifacts(p, sink)
	if len(sink.recorded) != 2 || sink.recorded[1].Generation != "g2" {
		t.Fatalf("recorded %+v", sink.recorded)
	}
	// A sink failure is optional evidence lost, never a panic or a retry.
	failing := &harUnitSink{fail: true}
	p.published = []protocol.BrowserHARArtifact{{Path: "/c.har", Generation: "g3"}}
	handOffBrowserHARArtifacts(p, failing)
	if len(failing.recorded) != 1 {
		t.Fatal("failed handoff was not attempted once")
	}
	// A sink without HAR support leaves artifacts in their HAR directory, and
	// a panicking provider is contained.
	p.published = []protocol.BrowserHARArtifact{{Path: "/d.har", Generation: "g4"}}
	handOffBrowserHARArtifacts(p, &concurrentCaptureSink{})
	handOffBrowserHARArtifacts(&harUnitProtocol{panics: true}, sink)
	if len(sink.recorded) != 2 {
		t.Fatal("a non-HAR sink or panicking provider changed the archive")
	}
}

func TestFinalTeardownHandsBrowserHARArtifactsToThePublishedSink(t *testing.T) {
	mc := NewMetricsCollector(16)
	p := &harUnitProtocol{published: []protocol.BrowserHARArtifact{{Path: "/a.har", Generation: "g1"}}}
	entry := mc.registerMessagingProgress(p, "group", nil)
	if entry == nil {
		t.Fatal("a HAR provider got no lifecycle registration")
	}
	sink := &harUnitSink{}
	entry.publishBrowser(sink, "", nil)
	_ = runProtocolTeardownWithMessagingProgress(p, entry)
	if len(sink.recorded) != 1 || sink.recorded[0].Generation != "g1" {
		t.Fatalf("final teardown handoff %+v", sink.recorded)
	}
}

type interactionUnitProtocol struct {
	harUnitProtocol
	evidence []*protocol.BrowserInteractionEvidence
}

func (p *interactionUnitProtocol) TakeBrowserInteractionEvidence() []*protocol.BrowserInteractionEvidence {
	out := p.evidence
	p.evidence = nil
	return out
}

type interactionUnitSink struct {
	harUnitSink
	interactions []*protocol.BrowserInteractionEvidence
}

func (s *interactionUnitSink) RecordBrowserInteractionEvidence(evidence *protocol.BrowserInteractionEvidence) error {
	s.interactions = append(s.interactions, evidence)
	return nil
}

func TestBrowserSessionEvidenceHandsOverInteractionsAndHARs(t *testing.T) {
	gen := strings.Repeat("a", 32)
	p := &interactionUnitProtocol{harUnitProtocol: harUnitProtocol{published: []protocol.BrowserHARArtifact{{Path: "/a.har", Generation: gen}}},
		evidence: []*protocol.BrowserInteractionEvidence{{Version: protocol.BrowserInteractionVersion, SourceGeneration: gen}, nil}}
	sink := &interactionUnitSink{}
	handOffBrowserSessionEvidence(p, sink)
	handOffBrowserSessionEvidence(p, sink)
	if len(sink.recorded) != 1 || len(sink.interactions) != 1 || sink.interactions[0].SourceGeneration != gen {
		t.Fatalf("handoff HARs %d interactions %d", len(sink.recorded), len(sink.interactions))
	}
}
