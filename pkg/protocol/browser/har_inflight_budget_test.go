package browser

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
	playwright "github.com/mxschmitt/playwright-go"
)

type harInFlightRequest struct {
	playwright.Request
	id int
}

func (*harInFlightRequest) Frame() playwright.Frame            { return nil }
func (*harInFlightRequest) IsNavigationRequest() bool          { return false }
func (*harInFlightRequest) RedirectedFrom() playwright.Request { return nil }

type harInFlightContext struct {
	playwright.BrowserContext
	closed atomic.Int32
}

func (c *harInFlightContext) Close(...playwright.BrowserContextCloseOptions) error {
	c.closed.Add(1)
	return nil
}

func inflightBrowser(t *testing.T, capturing bool) *Browser {
	t.Helper()
	b := &Browser{}
	if err := b.resetBrowserEvidence("test"); err != nil {
		t.Fatal(err)
	}
	if capturing {
		b.harLimitExceeded = make(chan struct{})
	}
	return b
}
func assertHARInflightNotExceeded(t *testing.T, b *Browser) {
	t.Helper()
	select {
	case <-b.harLimitExceeded:
		t.Fatal("HAR closed below its in-flight limit")
	default:
	}
	if b.harInFlightExceeded {
		t.Fatal("false in-flight exhaustion")
	}
}
func TestHARInFlightBudgetUsesOutstandingRequests(t *testing.T) {
	b := inflightBrowser(t, true)
	var requests []*harInFlightRequest
	for i := 0; i < maxRuntimeHARInFlightRequests; i++ {
		r := &harInFlightRequest{id: i}
		requests = append(requests, r)
		b.beginRequestEvidence(r)
	}
	assertHARInflightNotExceeded(t, b)
	for _, r := range requests[:100] {
		b.completeRequestEvidence(r, true)
		b.completeRequestEvidence(r, true)
	}
	if b.evidence.inFlight != maxRuntimeHARInFlightRequests-100 {
		t.Fatal("completion did not release exactly once", b.evidence.inFlight)
	}
	for i := 0; i < 100; i++ {
		b.beginRequestEvidence(&harInFlightRequest{id: 1000 + i})
	}
	assertHARInflightNotExceeded(t, b)
	b.beginRequestEvidence(requests[100]) // A duplicate native callback consumes no slot.
	assertHARInflightNotExceeded(t, b)
	b.beginRequestEvidence(&harInFlightRequest{id: 2000}, "old-generation")
	assertHARInflightNotExceeded(t, b)
	b.beginRequestEvidence(&harInFlightRequest{id: 2001})
	select {
	case <-b.harLimitExceeded:
	default:
		t.Fatal("overflow did not reach native close monitor")
	}
	if !b.harInFlightExceeded || !b.evidence.incomplete || b.evidence.inFlight != maxRuntimeHARInFlightRequests+1 {
		t.Fatal("overflow evidence lost")
	}
}
func TestHARInFlightBudgetDoesNotCapCompletedOrUnrecordedRequests(t *testing.T) {
	b := inflightBrowser(t, true)
	for i := 0; i < browserRequestIdentityLimit*3; i++ {
		r := &harInFlightRequest{id: i}
		b.beginRequestEvidence(r)
		b.completeRequestEvidence(r, true)
	}
	assertHARInflightNotExceeded(t, b)
	if b.evidence.inFlight != 0 || b.evidence.issued != browserRequestIdentityLimit*3 {
		t.Fatal("sequential requests counted as concurrent")
	}
	b = inflightBrowser(t, false)
	for i := 0; i < maxRuntimeHARInFlightRequests+10; i++ {
		b.beginRequestEvidence(&harInFlightRequest{id: i})
	}
	if b.harInFlightExceeded || b.resourceLimit != "" {
		t.Fatal("optional HAR budget affected a non-recording session")
	}
}
func TestHARInFlightOverflowClosesAndCensorsThroughMonitor(t *testing.T) {
	budget := useTestEvidenceBudget(t, 64<<30, noUsage, 0)
	b := inflightBrowser(t, false)
	native := &harInFlightContext{}
	b.pwContext = native
	path := filepath.Join(t.TempDir(), "capture.har.tmp")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	b.harWorkingPath = path
	b.evidence.documents["pending"] = &browserDocumentState{observations: map[string]protocol.BrowserDocumentObservation{"inp": {State: "provisional", Available: true, Qualified: true}}}
	b.startHARMonitor()
	t.Cleanup(func() { close(b.harMonitorStop); b.harMonitorWG.Wait() })
	done := make(chan struct{})
	go func() { b.harMonitorWG.Wait(); close(done) }()
	for i := 0; i <= maxRuntimeHARInFlightRequests; i++ {
		b.beginRequestEvidence(&harInFlightRequest{id: i})
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("overflow monitor did not close")
	}
	if budget.counters.harOverflow.Load() != 1 {
		t.Fatal("overflow counter was not retained")
	}
	if native.closed.Load() != 1 || b.resourceLimit != resourceLimitHARInFlight || !b.harErrorSeen {
		t.Fatal("overflow not an explicit context resource failure")
	}
	observation := b.evidence.documents["pending"].observations["inp"]
	if observation.State != "censored" || observation.Available || observation.Qualified || observation.Boundary != "resource_limit" {
		t.Fatalf("overflow observation=%+v", observation)
	}
	// Late callbacks cannot resurrect or close a different native generation.
	b.closeOwnedOversizedHARContext(nil, "old-generation")
	if native.closed.Load() != 1 {
		t.Fatal("stale callback closed the context")
	}
}

func TestHARInFlightOverflowCancellationRetainsExactWriter(t *testing.T) {
	budget := useTestEvidenceBudget(t, 64<<30, noUsage, 0)
	owner := fixtureHAROwner(t, t.TempDir())
	writeOwnedHARFixture(t, owner)
	if err := owner.beginWriter(); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var released sync.Once
	unblock := func() { released.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	var closes atomic.Int32
	owner.bindWriter(func() error { closes.Add(1); close(entered); <-release; return nil })
	b := inflightBrowser(t, false)
	b.harOwner, b.harWorkingPath = owner, owner.path
	b.startHARMonitor()
	t.Cleanup(func() { close(b.harMonitorStop); b.harMonitorWG.Wait() })
	for i := 0; i <= maxRuntimeHARInFlightRequests; i++ {
		b.beginRequestEvidence(&harInFlightRequest{id: i})
	}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("owned close did not begin")
	}
	// Teardown/cancel racing the overflow joins the same native close.
	owner.startFinish(false, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := owner.waitFinish(ctx); !errors.Is(err, errHAROwnershipUnproven) {
		t.Fatal("caller timeout treated as writer proof", err)
	}
	if owner.retiredSuccessfully() {
		t.Fatal("unjoined overflow writer retired")
	}
	if _, err := os.Stat(owner.path); err != nil {
		t.Fatal("unjoined working file removed", err)
	}
	b.closeOwnedOversizedHARContext(owner, b.evidence.generation)
	unblock()
	if _, err := owner.waitFinish(context.Background()); err != nil {
		t.Fatal("late writer join failed", err)
	}
	if !owner.retiredSuccessfully() || closes.Load() != 1 || budget.counters.harOverflow.Load() != 1 {
		t.Fatal("overflow/cancel did not retain one owner")
	}
}
