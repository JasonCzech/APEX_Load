package browser

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
	playwright "github.com/mxschmitt/playwright-go"
)

type harFinalizationCloseGate struct {
	playwright.BrowserContext
	frame *browserFinalizationHeldFrame
	gate  chan struct{}
	calls atomic.Int32
}

func (c *harFinalizationCloseGate) Close(...playwright.BrowserContextCloseOptions) error {
	c.calls.Add(1)
	<-c.gate
	select {
	case <-c.frame.release:
	default:
		close(c.frame.release)
	}
	<-c.frame.exited
	return nil
}

func TestHAROwnershipDocumentFinalizerDeadlineRetainsSameCloseOwner(t *testing.T) {
	o := fixtureHAROwner(t, t.TempDir())
	writeOwnedHARFixture(t, o)
	frame := &browserFinalizationHeldFrame{entered: make(chan struct{}), release: make(chan struct{}), exited: make(chan struct{})}
	native := &harFinalizationCloseGate{frame: frame, gate: make(chan struct{})}
	o.beginWriter()
	o.bindWriter(func() error { return native.Close() })
	b := &Browser{harOwner: o, harWorkingPath: o.path, harCapturePolicy: HARCaptureAlways, pwContext: native,
		evidence: browserEvidenceState{finalizer: "synthetic", lifecycleContext: context.Background(), documents: map[string]*browserDocumentState{"held": {frame: frame, page: &browserGenerationUntouchedPage{}, observations: map[string]protocol.BrowserDocumentObservation{}}}}}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- b.Teardown(ctx) }()
	<-frame.entered
	select {
	case err := <-result:
		if !errors.Is(err, errHAROwnershipUnproven) {
			t.Error("document worker deadline was hidden", err)
		}
	case <-time.After(150 * time.Millisecond):
		t.Error("document finalizer blocked Teardown before owner deadline")
	}
	if o.retiredSuccessfully() {
		t.Error("blocked document/close released ownership")
	}
	close(native.gate)
	if _, err := o.waitFinish(context.Background()); err != nil {
		t.Error("late exact finalizer join failed", err)
	}
	if native.calls.Load() != 1 {
		t.Error("document cancellation bypassed close-once owner", native.calls.Load())
	}
}

type harDocumentFinalizeFrame struct {
	playwright.Frame
	evaluate func() error
}

func (f *harDocumentFinalizeFrame) Evaluate(string, ...any) (any, error) { return nil, f.evaluate() }

func TestHAROwnershipStaleFrameFailureCannotCensorLiveSibling(t *testing.T) {
	for attempt := 0; attempt < 32; attempt++ {
		page := &browserGenerationUntouchedPage{}
		stale := &harDocumentFinalizeFrame{evaluate: func() error { return errors.New("synthetic detached old frame") }}
		liveDoc := &browserDocumentState{page: page, observations: map[string]protocol.BrowserDocumentObservation{"inp": {State: "provisional"}}}
		accepted := false
		live := &harDocumentFinalizeFrame{evaluate: func() error {
			if !liveDoc.ended {
				accepted = true
				liveDoc.observations["inp"] = protocol.BrowserDocumentObservation{State: "final", Boundary: "graceful_end", UnavailableReason: "no_interaction"}
			}
			return nil
		}}
		liveDoc.frame = live
		b := &Browser{pwContext: &cdpTestContext{}, evidence: browserEvidenceState{finalizer: "synthetic", documents: map[string]*browserDocumentState{
			"a-stale": {frame: stale, page: page, observations: map[string]protocol.BrowserDocumentObservation{"inp": {State: "provisional"}}},
			"z-live":  liveDoc,
		}}}
		b.finalizeDocumentEvidence(context.Background())
		if !accepted || liveDoc.observations["inp"].State != "final" {
			t.Fatal("one stale native frame erased the live sibling's valid final receipt")
		}
	}
}

// Synthetic contexts intentionally expose no additional current native pages.
func (c *harFinalizationCloseGate) Pages() []playwright.Page { return nil }
func (c *cdpTestContext) Pages() []playwright.Page           { return nil }
