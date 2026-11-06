package credentialpolicy

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

// A JSON string made of supported runtime references and literal text, such
// as the timestamp a JMeter import composes from two __time calls, is text,
// not an encoded JSON layer: the body around it is disclosed unchanged.
func TestCompositeRuntimeTemplatesAreNotNestedJSON(t *testing.T) {
	for _, template := range []string{
		"{{param.jmeter_time_1}}T{{param.jmeter_time_2}}Z",
		"{{var.date}} {{var.time}}",
		"{{fn.uuid()}}-{{run.id}}",
		"{{data.rows.id}}:{{env.REGION}}/{{param.seq}}",
		"  {{param.a}}{{param.b}}  ",
	} {
		body := `{"eventSentTime":` + mustJSONString(t, template) + `,"ordinary":"business"}`
		for _, input := range []string{body, "message=" + url.QueryEscape(body)} {
			output, result, err := ProjectRequestBody([]byte(input), "", Options{Mode: Redact})
			if err != nil || result.Disposition == Rejected || string(output) != input {
				t.Fatalf("composite template %q masked its body: %v / %s / %+v", template, err, result.Disposition, result.Findings)
			}
		}
		if nestedJSONObjectOrArray(template) {
			t.Fatalf("composite template %q classified as nested JSON", template)
		}
	}
}

// The exemption is narrow: an unsupported reference, braces or brackets in
// the literal text, or a credential member after a reference keep the value a
// nested JSON layer, which fails closed; and a known credential in the
// literal text is still masked as a scalar.
func TestCompositeRuntimeTemplateExemptionKeepsCredentialDetection(t *testing.T) {
	for _, inner := range []string{
		`{{param.a}}{"apiToken":"` + projectionCanary + `"}`,
		`{{param.a}}[{"apiToken":"` + projectionCanary + `"}]`,
		`{{param.a}}T{{bad object}}Z`,
		`{{param.a}}T{{"apiToken":"` + projectionCanary + `"}}`,
		`{{param.a}}}{"apiToken":"` + projectionCanary + `"`,
		`{{fn.unknown("apiToken":"` + projectionCanary + `")}}T{{param.b}}`,
	} {
		if compositeRuntimeTemplate(inner) {
			t.Fatalf("%q was exempted from nested-JSON detection", inner)
		}
		body := redactionJSONWrapper(t, inner)
		output, result, err := ProjectRequestBody([]byte(body), "application/json", Options{Mode: Redact})
		if err != nil {
			t.Fatal(err)
		}
		if result.Disposition != Rejected || output != nil {
			t.Fatalf("%q was disclosed instead of failing closed: %s", inner, result.Disposition)
		}
	}

	inner := "{{param.a}}T" + projectionCanary
	body := redactionJSONWrapper(t, inner)
	output, result, err := ProjectRequestBody([]byte(body), "application/json", Options{
		Mode:                   Redact,
		KnownCredentialMatcher: func(value string) bool { return strings.Contains(value, projectionCanary) },
	})
	if err != nil {
		t.Fatal(err)
	}
	assertDisclosureSafe(t, output, result)

	nested := `{"apiToken":"` + projectionCanary + `","when":"{{param.a}}T{{param.b}}Z"}`
	output, result, err = ProjectRequestBody([]byte(redactionJSONWrapper(t, nested)), "application/json", Options{Mode: Redact})
	if err != nil || result.Disposition == Rejected {
		t.Fatalf("real nested JSON holding a composite template failed: %v / %s", err, result.Disposition)
	}
	assertDisclosureSafe(t, output, result)
	if !strings.Contains(string(output), `{{param.a}}T{{param.b}}Z`) {
		t.Fatalf("composite template inside real nested JSON was not kept: %s", output)
	}

	if compositeRuntimeTemplate("{{param.a}}" + strings.Repeat("T", maxCompositeRuntimeTemplateBytes)) {
		t.Fatal("oversized composite template was exempted")
	}
}

func mustJSONString(t *testing.T, value string) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
