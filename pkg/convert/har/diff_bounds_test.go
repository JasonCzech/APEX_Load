package har

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/convert/sample"
)

// chainedRecording is a flow whose second producer's path carries the first
// producer's value: GET /api/cart/{session_key} answers cart_token.
func chainedRecording(session, cart string) []byte {
	host := "https://shop.example.test"
	return pairHAR([]string{
		pairEntry("GET", host+"/api/session", `{"name":"Content-Type","value":"application/json"}`, fmt.Sprintf(`{"session_key":%q}`, session)),
		pairEntry("GET", host+"/api/cart/"+session, `{"name":"Content-Type","value":"application/json"}`, fmt.Sprintf(`{"cart_token":%q}`, cart)),
		pairEntry("GET", host+"/api/checkout?cart_token="+cart, ``, `{"ok":true}`),
	})
}

func TestDiffAlignsProducersWhosePathCarriesAnEarlierDynamicValue(t *testing.T) {
	baseline := chainedRecording("Sx81kq2LmZpQ", "Cv72Ty0PwRn4")
	comparison := chainedRecording("Sy19Hd7ZaKuE", "Cw55Bn3JqMx8")
	opts := ConvertOptions{ScriptName: "Chained"}
	legacy, err := legacyDiffOracle(baseline, comparison, opts)
	if err != nil {
		t.Fatalf("oracle: %v", err)
	}
	report, err := Diff(baseline, comparison, opts)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if report.DynamicCandidates != 2 || legacy.DynamicCandidates != 2 {
		t.Fatalf("dynamic = %d (oracle %d), want 2: %#v", report.DynamicCandidates, legacy.DynamicCandidates, report.Classifications)
	}
	byValue := map[string]DiffClassification{}
	for _, classification := range report.Classifications {
		byValue[classification.BaselineValue] = classification
	}
	// The cart producer's paths differ by the session value; masked, they align.
	if got := byValue["Cv72Ty0PwRn4"]; got.Match != DiffMatchAlignedRequest || got.BaselineEntry != 2 || got.ComparisonEntry != 2 || got.ComparisonValue != "Cw55Bn3JqMx8" {
		t.Fatalf("cart token = %#v, want dynamic aligned to comparison entry 2", got)
	}
}

func TestDiffPathTemplatesStillRefuseAnUnrelatedPath(t *testing.T) {
	// The masked path must match as a template: the comparison's other
	// resource path does not stand in for the cart producer.
	baseline := chainedRecording("Sx81kq2LmZpQ", "Cv72Ty0PwRn4")
	host := "https://shop.example.test"
	comparison := pairHAR([]string{
		pairEntry("GET", host+"/api/session", `{"name":"Content-Type","value":"application/json"}`, `{"session_key":"Sy19Hd7ZaKuE"}`),
		pairEntry("GET", host+"/api/wishlist/Sy19Hd7ZaKuE", `{"name":"Content-Type","value":"application/json"}`, `{"cart_token":"Cw55Bn3JqMx8"}`),
		pairEntry("GET", host+"/api/checkout?cart_token=Cw55Bn3JqMx8", ``, `{"ok":true}`),
	})
	report, err := Diff(baseline, comparison, ConvertOptions{ScriptName: "Chained"})
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	for _, classification := range report.Classifications {
		if classification.BaselineValue == "Cv72Ty0PwRn4" {
			if classification.Classification != DiffClassificationCandidate || !strings.Contains(classification.Reason, "no request with the baseline producer's method, host and path") {
				t.Fatalf("cart token = %#v, want unmatched", classification)
			}
			return
		}
	}
	t.Fatalf("cart token correlation missing: %#v", report.Classifications)
}

// sameEndpointRecording is n POSTs to one Aura-like endpoint, each answering a
// fresh token. With chain set each request carries the previous token, so the
// tokens are correlations; key renames the token field.
func sameEndpointRecording(n, run int, chain bool, key string) []byte {
	host := "https://org.example.test"
	pad := strings.Repeat("x", 4000)
	token := func(index int) string { return fmt.Sprintf("Tok%dR%dZ%08d", index, run, index*7919) }
	entries := make([]string, 0, n)
	for index := 0; index < n; index++ {
		previous := ""
		if index > 0 && chain {
			previous = token(index - 1)
		}
		entries = append(entries, fmt.Sprintf(`{"startedDateTime":"2026-01-01T00:00:00Z","time":10,"request":{"method":"POST","url":%q,"headers":[{"name":"Content-Type","value":"application/x-www-form-urlencoded"}],"postData":{"mimeType":"application/x-www-form-urlencoded","text":%q}},"response":{"status":200,"headers":[{"name":"Content-Type","value":"application/json"}],"content":{"mimeType":"application/json","text":%q}}}`,
			host+"/s/sfsites/aura?r=1&other.Action=1", "message="+previous+"&pad="+pad, fmt.Sprintf(`{"actions":[{"state":"SUCCESS"}],%q:%q,"pad":%q}`, key, token(index), pad)))
	}
	return pairHAR(entries)
}

func TestDiffCostStaysLinearInSameEndpointRequests(t *testing.T) {
	const n = 800
	baseline := sameEndpointRecording(n, 0, true, "ctx_token")
	opts := ConvertOptions{ScriptName: "SameEndpoint"}
	converted, err := Convert(baseline, opts)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if len(converted.Correlations) < n-1 {
		t.Fatalf("baseline correlations = %d, want %d", len(converted.Correlations), n-1)
	}
	for name, comparison := range map[string][]byte{
		// Every response yields a token the comparison never reuses: each
		// producer used to try every response and scan every later request.
		"never reused": sameEndpointRecording(n, 1, false, "ctx_token"),
		// The extractor matches no comparison response.
		"extractor miss": sameEndpointRecording(n, 1, true, "other_key"),
	} {
		t.Run(name, func(t *testing.T) {
			prepared, err := PrepareComparison(comparison, opts)
			if err != nil {
				t.Fatalf("PrepareComparison: %v", err)
			}
			report, aligner, err := diffPrepared(context.Background(), converted, prepared)
			if err != nil {
				t.Fatalf("diffPrepared: %v", err)
			}
			if report.UnmatchedCandidates != len(converted.Correlations) {
				t.Fatalf("counts = %d/%d/%d, want every correlation unmatched", report.DynamicCandidates, report.StaticCandidates, report.UnmatchedCandidates)
			}
			// Each correlation tries at most its window of responses, and the
			// responses are shared, so one extraction per response suffices.
			if limit := len(prepared.observations); aligner.evaluations > limit {
				t.Fatalf("extractor evaluations = %d, want at most %d", aligner.evaluations, limit)
			}
			scanned := 0
			if aligner.reuse != nil {
				scanned = aligner.reuse.scanned
			}
			if limit := 2 * len(prepared.observations); scanned > limit {
				t.Fatalf("requests scanned for reuse = %d, want at most %d", scanned, limit)
			}
		})
	}
}

func TestDiffPreparedStopsWithItsContext(t *testing.T) {
	baseline := pairRecording([]pairFlow{{kind: 0, path: "/login", dynamic: true}}, 0, nil)
	comparison := pairRecording([]pairFlow{{kind: 0, path: "/login", dynamic: true}}, 1, nil)
	opts := ConvertOptions{ScriptName: "Canceled"}
	converted, err := Convert(baseline, opts)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	prepared, err := PrepareComparison(comparison, opts)
	if err != nil {
		t.Fatalf("PrepareComparison: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := DiffPreparedContext(ctx, converted, prepared); !errors.Is(err, context.Canceled) {
		t.Fatalf("DiffPreparedContext error = %v, want context.Canceled", err)
	}
	if report, err := DiffPreparedContext(context.Background(), converted, prepared); err != nil || report.DynamicCandidates != 1 {
		t.Fatalf("DiffPreparedContext = %#v, %v; want one dynamic correlation", report, err)
	}
}

func TestDiffAlignmentOrderStaysWithinItsWindow(t *testing.T) {
	aligner := &diffAligner{claimed: map[int]int{16: 99}}
	candidates := make([]sample.HARSourceObservation, 40)
	for index := range candidates {
		candidates[index].ID = index + 1
	}
	var ids []int
	for _, observation := range aligner.alignmentOrder(candidates, 20, 1) {
		ids = append(ids, observation.ID)
	}
	// Position 20 (entry 21) first, its neighbours within the window in
	// recording order, the one claimed by another producer last.
	want := []int{21}
	for id := 13; id <= 29; id++ {
		if id != 21 && id != 16 {
			want = append(want, id)
		}
	}
	want = append(want, 16)
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("order = %v, want %v", ids, want)
	}
	// A producer past the end of a shorter comparison tries the last window.
	ids = ids[:0]
	for _, observation := range aligner.alignmentOrder(candidates, 60, 1) {
		ids = append(ids, observation.ID)
	}
	if len(ids) != diffAlignmentWindow+1 || ids[0] != 32 || ids[len(ids)-1] != 40 {
		t.Fatalf("out-of-range order = %v, want entries 32-40", ids)
	}
	if got := aligner.alignmentOrder(nil, 0, 1); len(got) != 0 {
		t.Fatalf("empty order = %v", got)
	}
}

// linearReusedAfter is diffAligner.reusedAfter before diffReuseIndex: a scan
// of every later request for each question. It is the oracle for the index.
func linearReusedAfter(requests []sample.HARSourceObservation, reusedValues map[string]bool, value string, producerID int) bool {
	if reusedValues[value] {
		return true
	}
	if len(value) < diffReuseScanMinLength {
		return false
	}
	forms := []string{value}
	if escaped := url.QueryEscape(value); escaped != value {
		forms = append(forms, escaped)
	}
	for _, request := range requests {
		if request.ID <= producerID {
			continue
		}
		for _, form := range forms {
			if strings.Contains(request.RequestURL, form) || strings.Contains(request.RequestBody, form) {
				return true
			}
			for _, header := range request.RequestHeaders {
				if strings.Contains(header, form) {
					return true
				}
			}
		}
	}
	return false
}

func TestDiffReuseIndexAgreesWithALinearScan(t *testing.T) {
	random := rand.New(rand.NewSource(20261030))
	// A small alphabet makes partial matches and field-boundary near misses
	// common; '%', ' ' and '&' exercise the URL-encoded form.
	const alphabet = "abc%& 01"
	text := func(maxLength int) string {
		var builder strings.Builder
		for length := random.Intn(maxLength + 1); length > 0; length-- {
			builder.WriteByte(alphabet[random.Intn(len(alphabet))])
		}
		return builder.String()
	}
	for iteration := 0; iteration < 200; iteration++ {
		count := random.Intn(12)
		requests := make([]sample.HARSourceObservation, 0, count)
		for index := 0; index < count; index++ {
			request := sample.HARSourceObservation{Observation: correlate.Observation{
				// IDs ascend with gaps; some recordings list them out of order.
				ID:          1 + 2*index + random.Intn(2),
				RequestURL:  "https://h.test/" + text(12),
				RequestBody: text(24),
			}}
			if random.Intn(2) == 0 {
				request.RequestHeaders = map[string]string{"X-A": text(10), "X-B": text(10)}
			}
			requests = append(requests, request)
		}
		random.Shuffle(len(requests), func(i, j int) {
			if random.Intn(4) == 0 {
				requests[i], requests[j] = requests[j], requests[i]
			}
		})
		reusedValues := map[string]bool{}
		if random.Intn(3) == 0 {
			reusedValues[text(6)] = true
		}
		aligner := &diffAligner{requests: requests, reusedValues: reusedValues}
		// Ask many questions in random order, repeating values with other
		// producers, so every memo state of the index is consulted.
		var values []string
		for len(values) < 8 {
			value := text(7)
			if len(requests) > 0 && random.Intn(2) == 0 {
				source := requests[random.Intn(len(requests))]
				fields := []string{source.RequestURL, source.RequestBody}
				field := fields[random.Intn(len(fields))]
				if len(field) > 0 {
					from := random.Intn(len(field))
					value = field[from : from+random.Intn(len(field)-from)+1]
				}
			}
			values = append(values, value)
		}
		for question := 0; question < 60; question++ {
			value := values[random.Intn(len(values))]
			producerID := random.Intn(2*count+3) - 1
			want := linearReusedAfter(requests, reusedValues, value, producerID)
			if got := aligner.reusedAfter(value, producerID); got != want {
				t.Fatalf("iteration %d: reusedAfter(%q, %d) = %v, want %v; requests %#v", iteration, value, producerID, got, want, requests)
			}
		}
	}
}

func TestDiffReuseIndexHandlesEmptyAndShortText(t *testing.T) {
	aligner := &diffAligner{reusedValues: map[string]bool{}}
	if aligner.reusedAfter("abcdef", 0) {
		t.Fatal("a comparison without requests reused a value")
	}
	aligner = &diffAligner{reusedValues: map[string]bool{}, requests: []sample.HARSourceObservation{
		{Observation: correlate.Observation{ID: 2, RequestURL: "ab"}},
		{Observation: correlate.Observation{ID: 3, RequestBody: "token=Zq55 Lp09"}},
	}}
	for _, check := range []struct {
		value    string
		producer int
		want     bool
	}{
		{"Zq55 Lp09", 1, true},
		{"Zq55 Lp09", 3, false},
		{"Zq55+Lp09", 1, false},
		{"abcd", 0, false},
		{"Lp09", 2, true},
	} {
		if got := aligner.reusedAfter(check.value, check.producer); got != check.want {
			t.Fatalf("reusedAfter(%q, %d) = %v, want %v", check.value, check.producer, got, check.want)
		}
	}
}
