package openapi

import (
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/engine"
)

func TestConvertPathCredentialLocatorsRemainExactWithComposedParameters(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		parameters string
		password   string
		wantCount  int
	}{
		{
			name: "target after ordinary parameter",
			path: "/accounts/prefix-{tenant}-{password}-suffix",
			parameters: `[
			  {"name":"tenant","in":"path","required":true,"schema":{"type":"string"}},
			  {"name":"password","in":"path","required":true,"schema":{"type":"string","format":"password"},"example":"path-password-7319"}
			]`,
			password:  "path-password-7319",
			wantCount: 1,
		},
		{
			name: "target before ordinary parameter",
			path: "/accounts/prefix-{password}-{tenant}-suffix",
			parameters: `[
			  {"name":"tenant","in":"path","required":true,"schema":{"type":"string"}},
			  {"name":"password","in":"path","required":true,"schema":{"type":"string","format":"password"},"example":"path-password-7319"}
			]`,
			password:  "path-password-7319",
			wantCount: 1,
		},
		{
			name: "repeated target",
			path: "/accounts/prefix-{password}-middle-{password}-suffix",
			parameters: `[
			  {"name":"password","in":"path","required":true,"schema":{"type":"string","format":"password"},"example":"path-password-7319"}
			]`,
			password:  "path-password-7319",
			wantCount: 2,
		},
		{
			name: "encoded target",
			path: "/accounts/prefix-{tenant}-{password}-suffix",
			parameters: `[
			  {"name":"tenant","in":"path","required":true,"schema":{"type":"string"}},
			  {"name":"password","in":"path","required":true,"schema":{"type":"string","format":"password"},"example":"Q7m/L4v"}
			]`,
			password:  "Q7m/L4v",
			wantCount: 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := Convert(ImportRequest{Content: fmt.Sprintf(`{
			  "openapi":"3.0.3",
			  "info":{"title":"Path credentials","version":"1"},
			  "servers":[{"url":"https://api.example.test"}],
			  "paths":{%q:{"get":{
			    "operationId":"getAccount",
			    "parameters":%s,
			    "responses":{"204":{"description":"ok"}}
			  }}}
			}`, test.path, test.parameters)})
			if err != nil {
				t.Fatalf("Convert: %v", err)
			}
			if result.Script == nil || len(result.Script.Actions) != 1 {
				t.Fatalf("script = %#v, want one action", result.Script)
			}
			action := result.Script.Actions[0]
			rawPointer := ""
			if targets, ok := action.Metadata["import_credential_targets"].([]any); ok && len(targets) > 0 {
				if target, ok := targets[0].(map[string]any); ok {
					rawPointer, _ = target["pointer"].(string)
				}
			}
			if rawPointer == "" {
				// In-memory conversion normally keeps the typed target slice;
				// use a string search so this assertion remains representation
				// independent.
				rawPointer = fmt.Sprint(
					action.Metadata["import_credential_targets"],
				)
			}
			if !strings.Contains(rawPointer, openAPIPathCredentialPointerPrefix) {
				t.Fatalf("path credential locator = %q, want bounded span descriptor", rawPointer)
			}

			values, err := engine.ImportedCredentialValues(result.Script)
			if err != nil {
				t.Fatalf("ImportedCredentialValues: %v", err)
			}
			rawPassword := url.PathEscape(test.password)
			for _, want := range []string{rawPassword, test.password} {
				count := 0
				for _, value := range values {
					if value == want {
						count++
					}
				}
				if count == 0 {
					t.Fatalf("retained values = %#v, want %q", values, want)
				}
			}
			if strings.Count(action.Target, rawPassword) != test.wantCount {
				t.Fatalf(
					"action target = %q, want %d exact credential occurrence(s)",
					action.Target,
					test.wantCount,
				)
			}
		})
	}
}
