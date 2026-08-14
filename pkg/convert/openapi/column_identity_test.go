package openapi

import (
	"strings"
	"testing"
)

func TestOpenAPIDataColumnsPreserveDistinctSourceNamesAfterSanitization(t *testing.T) {
	result, err := Convert(ImportRequest{Content: `{
  "openapi":"3.0.3",
  "info":{"title":"Column identities","version":"1"},
  "servers":[{"url":"https://api.example.test"}],
  "paths":{"/items":{"get":{
    "parameters":[
      {"name":"foo-bar","in":"query","schema":{"type":"string","example":"dash-value"}},
      {"name":"foo_bar","in":"query","schema":{"type":"string","example":"underscore-value"}}
    ],
    "responses":{"200":{"description":"ok"}}
  }}}
}`})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if len(result.Script.Actions) != 1 {
		t.Fatalf("actions = %#v", result.Script.Actions)
	}
	target := result.Script.Actions[0].Target
	for _, expected := range []string{
		"foo-bar={{data.openapi.foo_bar}}",
		"foo_bar={{data.openapi.foo_bar_2}}",
	} {
		if !strings.Contains(target, expected) {
			t.Fatalf("target = %q, missing independently allocated %q", target, expected)
		}
	}
	if len(result.Summary.DataColumns) != 2 ||
		result.Summary.DataColumns[0] != "foo_bar" ||
		result.Summary.DataColumns[1] != "foo_bar_2" {
		t.Fatalf("data columns = %#v, want both source identities", result.Summary.DataColumns)
	}
	if len(result.Script.DataSources) == 0 ||
		!strings.Contains(result.Script.DataSources[0].InlineData, "foo_bar,foo_bar_2\n") ||
		!strings.Contains(result.Script.DataSources[0].InlineData, "dash-value,underscore-value\n") {
		t.Fatalf("data source did not retain independent samples: %#v", result.Script.DataSources)
	}
}

func TestOpenAPIDataColumnIdentityIsReusedForTheSameSourceName(t *testing.T) {
	result, err := Convert(ImportRequest{Content: `{
  "openapi":"3.0.3",
  "info":{"title":"Shared column","version":"1"},
  "paths":{
    "/first":{"get":{"parameters":[{"name":"tenant-id","in":"query","schema":{"type":"string"}}],"responses":{"200":{"description":"ok"}}}},
    "/second":{"get":{"parameters":[{"name":"tenant-id","in":"query","schema":{"type":"string"}}],"responses":{"200":{"description":"ok"}}}}
  }
}`})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if len(result.Summary.DataColumns) != 1 || result.Summary.DataColumns[0] != "tenant_id" {
		t.Fatalf("same source name was allocated more than once: %#v", result.Summary.DataColumns)
	}
	for _, action := range result.Script.Actions {
		if !strings.Contains(action.Target, "{{data.openapi.tenant_id}}") {
			t.Fatalf("action %q did not reuse tenant column: %q", action.Name, action.Target)
		}
	}
}

func TestOpenAPIUnusedStructuredParameterParentDoesNotReserveColumnName(t *testing.T) {
	result, err := Convert(ImportRequest{Content: `{
  "openapi":"3.0.3",
  "info":{"title":"Structured column identity","version":"1"},
  "servers":[{"url":"https://api.example.test"}],
  "paths":{
    "/a":{"get":{
      "parameters":[{"name":"foo-bar","in":"query","style":"deepObject","explode":true,"schema":{"type":"object","properties":{"id":{"type":"string"}}}}],
      "responses":{"200":{"description":"ok"}}
    }},
    "/b":{"get":{
      "parameters":[{"name":"foo_bar","in":"query","schema":{"type":"string","example":"actual-value"}}],
      "responses":{"200":{"description":"ok"}}
    }}
  }
}`})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if len(result.Script.Actions) != 2 {
		t.Fatalf("actions = %#v", result.Script.Actions)
	}
	if target := result.Script.Actions[1].Target; !strings.Contains(target, "foo_bar={{data.openapi.foo_bar}}") {
		t.Fatalf("unused structured parent reserved the actual column name: %q", target)
	}
	if len(result.Summary.DataColumns) != 2 ||
		result.Summary.DataColumns[0] != "foo_bar" ||
		result.Summary.DataColumns[1] != "foo_bar_id" {
		t.Fatalf("data columns = %#v, want only emitted leaf and simple parameter columns", result.Summary.DataColumns)
	}
	if len(result.Script.DataSources) == 0 || strings.Contains(result.Script.DataSources[0].InlineData, "foo_bar_2") {
		t.Fatalf("unused structured parent produced a phantom allocation: %#v", result.Script.DataSources)
	}
}
