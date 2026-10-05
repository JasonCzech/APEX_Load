package recorder

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestConvertSession_Basic(t *testing.T) {
	s := &Session{
		ID:      "test",
		Name:    "Login Flow",
		BaseURL: "http://example.com",
		Requests: []RecordedRequest{
			{
				Seq:            1,
				Method:         "GET",
				URL:            "http://example.com/",
				Path:           "/",
				Host:           "example.com",
				StatusCode:     200,
				ResponseTimeMs: 50,
				Headers:        map[string]string{"Accept": "text/html"},
			},
			{
				Seq:            2,
				Method:         "POST",
				URL:            "http://example.com/api/login",
				Path:           "/api/login",
				Host:           "example.com",
				Body:           `{"username":"test","password":"secret"}`,
				ContentType:    "application/json",
				StatusCode:     200,
				ResponseTimeMs: 120,
				ResponseBody:   `{"token":"abc123","session_id":"sess-xyz-789"}`,
				Headers:        map[string]string{"Content-Type": "application/json"},
				ThinkTimeMs:    2000,
			},
		},
	}

	result := ConvertSession(s, "")
	if result.Script == nil {
		t.Fatal("expected script")
	}
	if result.Script.Name != "Login Flow" {
		t.Fatalf("expected Login Flow, got %s", result.Script.Name)
	}
	if result.Script.Protocol != "http" {
		t.Fatalf("expected http protocol, got %s", result.Script.Protocol)
	}
	if result.Script.Config.BaseURL != "http://example.com" {
		t.Fatalf("expected base URL, got %s", result.Script.Config.BaseURL)
	}
	if len(result.Script.Actions) != 2 {
		t.Fatalf("expected 2 actions, got %d", len(result.Script.Actions))
	}

	a1 := result.Script.Actions[0]
	if a1.Method != "GET" {
		t.Fatalf("expected GET, got %s", a1.Method)
	}

	a2 := result.Script.Actions[1]
	if a2.Method != "POST" {
		t.Fatalf("expected POST, got %s", a2.Method)
	}
	if a2.Body == "" {
		t.Fatal("expected body on POST action")
	}
	if !a1.ThinkTimeAfter {
		t.Fatal("expected think time after first action before POST (2000ms)")
	}
	if a1.ThinkTime.MeanMs != 2000 {
		t.Fatalf("expected think time 2000ms, got %f", a1.ThinkTime.MeanMs)
	}
	if a2.ThinkTimeAfter {
		t.Fatal("did not expect think time after second action")
	}
}

func TestPrepareRecorderCorrelationApplicationRejectsEncodedRawLiteralCollision(t *testing.T) {
	const rawValue = "opaque token/123456"
	encodedValue := url.QueryEscape(rawValue)
	requests := []RecordedRequest{
		{Seq: 1, URL: "https://api.example.test/source-a"},
		{Seq: 2, URL: "https://api.example.test/source-b"},
		{Seq: 3, URL: "https://api.example.test/use?token=" + encodedValue},
	}
	correlations := []Correlation{
		{
			Name:        "corr_a",
			Source:      "body",
			ExtractType: "regex",
			Expression:  `(opaque token/123456)`,
			Value:       rawValue,
			FoundInSeq:  1,
			UsedInSeqs:  []int{3},
			Confidence:  0.9,
		},
		{
			Name:        "corr_b",
			Source:      "body",
			ExtractType: "regex",
			Expression:  `(opaque\+token%2F123456)`,
			Value:       encodedValue,
			FoundInSeq:  2,
			UsedInSeqs:  []int{3},
			Confidence:  0.9,
		},
	}

	application := prepareRecorderCorrelationApplication(requests, correlations)
	if len(application.vars) != 0 || len(application.sourceSequences) != 0 || len(application.extractors) != 0 {
		t.Fatalf("ambiguous collision application = %#v, want no automatic rewrite", application)
	}
	for _, correlation := range correlations {
		if correlation.PreApplied || correlation.ReviewAction != recorderManualCorrelationReplacement {
			t.Fatalf("collision correlation = %#v, want manual and not pre-applied", correlation)
		}
	}
}

func TestPrepareRecorderCorrelationApplicationRejectsEncodedValueBeyondRuntimeResultLimit(t *testing.T) {
	// Raw value fits the 16 KiB per-extraction ceiling, while QueryEscape
	// expands it beyond that ceiling.
	value := strings.Repeat("?", 6_000)
	encoded := url.QueryEscape(value)
	requests := []RecordedRequest{
		{Seq: 1, URL: "https://api.example.test/source"},
		{Seq: 2, URL: "https://api.example.test/use?token=" + encoded},
	}
	correlations := []Correlation{{
		Name:        "corr_token",
		Source:      "body",
		ExtractType: "regex",
		Expression:  `(token)`,
		Value:       value,
		FoundInSeq:  1,
		UsedInSeqs:  []int{2},
		Confidence:  0.9,
	}}
	application := prepareRecorderCorrelationApplication(requests, correlations)
	if len(application.vars) != 0 || len(application.sourceSequences) != 0 || len(application.extractors) != 0 {
		t.Fatalf("oversized encoded application = %#v, want none", application)
	}
	if correlations[0].PreApplied || correlations[0].ReviewAction != recorderManualCorrelationReplacement {
		t.Fatalf("oversized encoded correlation = %#v, want manual review", correlations[0])
	}
}

func TestPrepareRecorderCorrelationApplicationRespectsAggregateRuntimeResultLimit(t *testing.T) {
	requests := []RecordedRequest{{Seq: 1, URL: "https://api.example.test/source"}}
	correlations := make([]Correlation, 0, 5)
	extractionLimit := 0
	for index := 0; index < 5; index++ {
		value := fmt.Sprintf("token-%d-%s", index, strings.Repeat(string(rune('a'+index)), 12))
		name := fmt.Sprintf("corr_token_%d", index)
		if index < 4 {
			entryBytes, ok := correlate.CorrelationExtractionEntryBytes(name, value)
			if !ok {
				t.Fatalf("test entry %d unexpectedly exceeds the per-entry runtime limit", index)
			}
			extractionLimit += entryBytes
		}
		requests = append(requests, RecordedRequest{
			Seq: index + 2,
			URL: "https://api.example.test/use?token=" + value,
		})
		correlations = append(correlations, Correlation{
			Name:        name,
			Source:      "body",
			ExtractType: "regex",
			Expression:  `(token)`,
			Value:       value,
			FoundInSeq:  1,
			UsedInSeqs:  []int{index + 2},
			Confidence:  0.9,
		})
	}

	application := prepareRecorderCorrelationApplicationWithExtractionLimit(requests, correlations, extractionLimit)
	if len(application.vars) != 4 || len(application.sourceSequences) != 4 || len(application.extractors[1]) != 4 {
		t.Fatalf("aggregate-limited application = vars %d sources %d extractors %d, want four", len(application.vars), len(application.sourceSequences), len(application.extractors[1]))
	}
	for index := range correlations {
		if index < 4 {
			if !correlations[index].PreApplied || correlations[index].ReviewAction != "" {
				t.Fatalf("correlation %d = %#v, want pre-applied", index, correlations[index])
			}
			continue
		}
		if correlations[index].PreApplied || correlations[index].ReviewAction != recorderManualCorrelationReplacement {
			t.Fatalf("correlation %d = %#v, want aggregate-budget manual review", index, correlations[index])
		}
	}
}

func TestPrepareRecorderCorrelationApplicationRejectsSubstringEncodedRawLiteralCollision(t *testing.T) {
	const rawValue = "opaque token/123456"
	encodedValue := url.QueryEscape(rawValue)
	containingRawValue := "prefix-" + encodedValue + "-suffix"
	requests := []RecordedRequest{
		{Seq: 1, URL: "https://api.example.test/source-a"},
		{Seq: 2, URL: "https://api.example.test/source-b"},
		{Seq: 3, URL: "https://api.example.test/use?token=" + containingRawValue},
	}
	correlations := []Correlation{
		{
			Name:        "corr_a",
			Source:      "body",
			ExtractType: "regex",
			Expression:  `(opaque token/123456)`,
			Value:       rawValue,
			FoundInSeq:  1,
			UsedInSeqs:  []int{3},
			Confidence:  0.9,
		},
		{
			Name:        "corr_b",
			Source:      "body",
			ExtractType: "regex",
			Expression:  `(prefix-opaque\+token%2F123456-suffix)`,
			Value:       containingRawValue,
			FoundInSeq:  2,
			UsedInSeqs:  []int{3},
			Confidence:  0.9,
		},
	}

	application := prepareRecorderCorrelationApplication(requests, correlations)
	if len(application.vars) != 0 || len(application.sourceSequences) != 0 || len(application.extractors) != 0 {
		t.Fatalf("substring collision application = %#v, want no automatic rewrite", application)
	}
	for _, correlation := range correlations {
		if correlation.PreApplied || correlation.ReviewAction != recorderManualCorrelationReplacement {
			t.Fatalf("substring collision correlation = %#v, want manual and not pre-applied", correlation)
		}
	}
}

func TestPrepareRecorderCorrelationApplicationRejectsMergedSuffixPrefixCollision(t *testing.T) {
	const (
		leftValue  = "TOKENabc123"
		rightValue = "123SESSIONxyz"
	)
	requests := []RecordedRequest{
		{Seq: 1, URL: "https://api.example.test/source-a"},
		{Seq: 2, URL: "https://api.example.test/source-b"},
		{Seq: 3, URL: "https://api.example.test/use?blob=TOKENabc123SESSIONxyz"},
	}
	correlations := []Correlation{
		{
			Name: "corr_left", Source: "body", ExtractType: "regex", Expression: `(TOKENabc123)`,
			Value: leftValue, FoundInSeq: 1, UsedInSeqs: []int{3}, Confidence: 0.9,
		},
		{
			Name: "corr_right", Source: "body", ExtractType: "regex", Expression: `(123SESSIONxyz)`,
			Value: rightValue, FoundInSeq: 2, UsedInSeqs: []int{3}, Confidence: 0.9,
		},
	}

	application := prepareRecorderCorrelationApplication(requests, correlations)
	if len(application.vars) != 0 || len(application.sourceSequences) != 0 || len(application.extractors) != 0 {
		t.Fatalf("partial-overlap collision application = %#v, want no automatic rewrite", application)
	}
	for _, correlation := range correlations {
		if correlation.PreApplied || correlation.ReviewAction != recorderManualCorrelationReplacement {
			t.Fatalf("partial-overlap correlation = %#v, want manual and not pre-applied", correlation)
		}
	}
}

func TestPrepareRecorderCorrelationApplicationAllowsDisjointSuffixPrefixLiterals(t *testing.T) {
	const (
		leftValue  = "TOKENabc123"
		rightValue = "123SESSIONxyz"
	)
	requests := []RecordedRequest{
		{Seq: 1, URL: "https://api.example.test/source-a"},
		{Seq: 2, URL: "https://api.example.test/source-b"},
		{Seq: 3, URL: "https://api.example.test/use?a=" + leftValue + "&b=" + rightValue},
	}
	correlations := []Correlation{
		{
			Name: "corr_left", Source: "body", ExtractType: "regex", Expression: `(TOKENabc123)`,
			Value: leftValue, FoundInSeq: 1, UsedInSeqs: []int{3}, Confidence: 0.9,
		},
		{
			Name: "corr_right", Source: "body", ExtractType: "regex", Expression: `(123SESSIONxyz)`,
			Value: rightValue, FoundInSeq: 2, UsedInSeqs: []int{3}, Confidence: 0.9,
		},
	}

	application := prepareRecorderCorrelationApplication(requests, correlations)
	if len(application.vars) != 2 || len(application.extractors) != 2 {
		t.Fatalf("disjoint suffix-prefix application = %#v, want both automatic rewrites", application)
	}
	for _, correlation := range correlations {
		if !correlation.PreApplied || correlation.ReviewAction != "" {
			t.Fatalf("disjoint suffix-prefix correlation = %#v, want pre-applied", correlation)
		}
	}
}

func TestPrepareRecorderCorrelationApplicationFailsClosedWhenOverlapBudgetExhausts(t *testing.T) {
	const repeatedBytes = 64 << 10
	leftValue := "x" + strings.Repeat("a", repeatedBytes)
	rightValue := strings.Repeat("a", repeatedBytes) + "y"
	requests := []RecordedRequest{
		{Seq: 1, URL: "https://api.example.test/source-a"},
		{Seq: 2, URL: "https://api.example.test/source-b"},
		{Seq: 3, Body: leftValue + " " + rightValue},
	}
	correlations := []Correlation{
		{
			Name: "corr_left", Source: "body", ExtractType: "regex", Expression: `(left)`,
			Value: leftValue, FoundInSeq: 1, UsedInSeqs: []int{3}, Confidence: 0.9,
		},
		{
			Name: "corr_right", Source: "body", ExtractType: "regex", Expression: `(right)`,
			Value: rightValue, FoundInSeq: 2, UsedInSeqs: []int{3}, Confidence: 0.9,
		},
	}

	application := prepareRecorderCorrelationApplication(requests, correlations)
	if len(application.vars) != 0 || len(application.extractors) != 0 {
		t.Fatalf("budget-exhausted application = %#v, want no automatic rewrite", application)
	}
	for _, correlation := range correlations {
		if correlation.PreApplied || correlation.ReviewAction != recorderManualCorrelationReplacement {
			t.Fatalf("budget-exhausted correlation = %#v, want manual and not pre-applied", correlation)
		}
	}
}

func TestRecorderCorrelationReplacementCollisionsFailClosedWhenSurfaceLimitExhausts(t *testing.T) {
	const (
		leftValue  = "TOKENabc123"
		rightValue = "123SESSIONxyz"
	)
	requests := []RecordedRequest{{
		Seq:     1,
		URL:     "https://api.example.test/use?left=" + leftValue,
		Body:    "right=" + rightValue,
		Headers: map[string]string{"X-Third-Surface": "present"},
	}}
	plans := []recorderCorrelationPlan{
		{automatic: true, value: leftValue, usage: recorderCorrelationUse{raw: true}},
		{automatic: true, value: rightValue, usage: recorderCorrelationUse{raw: true}},
	}

	ambiguousLiterals, ambiguousPlans := recorderCorrelationReplacementCollisionsWithSurfaceLimit(requests, plans, 2)
	for index, value := range []string{leftValue, rightValue} {
		if !ambiguousLiterals[value] || !ambiguousPlans[index] {
			t.Fatalf("surface-limited collision result = literals %#v plans %#v; want every automatic claim ambiguous", ambiguousLiterals, ambiguousPlans)
		}
	}
}

func TestRecorderRequestReplacementSurfacesUseOnlyEditableFields(t *testing.T) {
	requests := []RecordedRequest{
		{
			Seq:          1,
			Path:         "/fallback",
			QueryString:  "token=path-value",
			Body:         "body-value",
			Headers:      map[string]string{"X-Editable": "header-value", "User-Agent": "filtered-value"},
			HeadersMulti: map[string][]string{"X-Repeated": {"first-value", "second-value"}},
		},
		{Seq: 2, URL: "https://example.test/excluded", Excluded: true},
		{Seq: 3, URL: "https://example.test/encoded", Body: "encoded-body", BodyEncoding: "base64"},
	}

	surfaces, complete := recorderRequestReplacementSurfaces(requests, 10)
	if !complete {
		t.Fatal("replacement surfaces unexpectedly exceeded the limit")
	}
	joined := strings.Join(surfaces, "\n")
	for _, want := range []string{"/fallback?token=path-value", "body-value", "header-value", "first-value", "second-value", "https://example.test/encoded"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("replacement surfaces = %#v, missing %q", surfaces, want)
		}
	}
	for _, unwanted := range []string{"filtered-value", "https://example.test/excluded", "encoded-body"} {
		if strings.Contains(joined, unwanted) {
			t.Fatalf("replacement surfaces = %#v, unexpectedly include %q", surfaces, unwanted)
		}
	}
}

func TestIndexRecordedRequestsBySequencePreservesFirstMatch(t *testing.T) {
	requests := []RecordedRequest{
		{Seq: 7, URL: "https://example.test/first"},
		{Seq: 9, URL: "https://example.test/other"},
		{Seq: 7, URL: "https://example.test/duplicate"},
	}
	indexed := indexRecordedRequestsBySequence(requests)
	if len(indexed) != 2 {
		t.Fatalf("indexed requests = %#v, want two sequence entries", indexed)
	}
	if indexed[7] != &requests[0] || indexed[9] != &requests[1] {
		t.Fatalf("indexed requests = %#v, want direct first-occurrence pointers", indexed)
	}
}

func TestPrepareRecorderCorrelationApplicationIncludesReviewOnlyOwnersInCollisionCheck(t *testing.T) {
	const shortValue = "token-123456"
	const longValue = "token-123456-extra"
	requests := []RecordedRequest{
		{Seq: 1, URL: "https://api.example.test/source-a"},
		{Seq: 2, URL: "https://api.example.test/source-b"},
		{Seq: 3, URL: "https://api.example.test/use?token=" + longValue},
	}
	correlations := []Correlation{
		{
			Name: "corr_short", Source: "body", ExtractType: "regex", Expression: `(token-123456)`,
			Value: shortValue, FoundInSeq: 1, UsedInSeqs: []int{3}, Confidence: 0.9,
		},
		{
			Name: "corr_long", Source: "body", ExtractType: "regex", Expression: `(token-123456-extra)`,
			Value: longValue, FoundInSeq: 2, UsedInSeqs: []int{3}, Confidence: 0.6,
		},
	}

	application := prepareRecorderCorrelationApplication(requests, correlations)
	if len(application.vars) != 0 || len(application.extractors) != 0 {
		t.Fatalf("mixed-confidence collision application = %#v, want no automatic rewrite", application)
	}
	for _, correlation := range correlations {
		if correlation.PreApplied || correlation.ReviewAction != recorderManualCorrelationReplacement {
			t.Fatalf("mixed-confidence owner = %#v, want manual value replacement", correlation)
		}
	}
}

func TestPrepareRecorderCorrelationApplicationCapsSourceExtractorsIncludingEncodedSibling(t *testing.T) {
	const mixedValue = "mixed token/000000"
	correlations := make([]Correlation, 0, dsl.MaxActionExtractors)
	correlations = append(correlations, Correlation{
		Name:        "corr_mixed",
		Source:      "body",
		ExtractType: "regex",
		Expression:  `(mixed token/000000)`,
		Value:       mixedValue,
		FoundInSeq:  1,
		UsedInSeqs:  []int{2},
		Confidence:  0.9,
	})
	rawValues := []string{mixedValue}
	for index := 1; index < dsl.MaxActionExtractors; index++ {
		value := fmt.Sprintf("value-%06d-abcdef", index)
		correlations = append(correlations, Correlation{
			Name:        fmt.Sprintf("corr_value_%06d", index),
			Source:      "body",
			ExtractType: "regex",
			Expression:  `(` + value + `)`,
			Value:       value,
			FoundInSeq:  1,
			UsedInSeqs:  []int{2},
			Confidence:  0.9,
		})
		rawValues = append(rawValues, value)
	}
	requests := []RecordedRequest{
		{Seq: 1, URL: "https://api.example.test/source"},
		{
			Seq:  2,
			URL:  "https://api.example.test/use?mixed=" + url.QueryEscape(mixedValue),
			Body: strings.Join(rawValues, "|"),
		},
	}

	application := prepareRecorderCorrelationApplication(requests, correlations)
	if got := len(application.extractors[1]); got != dsl.MaxActionExtractors {
		t.Fatalf("source extractors = %d, want capped at %d", got, dsl.MaxActionExtractors)
	}
	if application.extractors[1][1].Name != "corr_mixed_encoded" {
		t.Fatalf("encoded sibling extractor = %#v, want reserved corr_mixed_encoded", application.extractors[1][1])
	}
	for _, extractor := range application.extractors[1] {
		if extractor.OnMiss != "fail" {
			t.Fatalf("automatic extractor %q on_miss = %q, want fail", extractor.Name, extractor.OnMiss)
		}
	}
	if len(application.vars) != dsl.MaxActionExtractors || len(application.sourceSequences) != dsl.MaxActionExtractors {
		t.Fatalf("replacement maps = %d/%d, want %d safe replacements", len(application.vars), len(application.sourceSequences), dsl.MaxActionExtractors)
	}
	overflow := correlations[len(correlations)-1]
	if overflow.PreApplied || overflow.ReviewAction != recorderManualCorrelationReplacement {
		t.Fatalf("overflow correlation = %#v, want manual and not pre-applied", overflow)
	}
	if _, exists := application.vars[overflow.Value]; exists {
		t.Fatalf("overflow value %q was exposed as an automatic replacement", overflow.Value)
	}
}

func TestConvertSessionWarnsWhenCorrelationAnalysisExceedsWorkLimit(t *testing.T) {
	requests := make([]RecordedRequest, 4097)
	for index := range requests {
		requests[index] = RecordedRequest{
			Seq:    index + 1,
			Method: "GET",
			URL:    "https://api.example.test/item",
			Path:   "/item",
		}
	}
	result := ConvertSession(&Session{Name: "limit", BaseURL: "https://api.example.test", Requests: requests}, "")
	if !strings.Contains(strings.Join(result.Warnings, "\n"), "Correlation analysis was not completed") {
		t.Fatalf("warnings = %#v, want explicit correlation work-limit warning", result.Warnings)
	}
	if len(result.Correlations) != 0 {
		t.Fatalf("correlations = %#v, want no partial analysis results", result.Correlations)
	}
}

func TestConvertSession_CrossOriginTargetPreservesAbsoluteURLAndQuery(t *testing.T) {
	s := &Session{
		ID:      "cross-origin",
		Name:    "Cross Origin",
		BaseURL: "https://app.example.test",
		Requests: []RecordedRequest{{
			Seq:         1,
			Method:      "GET",
			URL:         "https://api.example.test/search?q=apex&cursor=abc123",
			Path:        "/search",
			QueryString: "q=apex&cursor=abc123",
			Host:        "api.example.test",
			StatusCode:  200,
			Headers:     map[string]string{},
		}},
	}

	result := ConvertSession(s, "")
	if len(result.Script.Actions) != 1 {
		t.Fatalf("actions = %d, want 1", len(result.Script.Actions))
	}
	if got := result.Script.Actions[0].Target; got != "https://api.example.test/search?q=apex&cursor=abc123" {
		t.Fatalf("target = %q, want absolute cross-origin URL with query", got)
	}
}

func TestConvertSession_CustomName(t *testing.T) {
	s := &Session{
		ID:      "test",
		Name:    "Original Name",
		BaseURL: "http://example.com",
		Requests: []RecordedRequest{
			{Seq: 1, Method: "GET", URL: "http://example.com/", Path: "/", StatusCode: 200},
		},
	}

	result := ConvertSession(s, "Custom Script Name")
	if result.Script.Name != "Custom Script Name" {
		t.Fatalf("expected Custom Script Name, got %s", result.Script.Name)
	}
}

func TestConvertSession_CarriesBinaryBodyEncoding(t *testing.T) {
	s := &Session{
		ID:      "test",
		Name:    "Binary Upload",
		BaseURL: "http://example.com",
		Requests: []RecordedRequest{
			{
				Seq:          1,
				Method:       "POST",
				URL:          "http://example.com/api/upload",
				Path:         "/api/upload",
				Host:         "example.com",
				Body:         "AP8QgA==",
				BodyEncoding: "base64",
				ContentType:  "application/octet-stream",
				StatusCode:   201,
			},
		},
	}

	result := ConvertSession(s, "")
	if len(result.Script.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(result.Script.Actions))
	}
	action := result.Script.Actions[0]
	if action.Body != "AP8QgA==" {
		t.Fatalf("body = %q, want base64 payload", action.Body)
	}
	if action.BodyEncoding != "base64" {
		t.Fatalf("body encoding = %q, want base64", action.BodyEncoding)
	}
}

func TestConvertSession_WebSocketMessages(t *testing.T) {
	s := &Session{
		ID:      "test",
		Name:    "Socket Flow",
		BaseURL: "ws://stream.example.com",
		Requests: []RecordedRequest{
			{
				Seq:        1,
				Method:     "GET",
				URL:        "ws://stream.example.com/orders?tenant=acme",
				Path:       "/orders",
				Host:       "stream.example.com",
				StatusCode: http.StatusSwitchingProtocols,
				Headers:    map[string]string{"Sec-WebSocket-Protocol": "json"},
				WebSocketMessages: []RecordedWebSocketMessage{
					{Direction: webSocketDirectionClientToServer, Opcode: webSocketOpcodeText, Payload: `{"type":"subscribe"}`},
					{Direction: webSocketDirectionServerToClient, Opcode: webSocketOpcodeText, Payload: `{"type":"subscribed"}`},
					{Direction: webSocketDirectionClientToServer, Opcode: webSocketOpcodeBinary, Payload: "AP8=", PayloadEncoding: "base64"},
				},
			},
		},
	}

	result := ConvertSession(s, "")
	if result.Script.Protocol != "websocket" {
		t.Fatalf("protocol = %q, want websocket", result.Script.Protocol)
	}
	if result.Script.Config.BaseURL != "ws://stream.example.com" {
		t.Fatalf("base_url = %q, want ws origin", result.Script.Config.BaseURL)
	}
	if len(result.Script.Actions) != 5 {
		t.Fatalf("action count = %d, want connect + 3 messages + close", len(result.Script.Actions))
	}
	connect := result.Script.Actions[0]
	if connect.Type != "ws_connect" || connect.Target != "/orders?tenant=acme" {
		t.Fatalf("connect action = %#v", connect)
	}
	send := result.Script.Actions[1]
	if send.Type != "ws_send" || send.Body != `{"type":"subscribe"}` || send.Params["type"] != "text" {
		t.Fatalf("send action = %#v", send)
	}
	receive := result.Script.Actions[2]
	if receive.Type != "ws_receive" || receive.Params["match"] != `{"type":"subscribed"}` {
		t.Fatalf("receive action = %#v", receive)
	}
	binarySend := result.Script.Actions[3]
	if binarySend.Type != "ws_send" || binarySend.Body != "AP8=" || binarySend.BodyEncoding != "base64" || binarySend.Params["type"] != "binary" {
		t.Fatalf("binary send action = %#v", binarySend)
	}
	if result.Script.Actions[4].Type != "ws_close" {
		t.Fatalf("final action type = %q, want ws_close", result.Script.Actions[4].Type)
	}
}

func TestConvertSession_MixedHTTPAndWebSocketKeepsHTTPActions(t *testing.T) {
	s := &Session{
		ID:      "mixed",
		Name:    "Mixed Flow",
		BaseURL: "https://app.example.test",
		Requests: []RecordedRequest{
			{
				Seq:        1,
				Method:     "GET",
				URL:        "https://app.example.test/api/bootstrap?tenant=acme",
				Path:       "/api/bootstrap",
				Host:       "app.example.test",
				StatusCode: 200,
				Headers:    map[string]string{},
			},
			{
				Seq:        2,
				Method:     "GET",
				URL:        "wss://app.example.test/events?tenant=acme",
				Path:       "/events",
				Host:       "app.example.test",
				StatusCode: http.StatusSwitchingProtocols,
				Headers:    map[string]string{"Sec-WebSocket-Protocol": "json"},
				WebSocketMessages: []RecordedWebSocketMessage{
					{Direction: webSocketDirectionClientToServer, Opcode: webSocketOpcodeText, Payload: `{"type":"subscribe"}`},
				},
			},
			{
				Seq:        3,
				Method:     "POST",
				URL:        "https://app.example.test/api/checkout",
				Path:       "/api/checkout",
				Host:       "app.example.test",
				StatusCode: 200,
				Headers:    map[string]string{},
			},
		},
	}

	result := ConvertSession(s, "")
	if result.Script.Protocol != "http" {
		t.Fatalf("protocol = %q, want http for mixed capture with replayable HTTP actions", result.Script.Protocol)
	}
	if len(result.Script.Actions) != 2 {
		t.Fatalf("actions = %d, want 2 HTTP actions", len(result.Script.Actions))
	}
	if got := result.Script.Actions[0].Target; got != "/api/bootstrap?tenant=acme" {
		t.Fatalf("first target = %q", got)
	}
	if got := result.Script.Actions[1].Target; got != "/api/checkout" {
		t.Fatalf("second target = %q", got)
	}
	if len(result.Warnings) == 0 || !strings.Contains(strings.Join(result.Warnings, "\n"), "WebSocket") {
		t.Fatalf("warnings = %#v, want mixed WebSocket warning", result.Warnings)
	}
	preserved := result.Script.Metadata.Import.Preserved
	if preserved["recorded_websocket_urls_available"] != true {
		t.Fatalf("preserved metadata = %#v, want recorded websocket URLs available", preserved)
	}
}

func TestConvertSession_WebSocketContinuationFramesAreCoalesced(t *testing.T) {
	s := &Session{
		ID:      "test",
		Name:    "Fragmented Socket Flow",
		BaseURL: "ws://stream.example.com",
		Requests: []RecordedRequest{
			{
				Seq:        1,
				Method:     "GET",
				URL:        "ws://stream.example.com/orders",
				Path:       "/orders",
				Host:       "stream.example.com",
				StatusCode: http.StatusSwitchingProtocols,
				WebSocketMessages: []RecordedWebSocketMessage{
					{Direction: webSocketDirectionClientToServer, Opcode: webSocketOpcodeText, Payload: "hel"},
					{Direction: webSocketDirectionClientToServer, Opcode: webSocketOpcodeContinuation, Payload: "lo"},
				},
			},
		},
	}

	result := ConvertSession(s, "")
	if len(result.Script.Actions) != 3 {
		t.Fatalf("action count = %d, want connect + coalesced send + close", len(result.Script.Actions))
	}
	send := result.Script.Actions[1]
	if send.Type != "ws_send" || send.Body != "hello" {
		t.Fatalf("send action = %#v, want coalesced hello payload", send)
	}
}

func TestConvertSession_CorrelatesCapturedCookies(t *testing.T) {
	s := &Session{
		ID:      "test",
		Name:    "Cookie Flow",
		BaseURL: "http://example.com",
		Filters: SessionFilters{CaptureCookies: true},
		Requests: []RecordedRequest{
			{
				Seq:        1,
				Method:     "GET",
				URL:        "http://example.com/login",
				Path:       "/login",
				Host:       "example.com",
				StatusCode: 200,
				ResponseHeaders: map[string]string{
					"Set-Cookie": "session=sid-abcdef123456; Path=/; HttpOnly",
				},
			},
			{
				Seq:        2,
				Method:     "GET",
				URL:        "http://example.com/account",
				Path:       "/account",
				Host:       "example.com",
				StatusCode: 200,
				Headers: map[string]string{
					"Cookie": "session=sid-abcdef123456; theme=dark",
				},
			},
		},
	}

	result := ConvertSession(s, "")
	if len(result.Correlations) != 1 {
		t.Fatalf("correlations = %#v, want one cookie correlation", result.Correlations)
	}
	corr := result.Correlations[0]
	if corr.Name != "corr_cookie_session" || corr.Source != "header" || corr.ExtractType != "regex" {
		t.Fatalf("unexpected correlation: %#v", corr)
	}
	// The rules-engine ranking metadata must flow through to the import-review UI.
	if corr.RuleID == "" {
		t.Fatalf("expected rule id on correlation, got %#v", corr)
	}
	if corr.Confidence <= 0 {
		t.Fatalf("expected positive confidence, got %v", corr.Confidence)
	}
	if len(corr.Reasons) == 0 {
		t.Fatalf("expected confidence reasons, got %#v", corr.Reasons)
	}
	if len(result.Script.Actions[0].Extractors) != 1 {
		t.Fatalf("first action extractors = %#v", result.Script.Actions[0].Extractors)
	}
	if got := result.Script.Actions[1].Headers["Cookie"]; got != "session={{var.corr_cookie_session}}; theme=dark" {
		t.Fatalf("correlated cookie header = %q", got)
	}
}

func TestConvertSession_UsesRawHeadersForCorrelationWithoutLeakingUnmatchedSecrets(t *testing.T) {
	s := &Session{
		ID:      "test",
		Name:    "Header Token Flow",
		BaseURL: "http://example.com",
		Requests: []RecordedRequest{
			{
				Seq:          1,
				Method:       "POST",
				URL:          "http://example.com/login",
				Path:         "/login",
				Host:         "example.com",
				StatusCode:   200,
				ResponseBody: `{"auth":{"access_token":"tok-abcdef123456"}}`,
			},
			{
				Seq:        2,
				Method:     "GET",
				URL:        "http://example.com/account",
				Path:       "/account",
				Host:       "example.com",
				StatusCode: 200,
				Headers: map[string]string{
					"Authorization": "[redacted]",
					"X-Api-Key":     "[redacted]",
				},
				RawHeaders: map[string]string{
					"Authorization": "Bearer tok-abcdef123456",
					"X-Api-Key":     "raw-api-secret",
				},
			},
		},
	}

	result := ConvertSession(s, "")
	if len(result.Correlations) != 1 || result.Correlations[0].Name != "corr_access_token" {
		t.Fatalf("correlations = %#v, want access token from raw request header", result.Correlations)
	}
	headers := result.Script.Actions[1].Headers
	if got := headers["Authorization"]; got != "Bearer {{var.corr_access_token}}" {
		t.Fatalf("authorization header = %q, want correlated variable", got)
	}
	if got := headers["X-Api-Key"]; got != "[redacted]" {
		t.Fatalf("unmatched API key header = %q, want redacted", got)
	}
}

func TestConvertSession_UsesRepeatedRawResponseHeadersWithoutDisclosingSamples(t *testing.T) {
	const token = "rotating-token-second-abcdef123456"
	s := &Session{
		ID:      "test",
		Name:    "Raw Response Header Flow",
		BaseURL: "http://example.com",
		Requests: []RecordedRequest{
			{
				Seq:                     1,
				Method:                  "POST",
				URL:                     "http://example.com/login",
				Path:                    "/login",
				Host:                    "example.com",
				StatusCode:              200,
				ResponseHeaders:         map[string]string{"X-Session-Token": "[redacted]"},
				RawResponseHeaders:      map[string]string{"X-Session-Token": token},
				RawResponseHeadersMulti: map[string][]string{"X-Session-Token": {"fixed-token-first", token}},
			},
			{
				Seq:        2,
				Method:     "GET",
				URL:        "http://example.com/account",
				Path:       "/account",
				Host:       "example.com",
				StatusCode: 200,
				Headers:    map[string]string{"X-Session-Token": "[redacted]"},
				RawHeaders: map[string]string{"X-Session-Token": token},
			},
		},
	}

	result := ConvertSession(s, "")
	if len(result.Correlations) != 1 {
		t.Fatalf("correlations = %#v, want reused raw response header", result.Correlations)
	}
	corr := result.Correlations[0]
	if corr.Name != "corr_x_session_token" || corr.Value != "" || corr.SampleValue != "" {
		t.Fatalf("public correlation must retain structure but omit samples: %#v", corr)
	}
	if len(result.Script.Actions[0].Extractors) != 1 || !strings.Contains(result.Script.Actions[0].Extractors[0].Expression, "{1}") {
		t.Fatalf("source extractor = %#v, want second repeated header", result.Script.Actions[0].Extractors)
	}
	if result.Script.Actions[0].Extractors[0].OnMiss != "fail" {
		t.Fatalf("source extractor on_miss = %q, want fail so a later iteration cannot reuse a stale value", result.Script.Actions[0].Extractors[0].OnMiss)
	}
	if got := result.Script.Actions[1].Headers["X-Session-Token"]; got != "{{var.corr_x_session_token}}" {
		t.Fatalf("reused header = %q, want correlation variable", got)
	}
}

func TestConvertSession_DoesNotRewriteLiteralBeforeCorrelationSource(t *testing.T) {
	const token = "source-ordered-recorder-token-123456"
	s := &Session{
		ID:      "test",
		Name:    "Source Order Flow",
		BaseURL: "http://example.com",
		Requests: []RecordedRequest{
			{Seq: 1, Method: "GET", URL: "http://example.com/before?echo=" + token, Path: "/before", StatusCode: 200},
			{Seq: 2, Method: "GET", URL: "http://example.com/bootstrap", Path: "/bootstrap", StatusCode: 200, ResponseBody: `{"access_token":"` + token + `"}`},
			{Seq: 3, Method: "GET", URL: "http://example.com/after?access_token=" + token, Path: "/after", StatusCode: 200},
		},
	}

	result := ConvertSession(s, "")
	if len(result.Correlations) != 1 {
		t.Fatalf("correlations = %#v, want one", result.Correlations)
	}
	if got := result.Script.Actions[0].Target; got != "/before?echo="+token {
		t.Fatalf("pre-source target = %q, want literal preserved", got)
	}
	if len(result.Script.Actions[1].Extractors) != 1 {
		t.Fatalf("source extractors = %#v, want one", result.Script.Actions[1].Extractors)
	}
	if got := result.Script.Actions[2].Target; got != "/after?access_token={{var.corr_access_token}}" {
		t.Fatalf("post-source target = %q, want correlation variable", got)
	}
}

func TestConvertSession_PreservesAndCorrelatesRepeatedRequestHeaders(t *testing.T) {
	const token = "repeated-request-id-token-abcdef123456"
	s := &Session{
		ID:      "test",
		Name:    "Repeated Request Header Flow",
		BaseURL: "http://example.com",
		Requests: []RecordedRequest{
			{
				Seq:                     1,
				Method:                  "GET",
				URL:                     "http://example.com/bootstrap",
				Path:                    "/bootstrap",
				StatusCode:              200,
				ResponseHeaders:         map[string]string{"X-Request-ID": "fixed-source"},
				RawResponseHeaders:      map[string]string{"X-Request-ID": "fixed-source"},
				ResponseHeadersMulti:    map[string][]string{"X-Request-ID": {"fixed-source", token}},
				RawResponseHeadersMulti: map[string][]string{"X-Request-ID": {"fixed-source", token}},
			},
			{
				Seq:             2,
				Method:          "GET",
				URL:             "http://example.com/account",
				Path:            "/account",
				StatusCode:      200,
				Headers:         map[string]string{"X-Request-ID": "fixed-client"},
				RawHeaders:      map[string]string{"X-Request-ID": "fixed-client"},
				HeadersMulti:    map[string][]string{"X-Request-ID": {"fixed-client", token}},
				RawHeadersMulti: map[string][]string{"X-Request-ID": {"fixed-client", token}},
			},
		},
	}

	result := ConvertSession(s, "")
	if len(result.Correlations) != 1 {
		t.Fatalf("correlations = %#v, want repeated response-header correlation", result.Correlations)
	}
	if got := result.Script.Actions[1].Headers["X-Request-ID"]; got != "{{var.corr_x_request_id}}" {
		t.Fatalf("summary header = %q, want the shared finalizer's last-value compatibility summary", got)
	}
	list := result.Script.Actions[1].HeaderList
	if len(list) != 2 || list[0].Name != "X-Request-ID" || list[0].Value != "fixed-client" || list[1].Value != "{{var.corr_x_request_id}}" {
		t.Fatalf("header_list = %#v, want both occurrences with second correlated", list)
	}
}

func TestCorrelatedRecordedHeadersDoesNotEmitPartialRawOnlyHeaderList(t *testing.T) {
	const token = "raw-only-repeated-token-abcdef123456"
	projection := correlatedRecordedHeaders(RecordedRequest{
		Headers:         map[string]string{"X-Request-ID": "safe-summary"},
		RawHeadersMulti: map[string][]string{"X-Request-ID": {"unmatched-raw-first", token}},
	}, false, map[string]string{token: "{{var.corr_x_request_id}}"})

	if len(projection.Repeated) != 0 {
		t.Fatalf("partial raw-only HeaderList = %#v, want omitted so it cannot shadow the safe summary", projection.Repeated)
	}
	if projection.Summary["X-Request-ID"] != "safe-summary" {
		t.Fatalf("summary header = %#v, want disclosure-safe fallback preserved", projection.Summary)
	}
}

func TestConvertSession_DropsRecorderControlHeadersFromLegacyInputs(t *testing.T) {
	const controlSecret = "recorder-control-secret-abcdef123456"
	s := &Session{
		ID:      "test",
		Name:    "Recorder Control Header Flow",
		BaseURL: "http://example.com",
		Requests: []RecordedRequest{
			{
				Seq:             1,
				Method:          "POST",
				URL:             "http://example.com/login",
				Path:            "/login",
				Host:            "example.com",
				StatusCode:      http.StatusOK,
				ResponseHeaders: map[string]string{"X-APEXLoad-Recorder-Seed": controlSecret},
			},
			{
				Seq:        2,
				Method:     "GET",
				URL:        "http://example.com/account",
				Path:       "/account",
				Host:       "example.com",
				StatusCode: http.StatusOK,
				Headers: map[string]string{
					"X-Trace":                   "safe-trace",
					"X-APEXLoad-Recorder-Token": "[redacted]",
				},
				RawHeaders: map[string]string{
					"X-Trace":                   "safe-trace",
					"x-apexload-recorder-token": controlSecret,
				},
			},
		},
	}

	correlationInputs := convertibleCorrelationRequests(s.Requests)
	for _, req := range correlationInputs {
		for name := range req.Headers {
			if isRecorderControlHeader(name) {
				t.Fatalf("correlation input retained recorder control request header %q", name)
			}
		}
		for name := range req.ResponseHeaders {
			if isRecorderControlHeader(name) {
				t.Fatalf("correlation input retained recorder control response header %q", name)
			}
		}
	}

	result := ConvertSession(s, "")
	if len(result.Script.Actions) != 2 {
		t.Fatalf("action count = %d, want 2", len(result.Script.Actions))
	}
	if got := result.Script.Actions[1].Headers["X-Trace"]; got != "safe-trace" {
		t.Fatalf("safe header = %q, want preserved", got)
	}
	for _, action := range result.Script.Actions {
		for name, value := range action.Headers {
			if isRecorderControlHeader(name) || strings.Contains(value, controlSecret) {
				t.Fatalf("generated action leaked recorder control header: %#v", action.Headers)
			}
		}
	}
	if len(result.Correlations) != 0 {
		t.Fatalf("recorder control headers produced correlations: %#v", result.Correlations)
	}
}

// TestConvertSession_PartialHeaderCorrelationKeepsRedacted locks in that when a
// correlation matches only a substring of a redacted secret header, leaving
// live credential material around the {{var}}, the header stays redacted rather
// than splicing the un-correlated remainder of the secret into the script.
func TestConvertSession_PartialHeaderCorrelationKeepsRedacted(t *testing.T) {
	s := &Session{
		ID:      "test",
		Name:    "Partial Header Correlation",
		BaseURL: "http://example.com",
		Requests: []RecordedRequest{
			{
				Seq:          1,
				Method:       "POST",
				URL:          "http://example.com/login",
				Path:         "/login",
				Host:         "example.com",
				StatusCode:   200,
				ResponseBody: `{"auth":{"access_token":"tok-abcdef123456"}}`,
			},
			{
				Seq:        2,
				Method:     "GET",
				URL:        "http://example.com/account",
				Path:       "/account",
				Host:       "example.com",
				StatusCode: 200,
				Headers:    map[string]string{"Authorization": "[redacted]"},
				// The correlated token is only a substring; the rest of the raw
				// header ("residualsecretmaterial") is un-correlated live secret.
				RawHeaders: map[string]string{"Authorization": "Bearer tok-abcdef123456 residualsecretmaterial"},
			},
		},
	}

	result := ConvertSession(s, "")
	if len(result.Correlations) != 1 {
		t.Fatalf("correlations = %#v, want the access token correlated", result.Correlations)
	}
	got := result.Script.Actions[1].Headers["Authorization"]
	if strings.Contains(got, "residualsecretmaterial") {
		t.Fatalf("authorization header leaked residual raw secret: %q", got)
	}
	if got != "[redacted]" {
		t.Fatalf("authorization header = %q, want it kept redacted (partial correlation must not splice raw secret)", got)
	}
}

func TestCorrelatedRecordedHeadersMatchesSanitizedKeysCaseInsensitively(t *testing.T) {
	const token = "tok-abcdef123456"
	safe := correlatedRecordedHeaders(RecordedRequest{
		Headers:    map[string]string{"Authorization": "[redacted]"},
		RawHeaders: map[string]string{"authorization": "Bearer " + token},
	}, false, map[string]string{token: "{{var.corr_access_token}}"})
	if got := safe.Summary["Authorization"]; got != "Bearer {{var.corr_access_token}}" {
		t.Fatalf("case-insensitive correlated authorization = %q", got)
	}
	if _, duplicate := safe.Summary["authorization"]; duplicate {
		t.Fatalf("case-only raw key created a duplicate header: %#v", safe.Summary)
	}

	partial := correlatedRecordedHeaders(RecordedRequest{
		Headers:    map[string]string{"Authorization": "[redacted]"},
		RawHeaders: map[string]string{"authorization": "Bearer " + token + " !!!"},
	}, false, map[string]string{token: "{{var.corr_access_token}}"})
	if got := partial.Summary["Authorization"]; got != "[redacted]" {
		t.Fatalf("punctuation-only residual secret was disclosed: %q", got)
	}
}

func TestConvertSession_StaticFiltered(t *testing.T) {
	s := &Session{
		ID:      "test",
		Name:    "Static Test",
		BaseURL: "http://example.com",
		Requests: []RecordedRequest{
			{Seq: 1, Method: "GET", URL: "http://example.com/api/data", Path: "/api/data", StatusCode: 200},
			{Seq: 2, Method: "GET", URL: "http://example.com/style.css", Path: "/style.css", StatusCode: 200, IsStatic: true},
			{Seq: 3, Method: "GET", URL: "http://example.com/logo.png", Path: "/logo.png", StatusCode: 200, IsStatic: true},
		},
	}

	result := ConvertSession(s, "")
	if len(result.Script.Actions) != 1 {
		t.Fatalf("expected 1 non-static action, got %d", len(result.Script.Actions))
	}
	if result.Script.Metadata == nil || result.Script.Metadata.Import == nil {
		t.Fatal("expected import metadata with recorded resources")
	}
	preserved := result.Script.Metadata.Import.Preserved
	if preserved["recorded_resource_urls_available"] != true {
		t.Fatalf("recorded_resource_urls_available = %#v, want true", preserved["recorded_resource_urls_available"])
	}
	urls, ok := preserved["recorded_resource_urls"].([]string)
	if !ok || len(urls) != 2 {
		t.Fatalf("recorded_resource_urls = %#v, want two static URLs", preserved["recorded_resource_urls"])
	}
}

func TestConvertSession_ExcludedRequestsAndStepNames(t *testing.T) {
	s := &Session{
		ID:      "test",
		Name:    "Curated Test",
		BaseURL: "http://example.com",
		Requests: []RecordedRequest{
			{Seq: 1, Method: "GET", URL: "http://example.com/api/keep", Path: "/api/keep", StatusCode: 200, StepName: "Load Keep API"},
			{Seq: 2, Method: "GET", URL: "http://example.com/api/drop", Path: "/api/drop", StatusCode: 200, Excluded: true},
		},
	}

	result := ConvertSession(s, "")
	if len(result.Script.Actions) != 1 {
		t.Fatalf("expected 1 included action, got %d", len(result.Script.Actions))
	}
	if !strings.Contains(result.Script.Actions[0].Name, "Load Keep API") {
		t.Fatalf("expected custom step name, got %q", result.Script.Actions[0].Name)
	}
}

func TestConvertSession_TransactionMetadata(t *testing.T) {
	s := &Session{
		ID:      "test",
		Name:    "Transaction Test",
		BaseURL: "http://example.com",
		Requests: []RecordedRequest{
			{Seq: 1, Method: "POST", URL: "http://example.com/api/login", Path: "/api/login", StatusCode: 200, TransactionName: "Login"},
			{Seq: 2, Method: "GET", URL: "http://example.com/api/home", Path: "/api/home", StatusCode: 200},
		},
	}

	result := ConvertSession(s, "")
	if len(result.Script.Actions) != 2 {
		t.Fatalf("expected 2 actions, got %d", len(result.Script.Actions))
	}
	if got := result.Script.Actions[0].Metadata["source_transaction"]; got != "Login" {
		t.Fatalf("expected source_transaction=Login on first action, got %v", got)
	}
	if result.Script.Actions[1].Metadata["source_transaction"] != nil {
		t.Fatalf("unexpected transaction on action without transaction, got %v", result.Script.Actions[1].Metadata)
	}
	// A legacy recording without observed versions inherits the script policy;
	// the missing observation is recorded but does not block execution.
	capture, _ := result.Script.Actions[1].Metadata["http_version_capture"].(map[string]any)
	if result.Script.Actions[1].Metadata["execution_blocker"] != nil || capture["reason_code"] != "http_version_unavailable" {
		t.Fatalf("legacy recording without observed versions: blocker=%v capture=%v", result.Script.Actions[1].Metadata["execution_blocker"], capture)
	}
}

func TestConvertSessionWithOptions_Parameterize(t *testing.T) {
	s := &Session{
		ID:      "test",
		Name:    "Param Test",
		BaseURL: "http://example.com",
		Requests: []RecordedRequest{
			{
				Seq:         1,
				Method:      "POST",
				URL:         "http://example.com/login",
				Path:        "/login",
				Host:        "example.com",
				Body:        "username=demo&password=s3cret%2Bvalue&remember=1",
				ContentType: "application/x-www-form-urlencoded",
				StatusCode:  200,
			},
			{
				Seq:        2,
				Method:     "GET",
				URL:        "http://example.com/search?query=widgets&page=2",
				Path:       "/search",
				Host:       "example.com",
				StatusCode: 200,
			},
		},
	}

	// Without parameterization the values stay literal.
	plain := ConvertSession(s, "")
	if len(plain.Script.DataSources) != 0 {
		t.Fatalf("expected no data sources without parameterization, got %d", len(plain.Script.DataSources))
	}

	result := ConvertSessionWithOptions(s, "", ConvertOptions{Parameterize: true})
	if len(result.Script.DataSources) != 1 {
		t.Fatalf("expected 1 generated data source, got %d", len(result.Script.DataSources))
	}
	ds := result.Script.DataSources[0]
	if ds.Name != "recorded_inputs" || ds.Type != "csv" || !ds.FirstRowHeader {
		t.Fatalf("unexpected data source: %+v", ds)
	}
	for _, col := range []string{"username", "password", "query"} {
		if !strings.Contains(ds.InlineData, col) {
			t.Fatalf("expected column %q in inline data %q", col, ds.InlineData)
		}
	}
	if !strings.Contains(ds.InlineData, "s3cret%2Bvalue") {
		t.Fatalf("generated data source discarded or re-encoded captured password: %q", ds.InlineData)
	}
	if strings.Contains(ds.InlineData, "__REPLACE_WITH_SECRET__") {
		t.Fatalf("generated data source still requires password re-entry: %q", ds.InlineData)
	}
	preserved := result.Script.Metadata.Import.Preserved
	secretColumns, ok := preserved["recorded_input_secret_columns"].([]string)
	if !ok || len(secretColumns) != 1 || secretColumns[0] != "password" {
		t.Fatalf("recorded_input_secret_columns = %#v, want [password]", preserved["recorded_input_secret_columns"])
	}

	bodies := result.Script.Actions[0].Body + result.Script.Actions[1].Target
	if !strings.Contains(bodies, "{{data.recorded_inputs.username}}") {
		t.Fatalf("expected username parameter reference, got body %q", result.Script.Actions[0].Body)
	}
	if !strings.Contains(result.Script.Actions[1].Target, "{{data.recorded_inputs.query}}") {
		t.Fatalf("expected query parameter reference in target, got %q", result.Script.Actions[1].Target)
	}
	// Non-input fields are left alone.
	if strings.Contains(result.Script.Actions[0].Body, "{{data.recorded_inputs.remember}}") {
		t.Fatalf("did not expect 'remember' to be parameterized: %q", result.Script.Actions[0].Body)
	}
	foundWarning := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "Parameterized") {
			foundWarning = true
		}
	}
	if !foundWarning {
		t.Fatalf("expected a parameterization warning, got %v", result.Warnings)
	}
	if !strings.Contains(strings.Join(result.Warnings, "\n"), "Secret-like") {
		t.Fatalf("expected a secret placeholder warning, got %v", result.Warnings)
	}
}

func TestParameterizeInputsKeepsSlugCollidingFieldsDistinct(t *testing.T) {
	script := &dsl.Script{Actions: []dsl.Action{{
		Type:   "request",
		Target: "/submit?first-name=Alice&first_name=Bob",
	}}}
	warnings := parameterizeInputs(script, "collision")
	if len(warnings) == 0 || len(script.DataSources) != 1 {
		t.Fatalf("parameterization = warnings %#v, data sources %#v", warnings, script.DataSources)
	}
	target := script.Actions[0].Target
	if !strings.Contains(target, "{{data.recorded_inputs.first_name}}") ||
		!strings.Contains(target, "{{data.recorded_inputs.first_name_2}}") {
		t.Fatalf("slug-colliding fields were merged: %q", target)
	}
	if got := script.DataSources[0].InlineData; !strings.Contains(got, "first_name,first_name_2\nAlice,Bob") {
		t.Fatalf("collision data source = %q", got)
	}
}

func TestParameterizeInputsWorkLimitIsTransactional(t *testing.T) {
	original := "/submit?username=Alice&email=alice@example.test"
	script := &dsl.Script{Actions: []dsl.Action{{Type: "request", Target: original}}}
	warnings := parameterizeInputsWithLimits(script, "limited", 1<<20, 1)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "not completed") {
		t.Fatalf("work-limit warnings = %#v", warnings)
	}
	if got := script.Actions[0].Target; got != original {
		t.Fatalf("work-limited parameterization partially rewrote target to %q", got)
	}
	if len(script.DataSources) != 0 || script.Metadata != nil {
		t.Fatalf("work-limited parameterization mutated script: data=%#v metadata=%#v", script.DataSources, script.Metadata)
	}
}

func TestConvertSessionNilIsSafe(t *testing.T) {
	result := ConvertSession(nil, "")
	if result == nil || result.Script != nil || len(result.Warnings) != 1 {
		t.Fatalf("ConvertSession(nil) = %#v, want nil script with warning", result)
	}
}

func TestConvertSession_Empty(t *testing.T) {
	s := &Session{
		ID:       "test",
		Name:     "Empty",
		Requests: []RecordedRequest{},
	}

	result := ConvertSession(s, "")
	if len(result.Script.Actions) != 0 {
		t.Fatalf("expected 0 actions, got %d", len(result.Script.Actions))
	}
	if len(result.Warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d", len(result.Warnings))
	}
}

func TestConvertSession_Assertions(t *testing.T) {
	s := &Session{
		ID:      "test",
		Name:    "Assert Test",
		BaseURL: "http://example.com",
		Requests: []RecordedRequest{
			{Seq: 1, Method: "GET", URL: "http://example.com/ok", Path: "/ok", StatusCode: 200},
			{Seq: 2, Method: "POST", URL: "http://example.com/create", Path: "/create", StatusCode: 201},
			{Seq: 3, Method: "GET", URL: "http://example.com/redirect", Path: "/redirect", StatusCode: 302},
			{Seq: 4, Method: "GET", URL: "http://example.com/not-found", Path: "/not-found", StatusCode: 404},
		},
	}

	result := ConvertSession(s, "")
	a1 := result.Script.Actions[0]
	if len(a1.Assertions) != 1 || a1.Assertions[0].Expected != 200 {
		t.Fatalf("expected status_code assertion 200, got %+v", a1.Assertions)
	}
	a2 := result.Script.Actions[1]
	if len(a2.Assertions) != 1 || a2.Assertions[0].Expected != 201 {
		t.Fatalf("expected status_code assertion 201, got %+v", a2.Assertions)
	}
	for i, action := range result.Script.Actions[2:] {
		if len(action.Assertions) != 0 {
			t.Fatalf("action %d should not assert redirect/error status, got %+v", i+3, action.Assertions)
		}
	}
}

func TestDetectCorrelations(t *testing.T) {
	requests := []RecordedRequest{
		{
			Seq:          1,
			Method:       "POST",
			URL:          "http://example.com/login",
			Path:         "/login",
			StatusCode:   200,
			ResponseBody: `{"token":"eyJhbGciOiJIUzI1NiJ9.testpayload.signature","session_id":"sess-abcdef-123456"}`,
			ResponseHeaders: map[string]string{
				"X-Csrf-Token": "csrf-token-value-here",
			},
		},
		{
			Seq:    2,
			Method: "GET",
			URL:    "http://example.com/api/data?token=eyJhbGciOiJIUzI1NiJ9.testpayload.signature",
			Path:   "/api/data",
			Headers: map[string]string{
				"X-Csrf-Token": "csrf-token-value-here",
			},
			StatusCode:   200,
			ResponseBody: `{"items":[]}`,
		},
		{
			Seq:    3,
			Method: "POST",
			URL:    "http://example.com/api/action",
			Path:   "/api/action",
			Body:   `{"session":"sess-abcdef-123456"}`,
			Headers: map[string]string{
				"Authorization": "Bearer eyJhbGciOiJIUzI1NiJ9.testpayload.signature",
			},
			StatusCode:   200,
			ResponseBody: `{"ok":true}`,
		},
	}

	correlations := detectCorrelations(requests)

	if len(correlations) == 0 {
		t.Fatal("expected at least one correlation detected")
	}

	foundToken := false
	for _, c := range correlations {
		if c.Name == "corr_token" || c.Name == "corr_session_id" || c.Name == "corr_x_csrf_token" {
			foundToken = true
			if len(c.UsedInSeqs) == 0 {
				t.Fatalf("correlation %s should have used_in_seqs", c.Name)
			}
		}
	}
	if !foundToken {
		t.Fatalf("expected token/session correlation, got %+v", correlations)
	}
}

func TestDetectCorrelations_HTMLFormToken(t *testing.T) {
	requests := []RecordedRequest{
		{
			Seq:        1,
			Method:     "GET",
			URL:        "http://example.com/login",
			Path:       "/login",
			StatusCode: 200,
			ResponseHeaders: map[string]string{
				"Content-Type": "text/html",
			},
			ResponseBody: `<form method="post"><input type="hidden" name="csrf_token" value="tok-9f8e7d6c5b4a"><input type="text" name="user"></form>`,
		},
		{
			Seq:        2,
			Method:     "POST",
			URL:        "http://example.com/login",
			Path:       "/login",
			Body:       `csrf_token=tok-9f8e7d6c5b4a&user=demo`,
			StatusCode: 200,
		},
	}

	correlations := detectCorrelations(requests)

	var csrf *Correlation
	for i := range correlations {
		if correlations[i].Name == "corr_csrf_token" {
			csrf = &correlations[i]
		}
	}
	if csrf == nil {
		t.Fatalf("expected corr_csrf_token correlation, got %+v", correlations)
	}
	if csrf.ExtractType != "regex" || csrf.Source != "body" {
		t.Fatalf("expected regex/body extractor, got %s/%s", csrf.ExtractType, csrf.Source)
	}
	if !strings.Contains(csrf.Expression, `name="csrf_token"`) || !strings.Contains(csrf.Expression, `value="([^"]{4,})"`) {
		t.Fatalf("unexpected expression: %q", csrf.Expression)
	}
	if len(csrf.UsedInSeqs) == 0 {
		t.Fatal("expected the form token to be used in a later request")
	}
}

func TestApplyCorrelationVars_LongestFirst(t *testing.T) {
	// A short value that is a substring of a longer value must not clobber it.
	corrMap := map[string]string{
		"123":       "{{var.short_id}}",
		"123456789": "{{var.long_token}}",
	}
	result := applyCorrelationVars("token=123456789", corrMap)
	if result != "token={{var.long_token}}" {
		t.Fatalf("expected longest value replaced first, got %q", result)
	}
}

func TestFilterRecordedHeaders(t *testing.T) {
	headers := map[string]string{
		"Content-Type":              "application/json",
		"Accept":                    "text/html",
		"Host":                      "example.com",
		"Content-Length":            "42",
		"X-Custom-Header":           "custom-value",
		"Authorization":             "Bearer xyz",
		"Cookie":                    "session=abc",
		"User-Agent":                "Mozilla/5.0",
		"X-APEXLoad-Recorder-Token": "recorder-control-secret",
	}

	filtered := filterRecordedHeaders(headers, false)

	if _, ok := filtered["Host"]; ok {
		t.Fatal("Host should be filtered")
	}
	if _, ok := filtered["Content-Length"]; ok {
		t.Fatal("Content-Length should be filtered")
	}
	if _, ok := filtered["Cookie"]; ok {
		t.Fatal("Cookie should be filtered")
	}
	if _, ok := filtered["User-Agent"]; ok {
		t.Fatal("User-Agent should be filtered")
	}
	if _, ok := filtered["Accept"]; ok {
		t.Fatal("Accept should be filtered")
	}
	if _, ok := filtered["X-APEXLoad-Recorder-Token"]; ok {
		t.Fatal("recorder control headers should be filtered")
	}
	if v, ok := filtered["Content-Type"]; !ok || v != "application/json" {
		t.Fatal("Content-Type should be kept")
	}
	if v, ok := filtered["X-Custom-Header"]; !ok || v != "custom-value" {
		t.Fatal("X-Custom-Header should be kept")
	}
	if v, ok := filtered["Authorization"]; !ok || v != "Bearer xyz" {
		t.Fatal("Authorization should be kept")
	}

	withCookies := filterRecordedHeaders(headers, true)
	if v, ok := withCookies["Cookie"]; !ok || v != "session=abc" {
		t.Fatalf("Cookie should be kept when cookie capture is enabled, got %q", v)
	}
	if _, ok := withCookies["X-APEXLoad-Recorder-Token"]; ok {
		t.Fatal("recorder control headers should be filtered when cookie capture is enabled")
	}
}

func TestSimplifyPath(t *testing.T) {
	tests := []struct {
		path   string
		expect string
	}{
		{"/api/v1/users", "/api/v1/users"},
		{"/", "/"},
		{"/api/v1/org/123/users/456/roles", "/users/456/roles"},
	}

	for _, tt := range tests {
		got := simplifyPath(tt.path)
		if got != tt.expect {
			t.Errorf("simplifyPath(%q) = %q, want %q", tt.path, got, tt.expect)
		}
	}
}

func TestApplyCorrelationVars(t *testing.T) {
	corrMap := map[string]string{
		"abc123":  "{{var.corr_token}}",
		"sess-42": "{{var.corr_session_id}}",
	}

	input := "http://example.com/api?token=abc123&session=sess-42"
	result := applyCorrelationVars(input, corrMap)

	if result != "http://example.com/api?token={{var.corr_token}}&session={{var.corr_session_id}}" {
		t.Fatalf("unexpected result: %s", result)
	}
}

func TestTruncate(t *testing.T) {
	if truncate("short", 10) != "short" {
		t.Fatal("short string should not be truncated")
	}
	result := truncate("this is a long string that should be truncated", 20)
	if len(result) != 23 { // 20 + "..."
		t.Fatalf("expected 23 chars, got %d: %q", len(result), result)
	}
}

func TestSimplifyPathPreservesValidUTF8(t *testing.T) {
	got := simplifyPath("/" + strings.Repeat("é", 30))
	if len(got) > 40 || !utf8.ValidString(got) {
		t.Fatalf("simplifyPath returned invalid or oversized UTF-8: %q (%d bytes)", got, len(got))
	}
}

func TestConvertSession_CorrelationApplied(t *testing.T) {
	s := &Session{
		ID:      "test",
		Name:    "Corr Test",
		BaseURL: "http://example.com",
		Requests: []RecordedRequest{
			{
				Seq:             1,
				Timestamp:       time.Now(),
				Method:          "POST",
				URL:             "http://example.com/auth",
				Path:            "/auth",
				Host:            "example.com",
				StatusCode:      200,
				ResponseBody:    `{"token":"dynamic-token-value-12345678"}`,
				ResponseHeaders: map[string]string{},
				Headers:         map[string]string{},
			},
			{
				Seq:        2,
				Timestamp:  time.Now(),
				Method:     "GET",
				URL:        "http://example.com/api/data?auth=dynamic-token-value-12345678",
				Path:       "/api/data",
				Host:       "example.com",
				StatusCode: 200,
				Headers:    map[string]string{},
			},
		},
	}

	result := ConvertSession(s, "")

	if len(result.Correlations) == 0 {
		t.Fatal("expected correlations to be detected")
	}

	foundExtractor := false
	for _, action := range result.Script.Actions {
		for _, ext := range action.Extractors {
			if ext.Name == "corr_token" {
				foundExtractor = true
			}
		}
	}
	if !foundExtractor {
		t.Fatal("expected extractor on the auth action")
	}

	action2 := result.Script.Actions[1]
	if action2.Target == "http://example.com/api/data?auth=dynamic-token-value-12345678" {
		t.Fatal("expected correlation var to replace dynamic value in target URL")
	}
}

func TestConvertSession_DoesNotAutoApplyLowConfidenceCorrelations(t *testing.T) {
	s := &Session{
		ID:      "test",
		Name:    "Low Confidence Corr Test",
		BaseURL: "http://example.com",
		Requests: []RecordedRequest{
			{
				Seq:             1,
				Method:          "POST",
				URL:             "http://example.com/orders",
				Path:            "/orders",
				Host:            "example.com",
				StatusCode:      200,
				ResponseBody:    `{"order_id":1234}`,
				ResponseHeaders: map[string]string{},
				Headers:         map[string]string{},
			},
			{
				Seq:        2,
				Method:     "GET",
				URL:        "http://example.com/orders/1234",
				Path:       "/orders/1234",
				Host:       "example.com",
				StatusCode: 200,
				Headers:    map[string]string{},
			},
		},
	}

	result := ConvertSession(s, "")
	if len(result.Correlations) == 0 {
		t.Fatal("expected low-confidence correlation candidate to remain available for review")
	}
	for _, corr := range result.Correlations {
		if corr.Confidence >= 0.8 {
			t.Fatalf("test setup produced high-confidence correlation unexpectedly: %#v", corr)
		}
	}
	if len(result.Script.Actions[0].Extractors) != 0 {
		t.Fatalf("extractors = %#v, want no auto-applied low-confidence extractor", result.Script.Actions[0].Extractors)
	}
	if got := result.Script.Actions[1].Target; got != "/orders/1234" {
		t.Fatalf("target = %q, want literal value left for review", got)
	}
}

func TestConvertSession_SkippedResponsesDoNotCreateDanglingCorrelationVars(t *testing.T) {
	s := &Session{
		ID:      "test",
		Name:    "Static Corr Test",
		BaseURL: "http://example.com",
		Requests: []RecordedRequest{
			{
				Seq:          1,
				Method:       "GET",
				URL:          "http://example.com/app.js",
				Path:         "/app.js",
				Host:         "example.com",
				StatusCode:   200,
				IsStatic:     true,
				ResponseBody: `window.token = "static-token-value-12345678";`,
			},
			{
				Seq:        2,
				Method:     "POST",
				URL:        "http://example.com/api/save?token=static-token-value-12345678",
				Path:       "/api/save",
				Host:       "example.com",
				StatusCode: 200,
			},
		},
	}

	result := ConvertSession(s, "")
	if len(result.Correlations) != 0 {
		t.Fatalf("correlations = %#v, want none from skipped static response", result.Correlations)
	}
	if got := result.Script.Actions[0].Target; strings.Contains(got, "{{var.") {
		t.Fatalf("target = %q, want literal value because extractor source was skipped", got)
	}
}

func TestConvertSession_WarnsWhenCaptureWasTruncated(t *testing.T) {
	session := &Session{
		ID:                       "test",
		Name:                     "Truncated Capture",
		DroppedRequests:          2,
		DroppedWebSocketMessages: 3,
		Truncated:                true,
	}

	result := ConvertSession(session, "")
	warnings := strings.Join(result.Warnings, "\n")
	if !strings.Contains(warnings, "capture was truncated") ||
		!strings.Contains(warnings, "2 request(s)") ||
		!strings.Contains(warnings, "3 WebSocket message(s)") {
		t.Fatalf("warnings = %q, want retained drop counts", warnings)
	}
}
