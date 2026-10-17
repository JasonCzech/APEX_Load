package engine

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/paramize"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestGeneratedImportPreservesRepeatedRequestIDs(t *testing.T) {
	script := &dsl.Script{Name: "generated IDs", Version: "1.0", Protocol: "http"}
	for i, id := range []string{"0a1b2c3d-1111-4aaa-8bbb-000000000001", "0a1b2c3d-2222-4aaa-8bbb-000000000002"} {
		script.Actions = append(script.Actions, dsl.Action{Name: fmt.Sprint(i), Type: "http_request", Method: "POST", Target: "https://example.test", Body: fmt.Sprintf(`{"requestId":%q,"parentRequestId":%q}`, id, id)})
	}
	result := paramize.Suggest(script, paramize.Options{EnableGenerated: true})
	selected := map[string]bool{}
	for _, suggestion := range result.Suggestions {
		selected[suggestion.ID] = true
	}
	applied, _, err := paramize.ApplySuggestions(script, result.Suggestions, selected)
	if err != nil {
		t.Fatal(err)
	}
	if len(applied.Parameters) != 2 {
		t.Fatalf("want separate parameters for the repeated values: %+v", applied.Parameters)
	}
	vu := &VUser{Script: applied, Variables: map[string]string{}}
	vu.Parameters = NewParameterRuntime(applied.Parameters, nil, 0)
	vu.Parameters.Prime()
	var previous string
	for _, action := range applied.Actions {
		resolved := vu.resolveAction(action)
		var body map[string]string
		if err := json.Unmarshal(resolved.Body, &body); err != nil {
			t.Fatal(err)
		}
		if body["requestId"] == "" || body["requestId"] != body["parentRequestId"] || body["requestId"] == previous {
			t.Fatalf("generated ID equality changed: body=%s previous=%s", resolved.Body, previous)
		}
		previous = body["requestId"]
	}
}

func TestGeneratedImportPreservesRepeatedOrderedHeaders(t *testing.T) {
	for _, suffix := range []string{"", "prefix"} {
		t.Run("second header prefix="+suffix, func(t *testing.T) {
			script := &dsl.Script{Name: "generated header IDs", Version: "1.0", Protocol: "http"}
			for i, id := range []string{"0a1b2c3d-1111-4aaa-8bbb-000000000001", "0a1b2c3d-2222-4aaa-8bbb-000000000002"} {
				script.Actions = append(script.Actions, dsl.Action{Name: fmt.Sprint(i), Type: "http_request", Method: "GET", Target: "https://example.test", Headers: map[string]string{"X-Request-Id": id}, HeaderList: dsl.HeaderList{{Name: "X-Request-Id", Value: id}, {Name: "X-Request-Id", Value: suffix + id}}})
			}
			result := paramize.Suggest(script, paramize.Options{EnableGenerated: true})
			selected := map[string]bool{}
			for _, suggestion := range result.Suggestions {
				selected[suggestion.ID] = true
			}
			applied, _, err := paramize.ApplySuggestions(script, result.Suggestions, selected)
			if err != nil {
				t.Fatal(err)
			}
			vu := &VUser{Script: applied, Variables: map[string]string{}}
			vu.Parameters = NewParameterRuntime(applied.Parameters, nil, 0)
			vu.Parameters.Prime()
			var previous string
			for _, action := range applied.Actions {
				resolved := vu.resolveAction(action)
				id := resolved.HeaderList[0].Value
				if id == "" || id == previous || id != strings.TrimPrefix(resolved.HeaderList[1].Value, suffix) || resolved.HeaderList[1].Value != resolved.Headers["X-Request-Id"] {
					t.Fatalf("header ID equality changed: headers=%+v map=%+v previous=%s", resolved.HeaderList, resolved.Headers, previous)
				}
				previous = id
			}
		})
	}
}
