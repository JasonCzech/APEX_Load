package raml

import (
	"strings"
	"testing"
)

func TestConvertBindsRAMLBaseURIParameters(t *testing.T) {
	result, err := Convert(ImportRequest{Content: `#%RAML 1.0
title: Versioned API
version: v2
baseUri: https://api.example.test/{version}/{tenant}
baseUriParameters:
  tenant:
    default: public
/items:
  get:
    responses:
      200: {}
`})
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Script.Config.BaseURL; got != "https://api.example.test/v2/public" {
		t.Fatalf("base_url = %q, want version/default substitutions", got)
	}
	if strings.Contains(result.Script.Config.BaseURL, "{") || len(result.Warnings) != 0 {
		t.Fatalf("resolved base URI = %q warnings=%#v", result.Script.Config.BaseURL, result.Warnings)
	}
}

func TestConvertGatesUnboundRAMLBaseURIParameterWithEnvironmentTemplate(t *testing.T) {
	result, err := Convert(ImportRequest{Content: `#%RAML 1.0
title: Tenant API
baseUri: https://api.example.test/{tenant}
/items:
  get: {}
`})
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Script.Config.BaseURL; got != "https://api.example.test/{{env.RAML_BASE_URI_PARAM_TENANT}}" {
		t.Fatalf("base_url = %q", got)
	}
	if !strings.Contains(strings.Join(result.Warnings, "\n"), "has no version/example/default binding") {
		t.Fatalf("warnings = %#v, want explicit binding requirement", result.Warnings)
	}
}

func TestConvertPreservesFallbackWhenRAMLHasNoBaseURI(t *testing.T) {
	result, err := Convert(ImportRequest{Content: `#%RAML 1.0
title: No Base URI
/items:
  get: {}
`})
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Script.Config.BaseURL; got != "{{env.BASE_URL}}" {
		t.Fatalf("base_url = %q, want the built-in environment fallback", got)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want no authored baseUri binding warning", result.Warnings)
	}
}

func TestConvertTreatsRAMLBaseURIParameterNamesAsCaseSensitive(t *testing.T) {
	result, err := Convert(ImportRequest{Content: `#%RAML 1.0
title: Case Sensitive Base URI
version: v1
baseUri: https://api.example.test/{Version}/{version}
baseUriParameters:
  Version:
    default: stable
/items:
  get: {}
`})
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Script.Config.BaseURL; got != "https://api.example.test/stable/v1" {
		t.Fatalf("base_url = %q, want exact declaration and reserved lowercase version bindings", got)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want both authored parameters resolved", result.Warnings)
	}
}
