package postman

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func findExtractor(action dsl.Action, name string) (dsl.Extractor, bool) {
	for _, e := range action.Extractors {
		if e.Name == name {
			return e, true
		}
	}
	return dsl.Extractor{}, false
}

func findCorrelation(result *ConvertResult, name string) (Correlation, bool) {
	for _, c := range result.Correlations {
		if c.Name == name {
			return c, true
		}
	}
	return Correlation{}, false
}

func TestDetectPostmanExtractorsUsesFinalEffectiveWrite(t *testing.T) {
	tests := []struct {
		name        string
		script      string
		source      string
		extractType string
		expression  string
	}{
		{
			name: "json then json",
			script: `pm.environment.set("token", pm.response.json().first);
pm.environment.set("token", pm.response.json().second);`,
			source:      "body",
			extractType: "jsonpath",
			expression:  "$.second",
		},
		{
			name: "json then header",
			script: `pm.environment.set("token", pm.response.json().first);
pm.environment.set("token", pm.response.headers.get("X-Final"));`,
			source:      "header",
			extractType: "header",
			expression:  "X-Final",
		},
		{
			name: "header then json",
			script: `pm.environment.set("token", pm.response.headers.get("X-First"));
pm.environment.set("token", pm.response.json().final);`,
			source:      "body",
			extractType: "jsonpath",
			expression:  "$.final",
		},
		{
			name: "higher scope beats later lower scope",
			script: `pm.environment.set("token", pm.response.json().environment);
pm.collectionVariables.set("token", pm.response.headers.get("X-Lower"));`,
			source:      "body",
			extractType: "jsonpath",
			expression:  "$.environment",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			detected := detectPostmanExtractors([]event{{
				Listen: "test",
				Script: eventScript{Exec: []string{tt.script}},
			}})
			if len(detected) != 1 {
				t.Fatalf("detected = %#v, want one final extractor", detected)
			}
			got := detected[0]
			if got.varName != "token" || got.source != tt.source ||
				got.extractType != tt.extractType || got.expression != tt.expression {
				t.Fatalf("extractor = %#v, want source=%q type=%q expression=%q", got, tt.source, tt.extractType, tt.expression)
			}
		})
	}
}

func TestConvertPostmanLocalResponseVariableDoesNotLeakIntoNextRequest(t *testing.T) {
	result, err := Convert(ImportRequest{Content: `{
  "info": {"name": "Local response lifetime"},
  "variable": [
    {"key": "baseUrl", "value": "https://api.example.test"},
    {"key": "token", "value": "collection-fallback"}
  ],
  "item": [
    {
      "name": "Producer",
      "event": [{"listen": "test", "script": {"exec": [
        "pm.variables.set(\"token\", pm.response.json().request_only);"
      ]}}],
      "request": {"method": "GET", "url": "{{baseUrl}}/produce"}
    },
    {
      "name": "Consumer",
      "request": {"method": "GET", "url": "{{baseUrl}}/consume/{{token}}"}
    }
  ]
}`})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Script.Actions) != 2 {
		t.Fatalf("actions = %d, want 2", len(result.Script.Actions))
	}
	if got := result.Script.Actions[0].Extractors; len(got) != 0 {
		t.Fatalf("request-local response write produced persistent extractors: %#v", got)
	}
	if got := result.Script.Actions[1].Target; got != "/consume/{{var.token}}" {
		t.Fatalf("consumer target = %q, want collection fallback binding", got)
	}
	if !postmanParameterNames(result, "token") {
		t.Fatalf("collection fallback parameter missing: %#v", result.Script.Parameters)
	}
}

func TestConvertPostmanChainsTestScriptCaptures(t *testing.T) {
	result, err := Convert(ImportRequest{
		Content: `{
  "info": {"name": "Chained API"},
  "variable": [{"key": "baseUrl", "value": "https://api.example.test"}],
  "item": [
    {
      "name": "Login",
      "event": [
        {"listen": "test", "script": {"exec": [
          "var jsonData = pm.response.json();",
          "pm.environment.set(\"token\", jsonData.access_token);",
          "pm.collectionVariables.set(\"user_id\", pm.response.json().user.id);",
          "pm.environment.set(\"rid\", pm.response.headers.get(\"X-Request-Id\"));"
        ]}}
      ],
      "request": {"method": "POST", "url": "{{baseUrl}}/login", "body": {"mode": "raw", "raw": "{\"u\":\"a\"}"}}
    },
    {
      "name": "Get Profile",
      "request": {
        "method": "GET",
        "header": [
          {"key": "Authorization", "value": "Bearer {{token}}"},
          {"key": "X-Trace", "value": "{{rid}}"}
        ],
        "url": "{{baseUrl}}/users/{{user_id}}/profile"
      }
    }
  ]
}`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if len(result.Script.Actions) != 2 {
		t.Fatalf("actions = %d, want 2", len(result.Script.Actions))
	}
	login := result.Script.Actions[0]
	if blocker := login.Metadata["execution_blocker"]; blocker != nil {
		t.Fatalf("fully translated correlation script was gated: %#v", login.Metadata)
	}

	// Body capture via an aliased pm.response.json() variable.
	if e, ok := findExtractor(login, "token"); !ok || e.Source != "body" || e.Type != "jsonpath" || e.Expression != "$.access_token" {
		t.Fatalf("token extractor = %#v ok=%v", e, ok)
	}
	// Body capture with a nested path directly off pm.response.json().
	if e, ok := findExtractor(login, "user_id"); !ok || e.Source != "body" || e.Expression != "$.user.id" {
		t.Fatalf("user_id extractor = %#v ok=%v", e, ok)
	}
	// Header capture.
	if e, ok := findExtractor(login, "rid"); !ok || e.Source != "header" || e.Type != "header" || e.Expression != "X-Request-Id" {
		t.Fatalf("rid extractor = %#v ok=%v", e, ok)
	}

	// Consumer request references the captured vars.
	profile := result.Script.Actions[1]
	if profile.Headers["Authorization"] != "Bearer {{var.token}}" {
		t.Fatalf("authorization header = %q", profile.Headers["Authorization"])
	}
	if profile.Headers["X-Trace"] != "{{var.rid}}" {
		t.Fatalf("x-trace header = %q", profile.Headers["X-Trace"])
	}
	if !strings.Contains(profile.Target, "{{var.user_id}}") {
		t.Fatalf("profile target = %q, want chained user_id", profile.Target)
	}

	// Correlations: each captured at step 1, consumed at step 2.
	if result.Stats.Correlations != 3 || result.Stats.Extractors != 3 {
		t.Fatalf("stats = %#v, want 3 correlations + 3 extractors", result.Stats)
	}
	for _, name := range []string{"token", "user_id", "rid"} {
		c, ok := findCorrelation(result, name)
		if !ok || c.FoundInStep != 1 || len(c.UsedInSteps) != 1 || c.UsedInSteps[0] != 2 {
			t.Fatalf("correlation %q = %#v ok=%v", name, c, ok)
		}
	}
}

func TestConvertPostmanIgnoresLiteralAndNonResponseSets(t *testing.T) {
	result, err := Convert(ImportRequest{
		Content: `{
  "info": {"name": "No Chain"},
  "item": [
    {
      "name": "Seed",
      "event": [
        {"listen": "test", "script": {"exec": [
          "pm.environment.set(\"flag\", \"on\");",
          "pm.environment.set(\"copy\", pm.variables.get(\"other\"));"
        ]}}
      ],
      "request": {"method": "GET", "url": "https://api.example.test/seed"}
    }
  ]
}`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	// A string literal becomes a parameter (handled elsewhere), and a non-response
	// expression is not a response capture — neither produces an extractor.
	if len(result.Script.Actions[0].Extractors) != 0 {
		t.Fatalf("unexpected extractors: %#v", result.Script.Actions[0].Extractors)
	}
	if result.Stats.Correlations != 0 {
		t.Fatalf("correlations = %d, want 0", result.Stats.Correlations)
	}
}
