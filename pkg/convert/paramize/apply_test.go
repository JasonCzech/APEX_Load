package paramize

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func selectAll(suggestions []Suggestion) map[string]bool {
	selected := make(map[string]bool, len(suggestions))
	for _, s := range suggestions {
		selected[s.ID] = true
	}
	return selected
}

func TestApplySuggestionsNilScript(t *testing.T) {
	if _, _, err := ApplySuggestions(nil, nil, nil); err == nil {
		t.Fatal("expected error for nil script")
	}
}

func TestApplyDataParamSuggestion(t *testing.T) {
	script := httpScript(
		dsl.Action{Name: "get user", Type: "http_request", Method: "GET", Target: "/users?user_id=12345"},
		dsl.Action{Name: "orders", Type: "http_request", Method: "GET", Target: "/orders?user_id=12345"},
	)
	result := Suggest(script, allClasses())
	params := findByKind(result.Suggestions, KindDataParam)
	if len(params) != 1 {
		t.Fatalf("expected 1 data_param, got %d", len(params))
	}

	applied, diffs, err := ApplySuggestions(script, result.Suggestions, selectAll(result.Suggestions))
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	for i, action := range applied.Actions {
		if !strings.Contains(action.Target, "user_id={{var.user_id}}") {
			t.Errorf("action %d target = %q, want parameterized user_id", i, action.Target)
		}
	}
	if len(applied.DataSources) != 1 {
		t.Fatalf("expected 1 data source, got %d", len(applied.DataSources))
	}
	source := applied.DataSources[0]
	if source.Name != consolidatedDataSourceName || source.Type != "csv" || !source.FirstRowHeader {
		t.Errorf("data source = %+v, want inline csv named %s", source, consolidatedDataSourceName)
	}
	if source.InlineData != "user_id\n12345" {
		t.Errorf("inline data = %q, want header + seed row", source.InlineData)
	}
	if len(applied.Parameters) != 1 || applied.Parameters[0].Name != "user_id" || applied.Parameters[0].DataSource != source.Name {
		t.Errorf("parameters = %+v, want one user_id parameter bound to %s", applied.Parameters, source.Name)
	}

	kinds := map[string]int{}
	for _, diff := range diffs {
		kinds[diff.Kind]++
	}
	if kinds["value_replaced"] != 2 || kinds["data_source_added"] != 1 || kinds["parameter_added"] != 1 {
		t.Errorf("diff kinds = %+v, want 2 value_replaced + 1 data_source_added + 1 parameter_added", kinds)
	}

	// Validate the applied script passes DSL validation.
	if issues := dsl.ValidateScriptIssues(applied); len(issues) > 0 {
		t.Errorf("applied script has validation issues: %v", issues)
	}
}

func TestApplySecretSuggestion(t *testing.T) {
	script := httpScript(dsl.Action{
		Name: "login", Type: "http_request", Method: "POST", Target: "/login",
		Headers: map[string]string{"Authorization": "Bearer tok123", "Accept": "application/json"},
		Body:    `{"password": "hunter2"}`,
	})
	result := Suggest(script, allClasses())
	secrets := findByKind(result.Suggestions, KindSecret)
	if len(secrets) != 2 {
		t.Fatalf("expected 2 secrets (header + body), got %d", len(secrets))
	}

	applied, diffs, err := ApplySuggestions(script, result.Suggestions, selectAll(result.Suggestions))
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	action := applied.Actions[0]
	if action.Headers["Authorization"] != "Bearer {{env.AUTHORIZATION}}" {
		t.Errorf("authorization = %q, want scheme-preserving env template", action.Headers["Authorization"])
	}
	if action.Headers["Accept"] != "application/json" {
		t.Errorf("unrelated header changed: %q", action.Headers["Accept"])
	}
	if !strings.Contains(action.Body, `"password": "{{env.PASSWORD}}"`) {
		t.Errorf("body = %q, want password externalized", action.Body)
	}
	if len(diffs) != 2 {
		t.Errorf("expected 2 value_replaced diffs, got %+v", diffs)
	}
	// Secrets add no data sources or parameters.
	if len(applied.DataSources) != 0 || len(applied.Parameters) != 0 {
		t.Errorf("secrets must not add data sources/parameters, got %d/%d", len(applied.DataSources), len(applied.Parameters))
	}
}

func TestApplyNumericSecretSuggestionKeepsJSONValid(t *testing.T) {
	script := httpScript(dsl.Action{
		Name: "login", Type: "http_request", Method: "POST", Target: "/login",
		Body: `{"passcode":123456,"safe":1}`,
	})
	result := Suggest(script, allClasses())
	secrets := findByKind(result.Suggestions, KindSecret)
	if len(secrets) != 1 {
		t.Fatalf("expected one numeric secret suggestion, got %#v", secrets)
	}

	applied, _, err := ApplySuggestions(script, result.Suggestions, map[string]bool{secrets[0].ID: true})
	if err != nil {
		t.Fatalf("apply numeric secret: %v", err)
	}
	if got := applied.Actions[0].Body; got != `{"passcode":"{{env.PASSCODE}}","safe":1}` {
		t.Fatalf("numeric secret body = %q", got)
	}
}

func TestReplaceHeaderUpdatesAllCaseVariants(t *testing.T) {
	action := dsl.Action{Headers: map[string]string{
		"X-Token": "abc123",
		"x-token": "prefix-abc123",
	}}
	_, _, changed := replaceHeader(&action, "X-Token", func(value string) (string, bool) {
		updated := strings.ReplaceAll(value, "abc123", "{{var.token}}")
		return updated, updated != value
	})
	if !changed {
		t.Fatal("replaceHeader reported no change")
	}
	if action.Headers["X-Token"] != "{{var.token}}" || action.Headers["x-token"] != "prefix-{{var.token}}" {
		t.Fatalf("case-variant headers were not both rewritten: %#v", action.Headers)
	}
}

func TestApplyHeaderSuggestionUpdatesHeaderList(t *testing.T) {
	script := httpScript(dsl.Action{
		Name:    "login",
		Type:    "http_request",
		Method:  "POST",
		Target:  "/login",
		Headers: map[string]string{"Authorization": "Bearer recorded-token"},
		HeaderList: dsl.HeaderList{
			{Name: "Accept", Value: "application/json"},
			{Name: "Authorization", Value: "Bearer recorded-token"},
		},
	})
	suggestion := Suggestion{
		ID:   "s-header",
		Kind: KindSecret,
		Locations: []Location{{
			Section:     "actions",
			ActionIndex: 0,
			Field:       "headers.Authorization",
			SearchText:  "Bearer recorded-token",
			ReplaceText: "Bearer {{env.AUTHORIZATION}}",
		}},
	}
	applied, diffs, err := ApplySuggestions(script, []Suggestion{suggestion}, map[string]bool{"s-header": true})
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	action := applied.Actions[0]
	if action.Headers["Authorization"] != "Bearer {{env.AUTHORIZATION}}" {
		t.Fatalf("headers map authorization = %q, want template", action.Headers["Authorization"])
	}
	if action.HeaderList[1].Value != "Bearer {{env.AUTHORIZATION}}" {
		t.Fatalf("header_list authorization = %q, want template", action.HeaderList[1].Value)
	}
	if len(diffs) != 1 || diffs[0].Before != "Bearer recorded-token" || diffs[0].After != "Bearer {{env.AUTHORIZATION}}" {
		t.Fatalf("diffs = %+v, want one header replacement diff", diffs)
	}
}

func TestApplyHeaderSuggestionPreservesDuplicateHeaderSummaryOrder(t *testing.T) {
	script := httpScript(dsl.Action{
		Name:    "submit",
		Type:    "http_request",
		Headers: map[string]string{"x-token": "encoded-recorded"},
		HeaderList: dsl.HeaderList{
			{Name: "X-Token", Value: "raw-recorded"},
			{Name: "x-token", Value: "encoded-recorded"},
		},
	})
	suggestion := Suggestion{
		ID:   "s-duplicate-header",
		Kind: KindSecret,
		Locations: []Location{{
			Section:     "actions",
			ActionIndex: 0,
			Field:       "headers.X-Token",
			SearchText:  "raw-recorded",
			ReplaceText: "{{var.raw_token}}",
		}},
	}

	applied, diffs, err := ApplySuggestions(script, []Suggestion{suggestion}, map[string]bool{suggestion.ID: true})
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	action := applied.Actions[0]
	if action.HeaderList[0].Value != "{{var.raw_token}}" || action.HeaderList[1].Value != "encoded-recorded" {
		t.Fatalf("header_list = %#v, want only the matching duplicate replaced", action.HeaderList)
	}
	if action.Headers["x-token"] != "encoded-recorded" {
		t.Fatalf("headers summary = %#v, want final wire-order duplicate", action.Headers)
	}
	if _, duplicate := action.Headers["X-Token"]; duplicate {
		t.Fatalf("headers summary gained a case-only duplicate: %#v", action.Headers)
	}
	if len(diffs) != 1 {
		t.Fatalf("diffs = %#v, want one header replacement", diffs)
	}
}

func TestApplyCorrelationSuggestion(t *testing.T) {
	script := httpScript(
		dsl.Action{Name: "login", Type: "http_request", Method: "POST", Target: "/login"},
		dsl.Action{Name: "profile", Type: "http_request", Method: "GET", Target: "/profile?token=tok_a1b2c3d4e5f6a1b2c3d4"},
	)
	observations := []correlate.Observation{
		{ID: 1, RequestURL: "https://api.example.com/login", ResponseBody: `{"access_token": "tok_a1b2c3d4e5f6a1b2c3d4"}`},
		{ID: 2, RequestURL: "https://api.example.com/profile?token=tok_a1b2c3d4e5f6a1b2c3d4"},
	}
	opts := allClasses()
	opts.Observations = observations
	result := Suggest(script, opts)
	correlations := findByKind(result.Suggestions, KindCorrelation)
	if len(correlations) != 1 {
		t.Fatalf("expected 1 correlation, got %d", len(correlations))
	}

	applied, diffs, err := ApplySuggestions(script, result.Suggestions, selectAll(result.Suggestions))
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	source := applied.Actions[0]
	if len(source.Extractors) != 1 {
		t.Fatalf("expected extractor appended to source action, got %+v", source.Extractors)
	}
	if source.Extractors[0].Name != correlations[0].VarName {
		t.Errorf("extractor name = %q, want %q", source.Extractors[0].Name, correlations[0].VarName)
	}
	if source.Extractors[0].OnMiss != "fail" {
		t.Errorf("extractor on_miss = %q, want fail", source.Extractors[0].OnMiss)
	}
	used := applied.Actions[1]
	if strings.Contains(used.Target, "tok_a1b2c3d4e5f6a1b2c3d4") {
		t.Errorf("target still contains the recorded literal: %q", used.Target)
	}
	if !strings.Contains(used.Target, "{{var."+correlations[0].VarName+"}}") {
		t.Errorf("target = %q, want template substitution", used.Target)
	}
	hasExtractorDiff := false
	for _, diff := range diffs {
		if diff.Kind == "extractor_added" {
			hasExtractorDiff = true
		}
	}
	if !hasExtractorDiff {
		t.Errorf("diffs missing extractor_added: %+v", diffs)
	}
}

func TestApplyCorrelationReplacesOnlyRawAndExactQueryEncodedRepresentations(t *testing.T) {
	const recordedValue = "tok +/== recorded"
	exactEncodedValue := url.QueryEscape(recordedValue)
	script := httpScript(
		dsl.Action{Name: "login", Type: "http_request", Method: "POST", Target: "/login"},
		dsl.Action{
			Name:       "submit",
			Type:       "http_request",
			Method:     "POST",
			Target:     "/submit?token=" + exactEncodedValue,
			Body:       "raw=" + recordedValue + "&exact=" + exactEncodedValue,
			HeaderList: dsl.HeaderList{{Name: "X-Token", Value: exactEncodedValue}},
		},
	)
	observations := []correlate.Observation{
		{ID: 1, ResponseBody: `{"access_token":"` + recordedValue + `"}`},
		{
			ID:             2,
			RequestURL:     "https://api.example.com/submit?token=" + exactEncodedValue,
			RequestBody:    script.Actions[1].Body,
			RequestHeaders: map[string]string{"X-Token": exactEncodedValue},
		},
	}
	opts := Options{EnableCorrelation: true, Observations: observations}
	result := Suggest(script, opts)
	correlations := findByKind(result.Suggestions, KindCorrelation)
	if len(correlations) != 2 {
		t.Fatalf("correlations = %#v, want raw and encoded siblings", correlations)
	}
	var rawCorr, encodedCorr *Suggestion
	for i := range correlations {
		if strings.HasSuffix(correlations[i].VarName, "_encoded") {
			encodedCorr = &correlations[i]
		} else {
			rawCorr = &correlations[i]
		}
	}
	if rawCorr == nil || encodedCorr == nil {
		t.Fatalf("correlations = %#v, want base and _encoded suggestions", correlations)
	}
	if rawCorr.Extractor == nil || rawCorr.Extractor.Convert != "" || encodedCorr.Extractor == nil || encodedCorr.Extractor.Convert != "url_encode" {
		t.Fatalf("extractors = %#v / %#v, want raw plus url_encode sibling", rawCorr.Extractor, encodedCorr.Extractor)
	}
	foundSearch := map[string]bool{}
	for _, corr := range correlations {
		for _, location := range corr.Locations {
			foundSearch[location.Field+"\x00"+location.SearchText] = true
		}
	}
	for _, key := range []string{
		"target\x00" + exactEncodedValue,
		"body\x00" + recordedValue,
		"body\x00" + exactEncodedValue,
		"headers.X-Token\x00" + exactEncodedValue,
	} {
		if !foundSearch[key] {
			t.Fatalf("missing correlation location %q in %#v", key, correlations)
		}
	}
	applied, diffs, err := ApplySuggestions(script, result.Suggestions, map[string]bool{rawCorr.ID: true, encodedCorr.ID: true})
	if err != nil {
		t.Fatalf("ApplySuggestions: %v", err)
	}
	rawTemplate := "{{var." + rawCorr.VarName + "}}"
	encodedTemplate := "{{var." + encodedCorr.VarName + "}}"
	used := applied.Actions[1]
	if used.Target != "/submit?token="+encodedTemplate {
		t.Fatalf("target = %q, want encoded literal replaced once", used.Target)
	}
	wantBody := "raw=" + rawTemplate + "&exact=" + encodedTemplate
	if used.Body != wantBody {
		t.Fatalf("body = %q, want exact query spelling rewritten and unsupported spellings preserved as %q", used.Body, wantBody)
	}
	if len(used.HeaderList) != 1 || used.HeaderList[0].Value != encodedTemplate {
		t.Fatalf("header_list = %#v, want template", used.HeaderList)
	}
	if used.Headers["X-Token"] != encodedTemplate {
		t.Fatalf("headers summary = %#v, want HeaderList replacement synchronized", used.Headers)
	}
	if strings.Contains(used.Target+used.HeaderList[0].Value, exactEncodedValue) {
		t.Fatalf("exact QueryEscape representation survived application: %#v", used)
	}
	if len(diffs) == 0 {
		t.Fatal("ApplySuggestions returned no diffs")
	}
}

func TestSuggestCorrelationFailsClosedOnUnsupportedEncodedConsumer(t *testing.T) {
	const recordedValue = "tok +/== recorded"
	exactEncodedValue := url.QueryEscape(recordedValue)
	percent20Value := strings.ReplaceAll(exactEncodedValue, "+", "%20")
	script := httpScript(
		dsl.Action{Name: "issue", Type: "http_request", Target: "/issue"},
		dsl.Action{Name: "use", Type: "http_request", Body: "raw=" + recordedValue + "&unsupported=" + percent20Value},
	)
	result := Suggest(script, Options{
		EnableCorrelation: true,
		Observations: []correlate.Observation{
			{ID: 1, ResponseBody: `{"access_token":"` + recordedValue + `"}`},
			{ID: 2, RequestBody: script.Actions[1].Body},
		},
	})
	if correlations := findByKind(result.Suggestions, KindCorrelation); len(correlations) != 0 {
		t.Fatalf("correlations = %#v, want the whole partial-repair suggestion suppressed", correlations)
	}
}

func TestApplySuggestionsRejectsCrossCorrelationLiteralCollision(t *testing.T) {
	const rawValue = "opaque token/123456"
	encodedValue := url.QueryEscape(rawValue)
	script := httpScript(
		dsl.Action{Name: "source-a", Type: "http_request", Target: "/source-a"},
		dsl.Action{Name: "source-b", Type: "http_request", Target: "/source-b"},
		dsl.Action{Name: "use", Type: "http_request", Target: "/use?token=" + encodedValue},
	)
	result := Suggest(script, Options{
		EnableCorrelation: true,
		Observations: []correlate.Observation{
			{ID: 1, ResponseBody: `{"access_token":"` + rawValue + `"}`},
			{ID: 2, ResponseBody: `{"csrf_token":"` + encodedValue + `"}`},
			{ID: 3, RequestURL: "https://example.test/use?token=" + encodedValue},
		},
	})
	correlations := findByKind(result.Suggestions, KindCorrelation)
	if len(correlations) != 2 {
		t.Fatalf("correlations = %#v, want both colliding owners visible for selection", correlations)
	}
	selected := map[string]bool{correlations[0].ID: true, correlations[1].ID: true}
	if _, _, err := ApplySuggestions(script, result.Suggestions, selected); err == nil || !strings.Contains(err.Error(), "ambiguous selected correlation replacements") {
		t.Fatalf("ApplySuggestions collision error = %v", err)
	}
	if got := script.Actions[2].Target; got != "/use?token="+encodedValue {
		t.Fatalf("input script mutated after rejected collision: %q", got)
	}
}

func TestApplySuggestionsAllowsSameLiteralOnDisjointSurfaces(t *testing.T) {
	const literal = "shared-token-123456"
	script := httpScript(
		dsl.Action{Name: "first", Type: "http_request", Target: "/first?token=" + literal},
		dsl.Action{Name: "second", Type: "http_request", Target: "/second?token=" + literal},
	)
	suggestions := []Suggestion{
		{
			ID: "first", Kind: KindCorrelation,
			Extractor:     &dsl.Extractor{Name: "first", Source: "body", Type: "regex", Expression: `(first)`},
			FoundInAction: &Location{Section: "actions", ActionIndex: 0},
			Locations:     []Location{{Section: "actions", ActionIndex: 0, Field: "target", SearchText: literal, ReplaceText: "{{var.first}}"}},
		},
		{
			ID: "second", Kind: KindCorrelation,
			Extractor:     &dsl.Extractor{Name: "second", Source: "body", Type: "regex", Expression: `(second)`},
			FoundInAction: &Location{Section: "actions", ActionIndex: 1},
			Locations:     []Location{{Section: "actions", ActionIndex: 1, Field: "target", SearchText: literal, ReplaceText: "{{var.second}}"}},
		},
	}
	applied, _, err := ApplySuggestions(script, suggestions, map[string]bool{"first": true, "second": true})
	if err != nil {
		t.Fatalf("ApplySuggestions returned error for disjoint surfaces: %v", err)
	}
	if applied.Actions[0].Target != "/first?token={{var.first}}" || applied.Actions[1].Target != "/second?token={{var.second}}" {
		t.Fatalf("disjoint replacements = %q / %q", applied.Actions[0].Target, applied.Actions[1].Target)
	}
}

func TestApplySuggestionsRejectsOverlappingCorrelationLiteralsOnSameSurface(t *testing.T) {
	script := httpScript(dsl.Action{Name: "use", Type: "http_request", Body: "token-123-extra"})
	suggestions := []Suggestion{
		{
			ID: "short", Kind: KindCorrelation,
			Extractor:     &dsl.Extractor{Name: "short", Source: "body", Type: "regex", Expression: `(short)`},
			FoundInAction: &Location{Section: "actions", ActionIndex: 0},
			Locations:     []Location{{Section: "actions", ActionIndex: 0, Field: "body", SearchText: "token-123", ReplaceText: "{{var.short}}"}},
		},
		{
			ID: "long", Kind: KindCorrelation,
			Extractor:     &dsl.Extractor{Name: "long", Source: "body", Type: "regex", Expression: `(long)`},
			FoundInAction: &Location{Section: "actions", ActionIndex: 0},
			Locations:     []Location{{Section: "actions", ActionIndex: 0, Field: "body", SearchText: "token-123-extra", ReplaceText: "{{var.long}}"}},
		},
	}
	if _, _, err := ApplySuggestions(script, suggestions, map[string]bool{"short": true, "long": true}); err == nil {
		t.Fatal("ApplySuggestions accepted order-dependent overlapping correlation literals")
	}
}

func partialOverlapSuggestions() []Suggestion {
	return []Suggestion{
		{
			ID: "a", Kind: KindCorrelation,
			Extractor:     &dsl.Extractor{Name: "a", Source: "body", Type: "regex", Expression: `(abcdef)`},
			FoundInAction: &Location{Section: "actions", ActionIndex: 0},
			Locations:     []Location{{Section: "actions", ActionIndex: 0, Field: "body", SearchText: "abcdef", ReplaceText: "{{var.a}}"}},
		},
		{
			ID: "b", Kind: KindCorrelation,
			Extractor:     &dsl.Extractor{Name: "b", Source: "body", Type: "regex", Expression: `(defghi)`},
			FoundInAction: &Location{Section: "actions", ActionIndex: 0},
			Locations:     []Location{{Section: "actions", ActionIndex: 0, Field: "body", SearchText: "defghi", ReplaceText: "{{var.b}}"}},
		},
	}
}

func TestApplySuggestionsRejectsPartiallyOverlappingCorrelationLiterals(t *testing.T) {
	// "abcdef" and "defghi" contain neither each other, but occupy one merged
	// region "abcdefghi" of the body: single-pass application would rewrite only
	// the leftmost literal and still append the losing extractor.
	script := httpScript(dsl.Action{Name: "use", Type: "http_request", Body: "abcdefghi"})
	suggestions := partialOverlapSuggestions()
	_, _, err := ApplySuggestions(script, suggestions, map[string]bool{"a": true, "b": true})
	if err == nil || !strings.Contains(err.Error(), "ambiguous selected correlation replacements") {
		t.Fatalf("ApplySuggestions partial-overlap error = %v, want ambiguous-replacements rejection", err)
	}
	if got := script.Actions[0].Body; got != "abcdefghi" {
		t.Fatalf("input script mutated after rejected partial overlap: %q", got)
	}
}

func TestApplySuggestionsAllowsSuffixPrefixLiteralsUsedDisjointly(t *testing.T) {
	// The same literal pair shares boundary characters, but the merged spelling
	// never occurs in the field: disjoint occurrences replace correctly and must
	// not be rejected.
	script := httpScript(dsl.Action{Name: "use", Type: "http_request", Body: "abcdef defghi"})
	suggestions := partialOverlapSuggestions()
	applied, _, err := ApplySuggestions(script, suggestions, map[string]bool{"a": true, "b": true})
	if err != nil {
		t.Fatalf("ApplySuggestions returned error for disjoint suffix/prefix literals: %v", err)
	}
	if got := applied.Actions[0].Body; got != "{{var.a}} {{var.b}}" {
		t.Fatalf("disjoint replacements = %q, want both literals rewritten", got)
	}
	if got := len(applied.Actions[0].Extractors); got != 2 {
		t.Fatalf("extractors = %#v, want both appended", applied.Actions[0].Extractors)
	}
}

func TestLiteralOverlapSearchBudgetBoundsLongPeriodicLiterals(t *testing.T) {
	const repeatedBytes = 64 << 10
	left := "x" + strings.Repeat("a", repeatedBytes)
	right := strings.Repeat("a", repeatedBytes) + "y"
	// The two literals occur disjointly. Their periodic suffix/prefix has tens of
	// thousands of valid overlap lengths, which previously materialized a
	// quadratic number of multi-kilobyte merged strings.
	surface := left + " " + right

	var overlap, exhausted bool
	allocations := testing.AllocsPerRun(3, func() {
		budget := NewLiteralOverlapSearchBudget()
		overlap = budget.LiteralsPartiallyOverlapInSurface(surface, left, right)
		exhausted = budget.Exhausted()
	})
	if !overlap || !exhausted {
		t.Fatalf("long periodic overlap = %t exhausted=%t, want fail-closed budget exhaustion", overlap, exhausted)
	}
	if allocations > 4 {
		t.Fatalf("long periodic overlap allocations = %.1f, want bounded non-materializing analysis", allocations)
	}
}

func TestApplySuggestionsRejectsCrossKindOverlappingLiterals(t *testing.T) {
	script := httpScript(dsl.Action{Name: "use", Type: "http_request", Body: "token-123-extra"})
	correlation := Suggestion{
		ID:            "correlation",
		Kind:          KindCorrelation,
		Extractor:     &dsl.Extractor{Name: "corr_long", Source: "body", Type: "regex", Expression: `(token-.+)`},
		FoundInAction: &Location{Section: "actions", ActionIndex: 0},
		Locations:     []Location{{Section: "actions", ActionIndex: 0, Field: "body", SearchText: "token-123-extra", ReplaceText: "{{var.corr_long}}"}},
	}
	secret := Suggestion{
		ID:        "secret",
		Kind:      KindSecret,
		Locations: []Location{{Section: "actions", ActionIndex: 0, Field: "body", SearchText: "token-123", ReplaceText: "{{env.TOKEN}}"}},
	}
	if _, _, err := ApplySuggestions(script, []Suggestion{correlation, secret}, map[string]bool{"correlation": true, "secret": true}); err == nil {
		t.Fatal("ApplySuggestions accepted a cross-kind overlapping replacement")
	}
	if got := script.Actions[0].Body; got != "token-123-extra" {
		t.Fatalf("rejected application mutated input body: %q", got)
	}
}

func TestApplySuggestionsPreflightsCorrelationSourceAndConsumers(t *testing.T) {
	base := httpScript(
		dsl.Action{Name: "source", Type: "http_request", Target: "/source"},
		dsl.Action{Name: "consumer", Type: "http_request", Body: "token=recorded-token-123456"},
	)
	makeSuggestion := func() Suggestion {
		return Suggestion{
			ID:            "corr",
			Kind:          KindCorrelation,
			Extractor:     &dsl.Extractor{Name: "corr_token", Source: "body", Type: "regex", Expression: `"token":"([^"]+)"`},
			FoundInAction: &Location{Section: "actions", ActionIndex: 0},
			Locations:     []Location{{Section: "actions", ActionIndex: 1, Field: "body", SearchText: "recorded-token-123456", ReplaceText: "{{var.corr_token}}"}},
		}
	}

	t.Run("missing source", func(t *testing.T) {
		suggestion := makeSuggestion()
		suggestion.FoundInAction.ActionIndex = 99
		if _, _, err := ApplySuggestions(base, []Suggestion{suggestion}, map[string]bool{"corr": true}); err == nil || !strings.Contains(err.Error(), "source action") {
			t.Fatalf("missing-source error = %v", err)
		}
		if len(base.Actions[0].Extractors) != 0 || strings.Contains(base.Actions[1].Body, "{{var.") {
			t.Fatal("missing-source rejection mutated the input script")
		}
	})

	t.Run("stale consumer", func(t *testing.T) {
		suggestion := makeSuggestion()
		suggestion.Locations[0].SearchText = "no-longer-present"
		if _, _, err := ApplySuggestions(base, []Suggestion{suggestion}, map[string]bool{"corr": true}); err == nil || !strings.Contains(err.Error(), "no longer matches") {
			t.Fatalf("stale-consumer error = %v", err)
		}
		if len(base.Actions[0].Extractors) != 0 {
			t.Fatal("stale-consumer rejection added an extractor")
		}
	})
}

func TestApplySuggestionsRejectsExtractorCapacityBeforeMutation(t *testing.T) {
	existing := make([]dsl.Extractor, dsl.MaxActionExtractors)
	for index := range existing {
		existing[index] = dsl.Extractor{
			Name:       fmt.Sprintf("existing_%d", index),
			Source:     "body",
			Type:       "regex",
			Expression: fmt.Sprintf("existing-%d=(.+)", index),
		}
	}
	script := httpScript(
		dsl.Action{Name: "source", Type: "http_request", Target: "/source", Extractors: existing},
		dsl.Action{Name: "consumer", Type: "http_request", Body: "token=recorded-token"},
	)
	suggestion := Suggestion{
		ID:        "new-correlation",
		Kind:      KindCorrelation,
		Extractor: &dsl.Extractor{Name: "new_token", Source: "body", Type: "regex", Expression: `"token":"([^"]+)"`, OnMiss: "fail"},
		FoundInAction: &Location{
			Section:     "actions",
			ActionIndex: 0,
		},
		Locations: []Location{{
			Section:     "actions",
			ActionIndex: 1,
			Field:       "body",
			SearchText:  "recorded-token",
			ReplaceText: "{{var.new_token}}",
		}},
	}
	original, err := json.Marshal(script)
	if err != nil {
		t.Fatal(err)
	}

	applied, diffs, err := ApplySuggestions(script, []Suggestion{suggestion}, map[string]bool{suggestion.ID: true})
	if err == nil || !strings.Contains(err.Error(), "maximum of") {
		t.Fatalf("capacity error = %v, want maximum-extractors rejection", err)
	}
	if applied != nil || diffs != nil {
		t.Fatalf("rejected application returned partial output: applied=%#v diffs=%#v", applied, diffs)
	}
	after, marshalErr := json.Marshal(script)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if string(after) != string(original) {
		t.Fatalf("capacity rejection mutated input:\nbefore: %s\nafter:  %s", original, after)
	}
}

func TestApplySuggestionsCountsSelectedSiblingExtractorsAtomically(t *testing.T) {
	tests := []struct {
		name          string
		existingCount int
		wantError     bool
	}{
		{name: "exact boundary", existingCount: dsl.MaxActionExtractors - 2},
		{name: "one over", existingCount: dsl.MaxActionExtractors - 1, wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			existing := make([]dsl.Extractor, tt.existingCount)
			for index := range existing {
				existing[index] = dsl.Extractor{
					Name:       fmt.Sprintf("existing_%d", index),
					Source:     "body",
					Type:       "regex",
					Expression: fmt.Sprintf("existing-%d=(.+)", index),
				}
			}
			script := httpScript(
				dsl.Action{Name: "source", Type: "http_request", Target: "/source", Extractors: existing},
				dsl.Action{Name: "consumer", Type: "http_request", Body: "first=recorded-first&second=recorded-second"},
			)
			suggestions := []Suggestion{
				{
					ID:        "first",
					Kind:      KindCorrelation,
					Extractor: &dsl.Extractor{Name: "first", Source: "body", Type: "regex", Expression: `"first":"([^"]+)"`, OnMiss: "fail"},
					FoundInAction: &Location{
						Section:     "actions",
						ActionIndex: 0,
					},
					Locations: []Location{{
						Section:     "actions",
						ActionIndex: 1,
						Field:       "body",
						SearchText:  "recorded-first",
						ReplaceText: "{{var.first}}",
					}},
				},
				{
					ID:        "second",
					Kind:      KindCorrelation,
					Extractor: &dsl.Extractor{Name: "second", Source: "body", Type: "regex", Expression: `"second":"([^"]+)"`, OnMiss: "fail"},
					FoundInAction: &Location{
						Section:     "actions",
						ActionIndex: 0,
					},
					Locations: []Location{{
						Section:     "actions",
						ActionIndex: 1,
						Field:       "body",
						SearchText:  "recorded-second",
						ReplaceText: "{{var.second}}",
					}},
				},
			}
			original, err := json.Marshal(script)
			if err != nil {
				t.Fatal(err)
			}

			applied, _, err := ApplySuggestions(script, suggestions, map[string]bool{"first": true, "second": true})
			if tt.wantError {
				if err == nil || !strings.Contains(err.Error(), "maximum of") {
					t.Fatalf("capacity error = %v, want selected-sibling overflow rejection", err)
				}
				if applied != nil {
					t.Fatalf("overflow returned a partially applied script: %#v", applied)
				}
			} else {
				if err != nil {
					t.Fatalf("boundary application failed: %v", err)
				}
				if got := len(applied.Actions[0].Extractors); got != dsl.MaxActionExtractors {
					t.Fatalf("extractor count = %d, want exact boundary %d", got, dsl.MaxActionExtractors)
				}
				if got := applied.Actions[1].Body; got != "first={{var.first}}&second={{var.second}}" {
					t.Fatalf("consumer body = %q, want both sibling replacements", got)
				}
			}
			after, marshalErr := json.Marshal(script)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			if string(after) != string(original) {
				t.Fatalf("application mutated input:\nbefore: %s\nafter:  %s", original, after)
			}
		})
	}
}

func TestApplyEncodedOnlyCorrelationUsesURLEncodeExtractor(t *testing.T) {
	const rawValue = "token-a&b-123456"
	const encodedValue = "token-a%26b-123456"
	script := httpScript(
		dsl.Action{Name: "issue", Type: "http_request", Target: "/issue"},
		dsl.Action{Name: "use", Type: "http_request", Target: "/next?token=" + encodedValue},
	)
	result := Suggest(script, Options{
		EnableCorrelation: true,
		Observations: []correlate.Observation{
			{ID: 1, ResponseBody: `{"access_token":"` + rawValue + `"}`},
			{ID: 2, RequestURL: "https://example.test/next?token=" + encodedValue},
		},
	})
	correlations := findByKind(result.Suggestions, KindCorrelation)
	if len(correlations) != 1 {
		t.Fatalf("correlations = %#v, want one encoded-only suggestion", correlations)
	}
	corr := correlations[0]
	if corr.Extractor == nil || corr.Extractor.Convert != "url_encode" {
		t.Fatalf("extractor = %#v, want url_encode for ampersand-safe replay", corr.Extractor)
	}
	if len(corr.Locations) != 1 || corr.Locations[0].SearchText != encodedValue {
		t.Fatalf("locations = %#v, want only encoded target occurrence", corr.Locations)
	}
	applied, _, err := ApplySuggestions(script, result.Suggestions, map[string]bool{corr.ID: true})
	if err != nil {
		t.Fatalf("ApplySuggestions: %v", err)
	}
	if want := "/next?token={{var." + corr.VarName + "}}"; applied.Actions[1].Target != want {
		t.Fatalf("target = %q, want %q", applied.Actions[1].Target, want)
	}
}

func TestSuggestMixedURLDecodedSourceUsesUnconvertedEncodedSibling(t *testing.T) {
	const rawValue = "tok+/==recorded"
	const encodedValue = "tok%2B%2F%3D%3Drecorded"
	script := httpScript(
		dsl.Action{Name: "issue", Type: "http_request", Target: "/issue"},
		dsl.Action{
			Name:    "use",
			Type:    "http_request",
			Target:  "/next?token=" + encodedValue,
			Headers: map[string]string{"X-Raw-Token": rawValue},
		},
	)
	result := Suggest(script, Options{
		EnableCorrelation: true,
		Observations: []correlate.Observation{
			{ID: 1, ResponseBody: "aura.token=" + encodedValue},
			{
				ID:             2,
				RequestURL:     "https://example.test/next?token=" + encodedValue,
				RequestHeaders: map[string]string{"X-Raw-Token": rawValue},
			},
		},
	})
	correlations := findByKind(result.Suggestions, KindCorrelation)
	if len(correlations) != 2 {
		t.Fatalf("correlations = %#v, want raw and encoded siblings", correlations)
	}
	for _, corr := range correlations {
		if corr.Extractor == nil {
			t.Fatalf("missing extractor: %#v", corr)
		}
		if strings.HasSuffix(corr.VarName, "_encoded") {
			if corr.Extractor.Convert != "" {
				t.Fatalf("encoded sibling convert = %q, want empty to preserve captured encoding", corr.Extractor.Convert)
			}
		} else if corr.Extractor.Convert != "url_decode" {
			t.Fatalf("raw extractor convert = %q, want url_decode", corr.Extractor.Convert)
		}
	}
}

func TestSuggestURLDecodedSourceRequiresExactCanonicalWireSpelling(t *testing.T) {
	const sourceWire = "space%20token%2F123456"
	const consumerWire = "space+token%2F123456"
	script := httpScript(
		dsl.Action{Name: "issue", Type: "http_request", Target: "/issue"},
		dsl.Action{Name: "use", Type: "http_request", Target: "/next?state=" + consumerWire},
	)
	result := Suggest(script, Options{
		EnableCorrelation: true,
		Observations: []correlate.Observation{
			{ID: 1, ResponseBody: "state=" + sourceWire},
			{ID: 2, RequestURL: "https://example.test/next?state=" + consumerWire},
		},
	})
	if correlations := findByKind(result.Suggestions, KindCorrelation); len(correlations) != 0 {
		t.Fatalf("correlations = %#v, want fail-closed suppression because source captures %%20 but encoded sibling would preserve +", correlations)
	}
	if got := script.Actions[1].Target; got != "/next?state="+consumerWire {
		t.Fatalf("Suggest mutated input target: %q", got)
	}
}

func TestSuggestSkipsEncodedCorrelationBeyondRuntimeResultLimit(t *testing.T) {
	// Raw value fits the 16 KiB per-extraction ceiling, while QueryEscape
	// expands it beyond that ceiling.
	value := strings.Repeat("?", 6_000)
	encoded := url.QueryEscape(value)
	script := httpScript(
		dsl.Action{Name: "issue", Type: "http_request", Target: "/issue"},
		dsl.Action{Name: "use", Type: "http_request", Target: "/next?token=" + encoded},
	)
	result := Suggest(script, Options{
		EnableCorrelation: true,
		Observations: []correlate.Observation{
			{ID: 1, ResponseBody: `{"access_token":"` + value + `"}`},
			{ID: 2, RequestURL: "https://example.test/next?token=" + encoded},
		},
	})
	if correlations := findByKind(result.Suggestions, KindCorrelation); len(correlations) != 0 {
		t.Fatalf("correlations = %#v, want oversized encoded result suppressed", correlations)
	}
}

func TestSuggestCorrelationsRespectAggregateRuntimeResultLimit(t *testing.T) {
	sourceFields := make([]string, 0, 5)
	actions := []dsl.Action{{Name: "issue", Type: "http_request", Target: "/issue"}}
	observations := []correlate.Observation{{ID: 1}}
	for index := 0; index < 5; index++ {
		value := fmt.Sprintf("token-%d-%s", index, strings.Repeat(string(rune('a'+index)), 14_990))
		sourceFields = append(sourceFields, fmt.Sprintf("%q:%q", fmt.Sprintf("access_token_%d", index), value))
		target := "/use?token=" + value
		actions = append(actions, dsl.Action{
			Name:   fmt.Sprintf("use-%d", index),
			Type:   "http_request",
			Target: target,
		})
		observations = append(observations, correlate.Observation{
			ID:         index + 2,
			RequestURL: "https://example.test" + target,
		})
	}
	observations[0].ResponseBody = "{" + strings.Join(sourceFields, ",") + "}"
	script := httpScript(actions...)

	result := Suggest(script, Options{
		EnableCorrelation: true,
		Observations:      observations,
	})
	correlations := findByKind(result.Suggestions, KindCorrelation)
	if len(correlations) != 4 {
		t.Fatalf("correlation suggestions = %d, want four within aggregate runtime result budget", len(correlations))
	}
	totalBytes := 0
	for _, suggestion := range correlations {
		if suggestion.Extractor == nil {
			t.Fatalf("suggestion missing extractor: %#v", suggestion)
		}
		entryBytes, ok := correlate.CorrelationExtractionEntryBytes(suggestion.Extractor.Name, suggestion.LiteralValue)
		if !ok {
			t.Fatalf("suggestion exceeds per-entry runtime limit: %#v", suggestion)
		}
		totalBytes += entryBytes
	}
	if totalBytes > protocol.MaxExtractionBytes {
		t.Fatalf("suggestions consume %d extraction bytes, max is %d", totalBytes, protocol.MaxExtractionBytes)
	}
}

func TestSuggestCorrelationsReservesRawAndEncodedSiblingsAtomically(t *testing.T) {
	sourceFields := make([]string, 0, 5)
	actions := []dsl.Action{{Name: "issue", Type: "http_request", Target: "/issue"}}
	observations := []correlate.Observation{{ID: 1}}
	for index := 0; index < 4; index++ {
		value := fmt.Sprintf("token-%d-%s", index, strings.Repeat(string(rune('a'+index)), 14_990))
		sourceFields = append(sourceFields, fmt.Sprintf("%q:%q", fmt.Sprintf("access_token_%d", index), value))
		target := "/use?token=" + value
		actions = append(actions, dsl.Action{Name: fmt.Sprintf("use-%d", index), Type: "http_request", Target: target})
		observations = append(observations, correlate.Observation{ID: index + 2, RequestURL: "https://example.test" + target})
	}
	mixedValue := "mixed-" + strings.Repeat("x ", 1_500) + "token"
	sourceFields = append(sourceFields, fmt.Sprintf("%q:%q", "access_token_z", mixedValue))
	mixedEncoded := url.QueryEscape(mixedValue)
	actions = append(actions, dsl.Action{
		Name: "mixed-use", Type: "http_request", Target: "/mixed",
		Body: "raw=" + mixedValue + "&encoded=" + mixedEncoded,
	})
	observations = append(observations, correlate.Observation{
		ID:          len(observations) + 1,
		RequestBody: "raw=" + mixedValue + "&encoded=" + mixedEncoded,
	})
	observations[0].ResponseBody = "{" + strings.Join(sourceFields, ",") + "}"

	result := Suggest(httpScript(actions...), Options{EnableCorrelation: true, Observations: observations})
	correlations := findByKind(result.Suggestions, KindCorrelation)
	if len(correlations) != 4 {
		t.Fatalf("correlation suggestions = %#v, want only the four complete earlier correlations", correlations)
	}
	for _, suggestion := range correlations {
		if suggestion.VarName == "corr_access_token_z" || strings.HasPrefix(suggestion.VarName, "corr_access_token_z_") {
			t.Fatalf("aggregate budgeting emitted a partial raw/encoded sibling: %#v", suggestion)
		}
	}
}

func TestSuggestCorrelationsNeverTargetsTransformedRequestBody(t *testing.T) {
	const value = "encoded-looking-token-123456"
	script := httpScript(
		dsl.Action{Name: "source", Type: "http_request", Target: "/source"},
		dsl.Action{Name: "binary consumer", Type: "http_request", Target: "/use?token=" + value, Body: value, BodyEncoding: "base64"},
	)
	result := Suggest(script, Options{
		EnableCorrelation: true,
		Observations: []correlate.Observation{
			{ID: 1, ResponseBody: `{"access_token":"` + value + `"}`},
			{ID: 2, RequestURL: "https://example.test/use?token=" + value, RequestBody: value, RequestBodyTransformed: true},
		},
	})
	if correlations := findByKind(result.Suggestions, KindCorrelation); len(correlations) != 0 {
		t.Fatalf("transformed request body produced correlation suggestions: %#v", correlations)
	}
}

func TestApplyReplacementsAreNonRecursiveAndProtectTemplates(t *testing.T) {
	script := httpScript(dsl.Action{
		Name: "submit", Type: "http_request",
		Body: "encoded=access%5Ftoken&raw=access_token&existing={{var.access_token}}",
	})
	first := Suggestion{
		ID: "first", Kind: KindSecret,
		Locations: []Location{
			{Section: "actions", ActionIndex: 0, Field: "body", SearchText: "access%5Ftoken", ReplaceText: "{{var.corr_access_token}}"},
			{Section: "actions", ActionIndex: 0, Field: "body", SearchText: "access_token", ReplaceText: "{{var.corr_access_token}}"},
		},
	}
	second := Suggestion{
		ID: "second", Kind: KindSecret,
		Locations: []Location{
			{Section: "actions", ActionIndex: 0, Field: "body", SearchText: "corr_access_token", ReplaceText: "{{env.CORR_ACCESS_TOKEN}}"},
		},
	}

	applied, diffs, err := ApplySuggestions(script, []Suggestion{first, second}, map[string]bool{"first": true, "second": true})
	if err != nil {
		t.Fatalf("ApplySuggestions: %v", err)
	}
	want := "encoded={{var.corr_access_token}}&raw={{var.corr_access_token}}&existing={{var.access_token}}"
	if got := applied.Actions[0].Body; got != want {
		t.Fatalf("body = %q, want non-recursive protected replacements %q", got, want)
	}
	if len(diffs) != 1 {
		t.Fatalf("diffs = %#v, want one grouped field diff", diffs)
	}
}

func TestApplySkipsUnselectedAndPreApplied(t *testing.T) {
	script := httpScript(dsl.Action{Name: "a", Type: "http_request", Target: "/u?user_id=1", Headers: map[string]string{"Authorization": "Bearer x"}})
	opts := allClasses()
	opts.PreApplied = []PreAppliedCorrelation{{Name: "corr_x", Source: "body", Expression: "x", ExtractType: "regex", SampleValue: "v", FoundInStep: 1}}
	result := Suggest(script, opts)

	// Select everything, including the pre-applied entry: it must still be a no-op.
	applied, diffs, err := ApplySuggestions(script, result.Suggestions, map[string]bool{})
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	if len(diffs) != 0 {
		t.Errorf("nothing selected: expected 0 diffs, got %+v", diffs)
	}
	if applied.Actions[0].Target != "/u?user_id=1" {
		t.Errorf("unselected suggestion mutated the script: %q", applied.Actions[0].Target)
	}

	appliedAll, _, err := ApplySuggestions(script, result.Suggestions, selectAll(result.Suggestions))
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	for _, action := range appliedAll.Actions {
		for _, extractor := range action.Extractors {
			if extractor.Name == "corr_x" {
				t.Error("pre-applied suggestion must not re-add its extractor")
			}
		}
	}
}

func TestApplyDoesNotMutateInput(t *testing.T) {
	script := httpScript(dsl.Action{Name: "a", Type: "http_request", Target: "/u?user_id=1", Headers: map[string]string{"Authorization": "Bearer x"}})
	original, err := json.Marshal(script)
	if err != nil {
		t.Fatal(err)
	}
	result := Suggest(script, allClasses())
	if _, _, err := ApplySuggestions(script, result.Suggestions, selectAll(result.Suggestions)); err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	after, err := json.Marshal(script)
	if err != nil {
		t.Fatal(err)
	}
	if string(original) != string(after) {
		t.Errorf("input script mutated by ApplySuggestions:\nbefore: %s\nafter:  %s", original, after)
	}
}

func TestApplyDataSourceNameCollision(t *testing.T) {
	script := httpScript(dsl.Action{Name: "a", Type: "http_request", Target: "/u?user_id=1"})
	script.DataSources = []dsl.DataSource{{Name: consolidatedDataSourceName, Type: "csv", InlineData: "x\n1", Strategy: "sequential", FirstRowHeader: true}}
	result := Suggest(script, allClasses())
	applied, _, err := ApplySuggestions(script, result.Suggestions, selectAll(result.Suggestions))
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	if len(applied.DataSources) != 2 {
		t.Fatalf("expected 2 data sources, got %d", len(applied.DataSources))
	}
	if applied.DataSources[1].Name != consolidatedDataSourceName+"_2" {
		t.Errorf("new source name = %q, want %s_2", applied.DataSources[1].Name, consolidatedDataSourceName)
	}
	if applied.Parameters[0].DataSource != consolidatedDataSourceName+"_2" {
		t.Errorf("parameter binds to %q, want the renamed source", applied.Parameters[0].DataSource)
	}
}

func TestApplyCSVEscaping(t *testing.T) {
	script := httpScript(dsl.Action{Name: "a", Type: "http_request", Body: `{"customer_email": "a,b@example.com"}`})
	result := Suggest(script, allClasses())
	applied, _, err := ApplySuggestions(script, result.Suggestions, selectAll(result.Suggestions))
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	if len(applied.DataSources) != 1 {
		t.Fatalf("expected 1 data source, got %d", len(applied.DataSources))
	}
	if !strings.Contains(applied.DataSources[0].InlineData, `"a,b@example.com"`) {
		t.Errorf("inline data = %q, want csv-quoted seed value", applied.DataSources[0].InlineData)
	}
}

// TestApplyDataParamNumericKeyOverlap reproduces the corruption where the JSON
// numeric value's digits also appear in the key (order1_id / value 1): the
// applied body must replace the value, not a character inside the key.
func TestApplyDataParamNumericKeyOverlap(t *testing.T) {
	script := httpScript(
		dsl.Action{Name: "create", Type: "http_request", Method: "POST", Target: "/orders", Body: `{"order1_id": 1}`},
	)
	result := Suggest(script, allClasses())
	params := findByKind(result.Suggestions, KindDataParam)
	if len(params) != 1 {
		t.Fatalf("expected 1 data_param, got %d (%+v)", len(params), params)
	}
	if params[0].VarName != "order1_id" {
		t.Fatalf("var name = %q, want order1_id", params[0].VarName)
	}

	applied, _, err := ApplySuggestions(script, result.Suggestions, selectAll(result.Suggestions))
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	got := applied.Actions[0].Body
	want := `{"order1_id": {{var.order1_id}}}`
	if got != want {
		t.Fatalf("applied body = %q, want %q (key must not be corrupted)", got, want)
	}
}
