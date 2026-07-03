package paramize

import (
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
