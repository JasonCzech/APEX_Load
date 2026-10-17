package browser

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// startRecordingSession opens a HAR-recording browser session on server.
func startRecordingSession(t *testing.T, engine string, server *httptest.Server) *Browser {
	t.Helper()
	b := &Browser{}
	if err := b.Init(context.Background(), map[string]any{"base_url": server.URL, "browser_engine": engine, "browser_headless": true, "browser_har_capture": HARCaptureAlways, "browser_har_dir": t.TempDir()}); err != nil {
		requireBrowserRuntime(t, engine, err)
	}
	t.Cleanup(func() { _ = b.Teardown(context.Background()) })
	return b
}

// Browser memory admission measures the Playwright driver's whole process
// tree: the browser the driver launched is found under it, with resident
// memory, on every platform's sampler.
func TestBrowserMemoryUsageMeasuresTheDriverTree(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping browser integration test in -short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	b := &Browser{}
	if err := b.Init(ctx, map[string]any{"browser_engine": "chromium", "browser_headless": true}); err != nil {
		requireBrowserRuntime(t, "chromium", err)
	}
	t.Cleanup(func() { _ = b.Teardown(context.Background()) })
	driver := playwrightDriverPID()
	if driver <= 0 {
		t.Fatal("no Playwright driver is running")
	}
	samples, err := listProcessTreeSamples(ctx, driver)
	if err != nil {
		t.Fatal(err)
	}
	tree := processTreeRSS(samples, driver)
	var driverRSS float64
	for _, sample := range samples {
		if sample.PID == driver {
			driverRSS = sample.RSSKB
		}
	}
	if driverRSS <= 0 || float64(tree) <= driverRSS*1024 {
		t.Fatalf("the driver's tree (%d bytes, driver %.0f KiB, from %d listed processes) holds no browser process", tree, driverRSS, len(samples))
	}
	usage, err := measureBrowserMemoryUsage(ctx)
	if err != nil || usage <= 0 {
		t.Fatalf("browser memory usage %d %v", usage, err)
	}
	t.Logf("driver tree %d MiB (driver %.0f MiB); measured usage %d MiB", tree>>20, driverRSS/1024, usage>>20)
}

// A response that never finishes is buffered by the driver, never by the HAR
// file; the memory watchdog closes the recording as an explicit resource-limit
// failure, censors its document and gives back its reservation at close. The
// measured memory is stubbed: the test proves the close path on a real
// session, not the driver's actual growth.
func TestNeverEndingChunkedBodyIsStoppedByTheMemoryWatchdog(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping browser integration test in -short mode")
	}
	// BE-4: controlled pages in Chromium and WebKit.
	for _, engine := range []string{"chromium", "webkit"} {
		t.Run(engine, func(t *testing.T) { testNeverEndingChunkedBody(t, engine) })
	}
}

func testNeverEndingChunkedBody(t *testing.T, engine string) {
	var streaming atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/stream" {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<!doctype html><html><body><p id="ready">ok</p><script>fetch('/stream')</script></body></html>`))
			return
		}
		streaming.Store(true)
		flusher := w.(http.Flusher)
		chunk := []byte(strings.Repeat("x", 64<<10))
		for r.Context().Err() == nil {
			if _, err := w.Write(chunk); err != nil {
				return
			}
			flusher.Flush()
			time.Sleep(5 * time.Millisecond)
		}
	}))
	defer server.Close()
	var overLimit atomic.Bool
	budget := useTestEvidenceBudget(t, 64<<30, func() (int64, error) {
		if overLimit.Load() {
			return 8 << 30, nil
		}
		return 1 << 20, nil
	}, 4<<30)
	b := startRecordingSession(t, engine, server)
	result, err := b.Execute(context.Background(), &protocol.Action{Name: "open", Type: "browser_navigate", Target: "/"})
	if err != nil || result.ErrorMessage != "" {
		t.Fatalf("navigate: %v %s", err, result.ErrorMessage)
	}
	for deadline := time.Now().Add(5 * time.Second); !streaming.Load() && time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
	}
	if !streaming.Load() {
		t.Fatal("the endless body never started")
	}
	budget.mu.Lock()
	writers := len(budget.writers)
	budget.mu.Unlock()
	if writers != 1 {
		t.Fatalf("recording held %d reservations, want 1", writers)
	}
	overLimit.Store(true)
	for deadline := time.Now().Add(5 * time.Second); b.resourceLimitReason() == "" && time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
	}
	if b.resourceLimitReason() != resourceLimitBrowserMemory {
		t.Fatalf("watchdog did not close the recording: %q", b.resourceLimitReason())
	}
	after, err := b.Execute(context.Background(), &protocol.Action{Name: "check", Type: "browser_wait", WaitFor: &protocol.WaitCondition{Type: "duration", Value: "1ms"}})
	if err != nil {
		t.Fatal(err)
	}
	if after.Success || !strings.HasPrefix(after.ErrorMessage, "browser resource limit (browser_memory)") {
		t.Fatalf("closed session reported %+v", after)
	}
	_ = b.Teardown(context.Background())
	budget.mu.Lock()
	writers = len(budget.writers)
	budget.mu.Unlock()
	if writers != 0 || budget.counters.watchdog.Load() != 1 {
		t.Fatalf("reservations after close %d, watchdog closures %d", writers, budget.counters.watchdog.Load())
	}
}

// A flood of requests that never finish keeps request evidence within the
// request identity bound, and the recording session still closes and
// releases its HAR reservation. Memory admission is disabled here (limit 0).
func TestNeverFinishingRequestFloodStaysBoundedAndCloses(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping browser integration test in -short mode")
	}
	// BE-4: controlled pages in Chromium and WebKit.
	for _, engine := range []string{"chromium", "webkit"} {
		t.Run(engine, func(t *testing.T) { testNeverFinishingRequestFlood(t, engine) })
	}
}

func testNeverFinishingRequestFlood(t *testing.T, engine string) {
	release := make(chan struct{})
	var held atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/hang/") {
			held.Add(1)
			select {
			case <-release:
			case <-r.Context().Done():
			}
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprintf(w, `<!doctype html><html><body><p id="ready">ok</p><script>for (let i = 0; i < %d; i++) fetch('/hang/' + i).catch(() => {})</script></body></html>`, browserRequestIdentityLimit+200)
	}))
	defer server.Close()
	defer close(release)
	budget := useTestEvidenceBudget(t, 64<<30, noUsage, 0)
	b := startRecordingSession(t, engine, server)
	result, err := b.Execute(context.Background(), &protocol.Action{Name: "open", Type: "browser_navigate", Target: "/"})
	if err != nil || result.ErrorMessage != "" {
		t.Fatalf("navigate: %v %s", err, result.ErrorMessage)
	}
	for deadline := time.Now().Add(5 * time.Second); held.Load() == 0 && time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
	}
	time.Sleep(500 * time.Millisecond)
	b.mu.Lock()
	tracked, inFlight := len(b.evidence.requests), b.evidence.inFlight
	b.mu.Unlock()
	if tracked > browserRequestIdentityLimit || inFlight == 0 {
		t.Fatalf("request evidence tracked=%d in flight=%d", tracked, inFlight)
	}
	start := time.Now()
	_ = b.Teardown(context.Background())
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("teardown with hung requests took %v", elapsed)
	}
	budget.mu.Lock()
	writers := len(budget.writers)
	budget.mu.Unlock()
	if writers != 0 {
		t.Fatalf("hung requests kept %d HAR reservations", writers)
	}
}

// A session refused at Init opens its context at a later iteration boundary
// once memory recovers, keeps its evidence lifecycle, and runs that
// iteration from its first action.
func TestRefusedChromiumSessionRejoinsAtAnIterationBoundary(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping browser integration test in -short mode")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><title>ok</title><p>ok</p>`))
	}))
	t.Cleanup(server.Close)
	var usage atomic.Int64
	usage.Store(2 << 30)
	budget := useTestEvidenceBudget(t, 64<<30, func() (int64, error) { return usage.Load(), nil }, 1<<30)
	shortenReadmissionBackoff(t, 0)
	lifecycle, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	b := &Browser{}
	if err := b.Init(lifecycle, map[string]any{"base_url": server.URL, "browser_headless": true, "browser_engine": "chromium", "browser_capture_document_metrics": true}); err != nil {
		requireBrowserRuntime(t, "chromium", err)
	}
	t.Cleanup(func() { _ = b.Teardown(context.Background()) })
	if b.currentPage() != nil {
		t.Fatal("fixture: Init was not refused")
	}
	// Memory recovers and the next sample sees it.
	usage.Store(100 << 20)
	budget.mu.Lock()
	budget.sampledAt = time.Time{}
	budget.mu.Unlock()
	if err := b.BeginIteration(context.Background()); err != nil {
		t.Fatalf("re-admission at the boundary: %v", err)
	}
	if b.currentPage() == nil || b.resourceLimitReason() != "" {
		t.Fatalf("the session did not rejoin: limit %q", b.resourceLimitReason())
	}
	b.mu.Lock()
	kept, refusalLogged := b.evidence.lifecycleContext == lifecycle, b.admissionRefusalLogged
	b.mu.Unlock()
	if !kept {
		t.Fatal("re-admission lost the session's evidence lifecycle")
	}
	if refusalLogged {
		t.Fatal("an open did not end the run of refusals; the next refusal would not be logged")
	}
	result, err := b.Execute(context.Background(), &protocol.Action{Name: "open", Type: "browser_navigate", Target: "/"})
	if err != nil || result.ErrorMessage != "" {
		t.Fatalf("the rejoined iteration's first action: %v %s", err, result.ErrorMessage)
	}
}
