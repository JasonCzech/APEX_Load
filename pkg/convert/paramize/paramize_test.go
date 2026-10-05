package paramize

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func httpScript(actions ...dsl.Action) *dsl.Script {
	return &dsl.Script{
		Name:     "test",
		Version:  "1.0",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://api.example.com"},
		Actions:  actions,
	}
}

func allClasses() Options {
	return Options{EnableCorrelation: true, EnableDataParams: true, EnableSecrets: true}
}

func findByKind(suggestions []Suggestion, kind Kind) []Suggestion {
	out := make([]Suggestion, 0)
	for _, s := range suggestions {
		if s.Kind == kind {
			out = append(out, s)
		}
	}
	return out
}

func TestSuggestNilScript(t *testing.T) {
	result := Suggest(nil, allClasses())
	if len(result.Suggestions) != 0 {
		t.Fatalf("expected no suggestions for nil script, got %d", len(result.Suggestions))
	}
	if result.Summary.Total != 0 {
		t.Fatalf("expected zero summary total, got %d", result.Summary.Total)
	}
}

func TestSuggestSecretRegexWorkLimitFailsClosedWithWarning(t *testing.T) {
	fields := make([]string, maxSuggestionRegexMatches+1)
	for index := range fields {
		fields[index] = fmt.Sprintf(`"token":"secret-value-%06d"`, index)
	}
	result := Suggest(httpScript(dsl.Action{
		Name: "oversized-secrets",
		Type: "http_request",
		Body: "{" + strings.Join(fields, ",") + "}",
	}), Options{EnableSecrets: true})

	if len(result.Suggestions) != 0 {
		t.Fatalf("secret suggestions = %d, want no partial results", len(result.Suggestions))
	}
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], ErrSuggestionWorkLimit.Error()) {
		t.Fatalf("secret warnings = %#v, want work-limit warning", result.Warnings)
	}
}

func TestSuggestDataParamRegexWorkLimitFailsClosedWithWarning(t *testing.T) {
	pairs := make([]string, maxSuggestionRegexMatches+1)
	for index := range pairs {
		pairs[index] = fmt.Sprintf("user_id_%06d=value-%06d", index, index)
	}
	result := Suggest(httpScript(dsl.Action{
		Name:   "oversized-query",
		Type:   "http_request",
		Target: "/search?" + strings.Join(pairs, "&"),
	}), Options{EnableDataParams: true})

	if len(result.Suggestions) != 0 {
		t.Fatalf("data-parameter suggestions = %d, want no partial results", len(result.Suggestions))
	}
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], ErrSuggestionWorkLimit.Error()) {
		t.Fatalf("data-parameter warnings = %#v, want work-limit warning", result.Warnings)
	}
}

func TestSuggestDataParams(t *testing.T) {
	tests := []struct {
		name        string
		action      dsl.Action
		wantVar     string
		wantLiteral string
		wantField   string
	}{
		{
			name:        "query parameter with id marker",
			action:      dsl.Action{Name: "get user", Type: "http_request", Method: "GET", Target: "/users?user_id=12345&page=2"},
			wantVar:     "user_id",
			wantLiteral: "12345",
			wantField:   "target",
		},
		{
			name:        "json body string field",
			action:      dsl.Action{Name: "create order", Type: "http_request", Method: "POST", Target: "/orders", Body: `{"customer_email": "jane@example.com", "note": "hello"}`},
			wantVar:     "customer_email",
			wantLiteral: "jane@example.com",
			wantField:   "body",
		},
		{
			name:        "json body numeric field",
			action:      dsl.Action{Name: "create order", Type: "http_request", Method: "POST", Target: "/orders", Body: `{"account_id": 99887, "qty": 1}`},
			wantVar:     "account_id",
			wantLiteral: "99887",
			wantField:   "body",
		},
		{
			name:        "form encoded body",
			action:      dsl.Action{Name: "search", Type: "http_request", Method: "POST", Target: "/search", Body: "order_sku=AB-123&limit=10"},
			wantVar:     "order_sku",
			wantLiteral: "AB-123",
			wantField:   "body",
		},
		{
			name:        "browser fill with data-like selector",
			action:      dsl.Action{Name: "fill email", Type: "browser_fill", Selector: "#signup_email", Value: "jane@example.com"},
			wantVar:     "signup_email",
			wantLiteral: "jane@example.com",
			wantField:   "value",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Suggest(httpScript(tt.action), allClasses())
			params := findByKind(result.Suggestions, KindDataParam)
			if len(params) != 1 {
				t.Fatalf("expected 1 data_param suggestion, got %d (%+v)", len(params), params)
			}
			got := params[0]
			if got.VarName != tt.wantVar {
				t.Errorf("var name = %q, want %q", got.VarName, tt.wantVar)
			}
			if got.LiteralValue != tt.wantLiteral {
				t.Errorf("literal = %q, want %q", got.LiteralValue, tt.wantLiteral)
			}
			if got.Replacement != "{{var."+tt.wantVar+"}}" {
				t.Errorf("replacement = %q, want {{var.%s}}", got.Replacement, tt.wantVar)
			}
			if len(got.Locations) != 1 || got.Locations[0].Field != tt.wantField {
				t.Errorf("locations = %+v, want one location on field %q", got.Locations, tt.wantField)
			}
			if got.AutoApply {
				t.Errorf("data_param suggestions must not auto-apply (confidence %v)", got.Confidence)
			}
			if got.Parameter == nil || got.Parameter.Column != tt.wantVar {
				t.Errorf("parameter = %+v, want column %q", got.Parameter, tt.wantVar)
			}
		})
	}
}

func TestSuggestDataParamSkipsTemplatedAndNonMatching(t *testing.T) {
	script := httpScript(
		dsl.Action{Name: "templated", Type: "http_request", Target: "/users?user_id={{var.existing}}"},
		dsl.Action{Name: "no marker", Type: "http_request", Target: "/search?term=hello"},
	)
	result := Suggest(script, allClasses())
	if params := findByKind(result.Suggestions, KindDataParam); len(params) != 0 {
		t.Fatalf("expected no data_param suggestions, got %+v", params)
	}
}

func TestSuggestDataParamsPreserveWholeKeyBoundaries(t *testing.T) {
	script := httpScript(dsl.Action{
		Name:   "query key boundary",
		Type:   "http_request",
		Target: "/users?customer_id=abc&id=123",
		Method: "GET",
		Body:   "customer_id=abc&id=123",
	}, dsl.Action{
		Name:   "form body",
		Type:   "http_request",
		Target: "/submit",
		Method: "POST",
		Body:   "order_id=xyz&user_id=987",
	})
	result := Suggest(script, allClasses())
	params := findByKind(result.Suggestions, KindDataParam)
	if len(params) != 4 {
		t.Fatalf("expected 4 data-param suggestions, got %d (%+v)", len(params), params)
	}
	countByVar := map[string]int{}
	for _, p := range params {
		if p.VarName == "id" || p.VarName == "customer_id" || p.VarName == "order_id" || p.VarName == "user_id" {
			countByVar[p.VarName]++
			continue
		}
		t.Fatalf("unexpected data-param variable %q", p.VarName)
	}
	for _, v := range []string{"id", "customer_id", "order_id", "user_id"} {
		if countByVar[v] != 1 {
			t.Fatalf("expected variable %q exactly once, got %+v", v, countByVar)
		}
	}
}

// The pair pattern anchors on a separator so a key is never harvested out of
// the middle of a longer token. That anchor must still admit `;`-separated
// pairs and bodies that do not start at offset 0.
func TestSuggestDataParamsHandleSemicolonAndOffsetPairs(t *testing.T) {
	script := httpScript(dsl.Action{
		Name:   "semicolon separated query",
		Type:   "http_request",
		Target: "/report?order_id=abc;user_id=987",
		Method: "GET",
	}, dsl.Action{
		Name:   "body with leading whitespace",
		Type:   "http_request",
		Target: "/submit",
		Method: "POST",
		Body:   "  customer_id=xyz",
	})
	result := Suggest(script, allClasses())

	values := map[string]string{}
	for _, p := range findByKind(result.Suggestions, KindDataParam) {
		values[p.VarName] = p.SampleValue
	}
	// Without `;` in the value class, order_id would swallow "abc;user_id=987"
	// and user_id would never be suggested at all.
	if values["order_id"] != "abc" {
		t.Fatalf("order_id = %q, want %q (semicolon must terminate the value)", values["order_id"], "abc")
	}
	if values["user_id"] != "987" {
		t.Fatalf("user_id = %q, want %q (semicolon must start a new pair)", values["user_id"], "987")
	}
	// Without multi-line/whitespace anchoring, an indented body drops its first pair.
	if values["customer_id"] != "xyz" {
		t.Fatalf("customer_id = %q, want %q (offset body pair must still match)", values["customer_id"], "xyz")
	}
}

func TestSuggestDataParamGroupsRepeatedValues(t *testing.T) {
	script := httpScript(
		dsl.Action{Name: "step1", Type: "http_request", Target: "/users?user_id=777"},
		dsl.Action{Name: "step2", Type: "http_request", Target: "/orders?user_id=777"},
	)
	result := Suggest(script, allClasses())
	params := findByKind(result.Suggestions, KindDataParam)
	if len(params) != 1 {
		t.Fatalf("expected repeated key=value grouped into 1 suggestion, got %d", len(params))
	}
	if len(params[0].Locations) != 2 {
		t.Fatalf("expected 2 locations, got %+v", params[0].Locations)
	}
}

func TestSuggestSecrets(t *testing.T) {
	tests := []struct {
		name            string
		action          dsl.Action
		wantVar         string
		wantReplacement string
	}{
		{
			name:            "authorization bearer header keeps scheme",
			action:          dsl.Action{Name: "call", Type: "http_request", Target: "/x", Headers: map[string]string{"Authorization": "Bearer abc123secret"}},
			wantVar:         "AUTHORIZATION",
			wantReplacement: "Bearer {{env.AUTHORIZATION}}",
		},
		{
			name:            "api key header",
			action:          dsl.Action{Name: "call", Type: "http_request", Target: "/x", Headers: map[string]string{"X-Api-Key": "k-99999"}},
			wantVar:         "X_API_KEY",
			wantReplacement: "{{env.X_API_KEY}}",
		},
		{
			name:            "json body password",
			action:          dsl.Action{Name: "login", Type: "http_request", Target: "/login", Body: `{"username": "jane", "password": "hunter2"}`},
			wantVar:         "PASSWORD",
			wantReplacement: "{{env.PASSWORD}}",
		},
		{
			name:            "json escaped string password",
			action:          dsl.Action{Name: "login", Type: "http_request", Target: "/login", Body: `{"password":"abc\"residual-secret"}`},
			wantVar:         "PASSWORD",
			wantReplacement: "{{env.PASSWORD}}",
		},
		{
			name:            "json numeric passcode",
			action:          dsl.Action{Name: "login", Type: "http_request", Target: "/login", Body: `{"passcode":123456}`},
			wantVar:         "PASSCODE",
			wantReplacement: "{{env.PASSCODE}}",
		},
		{
			name:            "browser fill password",
			action:          dsl.Action{Name: "fill pw", Type: "browser_fill", Selector: "#login_password", Value: "hunter2"},
			wantVar:         "LOGIN_PASSWORD",
			wantReplacement: "{{env.LOGIN_PASSWORD}}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Suggest(httpScript(tt.action), allClasses())
			secrets := findByKind(result.Suggestions, KindSecret)
			if len(secrets) != 1 {
				t.Fatalf("expected 1 secret suggestion, got %d (%+v)", len(secrets), secrets)
			}
			got := secrets[0]
			if got.VarName != tt.wantVar {
				t.Errorf("var name = %q, want %q", got.VarName, tt.wantVar)
			}
			if got.Replacement != tt.wantReplacement {
				t.Errorf("replacement = %q, want %q", got.Replacement, tt.wantReplacement)
			}
			if !got.HighRisk {
				t.Error("secret suggestions must be high risk")
			}
			if got.AutoApply {
				t.Error("secret suggestions must never auto-apply")
			}
		})
	}
}

func TestSuggestSecretSkipsRedactedValues(t *testing.T) {
	script := httpScript(dsl.Action{
		Name: "login", Type: "http_request", Target: "/login",
		Headers: map[string]string{"Authorization": "{{secret.authorization}}"},
		Body:    `{"password": "{{secret.redacted}}"}`,
	})
	result := Suggest(script, allClasses())
	if secrets := findByKind(result.Suggestions, KindSecret); len(secrets) != 0 {
		t.Fatalf("expected no secret suggestions for redacted values, got %+v", secrets)
	}
}

func TestSecretHandlingDoesNotTrustPartialTemplates(t *testing.T) {
	const partial = "{{var.recorded}}residual-secret"
	if got := SanitizeBodySecrets(`{"password":"` + partial + `"}`); strings.Contains(got, "residual-secret") || !strings.Contains(got, "{{secret.redacted}}") {
		t.Fatalf("SanitizeBodySecrets partial template = %q, want full-value redaction", got)
	}

	script := httpScript(
		dsl.Action{
			Name:    "partial header",
			Type:    "http_request",
			Headers: map[string]string{"Authorization": "Bearer " + partial},
		},
		dsl.Action{
			Name: "partial body",
			Type: "http_request",
			Body: `{"password":"` + partial + `"}`,
		},
		dsl.Action{
			Name:     "partial browser fill",
			Type:     "browser_fill",
			Selector: "#login_password",
			Value:    partial,
		},
	)
	result := Suggest(script, Options{EnableSecrets: true})
	secrets := findByKind(result.Suggestions, KindSecret)
	if len(secrets) != 3 {
		t.Fatalf("partial-template secret suggestions = %#v, want header/body/fill suggestions", secrets)
	}
}

func TestSuggestSecretSkipsFullyGovernedCredentialSurfaces(t *testing.T) {
	script := httpScript(
		dsl.Action{
			Name: "governed headers",
			Type: "http_request",
			Headers: map[string]string{
				"Authorization": "Bearer {{var.token}}",
				"Cookie":        "session={{secret.session}}; csrf={{var.csrf}}",
				"X-API-Key":     "{{fn.random_string(32)}}",
			},
		},
		dsl.Action{
			Name: "governed body",
			Type: "http_request",
			Body: `{"password":"{{secret.password}}","token":"{{fn.uuid()}}"}`,
		},
		dsl.Action{
			Name:     "governed fill",
			Type:     "browser_fill",
			Selector: "#password",
			Value:    "{{fake.uuid()}}",
		},
	)
	result := Suggest(script, Options{EnableSecrets: true})
	if secrets := findByKind(result.Suggestions, KindSecret); len(secrets) != 0 {
		t.Fatalf("fully governed credentials produced suggestions: %#v", secrets)
	}
	body := `{"token":"{{fn.random(1,4)}}","password":"{{fn.random_string(32)}}"}`
	if sanitized := SanitizeBodySecrets(body); sanitized != body {
		t.Fatalf("supported builtin templates were sanitized: %q", sanitized)
	}
}

func TestGovernedTemplateRejectsUnsupportedOrMalformedBuiltins(t *testing.T) {
	for _, value := range []string{
		"{{fn.random(1,literal-secret)}}",
		"{{fn.random_string(0)}}",
		"{{fake.email(literal-secret)}}",
		"{{fn.unknown()}}",
		"{{fn.uuid()}}residual-secret",
	} {
		if IsGovernedTemplate(value) {
			t.Fatalf("IsGovernedTemplate(%q) = true, want false", value)
		}
	}
}

func TestSuggestSecretScansHeaderListOnlyValues(t *testing.T) {
	const credential = "Bearer header-list-only-secret"
	script := httpScript(dsl.Action{
		Name: "call",
		Type: "http_request",
		HeaderList: dsl.HeaderList{
			{Name: "Accept", Value: "application/json"},
			{Name: "Authorization", Value: credential},
		},
	})

	result := Suggest(script, Options{EnableSecrets: true})
	secrets := findByKind(result.Suggestions, KindSecret)
	if len(secrets) != 1 {
		t.Fatalf("HeaderList-only secret suggestions = %#v, want one", secrets)
	}
	location := secrets[0].Locations[0]
	if location.Field != "headers.Authorization" || location.SearchText != credential {
		t.Fatalf("HeaderList-only location = %#v", location)
	}

	applied, _, err := ApplySuggestions(script, result.Suggestions, map[string]bool{secrets[0].ID: true})
	if err != nil {
		t.Fatalf("ApplySuggestions HeaderList-only secret: %v", err)
	}
	action := applied.Actions[0]
	if got := action.HeaderList[1].Value; got != "Bearer {{env.AUTHORIZATION}}" {
		t.Fatalf("HeaderList authorization = %q, want externalized credential", got)
	}
	if got := action.Headers["Authorization"]; got != "Bearer {{env.AUTHORIZATION}}" {
		t.Fatalf("Headers summary authorization = %q, want synchronized externalized credential", got)
	}
}

func TestSuggestCorrelations(t *testing.T) {
	script := httpScript(
		dsl.Action{Name: "login", Type: "http_request", Method: "POST", Target: "/login"},
		dsl.Action{Name: "profile", Type: "http_request", Method: "GET", Target: "/profile", Headers: map[string]string{"X-Auth": "tok_a1b2c3d4e5f6a1b2c3d4"}},
	)
	observations := []correlate.Observation{
		{
			ID:           1,
			RequestURL:   "https://api.example.com/login",
			ResponseBody: `{"access_token": "tok_a1b2c3d4e5f6a1b2c3d4"}`,
		},
		{
			ID:             2,
			RequestURL:     "https://api.example.com/profile",
			RequestHeaders: map[string]string{"X-Auth": "tok_a1b2c3d4e5f6a1b2c3d4"},
		},
	}
	opts := allClasses()
	opts.Observations = observations
	result := Suggest(script, opts)
	correlations := findByKind(result.Suggestions, KindCorrelation)
	if len(correlations) != 1 {
		t.Fatalf("expected 1 correlation suggestion, got %d (%+v)", len(correlations), correlations)
	}
	got := correlations[0]
	if got.Extractor == nil {
		t.Fatal("correlation suggestion must carry an extractor")
	}
	if got.Extractor.OnMiss != "fail" {
		t.Fatalf("correlation extractor on_miss = %q, want fail so an extraction miss cannot reuse a prior iteration's value", got.Extractor.OnMiss)
	}
	if got.FoundInAction == nil || got.FoundInAction.ActionIndex != 0 {
		t.Errorf("found_in_action = %+v, want source action index 0", got.FoundInAction)
	}
	if len(got.Locations) == 0 {
		t.Fatal("correlation suggestion must locate at least one replacement")
	}
	if got.Locations[0].Field != "headers.X-Auth" {
		t.Errorf("location field = %q, want headers.X-Auth", got.Locations[0].Field)
	}
	// json.auth_token base 0.85 + reuse bonus puts this at/above the auto-apply line.
	if got.Confidence < autoApplyConfidence {
		t.Errorf("confidence = %v, expected >= %v for an auth token reuse", got.Confidence, autoApplyConfidence)
	}
	if !got.AutoApply {
		t.Error("high-confidence correlation should be auto-apply eligible")
	}
}

func TestSuggestSurfacesCorrelationWorkLimit(t *testing.T) {
	observations := make([]correlate.Observation, 4097)
	for index := range observations {
		observations[index].ID = index + 1
	}
	result := Suggest(httpScript(dsl.Action{Name: "one"}), Options{
		EnableCorrelation: true,
		Observations:      observations,
	})
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "Correlation analysis was not completed") {
		t.Fatalf("warnings = %#v, want explicit correlation work-limit warning", result.Warnings)
	}
	if correlations := findByKind(result.Suggestions, KindCorrelation); len(correlations) != 0 {
		t.Fatalf("correlations = %#v, want no partial suggestions", correlations)
	}
}

func TestSuggestRejectsTruncatedCorrelationCandidatePrefix(t *testing.T) {
	candidateLimit := correlate.MaxCandidatesPerObservation
	var responseBody strings.Builder
	responseBody.WriteByte('{')
	for index := 0; index <= candidateLimit; index++ {
		if index > 0 {
			responseBody.WriteByte(',')
		}
		fmt.Fprintf(&responseBody, `"token_%d":"token-value-%06d"`, index, index)
	}
	responseBody.WriteByte('}')

	result := Suggest(httpScript(
		dsl.Action{Name: "source", Type: "request", Method: "GET", Target: "/source"},
		dsl.Action{Name: "consumer", Type: "request", Method: "GET", Target: fmt.Sprintf("/use?token=token-value-%06d", candidateLimit)},
	), Options{
		EnableCorrelation: true,
		Observations: []correlate.Observation{
			{ID: 1, ResponseBody: responseBody.String()},
			{ID: 2, RequestURL: fmt.Sprintf("https://api.example.com/use?token=token-value-%06d", candidateLimit)},
		},
	})
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "Correlation analysis was not completed") {
		t.Fatalf("warnings = %#v, want fail-closed candidate truncation warning", result.Warnings)
	}
	if correlations := findByKind(result.Suggestions, KindCorrelation); len(correlations) != 0 {
		t.Fatalf("correlations = %#v, want no suggestions from a truncated prefix", correlations)
	}
}

func TestSuggestSurfacesExactEncodedSourceValidationWorkLimit(t *testing.T) {
	const candidateCount = 80
	var location strings.Builder
	var consumer strings.Builder
	location.WriteString("https://example.test/callback?")
	for index := 0; index < candidateCount; index++ {
		if index > 0 {
			location.WriteByte('&')
			consumer.WriteByte('&')
		}
		wireValue := fmt.Sprintf("space+token%%2F%06d", index)
		fmt.Fprintf(&location, "state_%d=%s", index, wireValue)
		fmt.Fprintf(&consumer, "value_%d=%s", index, wireValue)
	}
	// Each Location key has a distinct replay expression. Exact-source
	// validation must scan the flattened response headers once per candidate;
	// the padding keeps detection within its evidence/search budgets while the
	// later 64 MiB exact validator is exhausted.
	responseHeaders := map[string]string{
		"Location":  location.String(),
		"X-Padding": strings.Repeat("x", 1<<20),
	}

	script := httpScript(
		dsl.Action{Name: "issue", Type: "http_request", Target: "/issue"},
		dsl.Action{Name: "use", Type: "http_request", Target: "/use", Body: consumer.String()},
	)
	result := Suggest(script, Options{
		EnableCorrelation: true,
		Observations: []correlate.Observation{
			{ID: 1, ResponseHeaders: responseHeaders},
			{ID: 2, RequestBody: consumer.String()},
		},
	})
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "exact encoded-source validation exhausted its budget") {
		t.Fatalf("warnings = %#v suggestions=%d, want exact encoded-source work-limit warning", result.Warnings, len(result.Suggestions))
	}
	if correlations := findByKind(result.Suggestions, KindCorrelation); len(correlations) != 0 {
		t.Fatalf("correlations = %d, want no partial suggestions", len(correlations))
	}
}

func TestSuggestCorrelationsWithZeroBasedObservationIDs(t *testing.T) {
	script := httpScript(
		dsl.Action{Name: "login", Type: "http_request", Method: "POST", Target: "/login"},
		dsl.Action{Name: "profile", Type: "http_request", Method: "GET", Target: "/profile?token=tok_a1b2c3d4e5f6a1b2c3d4"},
	)
	observations := []correlate.Observation{
		{
			ID:           0,
			RequestURL:   "https://api.example.com/login",
			ResponseBody: `{"access_token": "tok_a1b2c3d4e5f6a1b2c3d4"}`,
		},
		{
			ID:         1,
			RequestURL: "https://api.example.com/profile?token=tok_a1b2c3d4e5f6a1b2c3d4",
		},
	}
	opts := allClasses()
	opts.Observations = observations
	opts.ObservationIDsZeroBased = true

	result := Suggest(script, opts)
	correlations := findByKind(result.Suggestions, KindCorrelation)
	if len(correlations) != 1 {
		t.Fatalf("expected 1 zero-based correlation suggestion, got %d (%+v)", len(correlations), correlations)
	}
	got := correlations[0]
	if got.FoundInAction == nil || got.FoundInAction.ActionIndex != 0 {
		t.Fatalf("found_in_action = %+v, want action index 0", got.FoundInAction)
	}
	if len(got.Locations) != 1 || got.Locations[0].ActionIndex != 1 {
		t.Fatalf("locations = %+v, want replacement on action index 1", got.Locations)
	}
}

func TestSuggestAutoApplyBoundary(t *testing.T) {
	boundaryCases := []struct {
		confidence float64
		highRisk   bool
		preApplied bool
		want       bool
	}{
		{confidence: 0.8, highRisk: false, preApplied: false, want: true},
		{confidence: 0.79, highRisk: false, preApplied: false, want: false},
		{confidence: 0.95, highRisk: true, preApplied: false, want: false},
		{confidence: 0.95, highRisk: false, preApplied: true, want: false},
	}
	for _, tt := range boundaryCases {
		suggestions := []Suggestion{{Kind: KindCorrelation, VarName: "x", Confidence: tt.confidence, HighRisk: tt.highRisk, PreApplied: tt.preApplied}}
		sortSuggestions(suggestions)
		for i := range suggestions {
			suggestions[i].AutoApply = suggestions[i].Confidence >= autoApplyConfidence && !suggestions[i].HighRisk && !suggestions[i].PreApplied
		}
		if suggestions[0].AutoApply != tt.want {
			t.Errorf("confidence=%v highRisk=%v preApplied=%v: auto_apply = %v, want %v",
				tt.confidence, tt.highRisk, tt.preApplied, suggestions[0].AutoApply, tt.want)
		}
	}
}

func TestSuggestPreApplied(t *testing.T) {
	opts := allClasses()
	opts.PreApplied = []PreAppliedCorrelation{{
		Name:        "corr_csrf_token",
		Source:      "body",
		Expression:  `"csrf_token"\s*:\s*"([^"]+)"`,
		ExtractType: "regex",
		SampleValue: "tok123",
		FoundInStep: 1,
		UsedInSteps: []int{2, 3},
	}}
	result := Suggest(httpScript(dsl.Action{Name: "a", Type: "http_request", Target: "/x"}), opts)
	correlations := findByKind(result.Suggestions, KindCorrelation)
	if len(correlations) != 1 {
		t.Fatalf("expected 1 pre-applied correlation, got %d", len(correlations))
	}
	if !correlations[0].PreApplied {
		t.Error("expected pre_applied=true")
	}
	if correlations[0].AutoApply {
		t.Error("pre-applied suggestions must not be auto-apply")
	}
	if result.Summary.PreAppliedCount != 1 {
		t.Errorf("summary pre_applied_count = %d, want 1", result.Summary.PreAppliedCount)
	}
}

func TestSuggestSecretsClaimValuesBeforeDataParams(t *testing.T) {
	// api_key matches both the secret rule (named api_key) and a data-param
	// marker would not match; use a value claimed by secrets that also appears
	// as a query parameter with a data-like name.
	script := httpScript(dsl.Action{
		Name:   "call",
		Type:   "http_request",
		Target: "/lookup?session_user_id=sek-abc-123",
		Body:   `{"api_key": "sek-abc-123"}`,
	})
	result := Suggest(script, allClasses())
	secrets := findByKind(result.Suggestions, KindSecret)
	params := findByKind(result.Suggestions, KindDataParam)
	if len(secrets) != 1 {
		t.Fatalf("expected the secret pass to claim the value, got %d secrets", len(secrets))
	}
	if len(params) != 0 {
		t.Fatalf("expected no data_param for a secret-claimed value, got %+v", params)
	}
}

func TestSuggestDeterministicOrderingAndIDs(t *testing.T) {
	script := httpScript(
		dsl.Action{Name: "a", Type: "http_request", Target: "/u?user_id=1", Headers: map[string]string{"Authorization": "Bearer zz"}},
		dsl.Action{Name: "b", Type: "http_request", Target: "/o?order_id=2"},
	)
	first := Suggest(script, allClasses())
	second := Suggest(script, allClasses())
	if len(first.Suggestions) != len(second.Suggestions) {
		t.Fatalf("non-deterministic suggestion count: %d vs %d", len(first.Suggestions), len(second.Suggestions))
	}
	for i := range first.Suggestions {
		if first.Suggestions[i].ID != second.Suggestions[i].ID {
			t.Errorf("suggestion %d ID differs across runs: %q vs %q", i, first.Suggestions[i].ID, second.Suggestions[i].ID)
		}
		if !strings.HasPrefix(first.Suggestions[i].ID, "pz_") {
			t.Errorf("suggestion ID %q missing pz_ prefix", first.Suggestions[i].ID)
		}
	}
	// Kind ordering: all data_param before secret.
	lastKindRank := -1
	for _, s := range first.Suggestions {
		rank := kindOrder[s.Kind]
		if rank < lastKindRank {
			t.Errorf("suggestions out of kind order: %v after rank %d", s.Kind, lastKindRank)
		}
		lastKindRank = rank
	}
}

func TestDuplicateSensitiveHeaderSuggestionsHaveDistinctOpaqueIDs(t *testing.T) {
	script := httpScript(dsl.Action{
		Name: "authenticate",
		Type: "http_request",
		HeaderList: dsl.HeaderList{
			{Name: "Authorization", Value: "Bearer first-private-token"},
			{Name: "Authorization", Value: "Bearer second-private-token"},
		},
	})

	result := Suggest(script, Options{EnableSecrets: true})
	if len(result.Suggestions) != 2 {
		t.Fatalf("suggestions = %#v, want one per distinct credential", result.Suggestions)
	}
	first, second := result.Suggestions[0], result.Suggestions[1]
	if first.ID == second.ID {
		t.Fatalf("distinct credentials shared selectable suggestion ID %q", first.ID)
	}
	for _, suggestion := range result.Suggestions {
		if strings.Contains(suggestion.ID, "private-token") {
			t.Fatalf("suggestion ID leaked credential material: %q", suggestion.ID)
		}
	}

	applied, _, err := ApplySuggestions(script, result.Suggestions, map[string]bool{first.ID: true})
	if err != nil {
		t.Fatalf("apply one duplicate-header suggestion: %v", err)
	}
	templated := 0
	literal := 0
	for _, header := range applied.Actions[0].HeaderList {
		switch {
		case strings.Contains(header.Value, "{{env.AUTHORIZATION}}"):
			templated++
		case strings.Contains(header.Value, "private-token"):
			literal++
		}
	}
	if templated != 1 || literal != 1 {
		t.Fatalf("selecting one suggestion rewrote the wrong duplicate-header scope: %#v", applied.Actions[0].HeaderList)
	}
}

func TestSuggestionIDDoesNotFingerprintHiddenValues(t *testing.T) {
	first := Suggest(httpScript(dsl.Action{
		Name:    "login",
		Type:    "http_request",
		Target:  "/login",
		Headers: map[string]string{"X-Api-Key": "first-hidden-value"},
	}), allClasses())
	second := Suggest(httpScript(dsl.Action{
		Name:    "login",
		Type:    "http_request",
		Target:  "/login",
		Headers: map[string]string{"X-Api-Key": "second-hidden-value"},
	}), allClasses())

	firstSecrets := findByKind(first.Suggestions, KindSecret)
	secondSecrets := findByKind(second.Suggestions, KindSecret)
	if len(firstSecrets) != 1 || len(secondSecrets) != 1 {
		t.Fatalf("expected one secret suggestion in each result, got %d and %d", len(firstSecrets), len(secondSecrets))
	}
	if firstSecrets[0].LiteralValue == secondSecrets[0].LiteralValue {
		t.Fatal("test setup must use distinct hidden values")
	}
	if firstSecrets[0].ID != secondSecrets[0].ID {
		t.Fatalf("public IDs fingerprint hidden values: %q != %q", firstSecrets[0].ID, secondSecrets[0].ID)
	}

	preApplied := func(sample string) string {
		opts := Options{PreApplied: []PreAppliedCorrelation{{
			Name:        "csrf",
			Source:      "body",
			Expression:  `"csrf":"([^"]+)"`,
			ExtractType: "regex",
			SampleValue: sample,
			FoundInStep: 1,
			UsedInSteps: []int{2},
		}}}
		result := Suggest(httpScript(dsl.Action{Name: "request", Type: "http_request", Target: "/"}), opts)
		if len(result.Suggestions) != 1 {
			t.Fatalf("expected one pre-applied suggestion, got %d", len(result.Suggestions))
		}
		return result.Suggestions[0].ID
	}
	if firstID, secondID := preApplied("first-sample"), preApplied("second-sample"); firstID != secondID {
		t.Fatalf("pre-applied public IDs fingerprint hidden samples: %q != %q", firstID, secondID)
	}
}

func TestNameRegistryAvoidsCollisions(t *testing.T) {
	script := httpScript(dsl.Action{Name: "a", Type: "http_request", Target: "/u?user_id=1"})
	script.Parameters = []dsl.Parameter{{Name: "user_id", Type: "list", Values: []string{"x"}}}
	result := Suggest(script, allClasses())
	params := findByKind(result.Suggestions, KindDataParam)
	if len(params) != 1 {
		t.Fatalf("expected 1 suggestion, got %d", len(params))
	}
	if params[0].VarName != "user_id_2" {
		t.Errorf("var name = %q, want user_id_2 (collision with existing parameter)", params[0].VarName)
	}
}

func TestSummaryCounts(t *testing.T) {
	script := httpScript(
		dsl.Action{Name: "login", Type: "http_request", Target: "/login", Body: `{"password": "pw1", "user_email": "a@b.com"}`},
	)
	result := Suggest(script, allClasses())
	if result.Summary.Total != len(result.Suggestions) {
		t.Errorf("summary total = %d, want %d", result.Summary.Total, len(result.Suggestions))
	}
	if result.Summary.HighRiskCount != 1 {
		t.Errorf("high risk count = %d, want 1 (password)", result.Summary.HighRiskCount)
	}
	if result.Summary.ByKind[KindSecret] != 1 || result.Summary.ByKind[KindDataParam] != 1 {
		t.Errorf("by_kind = %+v, want 1 secret + 1 data_param", result.Summary.ByKind)
	}
}
