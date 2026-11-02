package salesforce

import (
	"net/url"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/correlate"
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
	correlations := []correlate.Correlation{{
		Name:        "corr_b",
		Source:      "body",
		Expression:  "$.b",
		FoundInID:   2,
		Value:       valueB,
		SampleValue: correlate.Truncate(valueB, 80),
		Confidence:  0.7,
	}}
	evidence := []DiffCorrelationEvidence{
		// Dynamic-first order: A's truncated sample collides with B's.
		{Source: "body", Expression: "$.a", BaselineValue: correlate.Truncate(valueA, 80), BaselineEntry: 2, Classification: "dynamic"},
		{Source: "body", Expression: "$.b", BaselineValue: correlate.Truncate(valueB, 80), BaselineEntry: 2, Classification: "static"},
	}
	applyDiffCorrelationEvidence(correlations, evidence)
	if correlations[0].Confidence != 0.7 {
		t.Fatalf("confidence = %v reasons = %v, want B's own static item to win over A's colliding sample", correlations[0].Confidence, correlations[0].Reasons)
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
	got := map[string]DomainObservation{}
	for _, domain := range domainObservations(refs, entries) {
		got[domain.Host] = domain
	}
	want := map[string][2]int{
		"acme.lightning.force.com":     {2, 2},
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
