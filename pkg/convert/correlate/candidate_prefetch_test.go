package correlate

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// sequentialDetectOracle is DetectCheckedWithWarnings as it was before
// candidate discovery was prefetched on a worker pool, with only the extractor
// call parameterized. The prefetched scan must match it exactly.
func sequentialDetectOracle(observations []Observation, extract candidateExtractor) ([]Correlation, []string, error) {
	if err := validateCorrelationObservations(observations); err != nil {
		return nil, nil, err
	}
	checkedValues := map[string]bool{}
	requests := NewRequestSearch(observations)
	var searchBytes int64
	chargeSearch := func(cost int64) error {
		if cost > maxCorrelationSearchBytes-searchBytes {
			return ErrCorrelationWorkLimit
		}
		searchBytes += cost
		return nil
	}
	var warnings []string
	correlations := make([]Correlation, 0)
	analyzedCandidates := 0
	truncated := false
scan:
	for index, observation := range observations {
		if observation.ResponseBody == "" && len(observation.ResponseHeaders) == 0 {
			continue
		}
		candidates, candidatesTruncated, err := extract(observation.ResponseBody, observation.ResponseHeaders)
		if err != nil {
			return nil, nil, err
		}
		if candidatesTruncated {
			warnings = append(warnings, fmt.Sprintf(
				"correlation candidate discovery for observation %d was incomplete after retaining %d candidate(s) (per-observation cap %d); later values in that response were not analyzed",
				observation.ID, len(candidates), maxCorrelationCandidatesPerObservation,
			))
		}
		for _, candidate := range candidates {
			if candidate.Value == "" || checkedValues[candidate.Value] || !candidateValueCanCorrelate(candidate) {
				continue
			}
			analyzedCandidates++
			if analyzedCandidates > maxCorrelationAnalyzedValuesPerAnalysis {
				truncated = true
				break scan
			}
			checkedValues[candidate.Value] = true
			usedIn, err := requests.UsedAfter(index, candidate.Value, chargeSearch)
			if err != nil {
				truncated = true
				break scan
			}
			if len(usedIn) == 0 {
				continue
			}
			if clientOriginCheckApplies(candidate.RuleID) {
				clientSent, err := requests.ClientSentThrough(index, candidate.Value, chargeSearch)
				if err != nil {
					truncated = true
					break scan
				}
				if clientSent {
					continue
				}
			}
			confidence, reasons := scoreConfidence(candidate, len(usedIn))
			correlations = append(correlations, Correlation{
				Name:        candidate.Name,
				Source:      candidate.Source,
				Expression:  candidate.Expression,
				ExtractType: candidate.ExtractType,
				Convert:     candidate.Convert,
				FoundInID:   observation.ID,
				UsedInIDs:   usedIn,
				Value:       candidate.Value,
				SampleValue: Truncate(candidate.Value, 80),
				RuleID:      candidate.RuleID,
				Confidence:  confidence,
				Reasons:     reasons,
			})
		}
	}
	if truncated {
		warnings = append(warnings, "correlation analysis exceeded its deterministic work budget; remaining candidates and observations were skipped")
	}
	sort.SliceStable(correlations, func(i, j int) bool {
		if correlations[i].Confidence != correlations[j].Confidence {
			return correlations[i].Confidence > correlations[j].Confidence
		}
		if correlations[i].FoundInID == correlations[j].FoundInID {
			return correlations[i].Name < correlations[j].Name
		}
		return correlations[i].FoundInID < correlations[j].FoundInID
	})
	uniquifyCorrelationNames(correlations)
	return correlations, warnings, nil
}

type detectOutcome struct {
	correlations []Correlation
	warnings     []string
	err          string
	panicked     bool
	panicValue   any
}

func runDetect(fn func() ([]Correlation, []string, error)) (outcome detectOutcome) {
	defer func() {
		if recovered := recover(); recovered != nil {
			outcome = detectOutcome{panicked: true, panicValue: recovered}
		}
	}()
	correlations, warnings, err := fn()
	outcome.correlations, outcome.warnings = correlations, warnings
	if err != nil {
		outcome.err = err.Error()
	}
	return outcome
}

func randomCorrelationToken(rng *rand.Rand) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	var builder strings.Builder
	for range 12 + rng.IntN(20) {
		builder.WriteByte(alphabet[rng.IntN(len(alphabet))])
	}
	return builder.String()
}

// randomPrefetchObservations builds a recording whose responses exercise the
// JSON, regex, HTML, Salesforce, and header families with shared, repeated,
// client-originated, and unused values, plus empty responses and occasional
// responses over the per-observation candidate cap.
func randomPrefetchObservations(rng *rand.Rand, count int, markers, allowOverCap bool) []Observation {
	pool := make([]string, 8+rng.IntN(40))
	for i := range pool {
		pool[i] = randomCorrelationToken(rng)
	}
	pick := func() string { return pool[rng.IntN(len(pool))] }
	overCap := !allowOverCap
	observations := make([]Observation, count)
	for i := range observations {
		observation := Observation{
			ID:             100 + i*3,
			RequestURL:     fmt.Sprintf("https://example.test/step/%d?state=%s", i, pick()),
			RequestHeaders: map[string]string{"X-CSRF-Token": pick()},
		}
		if rng.IntN(3) == 0 {
			observation.RequestBody = fmt.Sprintf("aura.token=%s&sessionId=%s&code=%s", pick(), pick(), pick())
		}
		var body strings.Builder
		switch rng.IntN(7) {
		case 0:
			// Empty response: skipped by the scan without discovery.
		case 1:
			fmt.Fprintf(&body, `{"sessionId":"%s","recordId":%d,"nested":{"csrf_token":"%s","items":[{"accessToken":"%s"}]}}`,
				pick(), 10000+rng.IntN(90000), pick(), pick())
		case 2:
			fmt.Fprintf(&body, `<form><input type="hidden" name="__VIEWSTATE" value="%s"><input name="csrf" value="%s"></form>`, pick(), pick())
		case 3:
			fmt.Fprintf(&body, `{"actions":[],"context":{"fwuid":"%s","app":"one:one","loaded":{"APPLICATION@markup://one:one":"%s"}},"aura.token":"%s"}`,
				pick(), pick(), pick())
		case 4:
			fmt.Fprintf(&body, `<soap:Envelope><soap:Body><SessionToken>%s</SessionToken><Item requestId="%s"/></soap:Body></soap:Envelope>`, pick(), pick())
		case 5:
			// Not one JSON document, so the bounded regex fallback runs.
			fmt.Fprintf(&body, `while(1);{"userid": %d, "sid":"%s"}`, 1000+rng.IntN(9000), pick())
		case 6:
			if overCap || rng.IntN(2) != 0 {
				fmt.Fprintf(&body, `<input name="state" value="%s">`, pick())
				break
			}
			// Over the per-observation cap with distinct values: exercises the
			// truncation warning. At most one per recording keeps the corpus fast;
			// TestPrefetchedDetectionAnalyzedValueBudget covers several.
			overCap = true
			body.WriteString(`{"rows":[`)
			for n := range maxCorrelationCandidatesPerObservation + 1 + rng.IntN(64) {
				if n > 0 {
					body.WriteByte(',')
				}
				fmt.Fprintf(&body, `{"accessToken":"Tk%02dZq%05dxR7"}`, i, n)
			}
			body.WriteString(`]}`)
		}
		observation.ResponseBody = body.String()
		if rng.IntN(3) == 0 {
			observation.ResponseHeaders = map[string]string{
				"Set-Cookie": fmt.Sprintf("sid=%s; Path=/; HttpOnly", pick()),
				"Location":   fmt.Sprintf("https://example.test/cb?code=%s&state=%s", pick(), pick()),
			}
		}
		if markers && observation.ResponseBody != "" {
			switch rng.IntN(12) {
			case 0:
				observation.ResponseBody += fmt.Sprintf(" ERR#%d", i)
			case 1:
				observation.ResponseBody += fmt.Sprintf(" PANIC#%d", i)
			}
		}
		observations[i] = observation
	}
	return observations
}

// markerExtractor wraps the real extractor with injected errors and panics
// keyed by response markers, so the first-error and panic behavior of the
// prefetched scan can be compared against the sequential oracle. Jitter
// varies worker completion order between runs.
func markerExtractor(jitter bool) candidateExtractor {
	return func(responseBody string, responseHeaders map[string]string) ([]Candidate, bool, error) {
		if jitter {
			time.Sleep(time.Duration(len(responseBody)%7) * 50 * time.Microsecond)
		}
		if at := strings.Index(responseBody, " ERR#"); at >= 0 {
			return nil, false, fmt.Errorf("%w: injected %s", ErrCorrelationWorkLimit, responseBody[at+1:])
		}
		if at := strings.Index(responseBody, " PANIC#"); at >= 0 {
			panic("injected " + responseBody[at+1:])
		}
		return candidatesCheckedWithTruncation(responseBody, responseHeaders)
	}
}

func assertDetectOutcomesEqual(t *testing.T, label string, want, got detectOutcome) {
	t.Helper()
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("%s: prefetched detection diverged from the sequential oracle:\nwant err=%q panicked=%v panic=%v warnings=%d correlations=%d\n got err=%q panicked=%v panic=%v warnings=%d correlations=%d",
			label, want.err, want.panicked, want.panicValue, len(want.warnings), len(want.correlations),
			got.err, got.panicked, got.panicValue, len(got.warnings), len(got.correlations))
	}
}

func waitForGoroutines(t *testing.T, baseline int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > baseline {
		if time.Now().After(deadline) {
			t.Fatalf("prefetch workers outlived detection: %d goroutines, baseline %d", runtime.NumGoroutine(), baseline)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestPrefetchedDetectionMatchesSequentialOracle(t *testing.T) {
	restore := maxCorrelationSearchBytes
	t.Cleanup(func() { maxCorrelationSearchBytes = restore })
	baseline := runtime.NumGoroutine()
	var sawCorrelations, sawCapWarning, sawBudgetWarning bool
	for seed := uint64(1); seed <= 40; seed++ {
		rng := rand.New(rand.NewPCG(seed, 0x9e3779b97f4a7c15))
		observations := randomPrefetchObservations(rng, 1+rng.IntN(24), false, seed%4 == 0)
		budgets := []int64{restore, int64(200 + rng.IntN(4000))}
		for _, budget := range budgets {
			maxCorrelationSearchBytes = budget
			want := runDetect(func() ([]Correlation, []string, error) {
				return sequentialDetectOracle(observations, candidatesCheckedWithTruncation)
			})
			for _, workers := range []int{0, 1, 3, 8} {
				got := runDetect(func() ([]Correlation, []string, error) {
					return detectCheckedWithWarnings(observations, candidatesCheckedWithTruncation, workers)
				})
				assertDetectOutcomesEqual(t, fmt.Sprintf("seed %d budget %d workers %d", seed, budget, workers), want, got)
			}
			got := runDetect(func() ([]Correlation, []string, error) { return DetectCheckedWithWarnings(observations) })
			assertDetectOutcomesEqual(t, fmt.Sprintf("seed %d budget %d public", seed, budget), want, got)
			sawCorrelations = sawCorrelations || len(want.correlations) > 0
			for _, warning := range want.warnings {
				sawCapWarning = sawCapWarning || strings.Contains(warning, "per-observation cap")
				sawBudgetWarning = sawBudgetWarning || strings.Contains(warning, "deterministic work budget")
			}
		}
	}
	if !sawCorrelations || !sawCapWarning || !sawBudgetWarning {
		t.Fatalf("randomized corpus is too weak: correlations=%v capWarning=%v budgetWarning=%v", sawCorrelations, sawCapWarning, sawBudgetWarning)
	}
	waitForGoroutines(t, baseline)
}

func TestPrefetchedDetectionMatchesOracleOnInjectedErrorsAndPanics(t *testing.T) {
	baseline := runtime.NumGoroutine()
	var sawError, sawPanic bool
	for seed := uint64(1); seed <= 80; seed++ {
		rng := rand.New(rand.NewPCG(seed, 0xd1b54a32d192ed03))
		observations := randomPrefetchObservations(rng, 2+rng.IntN(30), true, seed%8 == 0)
		want := runDetect(func() ([]Correlation, []string, error) {
			return sequentialDetectOracle(observations, markerExtractor(false))
		})
		sawError = sawError || want.err != ""
		sawPanic = sawPanic || want.panicked
		for _, workers := range []int{0, 2, 5, 8} {
			got := runDetect(func() ([]Correlation, []string, error) {
				return detectCheckedWithWarnings(observations, markerExtractor(seed%2 == 0), workers)
			})
			assertDetectOutcomesEqual(t, fmt.Sprintf("seed %d workers %d", seed, workers), want, got)
		}
	}
	if !sawError || !sawPanic {
		t.Fatalf("randomized corpus is too weak: error=%v panic=%v", sawError, sawPanic)
	}
	waitForGoroutines(t, baseline)
}

// TestPrefetchedDetectionAnalyzedValueBudget pins the cross-observation break:
// once the analyzed-value budget is spent, results already prefetched for later
// responses are discarded and the warnings match the sequential scan.
func TestPrefetchedDetectionAnalyzedValueBudget(t *testing.T) {
	observations := make([]Observation, 0, 12)
	for i := range 12 {
		var body strings.Builder
		body.WriteString(`{"rows":[`)
		for n := range maxCorrelationCandidatesPerObservation + 8 {
			if n > 0 {
				body.WriteByte(',')
			}
			fmt.Fprintf(&body, `{"accessToken":"Tk%02dZq%05dxR7"}`, i, n)
		}
		body.WriteString(`]}`)
		observations = append(observations, Observation{
			ID: i + 1, RequestURL: fmt.Sprintf("https://example.test/%d?token=Tk%02dZq00007xR7", i, i-1),
			ResponseBody: body.String(),
		})
	}
	want := runDetect(func() ([]Correlation, []string, error) {
		return sequentialDetectOracle(observations, candidatesCheckedWithTruncation)
	})
	if len(want.warnings) == 0 || !strings.Contains(want.warnings[len(want.warnings)-1], "deterministic work budget") {
		t.Fatalf("fixture must exhaust the analyzed-value budget, warnings=%d", len(want.warnings))
	}
	for _, workers := range []int{0, 2, 8} {
		got := runDetect(func() ([]Correlation, []string, error) {
			return detectCheckedWithWarnings(observations, candidatesCheckedWithTruncation, workers)
		})
		assertDetectOutcomesEqual(t, fmt.Sprintf("workers %d", workers), want, got)
	}
}

// TestCandidatePrefetchBoundsLookahead proves workers stop at the lookahead
// window while the scan has not consumed anything, that the window starts at
// the scan's position even when the largest responses come last (so the work
// just ahead of the scan is what runs in parallel), that it slides with the
// scan, and that the scan still reaches every response exactly once.
func TestCandidatePrefetchBoundsLookahead(t *testing.T) {
	const workers = 2
	window := workers * correlationCandidateLookaheadPerWorker
	observations := make([]Observation, 64)
	for i := range observations {
		// Sizes increase with index: a largest-first schedule would start at the end.
		observations[i] = Observation{ID: i, ResponseBody: strings.Repeat("x", i+1)}
	}
	var consumed atomic.Int64 // responses the scan has finished, set before each take
	var outside atomic.Int64
	started := make(chan int, len(observations))
	extract := func(body string, _ map[string]string) ([]Candidate, bool, error) {
		index := len(body) - 1
		if int64(index) >= consumed.Load()+int64(window) {
			outside.Add(1)
		}
		started <- index
		return []Candidate{{Value: body}}, false, nil
	}
	prefetch := startCandidatePrefetch(observations, extract, workers)
	deadline := time.Now().Add(2 * time.Second)
	for len(started) < window && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	if got := len(started); got != window {
		prefetch.stop()
		t.Fatalf("workers started %d discoveries before the scan consumed any, want lookahead window %d", got, window)
	}
	first := make([]int, 0, window)
	for range window {
		first = append(first, <-started)
	}
	sort.Ints(first)
	for position, index := range first {
		if index != position {
			prefetch.stop()
			t.Fatalf("workers started response %d before the scan consumed any; the window must start at the scan's position (0..%d)", index, window-1)
		}
	}
	for index := range observations {
		// take(index) frees a slot only after the scan has consumed index, so a
		// worker may then claim up to index+window, never further.
		consumed.Store(int64(index) + 1)
		candidates, _, err := prefetch.take(index)
		if err != nil || len(candidates) != 1 || candidates[0].Value != observations[index].ResponseBody {
			prefetch.stop()
			t.Fatalf("take(%d) returned the wrong result", index)
		}
	}
	prefetch.stop()
	if got := window + len(started); got != len(observations) {
		t.Fatalf("every response must be discovered exactly once, got %d of %d", got, len(observations))
	}
	if got := outside.Load(); got != 0 {
		t.Fatalf("%d discoveries started beyond the window measured from the scan's position", got)
	}
}

// panickingCandidateExtractor is a named frame so the test below can find it in
// the stack of the panic the scan re-raises.
func panickingCandidateExtractor(calls *atomic.Int64) candidateExtractor {
	return func(body string, _ map[string]string) ([]Candidate, bool, error) {
		if body == "panic" {
			calls.Add(1)
			rows := make([]Candidate, 0)
			_ = rows[len(body)] // index out of range, like a failing helper
		}
		return []Candidate{{Value: body}}, false, nil
	}
}

// TestCandidatePrefetchReraisesWorkerPanicWithExtractorStack pins that a panic
// a worker recovered reaches the caller with the same value and a stack through
// the failing extractor, as a sequential scan's panic would.
func TestCandidatePrefetchReraisesWorkerPanicWithExtractorStack(t *testing.T) {
	observations := make([]Observation, 16)
	for i := range observations {
		observations[i] = Observation{ID: i, ResponseBody: fmt.Sprintf("ok%d", i)}
	}
	observations[0].ResponseBody = "panic"
	var calls atomic.Int64
	prefetch := startCandidatePrefetch(observations, panickingCandidateExtractor(&calls), 2)
	defer prefetch.stop()
	deadline := time.Now().Add(2 * time.Second)
	for calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if calls.Load() != 1 {
		t.Fatalf("a worker must discover the first response before the scan reaches it, calls=%d", calls.Load())
	}
	var recovered any
	var stack string
	func() {
		defer func() {
			recovered = recover()
			stack = string(debug.Stack())
		}()
		_, _, _ = prefetch.take(0)
	}()
	runtimeErr, ok := recovered.(runtime.Error)
	if !ok || !strings.Contains(runtimeErr.Error(), "index out of range") {
		t.Fatalf("take must re-raise the extractor's own panic value, got %T %v", recovered, recovered)
	}
	if calls.Load() != 2 {
		t.Fatalf("the scan must rerun the panicking extraction on its own goroutine, calls=%d", calls.Load())
	}
	if !strings.Contains(stack, "panickingCandidateExtractor") || !strings.Contains(stack, "(*candidatePrefetch).take") {
		t.Fatalf("re-raised panic must carry a stack through the failing extractor under take:\n%s", stack)
	}
}

func TestCandidatePrefetchSequentialForTinyInputs(t *testing.T) {
	single := []Observation{{ID: 1, ResponseBody: `{"sessionId":"abcdef123456"}`}, {ID: 2}}
	if prefetch := startCandidatePrefetch(single, candidatesCheckedWithTruncation, 8); prefetch.workers != 0 {
		prefetch.stop()
		t.Fatalf("one response must not start a worker pool, got %d workers", prefetch.workers)
	}
	pair := []Observation{{ID: 1, ResponseBody: "a"}, {ID: 2, ResponseBody: "b"}}
	if prefetch := startCandidatePrefetch(pair, candidatesCheckedWithTruncation, 1); prefetch.workers != 0 {
		prefetch.stop()
		t.Fatalf("one worker must use the sequential scan, got %d workers", prefetch.workers)
	}
}
