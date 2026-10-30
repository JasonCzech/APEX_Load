package har

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/sample"
)

// legacyDiffOracle is har.Diff as it was before DiffPrepared: both recordings
// converted, correlations paired by source|expression|extract_type|name. It is
// kept as the oracle for every case where name pairing is sound.
func legacyDiffOracle(baseline []byte, comparison []byte, opts ConvertOptions) (*DiffReport, error) {
	if len(comparison) == 0 || strings.TrimSpace(string(comparison)) == "" {
		return nil, fmt.Errorf("comparison HAR content is required")
	}
	opts.ArtifactMode = ArtifactModeDesignHARPair
	baselineResult, err := Convert(baseline, opts)
	if err != nil {
		return nil, fmt.Errorf("baseline HAR: %w", err)
	}
	comparisonResult, err := Convert(comparison, opts)
	if err != nil {
		return nil, fmt.Errorf("comparison HAR: %w", err)
	}
	comparisonByKey := make(map[string]sample.Correlation, len(comparisonResult.Correlations))
	for _, corr := range comparisonResult.Correlations {
		comparisonByKey[correlationDiffKey(corr)] = corr
	}
	report := &DiffReport{
		ArtifactMode:           ArtifactModeDesignHARPair,
		Compared:               true,
		BaselineCorrelations:   len(baselineResult.Correlations),
		ComparisonCorrelations: len(comparisonResult.Correlations),
	}
	for _, corr := range baselineResult.Correlations {
		baselineValue := correlationValue(corr)
		classification := DiffClassification{
			Name:           corr.Name,
			Source:         corr.Source,
			Expression:     corr.Expression,
			ExtractType:    corr.ExtractType,
			SampleValue:    publicCorrelationValue(corr),
			BaselineValue:  publicCorrelationValue(corr),
			FoundInStep:    corr.FoundInStep,
			UsedInSteps:    append([]int(nil), corr.UsedInSteps...),
			Classification: DiffClassificationCandidate,
			DiffConfidence: 0.55,
			Reason:         "value was reused later in the baseline HAR but was not matched in the comparison HAR",
		}
		if comparisonCorr, ok := comparisonByKey[correlationDiffKey(corr)]; ok {
			comparisonValue := correlationValue(comparisonCorr)
			classification.ComparisonValue = publicCorrelationValue(comparisonCorr)
			if baselineValue != "" && comparisonValue != "" && baselineValue != comparisonValue {
				classification.Classification = DiffClassificationDynamic
				classification.DiffConfidence = 0.95
				classification.Reason = "value changed between recordings and was reused later in both HARs"
				report.DynamicCandidates++
			} else {
				classification.Classification = DiffClassificationStatic
				classification.DiffConfidence = 0.4
				classification.Reason = "value was reused later but stayed constant across both HARs"
				report.StaticCandidates++
			}
		} else {
			report.UnmatchedCandidates++
		}
		report.Classifications = append(report.Classifications, classification)
	}
	sort.SliceStable(report.Classifications, func(i, j int) bool {
		left := report.Classifications[i]
		right := report.Classifications[j]
		if diffRank(left.Classification) != diffRank(right.Classification) {
			return diffRank(left.Classification) < diffRank(right.Classification)
		}
		if left.DiffConfidence != right.DiffConfidence {
			return left.DiffConfidence > right.DiffConfidence
		}
		if left.FoundInStep != right.FoundInStep {
			return left.FoundInStep < right.FoundInStep
		}
		return left.Name < right.Name
	})
	return report, nil
}

func pairEntry(method, rawURL, responseHeaders, body string) string {
	return fmt.Sprintf(`{"startedDateTime":"2026-01-01T00:00:00Z","time":10,"request":{"method":%q,"url":%q,"headers":[]},"response":{"status":200,"headers":[%s],"content":{"mimeType":"application/json","text":%q}}}`,
		method, rawURL, responseHeaders, body)
}

func pairHAR(entries []string) []byte {
	return []byte(`{"log":{"entries":[` + strings.Join(entries, ",") + `]}}`)
}

// pairFlow is one producer/consumer exchange of a synthetic recording.
type pairFlow struct {
	kind    int // 0: JSON body token, 1: Location header query parameter
	path    string
	dynamic bool
}

func (flow pairFlow) entries(run int, index int) []string {
	value := fmt.Sprintf("Tk%02dq7Zx%dLm4Np", index, 1000+index)
	if flow.dynamic && run == 1 {
		value = fmt.Sprintf("Rv%02dw3Yb%dKc8Hs", index, 5000+index)
	}
	host := "https://shop.example.test"
	switch flow.kind {
	case 0:
		key := "access_token"
		return []string{
			pairEntry("POST", host+flow.path, `{"name":"Content-Type","value":"application/json"}`, fmt.Sprintf(`{%q:%q}`, key, value)),
			pairEntry("GET", fmt.Sprintf("%s%s/use?%s=%s", host, flow.path, key, value), ``, `{"ok":true}`),
		}
	default:
		return []string{
			pairEntry("GET", host+flow.path, fmt.Sprintf(`{"name":"Location","value":"%s/landing%d?sid=%s"}`, host, index, value), `{"ok":true}`),
			pairEntry("GET", fmt.Sprintf("%s/landing%d?sid=%s", host, index, value), ``, `{"ok":true}`),
		}
	}
}

func pairRecording(flows []pairFlow, run int, noise *rand.Rand) []byte {
	var entries []string
	for index, flow := range flows {
		if noise != nil && noise.Intn(3) == 0 {
			entries = append(entries, pairEntry("GET", fmt.Sprintf("https://shop.example.test/noise/%d", noise.Intn(1000)), ``, `{"ok":true}`))
		}
		entries = append(entries, flow.entries(run, index)...)
	}
	return pairHAR(entries)
}

func classificationsByName(report *DiffReport) map[string]DiffClassification {
	out := make(map[string]DiffClassification, len(report.Classifications))
	for _, classification := range report.Classifications {
		out[classification.Name] = classification
	}
	return out
}

func TestDiffWithBaselineReusesBaselineExactly(t *testing.T) {
	baseline := pairRecording([]pairFlow{{kind: 0, path: "/login", dynamic: true}, {kind: 1, path: "/go", dynamic: false}, {kind: 0, path: "/cart", dynamic: false}}, 0, nil)
	comparison := pairRecording([]pairFlow{{kind: 0, path: "/login", dynamic: true}, {kind: 1, path: "/go", dynamic: false}, {kind: 0, path: "/cart", dynamic: false}}, 1, nil)
	opts := ConvertOptions{ScriptName: "Pair"}
	want, err := Diff(baseline, comparison, opts)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	// The handler converts the baseline under its own artifact mode; only
	// governance depends on it, so the report must not change.
	for _, mode := range []string{"", ArtifactModeDesignHAR, ArtifactModeDesignHARPair, ArtifactModeRuntimeCanaryHAR} {
		baselineOpts := opts
		baselineOpts.ArtifactMode = mode
		converted, err := Convert(baseline, baselineOpts)
		if err != nil {
			t.Fatalf("Convert: %v", err)
		}
		got, err := DiffWithBaseline(converted, comparison, opts)
		if err != nil {
			t.Fatalf("DiffWithBaseline: %v", err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("mode %q: DiffWithBaseline = %#v, want %#v", mode, got, want)
		}
	}
	if want.DynamicCandidates != 1 || want.StaticCandidates != 2 || want.UnmatchedCandidates != 0 {
		t.Fatalf("counts = %d/%d/%d, want 1/2/0: %#v", want.DynamicCandidates, want.StaticCandidates, want.UnmatchedCandidates, want.Classifications)
	}
}

func TestDiffAgreesWithLegacyOracleWhenNamesAreStable(t *testing.T) {
	random := rand.New(rand.NewSource(20261029))
	for iteration := 0; iteration < 40; iteration++ {
		count := 1 + random.Intn(6)
		flows := make([]pairFlow, count)
		for index := range flows {
			flows[index] = pairFlow{kind: random.Intn(2), path: fmt.Sprintf("/step%d", index), dynamic: random.Intn(2) == 0}
		}
		baseline := pairRecording(flows, 0, nil)
		// Unrelated requests shift the comparison's entry numbers but leave the
		// correlation names, so the legacy name pairing stays a sound oracle.
		var noise *rand.Rand
		if iteration%2 == 1 {
			noise = rand.New(rand.NewSource(int64(iteration)))
		}
		comparison := pairRecording(flows, 1, noise)
		opts := ConvertOptions{ScriptName: "Oracle"}
		want, err := legacyDiffOracle(baseline, comparison, opts)
		if err != nil {
			t.Fatalf("iteration %d oracle: %v", iteration, err)
		}
		got, err := Diff(baseline, comparison, opts)
		if err != nil {
			t.Fatalf("iteration %d Diff: %v", iteration, err)
		}
		if got.DynamicCandidates != want.DynamicCandidates || got.StaticCandidates != want.StaticCandidates ||
			got.UnmatchedCandidates != want.UnmatchedCandidates || got.BaselineCorrelations != want.BaselineCorrelations ||
			got.ComparisonCorrelations != want.ComparisonCorrelations || len(got.Classifications) != len(want.Classifications) {
			t.Fatalf("iteration %d counts differ: got %#v want %#v", iteration, got, want)
		}
		dynamic := 0
		for _, flow := range flows {
			if flow.dynamic {
				dynamic++
			}
		}
		if got.DynamicCandidates != dynamic || got.StaticCandidates != count-dynamic {
			t.Fatalf("iteration %d: dynamic %d static %d, want %d/%d", iteration, got.DynamicCandidates, got.StaticCandidates, dynamic, count-dynamic)
		}
		for index := range want.Classifications {
			left, right := got.Classifications[index], want.Classifications[index]
			if left.Match != DiffMatchAlignedRequest || left.BaselineEntry == 0 || left.ComparisonEntry == 0 {
				t.Fatalf("iteration %d: %s was not aligned: %#v", iteration, left.Name, left)
			}
			left.Match, left.BaselineEntry, left.ComparisonEntry = "", 0, 0
			if !reflect.DeepEqual(left, right) {
				t.Fatalf("iteration %d classification %d = %#v, want %#v", iteration, index, left, right)
			}
		}
	}
}

func TestDiffAlignsProducersWhenTheComparisonLosesOne(t *testing.T) {
	// Both producers emit "id", so detection names them corr_id and corr_id_2.
	// The comparison lost the cart producer: name pairing would compare the
	// cart id with the wishlist id (a false "dynamic") and leave the wishlist
	// unmatched.
	cart := []string{
		pairEntry("POST", "https://shop.example.test/cart", `{"name":"Content-Type","value":"application/json"}`, `{"id":"crt7Qx29Lm"}`),
		pairEntry("GET", "https://shop.example.test/cart/crt7Qx29Lm/items", ``, `{"ok":true}`),
	}
	wishlist := []string{
		pairEntry("POST", "https://shop.example.test/wishlist", `{"name":"Content-Type","value":"application/json"}`, `{"id":"wsh4Rb81Kp"}`),
		pairEntry("GET", "https://shop.example.test/wishlist/wsh4Rb81Kp/items", ``, `{"ok":true}`),
	}
	baseline := pairHAR(append(append([]string{}, cart...), wishlist...))
	comparison := pairHAR(wishlist)
	opts := ConvertOptions{ScriptName: "Shifted"}

	legacy, err := legacyDiffOracle(baseline, comparison, opts)
	if err != nil {
		t.Fatalf("oracle: %v", err)
	}
	if legacy.DynamicCandidates != 1 {
		t.Fatalf("fixture no longer reproduces the name-shift mislabel: %#v", legacy.Classifications)
	}
	report, err := Diff(baseline, comparison, opts)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if report.DynamicCandidates != 0 || report.StaticCandidates != 1 || report.UnmatchedCandidates != 1 {
		t.Fatalf("counts = %d/%d/%d, want 0/1/1: %#v", report.DynamicCandidates, report.StaticCandidates, report.UnmatchedCandidates, report.Classifications)
	}
	byValue := map[string]DiffClassification{}
	for _, classification := range report.Classifications {
		byValue[classification.BaselineValue] = classification
	}
	if got := byValue["crt7Qx29Lm"]; got.Classification != DiffClassificationCandidate || !strings.Contains(got.Reason, "no request with the baseline producer's method, host and path") {
		t.Fatalf("cart = %#v, want unmatched with the alignment reason", got)
	}
	if got := byValue["wsh4Rb81Kp"]; got.Classification != DiffClassificationStatic || got.ComparisonValue != "wsh4Rb81Kp" || got.ComparisonEntry != 1 || got.BaselineEntry != 3 {
		t.Fatalf("wishlist = %#v, want static aligned to comparison entry 1", got)
	}
}

func TestDiffUsesQueryKeysAndOrderToAlignRepeatedRequests(t *testing.T) {
	post := func(query, key, value string) string {
		return pairEntry("POST", "https://shop.example.test/aura?"+query, `{"name":"Content-Type","value":"application/json"}`, fmt.Sprintf(`{%q:%q}`, key, value))
	}
	use := func(key, value string) string {
		return pairEntry("GET", fmt.Sprintf("https://shop.example.test/use?%s=%s", key, value), ``, `{"ok":true}`)
	}
	baseline := pairHAR([]string{
		post("r=1&a.Load=1", "tokenA", "Aa11Bb22Cc33"), use("tokenA", "Aa11Bb22Cc33"),
		post("r=2&b.Save=1", "tokenB", "Dd44Ee55Ff66"), use("tokenB", "Dd44Ee55Ff66"),
		post("r=3&b.Save=1", "tokenB2", "Gg77Hh88Ii99"), use("tokenB2", "Gg77Hh88Ii99"),
	})
	// The comparison adds an unrelated POST /aura first and rotates the second
	// Save token: the query-key set separates Load from Save, and the order
	// within Save picks the right one.
	comparison := pairHAR([]string{
		post("r=0&c.Ping=1", "other", "Zz00Yy99Xx88"),
		post("r=1&a.Load=1", "tokenA", "Aa11Bb22Cc33"), use("tokenA", "Aa11Bb22Cc33"),
		post("r=2&b.Save=1", "tokenB", "Dd44Ee55Ff66"), use("tokenB", "Dd44Ee55Ff66"),
		post("r=3&b.Save=1", "tokenB2", "Jj12Kk34Ll56"), use("tokenB2", "Jj12Kk34Ll56"),
	})
	report, err := Diff(baseline, comparison, ConvertOptions{ScriptName: "Repeated"})
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	byValue := map[string]DiffClassification{}
	for _, classification := range report.Classifications {
		byValue[classification.BaselineValue] = classification
	}
	for value, want := range map[string]struct {
		class string
		entry int
	}{
		"Aa11Bb22Cc33": {DiffClassificationStatic, 2},
		"Dd44Ee55Ff66": {DiffClassificationStatic, 4},
		"Gg77Hh88Ii99": {DiffClassificationDynamic, 6},
	} {
		got, ok := byValue[value]
		if !ok || got.Classification != want.class || got.ComparisonEntry != want.entry || got.Match != DiffMatchAlignedRequest {
			t.Fatalf("%s = %#v, want %s at comparison entry %d; all=%#v", value, got, want.class, want.entry, report.Classifications)
		}
	}
}

func TestDiffReportsWhyAnAlignedResponseDidNotMatch(t *testing.T) {
	baseline := pairHAR([]string{
		pairEntry("POST", "https://shop.example.test/login", `{"name":"Content-Type","value":"application/json"}`, `{"access_token":"Mq81Lz04Rt77"}`),
		pairEntry("GET", "https://shop.example.test/orders?access_token=Mq81Lz04Rt77", ``, `{"ok":true}`),
	})
	for name, tc := range map[string]struct {
		comparison []byte
		reason     string
		entry      int
	}{
		"extractor miss": {pairHAR([]string{
			pairEntry("POST", "https://shop.example.test/login", `{"name":"Content-Type","value":"application/json"}`, `{"error":"locked"}`),
		}), "did not match the aligned comparison response", 1},
		"empty value": {pairHAR([]string{
			pairEntry("POST", "https://shop.example.test/login", `{"name":"Content-Type","value":"application/json"}`, `{"access_token":""}`),
		}), "matched an empty value", 1},
		"no aligned request": {pairHAR([]string{
			pairEntry("POST", "https://shop.example.test/signin", `{"name":"Content-Type","value":"application/json"}`, `{"access_token":"Mq81Lz04Rt77"}`),
		}), "no request with the baseline producer's method, host and path", 0},
	} {
		t.Run(name, func(t *testing.T) {
			report, err := Diff(baseline, tc.comparison, ConvertOptions{ScriptName: "Miss"})
			if err != nil {
				t.Fatalf("Diff: %v", err)
			}
			if report.UnmatchedCandidates != 1 || len(report.Classifications) != 1 {
				t.Fatalf("report = %#v, want one unmatched candidate", report)
			}
			got := report.Classifications[0]
			if got.Classification != DiffClassificationCandidate || !strings.Contains(got.Reason, tc.reason) ||
				got.ComparisonEntry != tc.entry || got.ComparisonValue != "" || got.Match != DiffMatchAlignedRequest {
				t.Fatalf("classification = %#v, want candidate because %q", got, tc.reason)
			}
		})
	}
}

func TestDiffFallsBackToNamePairingForExtractorsItCannotEvaluate(t *testing.T) {
	baseline := pairRecording([]pairFlow{{kind: 0, path: "/login", dynamic: true}}, 0, nil)
	converted, err := Convert(baseline, ConvertOptions{ScriptName: "Fallback"})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	comparison, err := PrepareComparison(pairRecording([]pairFlow{{kind: 0, path: "/login", dynamic: true}}, 1, nil), ConvertOptions{ScriptName: "Fallback"})
	if err != nil {
		t.Fatalf("PrepareComparison: %v", err)
	}
	// An extractor kind the offline evaluation does not reproduce, applied to
	// both recordings alike, is still classified by the comparison correlation
	// of the same name.
	for _, corrs := range [][]sample.Correlation{converted.Correlations, comparison.correlations} {
		for index := range corrs {
			corrs[index].ExtractType = "xpath"
		}
	}
	report, err := DiffPrepared(converted, comparison)
	if err != nil {
		t.Fatalf("DiffPrepared: %v", err)
	}
	if report.DynamicCandidates != 1 || report.Classifications[0].Match != DiffMatchCorrelationName || report.Classifications[0].ComparisonEntry != 0 {
		t.Fatalf("report = %#v, want one dynamic candidate paired by name", report.Classifications)
	}
}

func TestDiffPreparedRequiresBothSides(t *testing.T) {
	if _, err := DiffPrepared(nil, &ComparisonRecording{}); err == nil {
		t.Fatal("nil baseline accepted")
	}
	if _, err := DiffPrepared(&ConvertResult{}, nil); err == nil {
		t.Fatal("nil comparison accepted")
	}
	if _, err := DiffWithBaseline(&ConvertResult{}, []byte("  "), ConvertOptions{}); err == nil || !strings.Contains(err.Error(), "comparison HAR") {
		t.Fatalf("blank comparison err = %v", err)
	}
}

func hiddenSIDHAR(value, encoded string) []byte {
	return pairHAR([]string{
		fmt.Sprintf(`{"startedDateTime":"2026-01-01T00:00:00Z","time":10,"request":{"method":"GET","url":"https://login.example.test/frontdoor.jsp","headers":[]},"response":{"status":200,"headers":[{"name":"Content-Type","value":"text/html"}],"content":{"mimeType":"text/html","text":%q}}}`,
			`<html><form method="POST" action="/contentDoor"><input type="hidden" name="sid" value="`+value+`"></form></html>`),
		fmt.Sprintf(`{"startedDateTime":"2026-01-01T00:00:01Z","time":10,"request":{"method":"POST","url":"https://login.example.test/contentDoor","headers":[{"name":"Content-Type","value":"application/x-www-form-urlencoded"}],"postData":{"mimeType":"application/x-www-form-urlencoded","text":%q}},"response":{"status":200,"headers":[],"content":{"text":"ok"}}}`,
			"sid="+encoded),
	})
}

func TestDiffComparesConvertedExtractorsInTheirRecordedForm(t *testing.T) {
	// The hidden-field rule records the captured text and URL-encodes it for
	// reuse; the comparison is classified on the captured text, as before.
	baseline := hiddenSIDHAR("00Dx0000000AbCd!AQ4AQHk2Lm9", "00Dx0000000AbCd%21AQ4AQHk2Lm9")
	for name, tc := range map[string]struct {
		comparison []byte
		class      string
	}{
		"static":  {hiddenSIDHAR("00Dx0000000AbCd!AQ4AQHk2Lm9", "00Dx0000000AbCd%21AQ4AQHk2Lm9"), DiffClassificationStatic},
		"dynamic": {hiddenSIDHAR("00Dx0000000AbCd!ZZ9QWer7Ty1", "00Dx0000000AbCd%21ZZ9QWer7Ty1"), DiffClassificationDynamic},
	} {
		t.Run(name, func(t *testing.T) {
			opts := ConvertOptions{ScriptName: "Hidden"}
			report, err := Diff(baseline, tc.comparison, opts)
			if err != nil {
				t.Fatalf("Diff: %v", err)
			}
			want, err := legacyDiffOracle(baseline, tc.comparison, opts)
			if err != nil {
				t.Fatalf("oracle: %v", err)
			}
			if len(report.Classifications) != 1 || len(want.Classifications) != 1 {
				t.Fatalf("classifications = %#v, want one", report.Classifications)
			}
			got := report.Classifications[0]
			if got.Classification != tc.class || got.Match != DiffMatchAlignedRequest ||
				got.ComparisonValue != want.Classifications[0].ComparisonValue || want.Classifications[0].Classification != tc.class {
				t.Fatalf("classification = %#v, oracle %#v, want %s", got, want.Classifications[0], tc.class)
			}
		})
	}
}

func TestDiffRequiresTheComparisonToReuseTheValue(t *testing.T) {
	// The comparison producer still answers, with a new token, but nothing in
	// the comparison uses it: the value cannot be called dynamic, and the
	// reason says what was checked.
	baseline := pairHAR([]string{
		pairEntry("POST", "https://shop.example.test/login", `{"name":"Content-Type","value":"application/json"}`, `{"access_token":"Mq81Lz04Rt77"}`),
		pairEntry("GET", "https://shop.example.test/orders?access_token=Mq81Lz04Rt77", ``, `{"ok":true}`),
	})
	comparison := pairHAR([]string{
		pairEntry("POST", "https://shop.example.test/login", `{"name":"Content-Type","value":"application/json"}`, `{"access_token":"Zq55Lp09Wx31"}`),
	})
	opts := ConvertOptions{ScriptName: "NoReuse"}
	legacy, err := legacyDiffOracle(baseline, comparison, opts)
	if err != nil {
		t.Fatalf("oracle: %v", err)
	}
	report, err := Diff(baseline, comparison, opts)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(report.Classifications) != 1 || legacy.Classifications[0].Classification != DiffClassificationCandidate {
		t.Fatalf("report = %#v, oracle = %#v", report.Classifications, legacy.Classifications)
	}
	got := report.Classifications[0]
	if got.Classification != DiffClassificationCandidate || report.UnmatchedCandidates != 1 || got.ComparisonEntry != 1 ||
		!strings.Contains(got.Reason, "never reuses the value") || got.ComparisonValue != "" {
		t.Fatalf("classification = %#v, want candidate because the comparison never reuses the value", got)
	}

	// The same token reused by a later comparison request is dynamic again.
	reused := pairHAR([]string{
		pairEntry("POST", "https://shop.example.test/login", `{"name":"Content-Type","value":"application/json"}`, `{"access_token":"Zq55Lp09Wx31"}`),
		pairEntry("GET", "https://shop.example.test/orders?access_token=Zq55Lp09Wx31", ``, `{"ok":true}`),
	})
	report, err = Diff(baseline, reused, opts)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if got := report.Classifications[0]; got.Classification != DiffClassificationDynamic || !strings.Contains(got.Reason, "reused later in both HARs") {
		t.Fatalf("classification = %#v, want dynamic", got)
	}
}

func TestDiffRetriesTheOtherResponsesOfTheRequestGroup(t *testing.T) {
	baseline := pairHAR([]string{
		pairEntry("POST", "https://shop.example.test/login", `{"name":"Content-Type","value":"application/json"}`, `{"access_token":"Mq81Lz04Rt77"}`),
		pairEntry("GET", "https://shop.example.test/orders?access_token=Mq81Lz04Rt77", ``, `{"ok":true}`),
	})
	for name, first := range map[string]string{
		// A failed first attempt the extractor does not match.
		"failed attempt": `{"error":"retry"}`,
		// A prefetch whose token is never used.
		"unused prefetch": `{"access_token":"Pf00Unused99"}`,
	} {
		t.Run(name, func(t *testing.T) {
			comparison := pairHAR([]string{
				pairEntry("POST", "https://shop.example.test/login", `{"name":"Content-Type","value":"application/json"}`, first),
				pairEntry("POST", "https://shop.example.test/login", `{"name":"Content-Type","value":"application/json"}`, `{"access_token":"Zq55Lp09Wx31"}`),
				pairEntry("GET", "https://shop.example.test/orders?access_token=Zq55Lp09Wx31", ``, `{"ok":true}`),
			})
			opts := ConvertOptions{ScriptName: "Retry"}
			legacy, err := legacyDiffOracle(baseline, comparison, opts)
			if err != nil {
				t.Fatalf("oracle: %v", err)
			}
			report, err := Diff(baseline, comparison, opts)
			if err != nil {
				t.Fatalf("Diff: %v", err)
			}
			got := report.Classifications[0]
			if got.Classification != DiffClassificationDynamic || got.ComparisonEntry != 2 || got.Match != DiffMatchAlignedRequest ||
				got.ComparisonValue != "Zq55Lp09Wx31" || legacy.Classifications[0].Classification != got.Classification {
				t.Fatalf("classification = %#v, oracle %#v, want dynamic from comparison entry 2", got, legacy.Classifications[0])
			}
		})
	}
}

func TestDiffPrefersResponsesNoOtherProducerWasAlignedWith(t *testing.T) {
	aligner := &diffAligner{claimed: map[int]int{2: 7, 3: 9}}
	candidates := []sample.HARSourceObservation{{}, {}, {}, {}}
	for index := range candidates {
		candidates[index].ID = index + 1
	}
	var ids []int
	for _, observation := range aligner.alignmentOrder(candidates, 0, 9) {
		ids = append(ids, observation.ID)
	}
	// Entry 1 is the ordinal pick, 3 is this producer's own, 2 belongs to
	// another producer and goes last.
	if want := []int{1, 3, 4, 2}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("order = %v, want %v", ids, want)
	}
	ids = ids[:0]
	for _, observation := range aligner.alignmentOrder(candidates, 5, 9) {
		ids = append(ids, observation.ID)
	}
	if want := []int{1, 3, 4, 2}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("out-of-range order = %v, want %v", ids, want)
	}
}
