package salesforce

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/convert/sample"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// reusedExtractorScript has a producer whose existing extractor reads $.tok
// with the given Convert, a JSON consumer, and a form consumer.
func reusedExtractorScript(convert, jsonBody, formBody string) *dsl.Script {
	return &dsl.Script{
		Name:     "SF reused extractor direction",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://acme.my.salesforce.com"},
		Actions: []dsl.Action{
			{
				Name: "Bootstrap", Type: "GET", Method: "GET", Target: "/bootstrap",
				Extractors: []dsl.Extractor{{Name: "corr_tok", Source: "body", Type: "jsonpath", Expression: "$.tok", Convert: convert, OnMiss: "fail"}},
			},
			{Name: "JSON use", Type: "POST", Method: "POST", Target: "/services/data/v60.0/use", Body: jsonBody},
			{Name: "Form use", Type: "POST", Method: "POST", Target: "/aura?r=1", Body: formBody},
		},
	}
}

func buildReusedExtractorPreview(t *testing.T, script *dsl.Script, value string) *NativePreview {
	t.Helper()
	preview, err := buildNativePreview(
		script,
		flattenRequestActions(script),
		[]harEntry{
			{ID: 1, Method: "GET", URL: "https://acme.my.salesforce.com/bootstrap"},
			{ID: 2, Method: "POST", URL: "https://acme.my.salesforce.com/services/data/v60.0/use"},
			{ID: 3, Method: "POST", URL: "https://acme.my.salesforce.com/aura?r=1"},
		},
		map[int]int{0: 1, 1: 2, 2: 3},
		[]correlate.Correlation{{
			Name:        "corr_salesforce_tok",
			Source:      "body",
			Expression:  "$.tok",
			ExtractType: "jsonpath",
			FoundInID:   1,
			UsedInIDs:   []int{2, 3},
			Value:       value,
			Confidence:  0.95,
		}},
		nil,
	)
	if err != nil {
		t.Fatalf("buildNativePreview: %v", err)
	}
	return preview
}

func extractorsByName(action dsl.Action) map[string]dsl.Extractor {
	out := make(map[string]dsl.Extractor, len(action.Extractors))
	for _, extractor := range action.Extractors {
		out[extractor.Name] = extractor
	}
	return out
}

func TestNativePreviewReusedRawExtractorKeepsRawConvertForEncodedLiterals(t *testing.T) {
	const value = "ab/cd+ef=="
	encoded := url.QueryEscape(value)
	// The sample converter templated the raw JSON consumer but left the
	// percent-encoded form spelling literal.
	script := reusedExtractorScript("", `{"t":"{{var.corr_tok}}"}`, "message=x&aura.token="+encoded)
	preview := buildReusedExtractorPreview(t, script, value)

	extractors := extractorsByName(preview.Script.Actions[0])
	if got := extractors["corr_tok"].Convert; got != "" {
		t.Fatalf("reused raw extractor Convert = %q, want unchanged \"\" (raw consumers would receive an encoded value): %#v", got, preview.Script.Actions[0].Extractors)
	}
	sibling, ok := extractors["corr_tok_encoded"]
	if !ok || sibling.Convert != "url_encode" || sibling.Expression != "$.tok" {
		t.Fatalf("extractors = %#v, want a url_encode corr_tok_encoded sibling", preview.Script.Actions[0].Extractors)
	}
	if got := preview.Script.Actions[1].Body; got != `{"t":"{{var.corr_tok}}"}` {
		t.Fatalf("raw consumer body = %q, want it still bound to corr_tok", got)
	}
	if got := preview.Script.Actions[2].Body; got != "message=x&aura.token={{var.corr_tok_encoded}}" {
		t.Fatalf("form body = %q, want the encoded literal bound to corr_tok_encoded", got)
	}
}

func TestNativePreviewReusedEncodedExtractorGivesRawLiteralsOwnVariable(t *testing.T) {
	const value = "ab/cd+ef=="
	// The sample converter extracted only the url_encode spelling and left the
	// raw JSON spelling literal.
	script := reusedExtractorScript("url_encode", `{"t":"`+value+`"}`, "message=x&aura.token={{var.corr_tok}}")
	preview := buildReusedExtractorPreview(t, script, value)

	extractors := extractorsByName(preview.Script.Actions[0])
	if got := extractors["corr_tok"].Convert; got != "url_encode" {
		t.Fatalf("reused encoded extractor Convert = %q, want url_encode: %#v", got, preview.Script.Actions[0].Extractors)
	}
	raw, ok := extractors["corr_tok_raw"]
	if !ok || raw.Convert != "" || raw.Expression != "$.tok" {
		t.Fatalf("extractors = %#v, want a raw corr_tok_raw extractor", preview.Script.Actions[0].Extractors)
	}
	if got := preview.Script.Actions[1].Body; got != `{"t":"{{var.corr_tok_raw}}"}` {
		t.Fatalf("raw consumer body = %q, want it bound to corr_tok_raw (not the url_encode corr_tok)", got)
	}
	if got := preview.Script.Actions[2].Body; got != "message=x&aura.token={{var.corr_tok}}" {
		t.Fatalf("form body = %q, want it still bound to corr_tok", got)
	}
	if len(preview.Correlations) != 1 || !preview.Correlations[0].PreApplied {
		t.Fatalf("correlations = %#v, want PreApplied", preview.Correlations)
	}
}

func TestExtractAuraDescriptorParsesBodyWhenEvidenceDoesNotDecode(t *testing.T) {
	message := url.QueryEscape(`{"actions":[{"id":"1;a","descriptor":"aura://RecordUiController/ACTION$getRecordWithFields","params":{}}]}`)
	script := &dsl.Script{
		Name:     "SF descriptor",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://acme.lightning.force.com"},
		Actions: []dsl.Action{{
			// A bare % in the name makes the joined evidence fail to
			// QueryUnescape, so only the body parse can find the descriptor.
			Name:   "list 50% loaded",
			Type:   "POST",
			Method: "POST",
			Target: "/aura?r=1",
			Body:   "message=" + message + "&aura.context=%7B%7D",
		}},
	}
	refs := flattenRequestActions(script)
	if len(refs) != 1 || refs[0].Descriptor != "aura://RecordUiController/ACTION$getRecordWithFields" {
		t.Fatalf("refs = %#v, want the body's first Aura descriptor", refs)
	}
}

func TestApplyDiffCorrelationEvidencePrefersExactMatchOverTruncatedValue(t *testing.T) {
	prefix := "https://acme-long-mydomain-name.lightning.force.com/lightning/r/Account/001ir00000"
	valueA := prefix + "AAAAAAA/view"
	valueB := prefix + "BBBBBBB/view"
	if correlate.Truncate(valueA, 80) != correlate.Truncate(valueB, 80) {
		t.Fatal("fixture values must share their truncated sample")
	}
	// The correlation's own expression matches neither item, so only the
	// exact-value grade can tell B's item from A's colliding sample.
	newCorrelation := func(name string) []correlate.Correlation {
		return []correlate.Correlation{{
			Name:        name,
			Source:      "body",
			Expression:  "$.record.url",
			FoundInID:   2,
			Value:       valueB,
			SampleValue: correlate.Truncate(valueB, 80),
			Confidence:  0.7,
		}}
	}

	// A's colliding sample comes first and says static; B's exact value says
	// dynamic. The exact value must win and boost.
	dynamicExact := newCorrelation("corr_b")
	applyDiffCorrelationEvidence(dynamicExact, []DiffCorrelationEvidence{
		{Source: "body", Expression: "$.a", BaselineValue: correlate.Truncate(valueA, 80), BaselineEntry: 2, Classification: "static"},
		{Source: "body", Expression: "$.b", BaselineValue: valueB, BaselineEntry: 2, Classification: "dynamic"},
	})
	if dynamicExact[0].Confidence != 0.9 {
		t.Fatalf("confidence = %v reasons = %v, want B's exact dynamic item to win over A's colliding static sample", dynamicExact[0].Confidence, dynamicExact[0].Reasons)
	}

	// Reversed: A's colliding sample says dynamic, B's exact value says
	// static. No boost, and the static outcome is visible in the reasons.
	staticExact := newCorrelation("corr_aura_fwuid_b")
	applyDiffCorrelationEvidence(staticExact, []DiffCorrelationEvidence{
		{Source: "body", Expression: "$.a", BaselineValue: correlate.Truncate(valueA, 80), BaselineEntry: 2, Classification: "dynamic"},
		{Source: "body", Expression: "$.b", BaselineValue: valueB, BaselineEntry: 2, Classification: "static"},
	})
	if staticExact[0].Confidence != 0.7 || len(staticExact[0].Reasons) != 1 || !strings.Contains(staticExact[0].Reasons[0], "same_day_release_coupled") {
		t.Fatalf("confidence = %v reasons = %v, want B's exact static item to win over A's colliding dynamic sample", staticExact[0].Confidence, staticExact[0].Reasons)
	}
}

func TestApplyDiffCorrelationEvidenceNeverLowersConfidence(t *testing.T) {
	const value = "a0B5e00000ZZZZZZZZ"
	correlations := []correlate.Correlation{{
		Name: "corr_rule", Source: "body", Expression: "$.id", FoundInID: 4,
		Value: value, SampleValue: value, Confidence: 1,
	}}
	applyDiffCorrelationEvidence(correlations, []DiffCorrelationEvidence{
		{Source: "body", Expression: "$.id", BaselineValue: value, BaselineEntry: 4, Classification: "dynamic"},
	})
	if correlations[0].Confidence != 1 {
		t.Fatalf("confidence = %v, want a project rule's 1.0 kept after the dynamic confirmation", correlations[0].Confidence)
	}
}

func TestApplyDiffCorrelationEvidenceBoostsLongNamePairedDynamicWithoutEntry(t *testing.T) {
	long := "vfcsrf_" + strings.Repeat("Q2xpZW50U2lkZUNTUkY", 6)
	if len(long) <= 80 {
		t.Fatal("fixture value must exceed the public sample length")
	}
	newCorrelation := func() []correlate.Correlation {
		return []correlate.Correlation{{
			Name: "corr_vf_csrf", Source: "body", Expression: "$.csrf", FoundInID: 4,
			Value: long, SampleValue: correlate.Truncate(long, 80), Confidence: 0.6,
		}}
	}
	// The diff only ever publishes the truncated sample; with the same
	// extractor it still names this correlation.
	byExpression := newCorrelation()
	applyDiffCorrelationEvidence(byExpression, []DiffCorrelationEvidence{
		{Name: "corr_csrf", Source: "body", Expression: "$.csrf", BaselineValue: correlate.Truncate(long, 80), Classification: "dynamic"},
	})
	if byExpression[0].Confidence != 0.8 {
		t.Fatalf("confidence = %v, want 0.8 after the name-paired dynamic boost of a long value", byExpression[0].Confidence)
	}
	byName := newCorrelation()
	applyDiffCorrelationEvidence(byName, []DiffCorrelationEvidence{
		{Name: "corr_vf_csrf", Source: "body", Expression: "$.page.csrf", BaselineValue: correlate.Truncate(long, 80), Classification: "dynamic"},
	})
	if byName[0].Confidence != 0.8 {
		t.Fatalf("confidence = %v, want 0.8 when the unplaced item carries the correlation's name", byName[0].Confidence)
	}
	// A shared sample under another extractor and name is not evidence.
	unrelated := newCorrelation()
	applyDiffCorrelationEvidence(unrelated, []DiffCorrelationEvidence{
		{Name: "corr_other", Source: "body", Expression: "$.other", BaselineValue: correlate.Truncate(long, 80), Classification: "dynamic"},
	})
	if unrelated[0].Confidence != 0.6 {
		t.Fatalf("confidence = %v, want no boost from an unrelated unplaced sample", unrelated[0].Confidence)
	}
}

func preAppliedSampleExtractorPreview(t *testing.T, confidence float64) (*NativePreview, string) {
	t.Helper()
	return preAppliedSampleExtractorPreviewWithMiss(t, confidence, true)
}

func preAppliedSampleExtractorPreviewWithMiss(t *testing.T, confidence float64, soft bool) (*NativePreview, string) {
	t.Helper()
	const value = "a0B5e00000ReusedId"
	recorded := value
	extractor := dsl.Extractor{Name: "corr_id", Source: "body", Type: "jsonpath", Expression: "$.id", OnMiss: "fail"}
	if soft {
		extractor.OnMiss = "default"
		extractor.DefaultValue = &recorded
	}
	script := &dsl.Script{
		Name:     "SF reused soft extractor",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://acme.my.salesforce.com"},
		Actions: []dsl.Action{
			{
				Name: "Bootstrap", Type: "GET", Method: "GET", Target: "/bootstrap",
				// The sample converter rated the value uncertain: a miss replays
				// the recorded literal.
				Extractors: []dsl.Extractor{extractor},
			},
			{Name: "Use", Type: "POST", Method: "POST", Target: "/services/data/v60.0/use", Body: `{"id":"{{var.corr_id}}"}`},
		},
	}
	preview, err := buildNativePreview(
		script,
		flattenRequestActions(script),
		[]harEntry{
			{ID: 1, Method: "GET", URL: "https://acme.my.salesforce.com/bootstrap"},
			{ID: 2, Method: "POST", URL: "https://acme.my.salesforce.com/services/data/v60.0/use"},
		},
		map[int]int{0: 1, 1: 2},
		[]correlate.Correlation{{
			Name: "corr_salesforce_record_id", Source: "body", Expression: "$.id", ExtractType: "jsonpath",
			FoundInID: 1, UsedInIDs: []int{2}, Value: value, Confidence: confidence,
		}},
		nil,
	)
	if err != nil {
		t.Fatalf("buildNativePreview: %v", err)
	}
	return preview, value
}

func TestNativePreviewRequiresAReusedSoftExtractorToMatchWhenConfirmed(t *testing.T) {
	preview, _ := preAppliedSampleExtractorPreview(t, 0.85)
	extractor, ok := extractorsByName(preview.Script.Actions[0])["corr_id"]
	if !ok || len(preview.Script.Actions[0].Extractors) != 1 {
		t.Fatalf("extractors = %#v, want the reused corr_id only", preview.Script.Actions[0].Extractors)
	}
	if extractor.OnMiss != "fail" || extractor.DefaultValue != nil {
		t.Fatalf("reused extractor on_miss = %q default = %v, want fail with no recorded fallback", extractor.OnMiss, extractor.DefaultValue)
	}
	if len(preview.Correlations) != 1 || !preview.Correlations[0].PreApplied || preview.Correlations[0].ReviewAction != "" {
		t.Fatalf("correlations = %#v, want PreApplied with no review action", preview.Correlations)
	}
}

func TestNativePreviewKeepsReviewForAWiredLowConfidenceReuseWithRecordedFallback(t *testing.T) {
	preview, value := preAppliedSampleExtractorPreview(t, 0.7)
	extractor := extractorsByName(preview.Script.Actions[0])["corr_id"]
	// Below the native threshold the sample converter's own policy stands.
	if extractor.OnMiss != "default" || extractor.DefaultValue == nil || *extractor.DefaultValue != value {
		t.Fatalf("reused extractor = %#v, want the sample's default fallback kept below the native threshold", extractor)
	}
	// A miss would silently replay the recorded literal, so the engineer must
	// still be asked to review it.
	if len(preview.Correlations) != 1 || !preview.Correlations[0].PreApplied || preview.Correlations[0].ReviewAction != "manual_review_required" {
		t.Fatalf("correlations = %#v, want PreApplied with manual_review_required", preview.Correlations)
	}
}

func TestNativePreviewDoesNotAskToReviewAnAlreadyWiredLowConfidenceReuse(t *testing.T) {
	preview, _ := preAppliedSampleExtractorPreviewWithMiss(t, 0.7, false)
	extractor := extractorsByName(preview.Script.Actions[0])["corr_id"]
	if extractor.OnMiss != "fail" || extractor.DefaultValue != nil {
		t.Fatalf("reused extractor = %#v, want its hard miss policy kept", extractor)
	}
	if len(preview.Correlations) != 1 || !preview.Correlations[0].PreApplied || preview.Correlations[0].ReviewAction != "" {
		t.Fatalf("correlations = %#v, want PreApplied with no manual_review_required", preview.Correlations)
	}
}

func TestBuildNativePreviewKeepsQueryMetadataLiteralWhenOnlyAnotherConsumerIsRewritten(t *testing.T) {
	const rawValue = "tok +/== recorded"
	unsupportedSpelling := url.PathEscape(rawValue)
	target := "/callback?access_token=" + unsupportedSpelling
	script := &dsl.Script{
		Name:     "SF query metadata with a raw consumer elsewhere",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://acme.my.salesforce.com"},
		Actions: []dsl.Action{
			{Name: "Bootstrap", Type: "GET", Method: "GET", Target: "/bootstrap"},
			{
				Name: "Use token", Type: "GET", Method: "GET", Target: target,
				Metadata: map[string]any{
					"har.request.query_string": []any{map[string]any{"name": "access_token", "value": rawValue}},
				},
			},
			{Name: "JSON use", Type: "POST", Method: "POST", Target: "/services/data/v60.0/use", Body: `{"t":"` + rawValue + `"}`},
		},
	}
	preview, err := buildNativePreview(
		script,
		flattenRequestActions(script),
		[]harEntry{
			{ID: 1, Method: "GET", URL: "https://acme.my.salesforce.com/bootstrap"},
			{ID: 2, Method: "GET", URL: "https://acme.my.salesforce.com" + target},
			{ID: 3, Method: "POST", URL: "https://acme.my.salesforce.com/services/data/v60.0/use"},
		},
		map[int]int{0: 1, 1: 2, 2: 3},
		[]correlate.Correlation{{
			Name: "corr_access_token", Source: "body", Expression: "$.access_token", ExtractType: "jsonpath",
			FoundInID: 1, UsedInIDs: []int{2, 3}, Value: rawValue, Confidence: 0.95,
		}},
		nil,
	)
	if err != nil {
		t.Fatalf("buildNativePreview: %v", err)
	}
	if got := preview.Script.Actions[2].Body; !strings.Contains(got, "{{var.corr_access_token}}") {
		t.Fatalf("JSON body = %q, want the raw consumer rewritten", got)
	}
	if got := preview.Script.Actions[1].Target; got != target {
		t.Fatalf("target = %q, want the unsupported spelling left literal", got)
	}
	query := preview.Script.Actions[1].Metadata["har.request.query_string"].([]any)[0].(map[string]any)["value"]
	if query != rawValue {
		t.Fatalf("har.request.query_string value = %v, want the recorded value kept while the wire still sends the literal", query)
	}
}

func TestApplyDiffCorrelationEvidenceBoostsNamePairedDynamicWithoutEntry(t *testing.T) {
	const value = "a0B5e00000ZZZZZZZZ"
	correlations := []correlate.Correlation{{
		Name:        "corr_salesforce_record_id",
		Source:      "body",
		Expression:  "$.id",
		FoundInID:   4,
		Value:       value,
		SampleValue: value,
		Confidence:  0.7,
	}}
	// A name-paired classification whose extractor could not be re-evaluated
	// offline carries no baseline entry.
	applyDiffCorrelationEvidence(correlations, []DiffCorrelationEvidence{
		{Name: "corr_record_id", Source: "body", Expression: "$.record.id", BaselineValue: value, Classification: "dynamic"},
	})
	if correlations[0].Confidence != 0.9 {
		t.Fatalf("confidence = %v, want 0.9 after the name-paired dynamic boost", correlations[0].Confidence)
	}

	// A truncated sample alone never ties an unplaced item to a correlation.
	long := strings.Repeat("x", 100)
	other := strings.Repeat("x", 90)
	truncatedOnly := []correlate.Correlation{{Name: "corr_long", FoundInID: 4, Value: long, SampleValue: correlate.Truncate(long, 80), Confidence: 0.7}}
	applyDiffCorrelationEvidence(truncatedOnly, []DiffCorrelationEvidence{
		{BaselineValue: correlate.Truncate(other, 80), Classification: "dynamic"},
	})
	if truncatedOnly[0].Confidence != 0.7 {
		t.Fatalf("confidence = %v, want no boost from an unplaced truncated sample", truncatedOnly[0].Confidence)
	}
}

func TestDomainObservationsCountImportedRequestsSeparatelyFromRawHAR(t *testing.T) {
	refs := []actionRef{
		{URL: "https://acme.lightning.force.com/aura?r=1", HARID: 1},
		{URL: "https://acme.lightning.force.com/aura?r=2", HARID: 2},
		// A host-less target still counts once, at its matched entry's host.
		{URL: "#html-step", HARID: 3},
	}
	entries := []harEntry{
		{ID: 1, URL: "https://acme.lightning.force.com/aura?r=1"},
		{ID: 2, URL: "https://acme.lightning.force.com/aura?r=2"},
		{ID: 3, URL: "https://test.salesforce.com/login"},
		{ID: 4, URL: "https://b.static.lightning.force.com/a.js"},
		{ID: 5, URL: "https://b.static.lightning.force.com/b.js"},
	}
	harURLs := make([]string, 0, len(entries)+1)
	for _, entry := range entries {
		harURLs = append(harURLs, entry.URL)
	}
	// A raw entry the observation parse drops (a telemetry beacon, tunnel or
	// socket upgrade) still counts toward its host's raw total.
	harURLs = append(harURLs, "https://acme.lightning.force.com/aura?r=telemetry")
	got := map[string]DomainObservation{}
	for _, domain := range domainObservations(refs, entries, harURLs) {
		got[domain.Host] = domain
	}
	want := map[string][2]int{
		"acme.lightning.force.com":     {2, 3},
		"test.salesforce.com":          {1, 1},
		"b.static.lightning.force.com": {0, 2},
	}
	if len(got) != len(want) {
		t.Fatalf("domains = %#v, want %v", got, want)
	}
	for host, counts := range want {
		if got[host].Count != counts[0] || got[host].HARCount != counts[1] {
			t.Fatalf("domain %s = %+v, want count=%d har_count=%d", host, got[host], counts[0], counts[1])
		}
	}
}

func TestTemplatedHARIndexBucketsOnlyMatchingShapes(t *testing.T) {
	entries := []harEntry{
		{ID: 1, Method: "GET", URL: "https://acme.my.salesforce.com/services/data/v60.0/ui-api/records/001A"},
		{ID: 2, Method: "GET", URL: "https://acme.my.salesforce.com/services/data/v60.0/ui-api/layouts/001A"},
		{ID: 3, Method: "GET", URL: "https://other.my.salesforce.com/services/data/v60.0/ui-api/records/001B"},
		{ID: 4, Method: "GET", URL: "https://acme.my.salesforce.com/services/data/v60.0/ui-api/records/001C/extra"},
		{ID: 5, Method: "GET", URL: "https://acme.my.salesforce.com/services/data/v60.0/ui-api/records/001D"},
	}
	candidates := make([]harCandidate, len(entries))
	for index, entry := range entries {
		candidates[index] = harCandidate{entry: entry, url: parseNormalizedURL(entry.URL)}
	}
	index := templatedHARIndex{}
	bucket := index.bucket(parseNormalizedURL("https://acme.my.salesforce.com/services/data/v60.0/ui-api/records/{{var.id}}"), candidates)
	var ids []int
	for _, candidateIndex := range bucket {
		ids = append(ids, candidates[candidateIndex].entry.ID)
	}
	if len(ids) != 2 || ids[0] != 1 || ids[1] != 5 {
		t.Fatalf("bucket ids = %v, want [1 5] in document order", ids)
	}

	refs := []actionRef{
		{Method: "GET", URL: "https://acme.my.salesforce.com/services/data/v60.0/ui-api/records/{{var.id}}"},
		{Method: "GET", URL: "https://acme.my.salesforce.com/services/data/v60.0/ui-api/records/{{var.id2}}"},
	}
	matches := matchHAR(refs, entries)
	if matches[0] != 1 || matches[1] != 5 {
		t.Fatalf("matches = %v, want templated refs paired with entries 1 and 5", matches)
	}
}

func TestExtractAuraDescriptorSkipsTheBodyParseOffAuraEndpoints(t *testing.T) {
	message := url.QueryEscape(`{"actions":[{"id":"1;a","descriptor":"aura://RecordUiController/ACTION$getRecordWithFields","params":{}}]}`)
	script := &dsl.Script{
		Name:     "SF composite body",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://acme.my.salesforce.com"},
		Actions: []dsl.Action{{
			// The undecodable name keeps the evidence fallback blind, so a
			// descriptor here could only come from parsing a non-Aura body.
			Name:   "composite 50% batch",
			Type:   "POST",
			Method: "POST",
			Target: "/services/data/v60.0/composite",
			Body:   "message=" + message + "&aura.context=%7B%7D",
		}},
	}
	refs := flattenRequestActions(script)
	if len(refs) != 1 || refs[0].Descriptor != "" {
		t.Fatalf("refs = %#v, want no descriptor parsed from a non-Aura request body", refs)
	}
}

func TestHARRequestURLsCountEntriesTheObservationParseDrops(t *testing.T) {
	document, err := sample.ParseHARDocument(`{"log":{"entries":[
		{"request":{"method":"GET","url":"https://acme.lightning.force.com/one/one.app","headers":[]},"response":{"status":200,"headers":[],"content":{"text":""}}},
		{"request":{"method":"CONNECT","url":"https://acme.lightning.force.com:443","headers":[]},"response":{"status":200,"headers":[],"content":{"text":""}}},
		{"request":{"method":"GET","url":"blob:https://acme.lightning.force.com/abc","headers":[]},"response":{"status":200,"headers":[],"content":{"text":""}}}
	]}}`)
	if err != nil {
		t.Fatalf("ParseHARDocument: %v", err)
	}
	entries, err := harDocumentEntries(document)
	if err != nil {
		t.Fatalf("harDocumentEntries: %v", err)
	}
	if len(entries) >= 3 {
		t.Fatalf("observation entries = %d, want the tunnel and blob entries dropped", len(entries))
	}
	var lightning DomainObservation
	for _, domain := range domainObservations(nil, entries, harRequestURLs(document)) {
		if domain.Host == "acme.lightning.force.com" {
			lightning = domain
		}
	}
	if lightning.HARCount != 2 {
		t.Fatalf("lightning har_count = %d, want 2 (the page and the CONNECT tunnel)", lightning.HARCount)
	}
}

func TestNativePreviewHardensTheSampleEncodedSiblingOfAConfirmedReuse(t *testing.T) {
	const value = "tok+/=recorded"
	encoded := url.QueryEscape(value)
	recordedRaw, recordedEncoded := value, encoded
	encodedConvert, ok := correlate.EncodedReuseExtractorConvert("")
	if !ok {
		t.Fatalf("no encoded-reuse convert for a raw extractor")
	}
	script := &dsl.Script{
		Name:     "SF reused soft extractor pair",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://acme.my.salesforce.com"},
		Actions: []dsl.Action{
			{
				Name: "Bootstrap", Type: "GET", Method: "GET", Target: "/bootstrap",
				// The sample converter rated the value uncertain and wrote a
				// raw extractor plus its url-encoded sibling, each falling back
				// to its own recorded spelling.
				Extractors: []dsl.Extractor{
					{Name: "corr_tok", Source: "body", Type: "jsonpath", Expression: "$.tok", OnMiss: "default", DefaultValue: &recordedRaw},
					{Name: "corr_tok_encoded", Source: "body", Type: "jsonpath", Expression: "$.tok", Convert: encodedConvert, OnMiss: "default", DefaultValue: &recordedEncoded},
				},
			},
			{Name: "Use JSON", Type: "POST", Method: "POST", Target: "/services/data/v60.0/use", Body: `{"tok":"{{var.corr_tok}}"}`},
			{Name: "Use form", Type: "POST", Method: "POST", Target: "/aura", Body: "aura.token={{var.corr_tok_encoded}}"},
		},
	}
	preview, err := buildNativePreview(
		script,
		flattenRequestActions(script),
		[]harEntry{
			{ID: 1, Method: "GET", URL: "https://acme.my.salesforce.com/bootstrap"},
			{ID: 2, Method: "POST", URL: "https://acme.my.salesforce.com/services/data/v60.0/use"},
			{ID: 3, Method: "POST", URL: "https://acme.my.salesforce.com/aura"},
		},
		map[int]int{0: 1, 1: 2, 2: 3},
		[]correlate.Correlation{{
			Name: "corr_salesforce_tok", Source: "body", Expression: "$.tok", ExtractType: "jsonpath",
			FoundInID: 1, UsedInIDs: []int{2, 3}, Value: value, Confidence: 0.85,
		}},
		nil,
	)
	if err != nil {
		t.Fatalf("buildNativePreview: %v", err)
	}
	byName := extractorsByName(preview.Script.Actions[0])
	for _, name := range []string{"corr_tok", "corr_tok_encoded"} {
		extractor, ok := byName[name]
		if !ok {
			t.Fatalf("extractors = %#v, want %s kept", preview.Script.Actions[0].Extractors, name)
		}
		if extractor.OnMiss != "fail" || extractor.DefaultValue != nil {
			t.Fatalf("%s on_miss = %q default = %v, want fail with no recorded fallback", name, extractor.OnMiss, extractor.DefaultValue)
		}
	}
}

func TestBuildNativePreviewDoesNotAddAnUnnamedExtractorWhenCapacityBails(t *testing.T) {
	const value = "capacity token/123456"
	encoded := url.QueryEscape(value)
	existing := make([]dsl.Extractor, dsl.MaxActionExtractors)
	for index := range existing {
		existing[index] = dsl.Extractor{Name: fmt.Sprintf("existing_%03d", index), Source: "body", Type: "regex", Expression: `(existing)`}
	}
	// The reused raw extractor already yields the value; the encoded consumer
	// would need one more slot the action does not have.
	existing[0] = dsl.Extractor{Name: "corr_capacity_token", Source: "body", Type: "regex", Expression: `(capacity token/123456)`}
	script := &dsl.Script{
		Name:     "capacity reuse preview",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://acme.my.salesforce.com"},
		Actions: []dsl.Action{
			{Name: "Source", Type: "GET", Method: "GET", Target: "/source", Extractors: existing},
			{Name: "Consumer", Type: "POST", Method: "POST", Target: "/use?encoded=" + encoded, Body: "raw={{var.corr_capacity_token}}"},
		},
	}
	preview, err := buildNativePreview(
		script,
		flattenRequestActions(script),
		[]harEntry{
			{ID: 1, Method: "GET", URL: "https://acme.my.salesforce.com/source", ResponseBody: "token=" + value},
			{ID: 2, Method: "POST", URL: "https://acme.my.salesforce.com/use?encoded=" + encoded, RequestBody: "raw=" + value},
		},
		map[int]int{0: 1, 1: 2},
		[]correlate.Correlation{{
			Name: "corr_capacity_token", Source: "body", Expression: `(capacity token/123456)`, ExtractType: "regex",
			FoundInID: 1, UsedInIDs: []int{2}, Value: value, Confidence: 0.95,
		}},
		nil,
	)
	if err != nil {
		t.Fatalf("buildNativePreview: %v", err)
	}
	extractors := preview.Script.Actions[0].Extractors
	if len(extractors) != dsl.MaxActionExtractors {
		t.Fatalf("source extractors = %d, want unchanged %d", len(extractors), dsl.MaxActionExtractors)
	}
	for _, extractor := range extractors {
		if extractor.Name == "" {
			t.Fatalf("source gained an unnamed extractor: %#v", extractor)
		}
	}
}

func TestApplyCorrelationVarsToActionQueryMetadataMatchesEachParameterByName(t *testing.T) {
	const value = "ab/cd"
	const template = "{{var.corr_t}}"
	for _, metadata := range []map[string]any{
		{"har.request.query_string": []any{
			map[string]any{"name": "a", "value": value},
			map[string]any{"name": "b", "value": value},
		}},
		{"har.request.query_string": []map[string]any{
			{"name": "a", "value": value},
			{"name": "b", "value": value},
		}},
	} {
		// a took the template; b still sends the percent-encoded literal.
		action := &dsl.Action{Target: "/p?a=" + template + "&b=ab%2Fcd", Metadata: metadata}
		applyCorrelationVarsToActionQueryMetadata(action, map[string]string{value: template})
		got := map[string]string{}
		switch items := action.Metadata["har.request.query_string"].(type) {
		case []any:
			for _, raw := range items {
				item := raw.(map[string]any)
				got[item["name"].(string)] = item["value"].(string)
			}
		case []map[string]any:
			for _, item := range items {
				got[item["name"].(string)] = item["value"].(string)
			}
		}
		if got["a"] != template || got["b"] != value {
			t.Fatalf("query provenance = %#v, want a templated and b kept literal like its wire value", got)
		}
	}
}

func TestAuraTokenCookieNamePatternAcceptsEitherSuffixSign(t *testing.T) {
	for _, recorded := range []string{"__Host-ERIC_PROD-2876452450001777317", "__Host-ERIC_PROD1049330172993496433"} {
		pattern := regexp.MustCompile("^" + auraTokenCookieNamePattern(recorded) + "$")
		for _, other := range []string{"__Host-ERIC_PROD-2876452450001777317", "__Host-ERIC_PROD1049330172993496433", "__Host-ERIC_PROD9"} {
			if !pattern.MatchString(other) {
				t.Fatalf("pattern %s from %s rejects %s", pattern, recorded, other)
			}
		}
		for _, other := range []string{"__Host-ERIC_PROD--1", "__Host-ERIC_DEV-1", "ERIC_PROD-1", "__Host-ERIC_PROD-"} {
			if pattern.MatchString(other) {
				t.Fatalf("pattern %s from %s accepts %s", pattern, recorded, other)
			}
		}
	}
}
