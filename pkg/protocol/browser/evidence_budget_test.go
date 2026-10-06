package browser

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
	playwright "github.com/mxschmitt/playwright-go"
)

// useTestEvidenceBudget installs a fresh budget with stubbed measurements.
func useTestEvidenceBudget(t *testing.T, free int64, usage func() (int64, error), limit int64) *evidenceBudget {
	t.Helper()
	budget := newEvidenceBudget()
	budget.scratchAvailable = func(string) (int64, error) { return free, nil }
	budget.memoryUsage = func(context.Context) (int64, error) { return usage() }
	budget.memoryLimit = func() int64 { return limit }
	budget.watchdogInterval = 10 * time.Millisecond
	previous := browserEvidenceBudget
	browserEvidenceBudget = budget
	t.Cleanup(func() {
		budget.mu.Lock()
		for b := range budget.writers {
			delete(budget.writers, b)
		}
		budget.mu.Unlock()
		// Let the watchdog observe the empty budget and exit.
		for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
			budget.mu.Lock()
			running := budget.watchdogRunning
			budget.mu.Unlock()
			if !running {
				break
			}
		}
		browserEvidenceBudget = previous
	})
	return budget
}

func noUsage() (int64, error) { return 0, errors.New("unmeasured") }

func TestHARWritersReserveTheAggregateBudgetAndScratch(t *testing.T) {
	budget := useTestEvidenceBudget(t, 64<<30, noUsage, 0)
	writers := make([]*Browser, 0, 11)
	for range 10 {
		b := &Browser{}
		if reason := budget.reserveHARWriter(b, t.TempDir()); reason != "" {
			t.Fatalf("writer %d refused: %s", len(writers), reason)
		}
		writers = append(writers, b)
	}
	// Ten 100 MiB writers fill the 1 GiB aggregate; the eleventh does not fit.
	late := &Browser{}
	if reason := budget.reserveHARWriter(late, t.TempDir()); reason != "har_budget_exhausted" {
		t.Fatalf("eleventh writer: %q", reason)
	}
	budget.releaseHARWriter(writers[0])
	budget.releaseHARWriter(writers[0]) // idempotent
	if reason := budget.reserveHARWriter(late, t.TempDir()); reason != "" {
		t.Fatalf("released allowance not reusable: %q", reason)
	}
	if budget.counters.harAdmitted.Load() != 11 || budget.counters.harRefused.Load() != 1 {
		t.Fatalf("counters admitted=%d refused=%d", budget.counters.harAdmitted.Load(), budget.counters.harRefused.Load())
	}

	// Scratch must hold every reservation plus headroom.
	tight := useTestEvidenceBudget(t, maxRuntimeHARBytes+harScratchHeadroomBytes-1, noUsage, 0)
	if reason := tight.reserveHARWriter(&Browser{}, t.TempDir()); reason != "har_scratch_headroom" || tight.counters.harScratch.Load() != 1 {
		t.Fatalf("scratch refusal %q", reason)
	}
}

func TestRefusedHARCaptureRunsWithoutRecordingAndSaysSoOnce(t *testing.T) {
	budget := useTestEvidenceBudget(t, 64<<30, noUsage, 0)
	for range 10 {
		budget.reserveHARWriter(&Browser{}, t.TempDir())
	}
	b := &Browser{harCapturePolicy: HARCaptureAlways, harDir: t.TempDir()}
	var options playwright.BrowserNewContextOptions
	if err := b.applyHARCaptureOptions(&options); err != nil {
		t.Fatalf("optional evidence refusal failed the session: %v", err)
	}
	if options.RecordHarPath != nil || b.harOwner != nil {
		t.Fatal("refused writer still recorded a HAR")
	}
	first, second := &protocol.Result{Success: true}, &protocol.Result{Success: true}
	b.applyEvidenceOutcome(first)
	b.applyEvidenceOutcome(second)
	if !first.Success || len(first.WarningCodes) != 1 || first.WarningCodes[0] != "browser_har_unavailable_har_budget_exhausted" || len(second.WarningCodes) != 0 {
		t.Fatalf("warnings %v / %v", first.WarningCodes, second.WarningCodes)
	}
}

func TestBrowserContextAdmissionCountsMeasuredMemoryAndBursts(t *testing.T) {
	limit := int64(1000 << 20)
	usage := int64(700 << 20)
	budget := useTestEvidenceBudget(t, 64<<30, func() (int64, error) { return usage, nil }, limit)
	// 700 MiB measured: two more 128 MiB contexts fit, a third would not.
	for i := range 2 {
		if err := budget.admitBrowserContext(context.Background()); err != nil {
			t.Fatalf("context %d refused: %v", i, err)
		}
	}
	err := budget.admitBrowserContext(context.Background())
	if !errors.Is(err, errBrowserResourceLimit) || !strings.HasPrefix(err.Error(), "browser resource limit") || budget.counters.contextsRefused.Load() != 1 {
		t.Fatalf("burst admitted past the limit: %v", err)
	}
	// Unmeasurable memory admits rather than refusing every session.
	unmeasured := useTestEvidenceBudget(t, 64<<30, noUsage, limit)
	if err := unmeasured.admitBrowserContext(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestMemoryWatchdogClosesTheLargestRecordingAsAResourceLimit(t *testing.T) {
	var usage atomic.Int64
	usage.Store(100 << 20)
	budget := useTestEvidenceBudget(t, 64<<30, func() (int64, error) { return usage.Load(), nil }, 1<<30)
	var smallClosed, largeClosed atomic.Bool
	small := &Browser{pwContext: &harCloseFixtureContext{close: func() error { smallClosed.Store(true); return nil }}, harObservedBytes: 10 << 20}
	large := &Browser{pwContext: &harCloseFixtureContext{close: func() error { largeClosed.Store(true); return nil }}, harObservedBytes: 90 << 20, evidence: browserEvidenceState{documents: map[string]*browserDocumentState{
		"doc": {observations: map[string]protocol.BrowserDocumentObservation{"lcp": {State: "provisional"}}},
	}}}
	for _, b := range []*Browser{small, large} {
		if reason := budget.reserveHARWriter(b, t.TempDir()); reason != "" {
			t.Fatal(reason)
		}
	}
	time.Sleep(50 * time.Millisecond)
	if largeClosed.Load() || smallClosed.Load() {
		t.Fatal("watchdog closed a session under the limit")
	}
	usage.Store(2 << 30)
	for deadline := time.Now().Add(2 * time.Second); !largeClosed.Load() && time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
	}
	if !largeClosed.Load() || smallClosed.Load() || budget.counters.watchdog.Load() < 1 {
		t.Fatalf("watchdog closed large=%v small=%v", largeClosed.Load(), smallClosed.Load())
	}
	if large.resourceLimitReason() != resourceLimitBrowserMemory {
		t.Fatalf("reason %q", large.resourceLimitReason())
	}
	if o := large.evidence.documents["doc"].observations["lcp"]; o.State != "censored" || o.Boundary != "resource_limit" {
		t.Fatalf("unfinished observation not censored at the limit: %+v", o)
	}
	result := &protocol.Result{Success: true}
	large.applyEvidenceOutcome(result)
	if result.Success || !strings.HasPrefix(result.ErrorMessage, "browser resource limit (browser_memory)") || result.WarningCodes[len(result.WarningCodes)-1] != "browser_resource_limit_browser_memory" {
		t.Fatalf("resource-limit close passed: %+v", result)
	}
}

func TestHAROverflowFailsTheSessionExplicitly(t *testing.T) {
	var closed atomic.Bool
	b := &Browser{pwContext: &harCloseFixtureContext{close: func() error { closed.Store(true); return nil }}, harLimitExceeded: make(chan struct{})}
	before := browserEvidenceBudget.counters.harOverflow.Load()
	b.noteHARNetworkUsage(maxRuntimeHARBytes + 1)
	b.closeOversizedHARContext()
	if !closed.Load() || b.resourceLimitReason() != resourceLimitHARCapture || browserEvidenceBudget.counters.harOverflow.Load() != before+1 {
		t.Fatalf("overflow close: closed=%v reason=%q", closed.Load(), b.resourceLimitReason())
	}
	result := &protocol.Result{Success: true}
	b.applyEvidenceOutcome(result)
	if result.Success || !strings.Contains(result.ErrorMessage, "(har_capture)") {
		t.Fatalf("an overflowed session reported success: %+v", result)
	}
	// Dropped network tasks are reported once, before their evidence is gone.
	b.mu.Lock()
	b.networkTasksDropped = 3
	b.mu.Unlock()
	first, second := &protocol.Result{}, &protocol.Result{}
	b.applyEvidenceOutcome(first)
	b.applyEvidenceOutcome(second)
	if !strings.Contains(strings.Join(first.WarningCodes, ","), "browser_network_tasks_dropped") || strings.Contains(strings.Join(second.WarningCodes, ","), "browser_network_tasks_dropped") {
		t.Fatalf("drop warnings %v / %v", first.WarningCodes, second.WarningCodes)
	}
}

func TestProcessTreeRSSIncludesTheRootAndDescendants(t *testing.T) {
	total, err := processTreeRSSBytes(context.Background(), 1)
	if err != nil {
		t.Skipf("ps unavailable: %v", err)
	}
	if total <= 0 {
		t.Fatal("init process tree has no resident memory")
	}
	if missing, err := processTreeRSSBytes(context.Background(), 1<<30); err != nil || missing != 0 {
		t.Fatalf("absent root %d %v", missing, err)
	}
}
