package salesforce

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// A HAR import can replay requests the browser had in flight together as one
// concurrent group. The native rewrite must not make one of them use a value
// a sibling extracts (the engine merges a group's extractions only after it
// joins), while the step after the group can use it.
func TestNativePreviewKeepsConcurrentGroupRequestsIndependent(t *testing.T) {
	const recordID = "001A000001abcdeQAA"
	script := &dsl.Script{
		Name: "Group", Protocol: "http",
		Config: dsl.ScriptConfig{BaseURL: "https://acme.my.salesforce.com"},
		Actions: []dsl.Action{
			{Name: "group", Type: "http_sequence", Method: "SEQUENCE", Target: "/services/data/v59.0/query", Params: map[string]any{
				"execution_mode": dsl.HTTPSequenceConcurrent,
				"requests": []dsl.Action{
					{Name: "query", Type: "request", Method: "GET", Target: "/services/data/v59.0/query?q=select"},
					{Name: "sibling", Type: "request", Method: "GET", Target: "/services/data/v59.0/sobjects/Account/" + recordID},
				},
			}},
			{Name: "after", Type: "request", Method: "GET", Target: "/services/data/v59.0/sobjects/Contact?account=" + recordID},
		},
	}
	entry := func(url, body string) map[string]any {
		return map[string]any{
			"request":  map[string]any{"method": "GET", "url": url, "headers": []any{}},
			"response": map[string]any{"status": 200, "headers": []any{map[string]any{"name": "Content-Type", "value": "application/json"}}, "content": map[string]any{"mimeType": "application/json", "text": body}},
		}
	}
	har, err := json.Marshal(map[string]any{"log": map[string]any{"entries": []any{
		entry("https://acme.my.salesforce.com/services/data/v59.0/query?q=select", `{"records":[{"attributes":{"type":"Account"},"Id":"`+recordID+`"}]}`),
		entry("https://acme.my.salesforce.com/services/data/v59.0/sobjects/Account/"+recordID, `{"Name":"x"}`),
		entry("https://acme.my.salesforce.com/services/data/v59.0/sobjects/Contact?account="+recordID, `{}`),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	all := false
	review, err := Analyze(script, AnalyzeOptions{BuildNative: true, HARData: har, HighConfidenceOnly: &all})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	native := review.NativePreview.Script
	children := salesforceSequenceChildren(native.Actions[0])
	if len(children) != 2 {
		t.Fatalf("group children = %#v", children)
	}
	if strings.Contains(children[1].Target, "{{") {
		t.Fatalf("sibling target = %q, want the recorded value: a group request cannot use its sibling's extraction", children[1].Target)
	}
	if err := dsl.ValidateHTTPConcurrentChildren(children); err != nil {
		t.Fatalf("group no longer validates: %v", err)
	}
	if !strings.Contains(native.Actions[1].Target, "{{") {
		t.Fatalf("after target = %q, want the step after the group to read the extracted id", native.Actions[1].Target)
	}
	for _, blocker := range review.Blockers {
		if strings.Contains(blocker, "concurrent request group") {
			t.Fatalf("unexpected group blocker: %s", blocker)
		}
	}
}

// Every request keeps its own sequence number for display, while a concurrent
// group's requests share one correlation rank.
func TestFlattenNumbersGroupRequestsAndSharesTheirRank(t *testing.T) {
	script := &dsl.Script{Config: dsl.ScriptConfig{BaseURL: "https://acme.my.salesforce.com"}, Actions: []dsl.Action{
		{Name: "group", Type: "http_sequence", Method: "SEQUENCE", Target: "/a", Params: map[string]any{
			"execution_mode": dsl.HTTPSequenceConcurrent,
			"requests":       []dsl.Action{{Name: "a", Type: "request", Method: "GET", Target: "/a"}, {Name: "b", Type: "request", Method: "GET", Target: "/b"}},
		}},
		{Name: "after", Type: "request", Method: "GET", Target: "/c"},
	}}
	refs, err := flattenRequestActionsChecked(script, false)
	if err != nil {
		t.Fatal(err)
	}
	var sequences, ranks []int
	for _, ref := range refs {
		sequences = append(sequences, ref.Sequence)
		ranks = append(ranks, ref.Rank)
	}
	if len(refs) != 3 || sequences[0] != 1 || sequences[1] != 2 || sequences[2] != 3 || ranks[0] != 1 || ranks[1] != 1 || ranks[2] != 2 {
		t.Fatalf("sequences = %v, ranks = %v, want 1 2 3 and 1 1 2", sequences, ranks)
	}
}
