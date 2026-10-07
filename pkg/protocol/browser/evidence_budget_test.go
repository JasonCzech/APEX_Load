package browser

import (
	"bytes"
	"context"
	"errors"
	"log"
	"log/slog"
	"os"
	"strings"
	"sync"
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
		if err := budget.admitBrowserContext(context.Background(), &Browser{}); err != nil {
			t.Fatalf("context %d refused: %v", i, err)
		}
	}
	err := budget.admitBrowserContext(context.Background(), &Browser{})
	if !errors.Is(err, errBrowserResourceLimit) || !strings.HasPrefix(err.Error(), "browser resource limit") || budget.counters.contextsRefused.Load() != 1 {
		t.Fatalf("burst admitted past the limit: %v", err)
	}
	// Unmeasurable memory admits rather than refusing every session.
	unmeasured := useTestEvidenceBudget(t, 64<<30, noUsage, limit)
	if err := unmeasured.admitBrowserContext(context.Background(), &Browser{}); err != nil {
		t.Fatal(err)
	}
	if unmeasured.counters.contextsUnmeasured.Load() != 1 || unmeasured.counters.sampleFailures.Load() != 1 {
		t.Fatalf("an unmeasured admission was not counted: %+v", BrowserEvidenceStats{ContextsUnmeasured: unmeasured.counters.contextsUnmeasured.Load(), MemorySampleFailures: unmeasured.counters.sampleFailures.Load()})
	}
}

func TestMemoryWatchdogClosesTheLargestRecordingAsAResourceLimit(t *testing.T) {
	var usage atomic.Int64
	usage.Store(100 << 20)
	budget := useTestEvidenceBudget(t, 64<<30, func() (int64, error) { return usage.Load(), nil }, 1<<30)
	var smallClosed, largeClosed atomic.Bool
	small := &Browser{pwContext: &harCloseFixtureContext{close: func() error { smallClosed.Store(true); return nil }}, harObservedBytes: 10 << 20, harMonitorStop: make(chan struct{})}
	// Closing the large recording frees its memory, so the watchdog's next
	// tick must not close the small one too.
	large := &Browser{pwContext: &harCloseFixtureContext{close: func() error { usage.Store(100 << 20); largeClosed.Store(true); return nil }}, harObservedBytes: 90 << 20, harMonitorStop: make(chan struct{}), evidence: browserEvidenceState{documents: map[string]*browserDocumentState{
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
	time.Sleep(50 * time.Millisecond)
	if !largeClosed.Load() || smallClosed.Load() || budget.counters.watchdog.Load() != 1 {
		t.Fatalf("watchdog closed large=%v small=%v closures=%d", largeClosed.Load(), smallClosed.Load(), budget.counters.watchdog.Load())
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
	// A root missing from the list is a failed measurement, not an empty tree.
	if missing, err := processTreeRSSBytes(context.Background(), 1<<30); err == nil || missing != 0 {
		t.Fatalf("absent root %d %v", missing, err)
	}
}

// The /proc reader Linux uses (not ps) sees this process with its parent
// and resident memory.
func TestProcProcessSamplesIncludeThisProcess(t *testing.T) {
	samples, err := readProcProcessSamples(context.Background(), "/proc")
	if err != nil {
		t.Skipf("/proc unavailable: %v", err)
	}
	for _, sample := range samples {
		if sample.PID == os.Getpid() {
			if sample.PPID != os.Getppid() || sample.RSSKB <= 0 {
				t.Fatalf("this process: %+v (parent %d)", sample, os.Getppid())
			}
			if total := processTreeRSS(samples, os.Getpid()); total < int64(sample.RSSKB*1024) {
				t.Fatalf("tree RSS %d below the root's own", total)
			}
			return
		}
	}
	t.Fatalf("this process is missing from %d processes read from /proc", len(samples))
}

// A stat line's command name may hold spaces and parentheses; malformed or
// short lines, and processes gone before their file is read, are left out.
func TestProcStatParsingAndVanishedProcesses(t *testing.T) {
	line := "4242 (renderer (gpu) x) S 4241 4242 4242 0 -1 4194560 100 0 0 0 250 50 0 0 20 0 9 0 12345 104857600 2560 18446744073709551615"
	sample, ok := parseProcStat(4242, line, 4)
	if !ok || sample.PPID != 4241 || sample.RSSKB != 2560*4 || sample.CPUTimeSeconds != 3 {
		t.Fatalf("parsed %+v %v", sample, ok)
	}
	for _, bad := range []string{"4242 renderer S 1", "4242 (renderer) S 1 2 3", "4242 (renderer) S x 4242 4242 0 -1 0 0 0 0 0 250 50 0 0 20 0 9 0 1 1 2560"} {
		if _, ok := parseProcStat(4242, bad, 4); ok {
			t.Fatalf("malformed stat line accepted: %q", bad)
		}
	}
	root := t.TempDir()
	for pid, content := range map[string]string{"10": "10 (driver) S 1 " + strings.Repeat("0 ", 9) + "100 100 0 0 0 0 0 0 0 0 256", "11": "11 (chrome) S 10 " + strings.Repeat("0 ", 9) + "50 50 0 0 0 0 0 0 0 0 512", "self": "not a process"} {
		if err := os.MkdirAll(root+"/"+pid, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(root+"/"+pid+"/stat", []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A process whose directory exists but whose stat file is gone.
	if err := os.MkdirAll(root+"/12", 0o755); err != nil {
		t.Fatal(err)
	}
	samples, err := readProcProcessSamples(context.Background(), root)
	if err != nil || len(samples) != 2 {
		t.Fatalf("samples %+v %v", samples, err)
	}
	if total := processTreeRSS(samples, 10); total != (256+512)*int64(os.Getpagesize()) {
		t.Fatalf("tree RSS %d", total)
	}
}

// Concurrent admitters must not erase each other's admissions when one of
// them refreshes the sample.
func TestConcurrentContextAdmissionsKeepEveryAdmission(t *testing.T) {
	var measuring atomic.Int32
	budget := useTestEvidenceBudget(t, 64<<30, func() (int64, error) {
		measuring.Add(1)
		time.Sleep(20 * time.Millisecond)
		return 700 << 20, nil
	}, 1000<<20)
	results := make(chan error, 16)
	for range 16 {
		go func() { results <- budget.admitBrowserContext(context.Background(), &Browser{}) }()
	}
	admitted := 0
	for range 16 {
		if <-results == nil {
			admitted++
		}
	}
	// 700 MiB measured: at most two more 128 MiB contexts fit, however the
	// admissions interleave with the measurement.
	if admitted > 2 {
		t.Fatalf("%d concurrent contexts admitted against one sample", admitted)
	}
	if measuring.Load() > 1 {
		t.Fatalf("%d measurements ran concurrently for one stale sample", measuring.Load())
	}
}

// A live context keeps its marginal footprint after the sample that admitted
// it expires, so blank contexts sampled one at a time cannot overcommit.
func TestLiveContextsKeepAFootprintFloor(t *testing.T) {
	budget := useTestEvidenceBudget(t, 64<<30, func() (int64, error) { return 100 << 20, nil }, 1000<<20)
	live := make([]*Browser, 0, 8)
	for i := range 8 {
		budget.mu.Lock()
		budget.sampledAt = time.Time{} // every admission sees a fresh, blank sample
		budget.mu.Unlock()
		b := &Browser{}
		err := budget.admitBrowserContext(context.Background(), b)
		if i < 6 && err != nil {
			t.Fatalf("context %d refused: %v", i, err)
		}
		if i >= 7 && !errors.Is(err, errBrowserResourceLimit) {
			t.Fatalf("context %d admitted past the live floor", i)
		}
		if err == nil {
			live = append(live, b)
		}
	}
	budget.releaseBrowserContext(live[0])
	budget.mu.Lock()
	budget.sampledAt = time.Time{}
	budget.mu.Unlock()
	if err := budget.admitBrowserContext(context.Background(), &Browser{}); err != nil {
		t.Fatalf("a released context still held its floor: %v", err)
	}
}

// A failed measurement keeps the last sample only until it ages out.
func TestStaleMemorySampleExpires(t *testing.T) {
	var fail atomic.Bool
	budget := useTestEvidenceBudget(t, 64<<30, func() (int64, error) {
		if fail.Load() {
			return 0, errors.New("ps: resource temporarily unavailable")
		}
		return 900 << 20, nil
	}, 1000<<20)
	if err := budget.admitBrowserContext(context.Background(), &Browser{}); !errors.Is(err, errBrowserResourceLimit) {
		t.Fatalf("over-limit admission: %v", err)
	}
	fail.Store(true)
	budget.mu.Lock()
	budget.sampledAt = time.Now().Add(-browserMemorySampleMaxAge - time.Second)
	budget.mu.Unlock()
	if err := budget.admitBrowserContext(context.Background(), &Browser{}); err != nil {
		t.Fatalf("an expired sample still refused: %v", err)
	}
	if budget.counters.contextsUnmeasured.Load() != 1 || budget.counters.sampleFailures.Load() < 1 {
		t.Fatalf("unmeasured=%d failures=%d", budget.counters.contextsUnmeasured.Load(), budget.counters.sampleFailures.Load())
	}
}

// Only fully open sessions are watchdog victims: one already closed for a
// limit, or still opening, is skipped even when it holds the most bytes.
func TestMemoryWatchdogSkipsClosedAndOpeningSessions(t *testing.T) {
	var usage atomic.Int64
	usage.Store(2 << 30)
	budget := useTestEvidenceBudget(t, 64<<30, func() (int64, error) { return usage.Load(), nil }, 1<<30)
	var closedClosed, openingClosed, liveClosed atomic.Bool
	alreadyClosed := &Browser{pwContext: &harCloseFixtureContext{close: func() error { closedClosed.Store(true); return nil }}, harObservedBytes: 200 << 20, harMonitorStop: make(chan struct{}), resourceLimit: resourceLimitHARCapture}
	opening := &Browser{pwContext: &harCloseFixtureContext{close: func() error { openingClosed.Store(true); return nil }}, harObservedBytes: 150 << 20}
	live := &Browser{pwContext: &harCloseFixtureContext{close: func() error { liveClosed.Store(true); return nil }}, harObservedBytes: 10 << 20, harMonitorStop: make(chan struct{})}
	for _, b := range []*Browser{alreadyClosed, opening, live} {
		if reason := budget.reserveHARWriter(b, t.TempDir()); reason != "" {
			t.Fatal(reason)
		}
	}
	for deadline := time.Now().Add(2 * time.Second); !liveClosed.Load() && time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
	}
	if !liveClosed.Load() || closedClosed.Load() || openingClosed.Load() {
		t.Fatalf("watchdog closed live=%v alreadyClosed=%v opening=%v", liveClosed.Load(), closedClosed.Load(), openingClosed.Load())
	}
}

// A context refused at open is not an Init error: the session runs and every
// action fails as a resource-limit result.
func TestRefusedContextFailsActionsAsResourceLimit(t *testing.T) {
	useTestEvidenceBudget(t, 64<<30, func() (int64, error) { return 2 << 30, nil }, 1<<30)
	shortenReadmissionBackoff(t, time.Millisecond)
	b := &Browser{}
	if err := b.openBrowserContext(); err != nil {
		t.Fatalf("an admission refusal failed the session: %v", err)
	}
	result, err := b.Execute(context.Background(), &protocol.Action{Name: "open", Type: "browser_navigate", Target: "/"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Success || !strings.HasPrefix(result.ErrorMessage, "browser resource limit ("+resourceLimitAdmission+")") || !strings.Contains(result.ErrorMessage, "refused") {
		t.Fatalf("refused session action: %+v", result)
	}
}

// A writer whose finish outlived the close wait keeps its reservation until
// the finish completes.
func TestHARReservationOutlivesAnUnfinishedOwner(t *testing.T) {
	budget := useTestEvidenceBudget(t, 64<<30, noUsage, 0)
	b := &Browser{}
	if reason := budget.reserveHARWriter(b, t.TempDir()); reason != "" {
		t.Fatal(reason)
	}
	owner, err := reserveHAROwner()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(owner.releaseReservation)
	budget.retireHARWriter(b, owner)
	reserved := func() int64 { budget.mu.Lock(); defer budget.mu.Unlock(); return budget.reserved }
	if reserved() != maxRuntimeHARBytes {
		t.Fatalf("reservation released before the owner retired: %d", reserved())
	}
	close(owner.finishDone)
	for deadline := time.Now().Add(time.Second); reserved() != 0 && time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
	}
	if reserved() != 0 {
		t.Fatalf("reservation not released after the owner finished: %d", reserved())
	}
}

// A close requested while the context is still opening waits for the writer
// to be bound and then closes it, instead of losing the close.
func TestHAROwnerHonoursACloseRequestedBeforeBind(t *testing.T) {
	owner, err := reserveHAROwner()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(owner.releaseReservation)
	owner.beginWriter()
	owner.startClose()
	var closed atomic.Bool
	time.Sleep(20 * time.Millisecond)
	owner.bindWriter(func() error { closed.Store(true); return nil })
	select {
	case <-owner.closeDone:
	case <-time.After(runtimeHARCloseWait + time.Second):
		t.Fatal("close never completed")
	}
	if !closed.Load() {
		t.Fatal("the writer bound after the close request was never closed")
	}
}

func TestHARScratchUnavailableIsNotHeadroom(t *testing.T) {
	budget := useTestEvidenceBudget(t, 64<<30, noUsage, 0)
	budget.scratchAvailable = func(string) (int64, error) { return 0, errors.New("no such directory") }
	if reason := budget.reserveHARWriter(&Browser{}, "/does/not/exist"); reason != "har_scratch_unavailable" || budget.counters.harScratchUnavailable.Load() != 1 || budget.counters.harScratch.Load() != 0 {
		t.Fatalf("missing scratch directory reported as %q", reason)
	}
}

func shortenReadmissionBackoff(t *testing.T, backoff time.Duration) {
	t.Helper()
	previous := browserContextReadmissionBackoff
	browserContextReadmissionBackoff = backoff
	t.Cleanup(func() { browserContextReadmissionBackoff = previous })
}

// A refused session tries admission again only at an iteration boundary,
// after the backoff: its actions fail at once as resource limits, its
// iterations do not spin, and its refusals log once per run.
func TestRefusedContextRetriesAdmissionAtIterationBoundaries(t *testing.T) {
	budget := useTestEvidenceBudget(t, 64<<30, func() (int64, error) { return 2 << 30, nil }, 1<<30)
	shortenReadmissionBackoff(t, 60*time.Millisecond)
	logs := captureEvidenceLogs(t)
	b := &Browser{}
	if err := b.openBrowserContext(); err != nil {
		t.Fatal(err)
	}
	action := &protocol.Action{Name: "open", Type: "browser_navigate", Target: "/"}
	started := time.Now()
	result, err := b.Execute(context.Background(), action)
	if err != nil || result.Success || !strings.HasPrefix(result.ErrorMessage, "browser resource limit ("+resourceLimitAdmission+")") || time.Since(started) > 50*time.Millisecond {
		t.Fatalf("a refused action waited or did not fail as a resource limit: %+v %v", result, err)
	}
	for range 2 {
		if err := b.BeginIteration(context.Background()); err != nil {
			t.Fatalf("a refused retry failed the iteration: %v", err)
		}
	}
	if elapsed := time.Since(started); elapsed < 110*time.Millisecond {
		t.Fatalf("two iteration retries took %s; the backoff was not honoured", elapsed)
	}
	if refused := budget.counters.contextsRefused.Load(); refused != 3 {
		t.Fatalf("admission attempts = %d, want the open plus one per iteration", refused)
	}
	b.mu.Lock()
	logged := b.admissionRefusalLogged
	b.mu.Unlock()
	if !logged || logs.count(refusalLogMessage) != 1 {
		t.Fatalf("a run of 3 refusals logged %d lines, want 1", logs.count(refusalLogMessage))
	}
	// A cancelled boundary does not wait out the backoff or try again.
	shortenReadmissionBackoff(t, time.Hour)
	b.mu.Lock()
	b.admissionRetryAt = time.Now().Add(time.Hour)
	b.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := b.BeginIteration(ctx); err != nil || budget.counters.contextsRefused.Load() != 3 {
		t.Fatalf("a cancelled boundary: %v, attempts %d", err, budget.counters.contextsRefused.Load())
	}
	// A session that was never refused does nothing at the boundary.
	if err := (&Browser{}).BeginIteration(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// A context admitted at the boundary that cannot open fails the iteration
// setup and gives its admission back.
func TestAdmittedContextThatCannotOpenFailsTheIterationSetup(t *testing.T) {
	budget := useTestEvidenceBudget(t, 64<<30, func() (int64, error) { return 100 << 20, nil }, 1<<30)
	shortenReadmissionBackoff(t, 0)
	// A previous recording whose retirement was never proven blocks the
	// next HAR writer before any browser is launched.
	b := &Browser{harCapturePolicy: HARCaptureAlways, harOwner: &harCaptureOwner{}}
	b.resourceLimit = resourceLimitAdmission
	b.admissionRefusalLogged = true
	if err := b.BeginIteration(context.Background()); !errors.Is(err, errHAROwnershipUnproven) {
		t.Fatalf("BeginIteration = %v, want the open failure", err)
	}
	budget.mu.Lock()
	_, held := budget.live[b]
	budget.mu.Unlock()
	if held {
		t.Fatal("a context that failed to open kept its admission")
	}
}

// A session the watchdog or its HAR size cap closed ends its VUser at the
// next iteration boundary rather than failing every action at once.
func TestSessionClosedByAResourceLimitEndsItsVUserAtTheBoundary(t *testing.T) {
	for _, limit := range []string{resourceLimitBrowserMemory, resourceLimitHARCapture} {
		b := &Browser{resourceLimit: limit}
		if err := b.BeginIteration(context.Background()); !errors.Is(err, errBrowserResourceLimit) || !strings.Contains(err.Error(), limit) {
			t.Fatalf("%s: BeginIteration = %v", limit, err)
		}
	}
}

const refusalLogMessage = "browser context refused to protect the generator"

type evidenceLogCapture struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (c *evidenceLogCapture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.Write(p)
}

func (c *evidenceLogCapture) count(message string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.Count(c.buf.String(), message)
}

func captureEvidenceLogs(t *testing.T) *evidenceLogCapture {
	t.Helper()
	capture := &evidenceLogCapture{}
	// slog.SetDefault also redirects the log package; restore both.
	previous, writer, flags := slog.Default(), log.Writer(), log.Flags()
	slog.SetDefault(slog.New(slog.NewTextHandler(capture, nil)))
	t.Cleanup(func() {
		slog.SetDefault(previous)
		log.SetOutput(writer)
		log.SetFlags(flags)
	})
	return capture
}

// Without a usable measurement the live-context floor still applies.
func TestUnmeasuredAdmissionKeepsTheLiveContextFloor(t *testing.T) {
	budget := useTestEvidenceBudget(t, 64<<30, noUsage, 3*browserContextMarginalBytes)
	for i := range 3 {
		if err := budget.admitBrowserContext(context.Background(), &Browser{}); err != nil {
			t.Fatalf("context %d refused below the floor: %v", i, err)
		}
	}
	if err := budget.admitBrowserContext(context.Background(), &Browser{}); !errors.Is(err, errBrowserResourceLimit) {
		t.Fatalf("an unmeasured admission ignored the live-context floor: %v", err)
	}
}

// Waiters woken by a finished measurement find its sample, not an empty
// budget they would admit unmeasured.
func TestMeasurementIsRecordedBeforeWaitersWake(t *testing.T) {
	release := make(chan struct{})
	var calls atomic.Int64
	budget := useTestEvidenceBudget(t, 64<<30, func() (int64, error) {
		if calls.Add(1) == 1 {
			<-release
		}
		return 100 << 20, nil
	}, 4<<30)
	done := make(chan error, 8)
	for range 8 {
		go func() { done <- budget.admitBrowserContext(context.Background(), &Browser{}) }()
	}
	for deadline := time.Now().Add(time.Second); calls.Load() == 0 && time.Now().Before(deadline); time.Sleep(time.Millisecond) {
	}
	time.Sleep(20 * time.Millisecond)
	close(release)
	for range 8 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if unmeasured := budget.counters.contextsUnmeasured.Load(); unmeasured != 0 {
		t.Fatalf("%d admissions woke before the sample was stored", unmeasured)
	}
}

// The browser footprint is the root process plus every descendant (driver,
// browser, renderers), and nothing outside its tree.
func TestProcessTreeRSSSumsTheRootAndEveryDescendant(t *testing.T) {
	samples := []processSample{
		{PID: 10, PPID: 1, RSSKB: 1000},  // generator
		{PID: 11, PPID: 10, RSSKB: 2000}, // driver
		{PID: 12, PPID: 11, RSSKB: 3000}, // browser
		{PID: 13, PPID: 12, RSSKB: 4000}, // renderer
		{PID: 20, PPID: 1, RSSKB: 9000},  // unrelated
		{PID: 21, PPID: 20, RSSKB: 9000}, // unrelated child
	}
	if got, want := processTreeRSS(samples, 10), int64(10000*1024); got != want {
		t.Fatalf("tree RSS = %d, want %d", got, want)
	}
	if got := processTreeRSS(samples, 99); got != 0 {
		t.Fatalf("absent root RSS = %d", got)
	}
}
