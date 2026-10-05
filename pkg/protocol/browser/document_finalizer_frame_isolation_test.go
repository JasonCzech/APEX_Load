package browser

import (
	"context"
	"errors"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
	playwright "github.com/mxschmitt/playwright-go"
)

type finalizerFrameIsolationFixture struct {
	playwright.Frame
	evaluate func() error
}

func (f *finalizerFrameIsolationFixture) Evaluate(string, ...any) (any, error) {
	return nil, f.evaluate()
}

func TestBrowserDocumentFinalizerFrameErrorIsolationInBothOrders(t *testing.T) {
	for _, staleFirst := range []bool{true, false} {
		name := "live_then_stale"
		if staleFirst {
			name = "stale_then_live"
		}
		t.Run(name, func(t *testing.T) {
			page := &browserGenerationUntouchedPage{}
			liveDoc := &browserDocumentState{page: page, observations: map[string]protocol.BrowserDocumentObservation{"inp": {State: "provisional"}}}
			stale := &finalizerFrameIsolationFixture{evaluate: func() error { return errors.New("synthetic detached old frame") }}
			accepted := false
			live := &finalizerFrameIsolationFixture{evaluate: func() error {
				if !liveDoc.ended {
					accepted = true
					liveDoc.observations["inp"] = protocol.BrowserDocumentObservation{State: "final", Boundary: "graceful_end", UnavailableReason: "no_interaction"}
				}
				return nil
			}}
			liveDoc.frame = live
			staleDoc := &browserDocumentState{frame: stale, page: page, observations: map[string]protocol.BrowserDocumentObservation{"inp": {State: "provisional"}}}
			b := &Browser{evidence: browserEvidenceState{documents: map[string]*browserDocumentState{"old": staleDoc, "live": liveDoc}}}
			frames := []playwright.Frame{live, stale}
			if staleFirst {
				frames = []playwright.Frame{stale, live}
			}
			// An explicitly ordered batch proves either order without relying on
			// Go map iteration or a probability of reproducing the stale first.
			b.finalizeDocumentFrameBatch(context.Background(), frames, "synthetic")
			b.censorDocumentEvidence(nil, "context_closed")
			if !accepted || liveDoc.observations["inp"].State != "final" || liveDoc.observations["inp"].UnavailableReason != "no_interaction" {
				t.Fatal("one stale frame erased its live sibling's valid final receipt")
			}
			if staleDoc.observations["inp"].State != "censored" {
				t.Fatal("unresolved stale document lost the final shared boundary")
			}
		})
	}
}
