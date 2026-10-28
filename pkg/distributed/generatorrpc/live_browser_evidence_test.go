package generatorrpc

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
)

type captureBrowserEvidenceSink struct {
	results          []*protocol.Result
	documents        []*protocol.BrowserDocumentEvidence
	interactions     []*protocol.BrowserInteractionEvidence
	liveDocuments    []*protocol.BrowserDocumentEvidence
	liveInteractions []*protocol.BrowserInteractionEvidence
}

func (s *captureBrowserEvidenceSink) RecordResult(res *protocol.Result) error {
	s.results = append(s.results, res)
	return nil
}

func (s *captureBrowserEvidenceSink) RecordBrowserDocumentEvidence(doc *protocol.BrowserDocumentEvidence) error {
	s.documents = append(s.documents, doc)
	return nil
}

func (s *captureBrowserEvidenceSink) RecordBrowserInteractionEvidence(ev *protocol.BrowserInteractionEvidence) error {
	s.interactions = append(s.interactions, ev)
	return nil
}

func (s *captureBrowserEvidenceSink) RecordLiveBrowserDocumentEvidence(doc *protocol.BrowserDocumentEvidence) error {
	s.liveDocuments = append(s.liveDocuments, doc)
	return nil
}

func (s *captureBrowserEvidenceSink) RecordLiveBrowserInteractionEvidence(ev *protocol.BrowserInteractionEvidence) error {
	s.liveInteractions = append(s.liveInteractions, ev)
	return nil
}

func TestTransactionBufferLiveBrowserEvidenceAndProtoRoundTrip(t *testing.T) {
	primary := &captureBrowserEvidenceSink{}
	txBuf := &transactionBuffer{}
	sink := combineSinks(primary, txBuf)

	docSink, ok := sink.(engine.BrowserDocumentEvidenceSink)
	if !ok {
		t.Fatal("combinedSink must implement BrowserDocumentEvidenceSink")
	}
	intSink, ok := sink.(engine.BrowserInteractionEvidenceSink)
	if !ok {
		t.Fatal("combinedSink must implement BrowserInteractionEvidenceSink")
	}
	liveDocSink, ok := sink.(engine.LiveBrowserDocumentEvidenceSink)
	if !ok {
		t.Fatal("combinedSink must implement LiveBrowserDocumentEvidenceSink")
	}
	liveIntSink, ok := sink.(engine.LiveBrowserInteractionEvidenceSink)
	if !ok {
		t.Fatal("combinedSink must implement LiveBrowserInteractionEvidenceSink")
	}

	now := time.Date(2026, 10, 27, 15, 0, 0, 0, time.UTC)
	lcp := 1420.0
	gen := "fedcba9876543210fedcba9876543210"
	doc := &protocol.BrowserDocumentEvidence{
		Version:          protocol.BrowserEvidenceVersion,
		SourceGeneration: gen,
		Observations: []protocol.BrowserDocumentObservation{{
			Version:          protocol.BrowserEvidenceVersion,
			SourceGeneration: gen,
			DocumentID:       gen + "-document-1",
			LifecycleID:      gen + "-lifecycle-1",
			FrameID:          gen + "-frame-1",
			NavigationID:     gen + "-navigation-1",
			MeasurementID:    gen + "-document-1/" + gen + "-lifecycle-1/lcp",
			Revision:         1,
			Metric:           "lcp",
			Algorithm:        protocol.BrowserVitalsAlgorithm,
			AlgorithmSHA256:  protocol.BrowserVitalsAlgorithmSHA256,
			Unit:             "ms",
			Engine:           "chromium",
			DeviceProfile:    "desktop",
			FrameScope:       "main_frame",
			LifecyclePhase:   "navigation",
			State:            "final",
			Boundary:         "graceful_end",
			WindowStart:      now.Add(-time.Second),
			WindowEnd:        now,
			CapturedAt:       now,
			Available:        true,
			Qualified:        false,
			Value:            &lcp,
		}},
	}
	if err := docSink.RecordBrowserDocumentEvidence(doc); err != nil {
		t.Fatalf("RecordBrowserDocumentEvidence: %v", err)
	}
	if err := liveDocSink.RecordLiveBrowserDocumentEvidence(doc); err != nil {
		t.Fatalf("RecordLiveBrowserDocumentEvidence: %v", err)
	}

	interaction := &protocol.BrowserInteractionEvidence{
		Version:          protocol.BrowserInteractionVersion,
		SourceGeneration: gen,
		Interactions: []protocol.BrowserInteraction{{
			Version:           protocol.BrowserInteractionVersion,
			SourceGeneration:  gen,
			InteractionID:     gen + "-interaction-1",
			ActionID:          gen + "-action-1",
			DocumentID:        gen + "-document-1",
			FrameScope:        "main_frame",
			Kind:              "tap",
			PointerType:       "touch",
			Points:            []protocol.BrowserInteractionPoint{{X: 100, Y: 200}},
			ViewportWidth:     400,
			ViewportHeight:    800,
			DeviceScaleFactor: 2,
			LayoutRevision:    "00000000000000aa",
			Route:             "/checkout",
			Outcome:           protocol.BrowserInteractionOutcome{Observed: protocol.InteractionOutcomeNotDeclared},
			Provenance:        protocol.InteractionProvenanceScripted,
			Success:           true,
			CapturedAt:        now,
		}},
	}
	if err := intSink.RecordBrowserInteractionEvidence(interaction); err != nil {
		t.Fatalf("RecordBrowserInteractionEvidence: %v", err)
	}
	if err := liveIntSink.RecordLiveBrowserInteractionEvidence(interaction); err != nil {
		t.Fatalf("RecordLiveBrowserInteractionEvidence: %v", err)
	}

	step := protocol.NewBrowserOriginalAuthoredStep(protocol.BrowserAuthoredDefinition{
		Version:         1,
		ScriptSHA256:    "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		StructureSHA256: "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210",
	}, "actions[0].control.steps[1]")
	txRes := &protocol.Result{
		StartTime:           now,
		ActionName:          "Checkout",
		Group:               "web",
		Phase:               "actions",
		VUserID:             1,
		VUserLifetime:       1,
		Iteration:           1,
		BusinessTransaction: true,
		StatusCode:          200,
		Success:             true,
		AuthoredStep:        step,
	}
	if err := sink.RecordResult(txRes); err != nil {
		t.Fatalf("RecordResult(tx): %v", err)
	}

	iterRes := &protocol.Result{
		StartTime:     now.Add(100 * time.Millisecond),
		ActionName:    "LastStep",
		Group:         "web",
		Phase:         "actions",
		VUserID:       1,
		VUserLifetime: 1,
		Iteration:     1,
		EndsIteration: true,
		StatusCode:    200,
		Success:       true,
	}
	if err := sink.RecordResult(iterRes); err != nil {
		t.Fatalf("RecordResult(iter): %v", err)
	}

	if len(primary.documents) != 1 || len(primary.interactions) != 1 || len(primary.results) != 2 {
		t.Fatalf("primary sink counts = docs:%d ints:%d res:%d", len(primary.documents), len(primary.interactions), len(primary.results))
	}

	_, aggWindow, _ := txBuf.DrainWindow()
	if aggWindow == nil {
		t.Fatal("expected non-nil aggregate window")
	}
	set := aggWindow.finalize(1000)
	if len(set.LiveBrowserDocumentsJSON) != 1 {
		t.Fatalf("LiveBrowserDocumentsJSON = %d, want 1", len(set.LiveBrowserDocumentsJSON))
	}
	if len(set.LiveBrowserInteractionsJSON) != 1 {
		t.Fatalf("LiveBrowserInteractionsJSON = %d, want 1", len(set.LiveBrowserInteractionsJSON))
	}
	if len(set.LiveJourneyEvents) != 2 {
		t.Fatalf("LiveJourneyEvents = %d, want 2", len(set.LiveJourneyEvents))
	}

	merged := mergeMetricsAggregateSets(map[string]metricsAggregateSet{
		"web": set,
	})
	roundTrippedEvents := liveJourneyEventsFromProto(liveJourneyEventsToProto(merged.LiveJourneyEvents))
	roundTrippedTrunc := liveBrowserTruncationFromProto(liveBrowserTruncationToProto(merged.LiveBrowserTruncation))
	if len(merged.LiveBrowserDocumentsJSON) != 1 || len(merged.LiveBrowserInteractionsJSON) != 1 || len(roundTrippedEvents) != 2 || roundTrippedTrunc != nil {
		t.Fatalf("roundTripped counts = docs:%d ints:%d events:%d trunc:%+v",
			len(merged.LiveBrowserDocumentsJSON), len(merged.LiveBrowserInteractionsJSON), len(roundTrippedEvents), roundTrippedTrunc)
	}
	gotEv := roundTrippedEvents[0]
	if !gotEv.BusinessTransaction || gotEv.AuthoredStep == nil || gotEv.AuthoredStep.StepPath != "actions[0].control.steps[1]" {
		t.Fatalf("roundTripped journey event[0] = %+v, step = %+v", gotEv, gotEv.AuthoredStep)
	}
	if !roundTrippedEvents[1].EndsIteration {
		t.Fatalf("roundTripped journey event[1] expected EndsIteration=true, got %+v", roundTrippedEvents[1])
	}

	var decodedInteraction protocol.BrowserInteractionEvidence
	if err := json.Unmarshal([]byte(merged.LiveBrowserInteractionsJSON[0]), &decodedInteraction); err != nil {
		t.Fatalf("decode roundTripped interaction: %v", err)
	}
	if len(decodedInteraction.Interactions) != 1 || decodedInteraction.Interactions[0].InteractionID != gen+"-interaction-1" {
		t.Fatalf("decodedInteraction = %+v", decodedInteraction)
	}
}

func TestTransactionBufferLiveBrowserEvidenceTruncationBounds(t *testing.T) {
	txBuf := &transactionBuffer{}
	now := time.Date(2026, 10, 27, 15, 0, 0, 0, time.UTC)
	lcp := 1200.0
	gen := "fedcba9876543210fedcba9876543210"
	doc := &protocol.BrowserDocumentEvidence{
		Version:          protocol.BrowserEvidenceVersion,
		SourceGeneration: gen,
		Observations: []protocol.BrowserDocumentObservation{{
			Version:          protocol.BrowserEvidenceVersion,
			SourceGeneration: gen,
			DocumentID:       gen + "-document-1",
			LifecycleID:      gen + "-lifecycle-1",
			FrameID:          gen + "-frame-1",
			NavigationID:     gen + "-navigation-1",
			MeasurementID:    gen + "-document-1/" + gen + "-lifecycle-1/lcp",
			Revision:         1,
			Metric:           "lcp",
			Algorithm:        protocol.BrowserVitalsAlgorithm,
			AlgorithmSHA256:  protocol.BrowserVitalsAlgorithmSHA256,
			Unit:             "ms",
			Engine:           "chromium",
			DeviceProfile:    "desktop",
			FrameScope:       "main_frame",
			LifecyclePhase:   "navigation",
			State:            "final",
			Boundary:         "graceful_end",
			WindowStart:      now.Add(-time.Second),
			WindowEnd:        now,
			CapturedAt:       now,
			Available:        true,
			Value:            &lcp,
		}},
	}
	for i := 0; i < maxWindowLiveBrowserDocumentRecords+5; i++ {
		_ = txBuf.RecordLiveBrowserDocumentEvidence(doc)
	}
	ev := &protocol.BrowserInteractionEvidence{
		Version:          protocol.BrowserInteractionVersion,
		SourceGeneration: gen,
		Interactions: []protocol.BrowserInteraction{{
			Version:           protocol.BrowserInteractionVersion,
			SourceGeneration:  gen,
			InteractionID:     gen + "-interaction-1",
			ActionID:          gen + "-action-1",
			DocumentID:        gen + "-document-1",
			FrameScope:        "main_frame",
			Kind:              "tap",
			PointerType:       "touch",
			Points:            []protocol.BrowserInteractionPoint{{X: 100, Y: 200}},
			ViewportWidth:     400,
			ViewportHeight:    800,
			DeviceScaleFactor: 2,
			LayoutRevision:    "00000000000000aa",
			Route:             "/checkout",
			Outcome:           protocol.BrowserInteractionOutcome{Observed: protocol.InteractionOutcomeNotDeclared},
			Provenance:        protocol.InteractionProvenanceScripted,
			Success:           true,
			CapturedAt:        now,
		}},
	}
	for i := 0; i < maxWindowLiveBrowserInteractionRecords+3; i++ {
		_ = txBuf.RecordLiveBrowserInteractionEvidence(ev)
	}
	res := &protocol.Result{
		StartTime:     time.Now().UTC(),
		ActionName:    "Step",
		Group:         "web",
		Phase:         "actions",
		VUserID:       1,
		VUserLifetime: 1,
		Iteration:     1,
		Success:       true,
	}
	for i := 0; i < maxWindowLiveJourneyEvents+7; i++ {
		_ = txBuf.RecordResult(res)
	}

	_, aggWindow, _ := txBuf.DrainWindow()
	set := aggWindow.finalize(1000)
	if set.LiveBrowserTruncation == nil {
		t.Fatal("expected LiveBrowserTruncation to be populated on overflow")
	}
	if set.LiveBrowserTruncation.DroppedDocuments != 5 {
		t.Fatalf("DroppedDocuments = %d, want 5", set.LiveBrowserTruncation.DroppedDocuments)
	}
	if set.LiveBrowserTruncation.DroppedInteractions != 3 {
		t.Fatalf("DroppedInteractions = %d, want 3", set.LiveBrowserTruncation.DroppedInteractions)
	}
	if set.LiveBrowserTruncation.DroppedJourneyEvents != 7 {
		t.Fatalf("DroppedJourneyEvents = %d, want 7", set.LiveBrowserTruncation.DroppedJourneyEvents)
	}
}
