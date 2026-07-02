package paramize

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/dsl"
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
