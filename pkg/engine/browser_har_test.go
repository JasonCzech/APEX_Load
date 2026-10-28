package engine

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

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

// A scrubbed route keeps the interaction valid, so the sink's validation
// never drops the whole record: a route the scrubber would lengthen past the
// route bound is recorded as none.
func TestScrubbedInteractionRoutesKeepRecordsValid(t *testing.T) {
	gen := strings.Repeat("c", 32)
	secret := "q7Zx"
	config := WithSecretValueRedaction(nil, []string{secret})
	marker := scrubSensitiveTextWithConfig("/"+secret, config)
	valid := func(route, n string) protocol.BrowserInteraction {
		return protocol.BrowserInteraction{Version: protocol.BrowserInteractionVersion, SourceGeneration: gen, InteractionID: gen + "-i" + n, ActionID: gen + "-a" + n, DocumentID: gen + "-idoc-" + strings.Repeat("b", 32), FrameScope: "main_frame", Kind: "click", PointerType: "mouse", Points: []protocol.BrowserInteractionPoint{{X: 140, Y: 170}},
			ViewportWidth: 1000, ViewportHeight: 700, DeviceScaleFactor: 1, LayoutRevision: "0123456789abcdef", Route: route,
			Outcome: protocol.BrowserInteractionOutcome{Observed: protocol.InteractionOutcomeNotDeclared}, Provenance: protocol.InteractionProvenanceScripted, Success: true, CapturedAt: time.Now().UTC()}
	}
	short := valid("/share/"+secret, "1")
	if !short.Valid() {
		t.Fatal("fixture interaction is invalid")
	}
	// Fill the route to the bound so any lengthening crosses it.
	long := valid("/"+strings.Repeat("a", 512-2-len(secret))+"/"+secret, "2")
	if len(long.Route) != 512 || !long.Valid() {
		t.Fatalf("long fixture route %d valid %v", len(long.Route), long.Valid())
	}
	p := &interactionUnitProtocol{evidence: []*protocol.BrowserInteractionEvidence{{Version: protocol.BrowserInteractionVersion, SourceGeneration: gen, Interactions: []protocol.BrowserInteraction{short, long}}}}
	sink := &interactionUnitSink{}
	handOffBrowserInteractions(p, sink, config)
	if len(sink.interactions) != 1 || len(sink.interactions[0].Interactions) != 2 {
		t.Fatalf("handoff %+v", sink.interactions)
	}
	for i, interaction := range sink.interactions[0].Interactions {
		if strings.Contains(interaction.Route, secret) || !interaction.Valid() {
			t.Fatalf("interaction %d route %q valid %v", i, interaction.Route, interaction.Valid())
		}
	}
	if sink.interactions[0].Clone() == nil {
		t.Fatal("the scrubbed record does not validate")
	}
	if got := sink.interactions[0].Interactions[1].Route; len(marker)-1 > len(secret) && got != "" {
		t.Fatalf("a route lengthened past the bound was kept: %d bytes", len(got))
	}
}

// A near-full record whose routes the run's redaction lengthens is split at
// handoff, so the sink keeps every interaction rather than refusing the
// record as too large.
func TestScrubLengthenedInteractionRecordIsSplitAtHandoff(t *testing.T) {
	gen := strings.Repeat("d", 32)
	secret := "q7Zx"
	config := WithSecretValueRedaction(nil, []string{secret})
	if marker := scrubSensitiveTextWithConfig("/"+secret, config); len(marker)-1 <= len(secret) {
		t.Skip("the redaction marker does not lengthen a route")
	}
	record := &protocol.BrowserInteractionEvidence{Version: protocol.BrowserInteractionVersion, SourceGeneration: gen}
	for n := 0; ; n++ {
		id := strconv.Itoa(n)
		interaction := protocol.BrowserInteraction{Version: protocol.BrowserInteractionVersion, SourceGeneration: gen, InteractionID: gen + "-i" + id, ActionID: gen + "-a" + id, DocumentID: gen + "-idoc-" + strings.Repeat("b", 32), FrameScope: "main_frame", Kind: "click", PointerType: "mouse", Points: []protocol.BrowserInteractionPoint{{X: 140, Y: 170}},
			ViewportWidth: 1000, ViewportHeight: 700, DeviceScaleFactor: 1, LayoutRevision: "0123456789abcdef", Route: "/" + strings.Repeat(secret+"/", 40),
			Outcome: protocol.BrowserInteractionOutcome{Observed: protocol.InteractionOutcomeNotDeclared}, Provenance: protocol.InteractionProvenanceScripted, Success: true, CapturedAt: time.Now().UTC()}
		record.Interactions = append(record.Interactions, interaction)
		raw, _ := json.Marshal(record)
		if len(raw) > protocol.MaxBrowserInteractionEvidenceBytes-2048 {
			break
		}
	}
	if record.Clone() == nil {
		t.Fatal("fixture record is invalid")
	}
	total := len(record.Interactions)
	sink := &interactionUnitSink{}
	handOffBrowserInteractions(&interactionUnitProtocol{evidence: []*protocol.BrowserInteractionEvidence{record}}, sink, config)
	if len(sink.interactions) < 2 {
		t.Fatalf("records %d: the lengthened record was not split", len(sink.interactions))
	}
	kept := 0
	for _, part := range sink.interactions {
		raw, _ := json.Marshal(part)
		if _, err := protocol.DecodeBrowserInteractionEvidence(raw); err != nil {
			t.Fatalf("a handed-off record does not decode (%d bytes): %v", len(raw), err)
		}
		kept += len(part.Interactions)
	}
	if kept != total {
		t.Fatalf("kept %d of %d interactions", kept, total)
	}
}

type liveInteractionUnitSink struct {
	interactionUnitSink
	live []*protocol.BrowserInteractionEvidence
}

func (s *liveInteractionUnitSink) RecordLiveBrowserInteractionEvidence(evidence *protocol.BrowserInteractionEvidence) error {
	s.live = append(s.live, evidence)
	return nil
}

func TestLiveBrowserInteractionsUseCurrentRuntimeSecretMatcherAndRespectImportedCredentialPolicy(t *testing.T) {
	const (
		variableName  = "opaque_handle"
		importedValue = "imp-cred-4471"
		runtimeSecret = "runtime-secret-after-init-9921"
	)
	if scrubSensitiveTextWithConfig(importedValue, nil) != importedValue {
		t.Fatalf("canary %q is removed by an origin-independent scrubber", importedValue)
	}
	gen := strings.Repeat("e", 32)

	for _, mode := range runEvidenceRedactionModes {
		t.Run(mode.name, func(t *testing.T) {
			mc := NewMetricsCollector(16)
			p := &interactionUnitProtocol{}
			initialConfig := &RedactionConfig{}
			entry := mc.registerMessagingProgressWithRedaction(p, "group", nil, func() *RedactionConfig { return initialConfig })
			if entry == nil {
				t.Fatal("expected messagingProgressRegistration for interaction provider")
			}
			sink := &liveInteractionUnitSink{}
			entry.publishBrowser(sink, "us-east", func() *RedactionConfig { return initialConfig })

			script := setRunEvidenceRedaction(importedCredentialVariableScript(variableName), mode.redact)
			vu := runShapedVUser(t, script)
			vu.Proto = p
			vu.Metrics = mc
			vu.RedactionConfig = initialConfig
			vu.Variables[variableName] = importedValue
			vu.bindRuntimeTextRedactor()

			if err := vu.refreshImportedCredentialRuntimeValues(); err != nil {
				t.Fatalf("refreshImportedCredentialRuntimeValues: %v", err)
			}
			if err := vu.observeExtractionClassification(&protocol.Result{
				Extractions: map[string]string{"access_token": runtimeSecret},
			}); err != nil {
				t.Fatalf("observeExtractionClassification: %v", err)
			}

			p.evidence = []*protocol.BrowserInteractionEvidence{{
				Version:          protocol.BrowserInteractionVersion,
				SourceGeneration: gen,
				Interactions: []protocol.BrowserInteraction{{
					Version:           protocol.BrowserInteractionVersion,
					SourceGeneration:  gen,
					InteractionID:     gen + "-i-1",
					ActionID:          gen + "-a-1",
					DocumentID:        gen + "-idoc-" + strings.Repeat("b", 32),
					FrameScope:        "main_frame",
					Kind:              "click",
					PointerType:       "mouse",
					Points:            []protocol.BrowserInteractionPoint{{X: 100, Y: 100}},
					ViewportWidth:     1000,
					ViewportHeight:    700,
					DeviceScaleFactor: 1,
					LayoutRevision:    "0123456789abcdef",
					Route:             "/orders/" + runtimeSecret + "/" + importedValue,
					Outcome:           protocol.BrowserInteractionOutcome{Observed: protocol.InteractionOutcomeNotDeclared},
					Provenance:        protocol.InteractionProvenanceScripted,
					Success:           true,
					CapturedAt:        time.Now().UTC(),
				}},
			}}

			mc.browserDocuments.sample(false)
			if len(sink.live) != 1 || len(sink.live[0].Interactions) != 1 {
				t.Fatalf("expected 1 live interaction record, got %+v", sink.live)
			}
			route := sink.live[0].Interactions[0].Route
			if strings.Contains(route, runtimeSecret) {
				t.Fatalf("platform-managed runtime secret %q leaked into live interaction route %q", runtimeSecret, route)
			}
			assertScriptSourcedRunEvidence(t, "live browser interaction route", route, importedValue, mode.redact)

			// Verify second iteration after resetProtocolForIteration re-binds harRedaction.
			const iter2Secret = "runtime-secret-iter2-7742"
			vu.resetProtocolForIteration(context.Background())
			if err := vu.refreshImportedCredentialRuntimeValues(); err != nil {
				t.Fatalf("refreshImportedCredentialRuntimeValues iter2: %v", err)
			}
			if err := vu.observeExtractionClassification(&protocol.Result{
				Extractions: map[string]string{"refresh_token": iter2Secret},
			}); err != nil {
				t.Fatalf("observeExtractionClassification iter2: %v", err)
			}
			p.evidence = []*protocol.BrowserInteractionEvidence{{
				Version:          protocol.BrowserInteractionVersion,
				SourceGeneration: gen,
				Interactions: []protocol.BrowserInteraction{{
					Version:           protocol.BrowserInteractionVersion,
					SourceGeneration:  gen,
					InteractionID:     gen + "-i-2",
					ActionID:          gen + "-a-1",
					DocumentID:        gen + "-idoc-" + strings.Repeat("b", 32),
					FrameScope:        "main_frame",
					Kind:              "click",
					PointerType:       "mouse",
					Points:            []protocol.BrowserInteractionPoint{{X: 120, Y: 140}},
					ViewportWidth:     1000,
					ViewportHeight:    700,
					DeviceScaleFactor: 1,
					LayoutRevision:    "0123456789abcdef",
					Route:             "/orders/" + iter2Secret + "/" + importedValue,
					Outcome:           protocol.BrowserInteractionOutcome{Observed: protocol.InteractionOutcomeNotDeclared},
					Provenance:        protocol.InteractionProvenanceScripted,
					Success:           true,
					CapturedAt:        time.Now().UTC(),
				}},
			}}
			mc.browserDocuments.sample(false)
			if len(sink.live) != 2 || len(sink.live[1].Interactions) != 1 {
				t.Fatalf("expected 2nd live interaction record after resetProtocolForIteration, got %+v", sink.live)
			}
			route2 := sink.live[1].Interactions[0].Route
			if strings.Contains(route2, iter2Secret) {
				t.Fatalf("iter2 runtime secret %q leaked into live interaction route %q", iter2Secret, route2)
			}
			assertScriptSourcedRunEvidence(t, "live browser interaction route iter2", route2, importedValue, mode.redact)
		})
	}
}
