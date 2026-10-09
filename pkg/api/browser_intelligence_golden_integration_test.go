package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"maps"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/protocol/browser"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Browser intelligence golden acceptance (BI-4, E7). A controlled page with
// known layout shifts, a hero image the fixture server holds, and a tap
// target runs in Chromium and WebKit. The page's real document and
// interaction evidence and its actions' results go through the archive
// writer, and the archive is read by the code that serves findings,
// interaction patterns and journeys. Every result is checked against ground
// truth measured independently of the product:
//   - the page's own PerformanceObserver entries: an engine whose
//     supportedEntryTypes lacks an entry type must report the metric
//     unavailable as unsupported, never a value;
//   - the fixture server's clock: the hero is held for biGoldenHeroHold, so
//     LCP cannot be earlier;
//   - the fixture server's request counter: capture and analysis add no
//     target traffic.
//
// Document evidence is published three times (a mid-run snapshot, then the
// final evidence twice), as a generator's repeated publications can be, and
// must be counted once.

const biGoldenHeroHold = 300 * time.Millisecond

const biGoldenPage = `<!doctype html><html><head><title>browser intelligence golden</title><link rel="icon" href="data:,">
<style>body{margin:0;font:16px sans-serif}#banner{height:0;background:#fc0}#hero{display:block}#add{width:160px;height:48px}</style></head>
<body><div id="banner"></div><img id="hero" src="/hero.png" width="640" height="360" alt="hero"><p>golden page</p><button id="add">Add to cart</button>
<script>
const supported=(PerformanceObserver.supportedEntryTypes||[]).slice();
window.oracle={supported,shifts:[],lcp:[],fcp:null};
if(supported.includes('layout-shift'))new PerformanceObserver(l=>{for(const e of l.getEntries())if(!e.hadRecentInput)oracle.shifts.push({value:e.value,time:e.startTime})}).observe({type:'layout-shift',buffered:true});
if(supported.includes('largest-contentful-paint'))new PerformanceObserver(l=>{for(const e of l.getEntries())oracle.lcp.push(e.startTime)}).observe({type:'largest-contentful-paint',buffered:true});
if(supported.includes('paint'))new PerformanceObserver(l=>{for(const e of l.getEntries())if(e.name==='first-contentful-paint')oracle.fcp=e.startTime}).observe({type:'paint',buffered:true});
// Two shifts 300 ms apart form one session window; a third 1.4 s later
// starts another, so CLS (the largest window) differs from the sum.
setTimeout(()=>{document.getElementById('banner').style.height='80px'},900);
setTimeout(()=>{document.getElementById('banner').style.height='160px'},1200);
setTimeout(()=>{document.getElementById('banner').style.height='0px'},2600);
</script></body></html>`

// biGoldenOracle is what the page measured itself.
type biGoldenOracle struct {
	Supported []string `json:"supported"`
	Shifts    []struct {
		Value float64 `json:"value"`
		Time  float64 `json:"time"`
	} `json:"shifts"`
	LCP []float64 `json:"lcp"`
	FCP *float64  `json:"fcp"`
}

var biGoldenEntryTypes = map[string]string{"cls": "layout-shift", "lcp": "largest-contentful-paint", "fcp": "paint"}

func (o biGoldenOracle) supports(metric string) bool {
	return slices.Contains(o.Supported, biGoldenEntryTypes[metric])
}

// complete reports whether the page has measured everything its engine
// supports: three shifts, an LCP entry and FCP.
func (o biGoldenOracle) complete() bool {
	return (!o.supports("cls") || len(o.Shifts) >= 3) && (!o.supports("lcp") || len(o.LCP) > 0) && (!o.supports("fcp") || o.FCP != nil)
}

// maxSessionWindow is CLS as the Layout Instability definition (and
// web-vitals) computes it: the largest sum of shifts in a session window,
// shifts less than one second apart within five seconds.
func (o biGoldenOracle) maxSessionWindow() (largest, sum float64) {
	var session, start, last float64
	for i, shift := range o.Shifts {
		sum += shift.Value
		if i > 0 && shift.Time-last < 1000 && shift.Time-start < 5000 {
			session += shift.Value
		} else {
			session, start = shift.Value, shift.Time
		}
		last = shift.Time
		largest = max(largest, session)
	}
	return largest, sum
}

// expected is the oracle's value of metric, or nil when the engine cannot
// measure it.
func (o biGoldenOracle) expected(metric string) *float64 {
	if !o.supports(metric) {
		return nil
	}
	switch metric {
	case "cls":
		value, _ := o.maxSessionWindow()
		return &value
	case "lcp":
		if len(o.LCP) == 0 {
			return nil
		}
		value := o.LCP[len(o.LCP)-1]
		return &value
	case "fcp":
		return o.FCP
	}
	return nil
}

// biGoldenEngineResult is one engine's golden outcome, written for the F4
// acceptance requests.
type biGoldenEngineResult struct {
	Engine         string              `json:"engine"`
	BrowserVersion string              `json:"browser_version"`
	Platform       string              `json:"platform"`
	Failed         bool                `json:"failed"`
	Unsupported    []string            `json:"unsupported"`
	Oracle         map[string]*float64 `json:"oracle"`
	Measured       map[string]*float64 `json:"measured"`
	ScoreSeverity  string              `json:"score_severity"`
	UnscoredReason string              `json:"unscored_reason,omitempty"`
	ActiveFindings []string            `json:"active_findings"`
	RepeatedTaps   int64               `json:"repeated_taps"`
	JourneyFailed  int64               `json:"journey_failed"`
	TargetRequests map[string]int      `json:"target_requests"`
}

func biGoldenHeroPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 640, 360))
	for y := 0; y < 360; y++ {
		for x := 0; x < 640; x++ {
			img.Set(x, y, color.RGBA{R: 30, G: 90, B: 200, A: 255})
		}
	}
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestBrowserIntelligenceGoldenAcceptance(t *testing.T) {
	switch strings.TrimSpace(os.Getenv("APEXLOAD_BROWSER_TESTS")) {
	case "require":
	case "", "auto", "skip":
		t.Skip("the browser intelligence golden requires APEXLOAD_BROWSER_TESTS=require")
	default:
		t.Fatal("invalid APEXLOAD_BROWSER_TESTS value; use require, auto or skip")
	}
	var results []biGoldenEngineResult
	for _, engine := range []string{"chromium", "webkit"} {
		result := biGoldenEngineResult{Engine: engine, Platform: runtime.GOOS + "-" + runtime.GOARCH, Oracle: map[string]*float64{}, Measured: map[string]*float64{}}
		t.Run(engine, func(t *testing.T) {
			defer func() { result.Failed = t.Failed() }()
			runBrowserIntelligenceGolden(t, engine, &result)
		})
		results = append(results, result)
	}
	if dir := strings.TrimSpace(os.Getenv("APEXLOAD_BI_ACCEPTANCE_OUT")); dir != "" {
		writeBrowserIntelligenceAcceptance(t, dir, results)
	}
}

func runBrowserIntelligenceGolden(t *testing.T, engine string, out *biGoldenEngineResult) {
	hero := biGoldenHeroPNG(t)
	var mu sync.Mutex
	requests := map[string]int{}
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests[r.URL.Path]++
		mu.Unlock()
		w.Header().Set("Cache-Control", "no-store")
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, biGoldenPage)
		case "/hero.png":
			time.Sleep(biGoldenHeroHold)
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(hero)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(fixture.Close)
	targetRequests := func() map[string]int {
		mu.Lock()
		defer mu.Unlock()
		return maps.Clone(requests)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	page := &browser.Browser{}
	if err := page.Init(ctx, map[string]any{
		"base_url": fixture.URL, "browser_engine": engine, "browser_headless": true,
		"browser_capture_document_metrics": true, "browser_capture_interactions": true, "browser_touch_mode": true,
		"browser_runtime_new_user_policy": "persistent_context", "browser_viewport_width": 800, "browser_viewport_height": 900,
	}); err != nil {
		t.Fatalf("%s runtime initialization failed (APEXLOAD_BROWSER_TESTS=require needs it installed): %v", engine, err)
	}
	tornDown := false
	t.Cleanup(func() {
		if !tornDown {
			cleanup, done := context.WithTimeout(context.Background(), 15*time.Second)
			defer done()
			_ = page.Teardown(cleanup)
		}
	})

	// The script: open the shop, add to cart three times (synthetic taps),
	// then wait for a confirmation that never appears.
	const lifetime = 41
	var results []*protocol.Result
	execute := func(action *protocol.Action, wantSuccess bool) {
		t.Helper()
		result, err := page.Execute(ctx, action)
		if result == nil {
			t.Fatalf("%s returned no result: %v", action.Name, err)
		}
		if result.Success != wantSuccess {
			t.Fatalf("%s success = %v, want %v (%v %s)", action.Name, result.Success, wantSuccess, err, result.ErrorMessage)
		}
		result.Group, result.VUserID, result.Iteration, result.VUserLifetime = "golden", 1, 1, lifetime
		results = append(results, result)
	}
	// readOracle reads what the page measured itself. Evaluating script in
	// the page sends nothing to the target.
	readOracle := func() biGoldenOracle {
		t.Helper()
		result, err := page.Execute(ctx, &protocol.Action{Name: "read oracle", Type: "browser_evaluate_js", Target: "JSON.stringify(window.oracle)"})
		if err != nil || result == nil || !result.Success {
			t.Fatalf("oracle read failed: %v", err)
		}
		var oracle biGoldenOracle
		if err := json.Unmarshal([]byte(result.Extractions["result"]), &oracle); err != nil {
			t.Fatalf("oracle %q: %v", result.Extractions["result"], err)
		}
		return oracle
	}

	execute(&protocol.Action{Name: "open shop", Type: "browser_navigate", Target: "/", Params: map[string]any{"wait_until": "load"}}, true)
	var oracle biGoldenOracle
	deadline := time.Now().Add(15 * time.Second)
	for oracle = readOracle(); !oracle.complete(); oracle = readOracle() {
		if time.Now().After(deadline) {
			t.Fatalf("the page never measured what %s supports: %+v", engine, oracle)
		}
		time.Sleep(100 * time.Millisecond)
	}
	// A mid-run publication: the generator publishes evidence while the
	// document is still open (provisional observations).
	midRun := page.BrowserDocumentEvidence().Clone()
	for i := 0; i < 3; i++ {
		execute(&protocol.Action{Name: "Add to cart", Type: "browser_click", Selector: "#add"}, true)
	}
	execute(&protocol.Action{Name: "confirm order", Type: "browser_wait", WaitFor: &protocol.WaitCondition{Type: "selector", Value: "#confirmation", TimeoutMs: 400}}, false)
	interactions := page.TakeBrowserInteractionEvidence()
	tornDown = true
	if err := page.Teardown(ctx); err != nil {
		t.Fatalf("teardown: %v", err)
	}
	interactions = append(interactions, page.TakeBrowserInteractionEvidence()...)
	final := page.BrowserDocumentEvidence().Clone()
	if midRun == nil || final == nil {
		t.Fatal("the runtime published no document evidence")
	}
	for _, observation := range final.Observations {
		if observation.Cohort != nil && observation.Cohort.BrowserVersion != "" {
			out.BrowserVersion = observation.Cohort.BrowserVersion
		}
	}

	// Archive the run as the controller does.
	root := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	t.Setenv(envArchiveAdjunctMinFreeBytes, "1")
	runID := "bi-golden-" + engine
	started := results[0].StartTime
	managed := &ManagedTest{ID: runID, Name: "Browser intelligence golden (" + engine + ")", ProjectID: storage.DefaultProjectID, Groups: []TestGroup{{Name: "golden", BrowserDocumentCaptureRequested: true}}}
	writer, err := newRunArchiveWriter(managed, RunArchiveProvenance{ProjectID: storage.DefaultProjectID, RequestedAt: started, StartedAt: &started})
	if err != nil {
		t.Fatal(err)
	}
	finalized := false
	t.Cleanup(func() {
		if !finalized {
			_, _ = writer.Finalize("FAILED", time.Now().UTC(), "golden stopped before publication", false, false, nil)
		}
	})
	for _, result := range results {
		if err := writer.RecordResult(result); err != nil {
			t.Fatal(err)
		}
	}
	// The iteration row the engine records when an iteration ends on its own
	// (the script continues past the failed wait and ends).
	last := results[len(results)-1]
	iterationEnd := &protocol.Result{ActionName: "Iteration", BusinessTransaction: true, Success: false, StartTime: started, Duration: last.StartTime.Add(last.Duration).Sub(started),
		Group: "golden", VUserID: 1, Iteration: 1, VUserLifetime: lifetime, EndsIteration: true}
	if err := writer.RecordResult(iterationEnd); err != nil {
		t.Fatal(err)
	}
	for _, evidence := range []*protocol.BrowserDocumentEvidence{midRun, final, final} {
		if err := writer.RecordBrowserDocumentEvidence(evidence); err != nil {
			t.Fatal(err)
		}
	}
	for _, evidence := range interactions {
		if err := writer.RecordBrowserInteractionEvidence(evidence); err != nil {
			t.Fatal(err)
		}
	}
	archive, err := writer.Finalize("COMPLETE", time.Now().UTC(), "", false, false, nil)
	finalized = true
	if err != nil {
		t.Fatal(err)
	}
	server, store := baselineTestServer(t)
	if err := store.SaveTest(&storage.TestRecord{ID: runID, Name: managed.Name, ProjectID: storage.DefaultProjectID, ScenarioID: "scenario", Status: "COMPLETE", Groups: []any{}, StartTime: started}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRunArchive(archive); err != nil {
		t.Fatal(err)
	}
	ended := time.Now().UTC()
	server.runner.mu.Lock()
	server.runner.tests[runID] = &ManagedTest{ID: runID, Name: managed.Name, ProjectID: storage.DefaultProjectID, Status: "COMPLETE", StartedAt: &started, EndedAt: &ended}
	server.runner.archives[runID] = archive
	server.runner.mu.Unlock()
	trafficBeforeAnalysis := targetRequests()

	// Document metrics against the page's own measurements.
	for _, metric := range []string{"cls", "lcp", "fcp"} {
		out.Oracle[metric] = oracle.expected(metric)
		if !oracle.supports(metric) {
			out.Unsupported = append(out.Unsupported, metric)
		}
	}
	if largest, sum := oracle.maxSessionWindow(); oracle.supports("cls") && !(largest > 0 && largest < sum) {
		t.Fatalf("the layout shifts do not separate the largest session window (%g) from the sum (%g)", largest, sum)
	}
	if oracle.supports("lcp") && *out.Oracle["lcp"] < float64(biGoldenHeroHold/time.Millisecond) {
		t.Fatalf("the page's own LCP %.1f ms is earlier than the server held the hero (%v)", *out.Oracle["lcp"], biGoldenHeroHold)
	}
	// The golden page is the context's last main-frame document; the blank
	// document every context starts with precedes it and measures nothing.
	golden, goldenStart := "", time.Time{}
	for _, observation := range final.Observations {
		if observation.FrameScope == "main_frame" && observation.LifecyclePhase == "navigation" && observation.WindowStart.After(goldenStart) {
			golden, goldenStart = observation.DocumentID, observation.WindowStart
		}
	}
	finals := map[string]int{}
	for _, observation := range final.Observations {
		if observation.FrameScope != "main_frame" || observation.LifecyclePhase != "navigation" || observation.State != "final" {
			continue
		}
		if observation.DocumentID != golden {
			if observation.Available || observation.Value != nil {
				t.Errorf("%s: the initial blank document reported a value: %+v", engine, observation)
			}
			continue
		}
		expected, known := out.Oracle[observation.Metric]
		if !known {
			continue
		}
		finals[observation.Metric]++
		if !oracle.supports(observation.Metric) {
			if observation.Available || observation.Value != nil || observation.UnavailableReason != "unsupported" {
				t.Errorf("%s: %s is not in the engine's supportedEntryTypes but was reported %+v", engine, observation.Metric, observation)
			}
			continue
		}
		if !observation.Available || !observation.Qualified || observation.Value == nil {
			t.Errorf("%s: %s final observation is not a qualified value: %+v", engine, observation.Metric, observation)
			continue
		}
		out.Measured[observation.Metric] = observation.Value
		tolerance := 1.0 // ms
		if observation.Metric == "cls" {
			tolerance = 1e-9
		}
		if expected == nil || math.Abs(*observation.Value-*expected) > tolerance {
			t.Errorf("%s: %s = %v, the page measured %v", engine, observation.Metric, *observation.Value, biGoldenValue(expected))
		}
	}
	for _, metric := range []string{"cls", "lcp", "fcp"} {
		if finals[metric] != 1 {
			t.Errorf("%s: %d final main-frame %s observations, want 1", engine, finals[metric], metric)
		}
	}

	// Findings and score under a policy that makes the held hero and the
	// shifts poor and FCP good.
	policy := BrowserPolicyDefinition{Version: 1, Baseline: BrowserPolicyBaseline{Mode: "none"}, Metrics: []BrowserPolicyMetric{
		{Metric: "lcp", Good: 100, Poor: 200, Weight: 2},
		{Metric: "cls", Good: 0.001, Poor: 0.002, Weight: 1},
		{Metric: "fcp", Good: 60000, Poor: 120000, Weight: 1},
	}}
	definition, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeAnalysisPolicy(storage.AnalysisDefinitionKindBrowserPolicy, definition); err != nil {
		t.Fatalf("golden policy is invalid: %v", err)
	}
	findings, err := evaluateArchiveBrowserFindings(context.Background(), &browserIntelligenceInputs{runID: runID, record: archive, policy: policy,
		ref: BrowserPolicyRef{ID: "golden", Name: "Golden vitals", Revision: 1, Source: "requested"}})
	if err != nil {
		t.Fatal(err)
	}
	out.ScoreSeverity, out.UnscoredReason = findings.Score.Severity, findings.Score.UnscoredReason
	for _, finding := range findings.Findings {
		if finding.State == "active" {
			out.ActiveFindings = append(out.ActiveFindings, finding.RuleID+":"+finding.Metric)
		}
		if finding.Category == browserFindingCategoryCollection {
			t.Errorf("%s: repeated publications were judged incomplete: %+v", engine, finding)
		}
	}
	for _, metric := range []string{"lcp", "cls", "fcp"} {
		finding := biGoldenFinding(findings, metric)
		switch {
		case !oracle.supports(metric):
			if finding == nil || finding.State != "unscored" || finding.Reason != "no_qualified_final_observations" || finding.Quality.ExcludedReasons["unsupported"] != 1 {
				t.Errorf("%s: unsupported %s finding %+v", engine, metric, finding)
			}
		case metric == "fcp":
			if finding != nil {
				t.Errorf("%s: FCP within the good bound became a finding: %+v", engine, finding)
			}
		default:
			if finding == nil || finding.State != "active" || finding.Severity != "poor" || finding.Observed == nil || *finding.Observed != *out.Measured[metric] ||
				finding.Quality.EligibleFinal != 1 || len(finding.Evidence.Documents) != 1 {
				t.Errorf("%s: %s finding %+v", engine, metric, finding)
			}
		}
	}
	missing := []string{}
	for _, metric := range []string{"lcp", "cls", "fcp"} {
		if !oracle.supports(metric) {
			missing = append(missing, metric)
		}
	}
	if len(missing) == 0 {
		if findings.Score.Score == nil || findings.Score.Coverage.Observed != 3 || findings.Score.Coverage.Required != 3 {
			t.Errorf("%s: score %+v", engine, findings.Score)
		}
	} else if findings.Score.Score != nil || findings.Score.UnscoredReason != "missing_required_metric" || !slices.Equal(findings.Score.Missing, missing) {
		t.Errorf("%s: an engine without %v was scored: %+v", engine, missing, findings.Score)
	}

	// Interaction patterns: three synthetic taps on one action.
	analysis, err := server.runArchiveBrowserInteractions(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	for _, pattern := range analysis.Patterns {
		if pattern.Kind == interactionPatternScriptedRepetition && pattern.ActionName == "Add to cart" {
			out.RepeatedTaps = int64(pattern.Count)
		}
	}
	if analysis.Status != "ready" || out.RepeatedTaps != 3 || analysis.Coverage.Excluded[protocol.InteractionUnavailableSyntheticDispatch] != 3 || len(analysis.Heatmaps) != 0 {
		t.Errorf("%s: interactions %+v", engine, analysis)
	}

	// Journey accounting: the confirmation never appeared.
	manifest, err := loadRunArchiveManifestFromRecord(archive)
	if err != nil {
		t.Fatal(err)
	}
	journeyPolicy := JourneyPolicyDefinition{Version: 1, Journeys: []JourneyDefinition{{ID: "checkout", Name: "Checkout", Start: JourneyBoundary{Action: "open shop"}, Success: JourneyBoundary{Action: "confirm order"}, Weight: 1}}}
	journeys, err := accountJourneys(context.Background(), archive, manifest, journeyPolicy, JourneyPolicyRef{ID: "golden-journeys", Revision: 1, Source: "requested"})
	if err != nil {
		t.Fatal(err)
	}
	if len(journeys.Journeys) != 1 {
		t.Fatalf("%s: journeys %+v", engine, journeys)
	}
	checkout := journeys.Journeys[0]
	out.JourneyFailed = checkout.Failed
	if checkout.Eligible != 1 || checkout.Failed != 1 || checkout.Completed != 0 || checkout.Censored != 0 || journeys.WeightedFailureIndex == nil || *journeys.WeightedFailureIndex != 1 {
		t.Errorf("%s: checkout %+v (index %v)", engine, checkout, journeys.WeightedFailureIndex)
	}

	// Capture and analysis added no target traffic: the page and its hero,
	// each once.
	out.TargetRequests = targetRequests()
	want := map[string]int{"/": 1, "/hero.png": 1}
	if !maps.Equal(trafficBeforeAnalysis, want) || !maps.Equal(out.TargetRequests, want) {
		t.Errorf("%s: target requests %v before analysis, %v after; want %v", engine, trafficBeforeAnalysis, out.TargetRequests, want)
	}
	t.Logf("%s %s: oracle %v measured %v unsupported %v score %s%s active %v repeated taps %d journey failed %d", engine, out.BrowserVersion,
		biGoldenValues(out.Oracle), biGoldenValues(out.Measured), out.Unsupported, out.ScoreSeverity, biGoldenPrefixed(" ", out.UnscoredReason), out.ActiveFindings, out.RepeatedTaps, out.JourneyFailed)
}

func biGoldenFinding(result *BrowserIntelligenceResult, metric string) *BrowserFinding {
	for index := range result.Findings {
		finding := &result.Findings[index]
		if finding.RuleID == browserFindingRuleThreshold && finding.Metric == metric {
			return finding
		}
	}
	return nil
}

// writeBrowserIntelligenceAcceptance writes the golden results and one F4
// feature acceptance request per engine, naming the results' SHA-256. A
// request is pending review when the engine's golden held and failed when it
// did not: a golden run never records a pass by itself, since a reviewer
// records acceptance against a release binary.
func writeBrowserIntelligenceAcceptance(t *testing.T, dir string, results []biGoldenEngineResult) {
	t.Helper()
	body, err := json.MarshalIndent(map[string]any{"schema": "apexload.browser_intelligence_golden.v1", "generated_at": time.Now().UTC(), "results": results}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "browser-intelligence-golden.json")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	t.Logf("browser intelligence golden written to %s (sha256 %x)", path, sum)
	commit := strings.ToLower(strings.TrimSpace(os.Getenv("APEXLOAD_COMMIT_SHA")))
	if !regexp.MustCompile(`^[0-9a-f]{7,64}$`).MatchString(commit) {
		t.Logf("APEXLOAD_COMMIT_SHA is not a commit; no acceptance requests written")
		return
	}
	var requests []map[string]any
	for _, result := range results {
		status := "pending"
		if result.Failed {
			status = "failed"
		}
		notes := fmt.Sprintf("%s %s: unsupported %v; score %s%s; active %d; repeated taps %d; journey failed %d; target requests %d.",
			result.Engine, result.BrowserVersion, result.Unsupported, result.ScoreSeverity, biGoldenPrefixed(" ", result.UnscoredReason), len(result.ActiveFindings), result.RepeatedTaps, result.JourneyFailed, biGoldenRequestTotal(result.TargetRequests))
		if len(notes) > 256 {
			t.Errorf("acceptance notes exceed the 256 bytes a record accepts: %q", notes)
		}
		requests = append(requests, map[string]any{
			"requirement_id": "BI-4", "feature": "browser-intelligence-golden", "profile": result.Engine,
			"platform": result.Platform, "commit_sha": commit, "suite": "pkg/api TestBrowserIntelligenceGoldenAcceptance", "result": status,
			"artifacts": []map[string]string{{"name": "browser-intelligence-golden.json", "sha256": hex.EncodeToString(sum[:])}},
			"notes":     notes,
		})
	}
	records, err := json.MarshalIndent(requests, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "browser-intelligence-acceptance-requests.json"), records, 0o644); err != nil {
		t.Fatal(err)
	}
}

// The acceptance requests the golden writes name the measured commit, are
// pending review when an engine's golden held and failed when it did not
// (never passed by themselves), and carry the F4 shape.
func TestBrowserIntelligenceAcceptanceRequestsArePendingOrFailed(t *testing.T) {
	dir := t.TempDir()
	cls := 0.25
	held := biGoldenEngineResult{Engine: "chromium", BrowserVersion: "149.0.7827.55", Platform: "darwin-arm64", Oracle: map[string]*float64{"cls": &cls}, Measured: map[string]*float64{"cls": &cls},
		ScoreSeverity: "poor", ActiveFindings: []string{"threshold:lcp", "threshold:cls"}, RepeatedTaps: 3, JourneyFailed: 1, TargetRequests: map[string]int{"/": 1, "/hero.png": 1}}
	broken := held
	broken.Engine, broken.Failed, broken.Unsupported, broken.ScoreSeverity, broken.UnscoredReason = "webkit", true, []string{"cls", "lcp"}, "unscored", "missing_required_metric"
	requests := filepath.Join(dir, "browser-intelligence-acceptance-requests.json")
	t.Setenv("APEXLOAD_COMMIT_SHA", "")
	writeBrowserIntelligenceAcceptance(t, dir, []biGoldenEngineResult{held, broken})
	if _, err := os.Stat(requests); !os.IsNotExist(err) {
		t.Fatalf("requests were written without a commit: %v", err)
	}
	t.Setenv("APEXLOAD_COMMIT_SHA", strings.Repeat("b", 40))
	writeBrowserIntelligenceAcceptance(t, dir, []biGoldenEngineResult{held, broken})
	raw, err := os.ReadFile(requests)
	if err != nil {
		t.Fatal(err)
	}
	var written []struct {
		RequirementID string `json:"requirement_id"`
		Feature       string `json:"feature"`
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
	results, err := os.ReadFile(filepath.Join(dir, "browser-intelligence-golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(results)
	if len(written) != 2 || written[0].Result != "pending" || written[1].Result != "failed" {
		t.Fatalf("requests %+v", written)
	}
	for _, request := range written {
		if request.RequirementID != "BI-4" || request.Feature != "browser-intelligence-golden" || request.CommitSHA != strings.Repeat("b", 40) || len(request.Notes) > 256 ||
			len(request.Artifacts) != 1 || request.Artifacts[0].SHA256 != hex.EncodeToString(digest[:]) || !regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`).MatchString(request.Profile) {
			t.Fatalf("request %+v", request)
		}
	}
}

func biGoldenValue(value *float64) any {
	if value == nil {
		return nil
	}
	return *value
}

func biGoldenValues(values map[string]*float64) map[string]any {
	out := make(map[string]any, len(values))
	for key, value := range values {
		out[key] = biGoldenValue(value)
	}
	return out
}

func biGoldenPrefixed(prefix, value string) string {
	if value == "" {
		return ""
	}
	return prefix + value
}

func biGoldenRequestTotal(requests map[string]int) int {
	total := 0
	for _, count := range requests {
		total += count
	}
	return total
}
