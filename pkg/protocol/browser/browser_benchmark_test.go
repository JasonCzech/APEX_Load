package browser

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
	playwright "github.com/mxschmitt/playwright-go"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/process"
)

// The browser acceptance benchmark (BE-4, D8) is opt-in, under the browser
// benchmarks gate: APEXLOAD_BROWSER_BENCHMARKS=1.
// For each engine and an engine-appropriate desktop and mobile profile it
// measures cold browser launch, per-session start, the browser process tree's
// CPU and RSS, per-action and document-capture overhead, throttled timing
// separation, cleanup and stability, against a local fixture page, and writes the results with F4 feature acceptance requests
// (BE-4, browser-benchmark) to APEXLOAD_BROWSER_BENCHMARK_OUT. The published
// five-browser-VUser ceiling stays until such evidence authorizes more;
// synthetic shaping is a calibrated approximation, not a device claim.

// BrowserBenchmarkResult is one engine and profile's measurements.
type BrowserBenchmarkResult struct {
	Engine         string `json:"engine"`
	Profile        string `json:"profile"`
	Platform       string `json:"platform"`
	BrowserVersion string `json:"browser_version"`
	Iterations     int    `json:"iterations"`
	Failures       int    `json:"failures"`
	// ColdLaunchMs starts the engine's browser process and a session from
	// nothing; the other figures are milliseconds, medians over the
	// iterations, against the pooled browser a generator keeps running.
	ColdLaunchMs       float64 `json:"cold_launch_ms"`
	SessionStartMs     float64 `json:"session_start_ms"`
	ActionMs           float64 `json:"action_ms"`
	CapturedActionMs   float64 `json:"captured_action_ms"`
	CaptureOverheadMs  float64 `json:"capture_overhead_ms"`
	ActionOverheadMs   float64 `json:"action_overhead_ms"`
	ActionOverheadKind string  `json:"action_overhead_kind,omitempty"`
	ThrottledActionMs  float64 `json:"throttled_action_ms"`
	// ThrottleSeparationMs is the throttled navigation's excess over the
	// unthrottled one, for the configured latency.
	ThrottleSeparationMs float64 `json:"throttle_separation_ms"`
	ThrottleLatencyMs    int     `json:"throttle_latency_ms"`
	CleanupMs            float64 `json:"cleanup_ms"`
	// PerVUserRSSBytes attributes type=="renderer" processes for the VUser's
	// context via CDP SystemInfo.getProcessInfo; FixedOverheadBytes partitions
	// the shared Playwright driver and Chromium browser/zygote/gpu/utility
	// processes. WebKit reports RSSAttribution="unavailable" with nil bytes.
	PerVUserRSSBytes   *uint64 `json:"per_vuser_rss_bytes"`
	PerVUserPSSBytes   *uint64 `json:"per_vuser_pss_bytes,omitempty"`
	FixedOverheadBytes *uint64 `json:"fixed_overhead_bytes"`
	RSSAttribution     string  `json:"rss_attribution"`
	// PeakRSSBytes and RSSPopulation retain the observed descendant-tree
	// snapshot for v2 compatibility.
	PeakRSSBytes          *uint64 `json:"peak_rss_bytes"`
	RSSPopulation         string  `json:"rss_population"`
	RSSSamples            int     `json:"rss_samples"`
	RSSUnavailableSamples int     `json:"rss_unavailable_samples"`
	RSSPartialSamples     int     `json:"rss_partial_samples"`
	CPUSeconds            float64 `json:"cpu_seconds"`
	// LeftoverProcesses remain after every session is torn down and the
	// browser is closed.
	LeftoverProcesses int `json:"leftover_processes"`
}

func TestBrowserBenchmark(t *testing.T) {
	if !mobileBrowserBenchmarksEnabled() {
		t.Skipf("set %s=1 to run the browser acceptance benchmark", mobileBrowserBenchmarkEnv)
	}
	out := os.Getenv("APEXLOAD_BROWSER_BENCHMARK_OUT")
	if out == "" {
		out = t.TempDir()
	}
	iterations := 5
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, `<!doctype html><title>benchmark</title><h1 id="ready">ready</h1><button id="target" onclick="document.body.dataset.clicked=(Number(document.body.dataset.clicked||0)+1).toString()">Tap</button>`)
	}))
	defer server.Close()
	const latencyMs = 200
	var results []BrowserBenchmarkResult
	// Each engine is its own subtest, so an engine missing from this host
	// (a generator image with system Chromium only) skips or fails alone and
	// the other engine's results are still written.
	for _, engine := range []string{"chromium", "webkit"} {
		t.Run(engine, func(t *testing.T) {
			for _, target := range benchmarkTargets(engine) {
				rssAttribution := BrowserRSSAttributionCDPRendererPerVUser
				rssPopulation := "observed_test_process_descendants"
				if engine == "webkit" {
					rssAttribution = BrowserRSSAttributionUnavailable
					rssPopulation = BrowserRSSAttributionUnavailable
				}
				result := BrowserBenchmarkResult{
					Engine:            engine,
					Profile:           target.label,
					Platform:          runtime.GOOS + "-" + runtime.GOARCH,
					Iterations:        iterations,
					ThrottleLatencyMs: latencyMs,
					RSSAttribution:    rssAttribution,
					RSSPopulation:     rssPopulation,
				}
				// Start from a closed browser, so the first session times the
				// browser's launch.
				baseline := benchmarkColdPool(t, engine)
				cpu := benchmarkCPU{start: benchmarkCPUByPID()}
				var sessionStart, action, captured, throttled, cleanup []float64
				var clickBaseline, capturedClick, touchAction []float64
				for i := 0; i < iterations; i++ {
					for _, mode := range []string{"plain", "captured", "throttled"} {
						config := map[string]any{"base_url": server.URL, "browser_engine": engine, "browser_headless": true}
						for key, value := range target.config {
							config[key] = value
						}
						switch mode {
						case "captured":
							config["browser_capture_document_metrics"] = true
							config["browser_client_metrics"] = true
						case "throttled":
							config["web_network_simulation_latency_ms"] = latencyMs
						}
						baselineRenderers := snapshotBaselineRendererPIDs(engine)
						ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
						b := &Browser{}
						started := time.Now()
						if err := b.Init(ctx, config); err != nil {
							// The engine is installed (benchmarkColdPool launched it),
							// so a session that fails to start is a counted failure.
							cancel()
							t.Logf("%s/%s %s session failed to start: %v", engine, target.label, mode, err)
							result.Failures++
							continue
						}
						launched := time.Since(started)
						if i == 0 && mode == "plain" {
							result.ColdLaunchMs = benchmarkMs(launched)
							b.mu.Lock()
							if b.evidence.cohort != nil {
								result.BrowserVersion = b.evidence.cohort.BrowserVersion
							}
							b.mu.Unlock()
						} else if mode == "plain" {
							sessionStart = append(sessionStart, benchmarkMs(launched))
						}
						navigated := time.Now()
						res, err := b.Execute(ctx, &protocol.Action{Name: "open", Type: "browser_navigate", Target: "/"})
						elapsed := time.Since(navigated)
						if err != nil || res == nil || !res.Success {
							t.Logf("%s/%s %s navigation failed: %v %+v", engine, target.label, mode, err, res)
							result.Failures++
						} else {
							switch mode {
							case "plain":
								action = append(action, benchmarkMs(elapsed))
								clickStarted := time.Now()
								if clickRes, clickErr := b.Execute(ctx, &protocol.Action{Name: "click baseline", Type: "browser_click", Target: "#target"}); clickErr == nil && clickRes != nil && clickRes.Success {
									clickBaseline = append(clickBaseline, benchmarkMs(time.Since(clickStarted)))
								}
								if target.hasTouch {
									tapStarted := time.Now()
									if tapRes, tapErr := b.Execute(ctx, &protocol.Action{Name: "touch action", Type: "browser_tap", Target: "#target"}); tapErr == nil && tapRes != nil && tapRes.Success {
										touchAction = append(touchAction, benchmarkMs(time.Since(tapStarted)))
									}
								}
							case "captured":
								captured = append(captured, benchmarkMs(elapsed))
								if !target.hasTouch {
									clickStarted := time.Now()
									if clickRes, clickErr := b.Execute(ctx, &protocol.Action{Name: "captured click", Type: "browser_click", Target: "#target"}); clickErr == nil && clickRes != nil && clickRes.Success {
										capturedClick = append(capturedClick, benchmarkMs(time.Since(clickStarted)))
									}
								}
							case "throttled":
								throttled = append(throttled, benchmarkMs(elapsed))
							}
						}
						if engine == "webkit" {
							result.recordWebKitUnavailableRSSSample()
						} else {
							sample := b.sampleBenchmarkVUserMemory(baselineRenderers, true)
							result.recordAttributedRSSSample(sample)
						}
						cpu.sample()
						closing := time.Now()
						if err := b.Teardown(context.Background()); err != nil {
							t.Logf("%s/%s %s teardown failed: %v", engine, target.label, mode, err)
							result.Failures++
						}
						if mode == "plain" {
							cleanup = append(cleanup, benchmarkMs(time.Since(closing)))
						}
						cancel()
					}
				}
				cpu.sample()
				result.CPUSeconds = cpu.total()
				result.SessionStartMs, result.ActionMs, result.CapturedActionMs, result.ThrottledActionMs, result.CleanupMs = benchmarkMedian(sessionStart), benchmarkMedian(action), benchmarkMedian(captured), benchmarkMedian(throttled), benchmarkMedian(cleanup)
				result.CaptureOverheadMs = result.CapturedActionMs - result.ActionMs
				result.ActionOverheadKind, result.ActionOverheadMs = computeBenchmarkActionOverhead(target.hasTouch, clickBaseline, capturedClick, touchAction)
				result.ThrottleSeparationMs = result.ThrottledActionMs - result.ActionMs
				benchmarkClosePooledBrowser(engine)
				result.LeftoverProcesses = benchmarkLeftover(baseline)
				t.Logf("%s/%s: %+v", engine, target.label, result)
				results = append(results, result)
			}
		})
	}
	// Every engine skipped (none installed): there is no evidence to write.
	if len(results) == 0 {
		t.Skip("no browser engine ran; nothing was measured")
	}
	writeBrowserBenchmark(t, out, results)
	for _, result := range results {
		if result.Failures > 0 {
			t.Errorf("%s/%s failed %d of %d sessions", result.Engine, result.Profile, result.Failures, 3*result.Iterations)
		}
		if result.ThrottleSeparationMs < float64(result.ThrottleLatencyMs)/2 {
			t.Errorf("%s/%s throttling separated navigations by %.0f ms for %d ms of latency", result.Engine, result.Profile, result.ThrottleSeparationMs, result.ThrottleLatencyMs)
		}
		if result.LeftoverProcesses > 0 {
			t.Errorf("%s/%s left %d processes running after its browser closed", result.Engine, result.Profile, result.LeftoverProcesses)
		}
	}
}

type benchmarkTarget struct {
	label    string
	hasTouch bool
	config   map[string]any
}

// benchmarkTargets gives each engine a desktop and a mobile profile it
// accepts: device profiles are bound to one engine, and WebKit has no desktop
// profile, so its desktop run sets the same 1920x1080 viewport.
func benchmarkTargets(engine string) []benchmarkTarget {
	if engine == "webkit" {
		return []benchmarkTarget{
			{label: "desktop_1920x1080", hasTouch: false, config: map[string]any{"browser_viewport_width": 1920, "browser_viewport_height": 1080}},
			{label: "iphone_15_pro", hasTouch: true, config: map[string]any{"browser_device_profile": "iphone_15_pro"}},
		}
	}
	return []benchmarkTarget{
		{label: "desktop_1080p", hasTouch: false, config: map[string]any{"browser_device_profile": "desktop_1080p"}},
		{label: "pixel_8", hasTouch: true, config: map[string]any{"browser_device_profile": "pixel_8"}},
	}
}

// benchmarkColdPool starts the Playwright driver if it is not running and the
// engine to prove it is installed, closes every pooled browser (an idle
// browser of another engine would count toward this one's RSS and CPU) and
// returns the processes left: the baseline a closed browser returns to.
func benchmarkColdPool(t *testing.T, engine string) map[int32]bool {
	t.Helper()
	if _, err := getBrowserInstance(engine, true); err != nil {
		requireBrowserRuntime(t, engine, err)
	}
	globalMutex.Lock()
	pooled := make([]playwright.Browser, 0, len(globalBrowsers))
	for key, browser := range globalBrowsers {
		pooled = append(pooled, browser)
		delete(globalBrowsers, key)
	}
	globalMutex.Unlock()
	for _, browser := range pooled {
		_ = browser.Close()
	}
	return benchmarkSettledPIDs()
}

func benchmarkClosePooledBrowser(engine string) {
	globalMutex.Lock()
	key := browserPoolKey(engine, true, browserQUICAuto)
	pooled := globalBrowsers[key]
	delete(globalBrowsers, key)
	globalMutex.Unlock()
	if pooled != nil {
		_ = pooled.Close()
	}
}

// benchmarkSettledPIDs waits up to two seconds for exiting processes to go.
func benchmarkSettledPIDs() map[int32]bool {
	var pids map[int32]bool
	for wait := 0; wait < 20; wait++ {
		next := map[int32]bool{}
		for _, p := range browserDescendants() {
			next[p.Pid] = true
		}
		if pids != nil && len(next) == len(pids) {
			return next
		}
		pids = next
		time.Sleep(100 * time.Millisecond)
	}
	return pids
}

func benchmarkLeftover(baseline map[int32]bool) int {
	leftover := 0
	for pid := range benchmarkSettledPIDs() {
		if !baseline[pid] {
			leftover++
		}
	}
	return leftover
}

func benchmarkMs(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

func benchmarkMedian(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	middle := len(sorted) / 2
	if len(sorted)%2 == 0 {
		// An even count's median is the mean of its two middle values.
		return (sorted[middle-1] + sorted[middle]) / 2
	}
	return sorted[middle]
}

func TestBenchmarkMedian(t *testing.T) {
	for _, tc := range []struct {
		values []float64
		want   float64
	}{{nil, 0}, {[]float64{3}, 3}, {[]float64{4, 1, 3}, 3}, {[]float64{4, 1, 3, 2}, 2.5}} {
		if got := benchmarkMedian(tc.values); got != tc.want {
			t.Fatalf("benchmarkMedian(%v) = %v, want %v", tc.values, got, tc.want)
		}
	}
}

// browserDescendants are the test process's descendant processes (the
// Playwright driver and the browsers it launched).
func browserDescendants() []*process.Process {
	self, err := process.NewProcess(int32(os.Getpid()))
	if err != nil {
		return nil
	}
	var out []*process.Process
	queue := []*process.Process{self}
	for len(queue) > 0 && len(out) < 512 {
		children, _ := queue[0].Children()
		queue = queue[1:]
		out = append(out, children...)
		queue = append(queue, children...)
	}
	return out
}

// This observed descendant-tree snapshot includes the Playwright driver and
// browser processes, not an independently attributed per-VUser population.
// Missing reads remain explicit; no readable descendants is unavailable.
func benchmarkRSSBytes() (*uint64, int) {
	return benchmarkRSSFromProcesses(browserDescendants(), func(p *process.Process) (uint64, error) {
		memory, err := p.MemoryInfo()
		if err != nil {
			return 0, err
		}
		return memory.RSS, nil
	})
}
func benchmarkRSSFromProcesses(processes []*process.Process, read func(*process.Process) (uint64, error)) (*uint64, int) {
	var total uint64
	measured, missing := 0, 0
	for _, p := range processes {
		value, err := read(p)
		if err != nil {
			missing++
			continue
		}
		if ^uint64(0)-total < value {
			return nil, len(processes)
		}
		total += value
		measured++
	}
	if measured == 0 {
		return nil, missing
	}
	return &total, missing
}

func computeBenchmarkActionOverhead(hasTouch bool, clickBaseline, capturedClick, touchAction []float64) (string, float64) {
	if hasTouch {
		return "touch_over_click_baseline", benchmarkMedian(touchAction) - benchmarkMedian(clickBaseline)
	}
	return "click_evidence_capture_overhead", benchmarkMedian(capturedClick) - benchmarkMedian(clickBaseline)
}

func (b *Browser) sampleBenchmarkVUserMemory(baselineRendererPIDs map[int32]bool, singleContext bool) vuserMemoryAttributionSample {
	descendants := browserDescendants()
	descendantPIDs := make([]int32, 0, len(descendants))
	for _, p := range descendants {
		descendantPIDs = append(descendantPIDs, p.Pid)
	}
	classified, err := b.queryCDPProcessInfo()
	if err != nil {
		return vuserMemoryAttributionSample{MissingProcesses: len(descendantPIDs)}
	}
	var pssReader func(int32) (uint64, error)
	if runtime.GOOS == "linux" {
		pssReader = readLinuxProcessPSSBytes
	}
	return computeAttributedVUserMemory(
		baselineRendererPIDs,
		classified,
		descendantPIDs,
		int32(playwrightDriverPID()),
		singleContext,
		readProcessRSSBytesByPID,
		pssReader,
	)
}

// Count every attempted RSS snapshot, including unavailable ones. Partial and
// unavailable counts cannot be interpreted without this denominator.
func (result *BrowserBenchmarkResult) recordRSSSample(sample *uint64, missing int) {
	result.RSSSamples++
	if sample == nil {
		result.RSSUnavailableSamples++
		return
	}
	if result.PeakRSSBytes == nil || *sample > *result.PeakRSSBytes {
		value := *sample
		result.PeakRSSBytes = &value
	}
	if missing > 0 {
		result.RSSPartialSamples++
	}
}

func (result *BrowserBenchmarkResult) recordWebKitUnavailableRSSSample() {
	result.RSSAttribution = BrowserRSSAttributionUnavailable
	result.RSSPopulation = BrowserRSSAttributionUnavailable
	result.PerVUserRSSBytes = nil
	result.PerVUserPSSBytes = nil
	result.FixedOverheadBytes = nil
	result.PeakRSSBytes = nil
	result.recordRSSSample(nil, 0)
}

func (result *BrowserBenchmarkResult) recordAttributedRSSSample(sample vuserMemoryAttributionSample) {
	result.RSSAttribution = BrowserRSSAttributionCDPRendererPerVUser
	if sample.PerVUserRSSBytes == nil || sample.FixedOverheadBytes == nil {
		result.recordRSSSample(nil, sample.MissingProcesses)
		return
	}
	result.recordRSSSample(sample.TotalDescendantRSS, sample.MissingProcesses)
	if result.PerVUserRSSBytes == nil || *sample.PerVUserRSSBytes > *result.PerVUserRSSBytes {
		v := *sample.PerVUserRSSBytes
		result.PerVUserRSSBytes = &v
	}
	if sample.PerVUserPSSBytes != nil && (result.PerVUserPSSBytes == nil || *sample.PerVUserPSSBytes > *result.PerVUserPSSBytes) {
		v := *sample.PerVUserPSSBytes
		result.PerVUserPSSBytes = &v
	}
	if result.FixedOverheadBytes == nil || *sample.FixedOverheadBytes > *result.FixedOverheadBytes {
		v := *sample.FixedOverheadBytes
		result.FixedOverheadBytes = &v
	}
}

func benchmarkRSSNote(result BrowserBenchmarkResult) string {
	if result.PerVUserRSSBytes != nil {
		return fmt.Sprintf("%.0f MiB", float64(*result.PerVUserRSSBytes)/(1<<20))
	}
	if result.PeakRSSBytes == nil {
		return "n/a"
	}
	return fmt.Sprintf("%.0f MiB", float64(*result.PeakRSSBytes)/(1<<20))
}

func benchmarkCPUByPID() map[int32]float64 {
	usage := map[int32]float64{}
	for _, p := range browserDescendants() {
		if times, err := p.Times(); err == nil {
			usage[p.Pid] = times.User + times.System
		}
	}
	return usage
}

// benchmarkCPU totals the CPU seconds the process tree spent after start:
// each process's last cumulative reading less its reading at start, so a
// long-lived process is not counted again at every sample and a short-lived
// one counts up to its last sample.
type benchmarkCPU struct {
	start, last map[int32]float64
}

func (c *benchmarkCPU) sample() {
	if c.last == nil {
		c.last = map[int32]float64{}
	}
	for pid, seconds := range benchmarkCPUByPID() {
		c.last[pid] = max(c.last[pid], seconds)
	}
}

func (c *benchmarkCPU) total() float64 {
	var total float64
	for pid, seconds := range c.last {
		total += max(0, seconds-c.start[pid])
	}
	return total
}

// benchmarkHost describes the machine and driver the results were measured
// on; each result names its browser version.
func benchmarkHost() map[string]any {
	info := map[string]any{"goos": runtime.GOOS, "goarch": runtime.GOARCH, "logical_cpus": runtime.NumCPU(), "go_version": runtime.Version()}
	if hostInfo, err := host.Info(); err == nil {
		info["platform"], info["platform_version"], info["kernel_version"] = hostInfo.Platform, hostInfo.PlatformVersion, hostInfo.KernelVersion
	}
	if cpus, err := cpu.Info(); err == nil && len(cpus) > 0 {
		info["cpu_model"] = cpus[0].ModelName
	}
	if memory, err := mem.VirtualMemory(); err == nil {
		info["memory_bytes"] = memory.Total
	}
	if driver, err := playwright.NewDriver(); err == nil {
		info["playwright_driver_version"] = driver.Version
	}
	return info
}

type browserBenchmarkArtifact struct {
	Schema      string                   `json:"schema"`
	GeneratedAt time.Time                `json:"generated_at"`
	Host        map[string]any           `json:"host"`
	Results     []BrowserBenchmarkResult `json:"results"`
}

func parseBrowserBenchmarkArtifact(data []byte) (browserBenchmarkArtifact, error) {
	var doc browserBenchmarkArtifact
	if err := json.Unmarshal(data, &doc); err != nil {
		return browserBenchmarkArtifact{}, err
	}
	if doc.Schema != BrowserBenchmarkSchemaV2 && doc.Schema != BrowserBenchmarkSchemaV3 {
		return browserBenchmarkArtifact{}, fmt.Errorf("unsupported browser benchmark schema %q", doc.Schema)
	}
	return doc, nil
}

// writeBrowserBenchmark writes the results and one F4 feature acceptance
// request per engine and profile, naming the results' SHA-256.
func writeBrowserBenchmark(t *testing.T, dir string, results []BrowserBenchmarkResult) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body, err := json.MarshalIndent(map[string]any{"schema": BrowserBenchmarkSchemaV3, "generated_at": time.Now().UTC(), "host": benchmarkHost(), "results": results}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "browser-benchmark.json")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	t.Logf("browser benchmark written to %s (sha256 %x)", path, sum)
	// A request names the measured commit; without one there is nothing an
	// operator could truthfully record.
	commit := strings.ToLower(strings.TrimSpace(os.Getenv("APEXLOAD_COMMIT_SHA")))
	if !regexp.MustCompile(`^[0-9a-f]{7,64}$`).MatchString(commit) {
		t.Logf("APEXLOAD_COMMIT_SHA is not a commit; no acceptance requests written")
		return
	}
	var requests []map[string]any
	for _, result := range results {
		// A benchmark never claims a pass on its own: the published targets
		// (launch, action overhead, RSS per VUser) need a reviewer, so a
		// stable run is pending review and an unstable one failed.
		status := "pending"
		if result.Failures > 0 || result.LeftoverProcesses > 0 || result.ThrottleSeparationMs < float64(result.ThrottleLatencyMs)/2 {
			status = "failed"
		}
		notes := fmt.Sprintf("%s %s %s: cold %.0f ms, session %.0f ms, nav %.0f ms, capture %+.0f ms, throttle %+.0f/%d ms, RSS %s, CPU %.1f s, cleanup %.0f ms, leftover %d. Five-VUser ceiling stands.",
			result.Engine, result.BrowserVersion, result.Profile, result.ColdLaunchMs, result.SessionStartMs, result.ActionMs, result.CaptureOverheadMs, result.ThrottleSeparationMs, result.ThrottleLatencyMs, benchmarkRSSNote(result), result.CPUSeconds, result.CleanupMs, result.LeftoverProcesses)
		if len(notes) > 256 {
			t.Errorf("acceptance notes exceed the 256 bytes a record accepts: %q", notes)
		}
		requests = append(requests, map[string]any{
			"requirement_id": "BE-4", "feature": "browser-benchmark", "profile": strings.ReplaceAll(result.Engine+"-"+result.Profile, "_", "-"),
			"platform": result.Platform, "commit_sha": commit, "suite": "pkg/protocol/browser TestBrowserBenchmark", "result": status,
			"artifacts": []map[string]string{{"name": "browser-benchmark.json", "sha256": hex.EncodeToString(sum[:])}},
			"notes":     notes,
		})
	}
	records, err := json.MarshalIndent(requests, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "browser-benchmark-acceptance-requests.json"), records, 0o644); err != nil {
		t.Fatal(err)
	}
}

// The acceptance requests a benchmark writes name the measured commit, are
// pending review when the run was stable and failed when it was not (never
// passed by themselves), and carry the F4 shape: a slug profile, the results'
// digest and notes within 256 bytes.
func TestBrowserBenchmarkRequestsArePendingOrFailed(t *testing.T) {
	dir := t.TempDir()
	stable := BrowserBenchmarkResult{Engine: "chromium", Profile: "desktop_1080p", Platform: "darwin-arm64", BrowserVersion: "149.0.7827.55", Iterations: 5, ThrottleLatencyMs: 200, ThrottleSeparationMs: 199,
		ColdLaunchMs: 9999.9, SessionStartMs: 9999.9, ActionMs: 9999.9, CaptureOverheadMs: -9999.9, PeakRSSBytes: func() *uint64 { v := uint64(104857495142); return &v }(), RSSPopulation: "observed_test_process_descendants", CPUSeconds: 9999.9, CleanupMs: 9999.9}
	unstable := stable
	unstable.Engine, unstable.Profile, unstable.Failures = "webkit", "iphone_15_pro", 1
	requests := filepath.Join(dir, "browser-benchmark-acceptance-requests.json")
	t.Setenv("APEXLOAD_COMMIT_SHA", "")
	writeBrowserBenchmark(t, dir, []BrowserBenchmarkResult{stable, unstable})
	if _, err := os.Stat(requests); !os.IsNotExist(err) {
		t.Fatalf("requests were written without a commit: %v", err)
	}
	t.Setenv("APEXLOAD_COMMIT_SHA", strings.Repeat("a", 40))
	writeBrowserBenchmark(t, dir, []BrowserBenchmarkResult{stable, unstable})
	raw, err := os.ReadFile(requests)
	if err != nil {
		t.Fatal(err)
	}
	var written []struct {
		RequirementID string `json:"requirement_id"`
		Profile       string `json:"profile"`
		Result        string `json:"result"`
		CommitSHA     string `json:"commit_sha"`
		Notes         string `json:"notes"`
		Artifacts     []struct {
			SHA256 string `json:"sha256"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal(raw, &written); err != nil {
		t.Fatal(err)
	}
	results, err := os.ReadFile(filepath.Join(dir, "browser-benchmark.json"))
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(results)
	slug := regexp.MustCompile(`^[a-z0-9][a-z0-9._/-]{0,127}$`)
	want := map[string]string{"chromium-desktop-1080p": "pending", "webkit-iphone-15-pro": "failed"}
	if len(written) != 2 {
		t.Fatalf("requests = %+v", written)
	}
	for _, request := range written {
		if want[request.Profile] != request.Result || request.RequirementID != "BE-4" || request.CommitSHA != strings.Repeat("a", 40) || !slug.MatchString(request.Profile) ||
			len(request.Notes) > 256 || len(request.Artifacts) != 1 || request.Artifacts[0].SHA256 != hex.EncodeToString(digest[:]) {
			t.Fatalf("request = %+v", request)
		}
	}
}
