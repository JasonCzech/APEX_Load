package openapi

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/scrub"
	"gopkg.in/yaml.v3"
)

func TestRedactOpenAPISourceValuesPreservesQuotedMappingKeys(t *testing.T) {
	t.Run("JSON", func(t *testing.T) {
		const source = `{
  "test": "retained root key",
  "example": "test",
  "nested": {"test": "test"}
}`
		redacted := redactOpenAPISourceValues(source, []string{"test"})
		var decoded map[string]any
		if err := json.Unmarshal([]byte(redacted), &decoded); err != nil {
			t.Fatalf("redacted JSON is invalid: %v\n%s", err, redacted)
		}
		if decoded["test"] != "retained root key" {
			t.Fatalf("quoted JSON mapping key was rewritten: %#v", decoded)
		}
		if decoded["example"] != scrub.Redacted {
			t.Fatalf("quoted JSON scalar value = %#v, want %q", decoded["example"], scrub.Redacted)
		}
		nested, ok := decoded["nested"].(map[string]any)
		if !ok || nested["test"] != scrub.Redacted {
			t.Fatalf("nested quoted JSON key/value = %#v", decoded["nested"])
		}
	})

	t.Run("quoted YAML", func(t *testing.T) {
		const source = `double:
  "test": retained double key
  example: "test"
single:
  'test': retained single key
  example: 'test'
`
		redacted := redactOpenAPISourceValues(source, []string{"test"})
		var decoded map[string]map[string]string
		if err := yaml.Unmarshal([]byte(redacted), &decoded); err != nil {
			t.Fatalf("redacted YAML is invalid: %v\n%s", err, redacted)
		}
		for _, section := range []string{"double", "single"} {
			if decoded[section]["test"] != "retained "+section+" key" {
				t.Fatalf("%s-quoted YAML mapping key was rewritten: %#v", section, decoded[section])
			}
			if decoded[section]["example"] != scrub.Redacted {
				t.Fatalf("%s-quoted YAML scalar value = %q, want %q", section, decoded[section]["example"], scrub.Redacted)
			}
		}
		for _, preserved := range []string{`"test": retained double key`, `'test': retained single key`} {
			if !strings.Contains(redacted, preserved) {
				t.Fatalf("redacted YAML lost quoted key %q:\n%s", preserved, redacted)
			}
		}
	})
}

func TestRedactOpenAPISourceValuesDeduplicatesShortValuesAndParsesOnce(t *testing.T) {
	const source = `values:
  first: test
  second: prod
  third: test
  "test": retained key
`
	parseCalls := 0
	redacted := redactOpenAPISourceValuesWithParser(
		source,
		[]string{"test", "prod", "test", "", "prod", "test"},
		func(content []byte, root *yaml.Node) error {
			parseCalls++
			return yaml.Unmarshal(content, root)
		},
	)
	if parseCalls != 1 {
		t.Fatalf("short-value source parses = %d, want 1", parseCalls)
	}
	var decoded struct {
		Values map[string]string `yaml:"values"`
	}
	if err := yaml.Unmarshal([]byte(redacted), &decoded); err != nil {
		t.Fatalf("redacted YAML is invalid: %v\n%s", err, redacted)
	}
	if decoded.Values["test"] != "retained key" {
		t.Fatalf("quoted mapping key was rewritten: %#v", decoded.Values)
	}
	for _, key := range []string{"first", "second", "third"} {
		if decoded.Values[key] != scrub.Redacted {
			t.Fatalf("values[%q] = %q, want %q", key, decoded.Values[key], scrub.Redacted)
		}
	}
}

func TestConvertShortCredentialExampleDoesNotShredSourceReview(t *testing.T) {
	const source = `openapi: 3.0.3
info:
  title: test API
  version: 1.0.0
paths:
  /tests:
    post:
      operationId: create_test_user
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              properties:
                password:
                  type: string
                  example: test
      responses:
        "200":
          description: ok
`
	baseline := scrub.Value(source, scrub.Policy{})
	for _, preserved := range []string{"title: test API", "/tests:", "create_test_user"} {
		if !strings.Contains(baseline, preserved) {
			t.Fatalf("baseline scrub lost %q:\n%s", preserved, baseline)
		}
	}
	targeted := redactOpenAPISourceValues(baseline, []string{"test"})
	for _, preserved := range []string{"title: test API", "/tests:", "create_test_user"} {
		if !strings.Contains(targeted, preserved) {
			t.Fatalf("targeted scrub lost %q:\n%s", preserved, targeted)
		}
	}
	result, err := Convert(ImportRequest{Content: source})
	if err != nil {
		t.Fatal(err)
	}
	if result.SourceReview == nil || len(result.SourceReview.SourceFiles) != 1 {
		t.Fatalf("source review = %#v", result.SourceReview)
	}
	review := result.SourceReview.SourceFiles[0].Content
	for _, preserved := range []string{"title: test API", "/tests:", "create_test_user"} {
		if !strings.Contains(review, preserved) {
			t.Fatalf("source review lost %q:\n%s", preserved, review)
		}
	}
	if strings.Contains(review, "example: test") || review == openAPISourceReviewCredentialOmission {
		t.Fatalf("credential example was not safely redacted:\n%s", review)
	}
}

func TestConvertLongCredentialExampleDoesNotShredSourceReview(t *testing.T) {
	const source = `openapi: 3.0.3
info:
  title: customer API
  version: 1.0.0
paths:
  /customers:
    post:
      operationId: create_customer
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              properties:
                password:
                  type: string
                  example: customer
      responses:
        "200":
          description: customer created
`
	result, err := Convert(ImportRequest{Content: source})
	if err != nil {
		t.Fatal(err)
	}
	if result.SourceReview == nil || len(result.SourceReview.SourceFiles) != 1 {
		t.Fatalf("source review = %#v", result.SourceReview)
	}
	review := result.SourceReview.SourceFiles[0].Content
	for _, preserved := range []string{"title: customer API", "/customers:", "create_customer", "description: customer created"} {
		if !strings.Contains(review, preserved) {
			t.Fatalf("source review lost %q:\n%s", preserved, review)
		}
	}
	if strings.Contains(review, "example: customer") || review == openAPISourceReviewCredentialOmission {
		t.Fatalf("credential example was not structurally redacted:\n%s", review)
	}
}

func TestDisclosureSafeOpenAPISourceReviewOmitsUnreplacedCredentialSubstring(t *testing.T) {
	const source = `openapi: 3.0.3
info:
  title: customer API
  version: 1.0.0
paths:
  /customers:
    post:
      operationId: create_customer
      requestBody:
        content:
          application/json:
            example:
              password: prefix-customer-suffix
      responses:
        "200":
          description: ok
`
	content, omitted := disclosureSafeOpenAPISourceReview(source, []string{"customer"})
	if !omitted || content != openAPISourceReviewCredentialOmission {
		t.Fatalf("credential-bearing substring was not omitted: omitted=%t content=%q", omitted, content)
	}
}

func TestDisclosureSafeOpenAPISourceReviewIgnoresExactRedactionMarker(t *testing.T) {
	const sourceTemplate = `openapi: 3.0.3
info:
  title: short credential API
  version: 1.0.0
paths:
  /login:
    post:
      requestBody:
        content:
          application/json:
            example:
              password: SHORT_CREDENTIAL
      responses:
        "200":
          description: ok
`
	for _, protected := range []string{"A", "RED", "ACT"} {
		t.Run(protected, func(t *testing.T) {
			result, err := Convert(ImportRequest{Content: strings.ReplaceAll(sourceTemplate, "SHORT_CREDENTIAL", protected)})
			if err != nil {
				t.Fatal(err)
			}
			if result.SourceReview == nil || len(result.SourceReview.SourceFiles) != 1 {
				t.Fatalf("source review = %#v", result.SourceReview)
			}
			content := result.SourceReview.SourceFiles[0].Content
			if content == openAPISourceReviewCredentialOmission ||
				!strings.Contains(content, `password: "`+scrub.Redacted+`"`) {
				t.Fatalf("exact redaction marker caused omission for %q: %q", protected, content)
			}
		})
	}
}
