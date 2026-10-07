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
	handOffBrowserSessionEvidence(p, sink, nil)
	handOffBrowserSessionEvidence(p, sink, nil)
	if len(sink.recorded) != 1 || len(sink.interactions) != 1 || sink.interactions[0].SourceGeneration != gen {
		t.Fatalf("handoff HARs %d interactions %d", len(sink.recorded), len(sink.interactions))
	}
}

// A runtime secret in an interaction's route (a signed or magic-link path) is
// redacted at handoff, as every other run evidence path is, and the route
// stays a path.
func TestBrowserInteractionRoutesAreScrubbedAtHandoff(t *testing.T) {
	gen := strings.Repeat("b", 32)
	secret := "tok3nVa1ueQx9ZrT5mW2pL"
	config := WithSecretValueRedaction(nil, []string{secret})
	p := &interactionUnitProtocol{evidence: []*protocol.BrowserInteractionEvidence{{Version: protocol.BrowserInteractionVersion, SourceGeneration: gen,
		Interactions: []protocol.BrowserInteraction{{Version: protocol.BrowserInteractionVersion, SourceGeneration: gen, Kind: "click", Route: "/share/" + secret}}}}}
	sink := &interactionUnitSink{}
	handOffBrowserInteractions(p, sink, config)
	if len(sink.interactions) != 1 {
		t.Fatalf("interactions %d", len(sink.interactions))
	}
	route := sink.interactions[0].Interactions[0].Route
	if strings.Contains(route, secret) || !strings.HasPrefix(route, "/share/") {
		t.Fatalf("route %q", route)
	}
}
