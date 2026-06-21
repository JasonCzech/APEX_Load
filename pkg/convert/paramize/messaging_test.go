package paramize

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func kafkaScript(actions ...dsl.Action) *dsl.Script {
	return &dsl.Script{
		Name:     "test",
		Version:  "1.0",
		Protocol: "kafka",
		Actions:  actions,
	}
}

func findDataParam(suggestions []Suggestion, varNameOrField string) *Suggestion {
	for i := range suggestions {
		if suggestions[i].Kind != KindDataParam {
			continue
		}
		if suggestions[i].VarName == varNameOrField {
			return &suggestions[i]
		}
		for _, loc := range suggestions[i].Locations {
			if loc.Field == varNameOrField {
				return &suggestions[i]
			}
		}
	}
	return nil
}

func TestMessagingParamsSurfaced(t *testing.T) {
	script := kafkaScript(dsl.Action{
		Name:   "publish-order",
		Type:   "kafka_produce",
		Target: "orders",
		Params: map[string]any{
			"correlation_id":   "abc-123",
			"key":              "cust-9",
			"destination_type": "topic", // structural — must NOT be suggested
			"timeout_ms":       1000,    // non-string — must be ignored
		},
	})
	result := Suggest(script, Options{EnableDataParams: true})

	corr := findDataParam(result.Suggestions, "params.correlation_id")
	if corr == nil {
		t.Fatal("expected a data-param suggestion for params.correlation_id")
	}
	if corr.LiteralValue != "abc-123" {
		t.Fatalf("correlation_id literal = %q, want abc-123", corr.LiteralValue)
	}
	if key := findDataParam(result.Suggestions, "params.key"); key == nil {
		t.Fatal("expected a data-param suggestion for params.key")
	}
	// destination_type / timeout_ms must never be surfaced.
	for _, s := range result.Suggestions {
		for _, loc := range s.Locations {
			if loc.Field == "params.destination_type" || loc.Field == "params.timeout_ms" {
				t.Fatalf("structural param %q must not be parameterized", loc.Field)
			}
		}
	}
}

func TestMessagingParamsSkipsTemplatedAndEmpty(t *testing.T) {
	script := kafkaScript(dsl.Action{
		Name: "publish",
		Type: "kafka_produce",
		Params: map[string]any{
			"correlation_id": "{{fn.uuid()}}", // already templated — skip
			"key":            "",              // empty — skip
		},
	})
	result := Suggest(script, Options{EnableDataParams: true})
	if findDataParam(result.Suggestions, "params.correlation_id") != nil {
		t.Fatal("templated correlation_id must not be re-parameterized")
	}
	if findDataParam(result.Suggestions, "params.key") != nil {
		t.Fatal("empty key must not be parameterized")
	}
}

func TestNonMessagingParamsIgnored(t *testing.T) {
	// An HTTP action with a Params map must not trigger the messaging scan.
	script := httpScript(dsl.Action{
		Name:   "get",
		Type:   "http_request",
		Method: "GET",
		Target: "/x",
		Params: map[string]any{"correlation_id": "abc-123"},
	})
	result := Suggest(script, Options{EnableDataParams: true})
	if findDataParam(result.Suggestions, "params.correlation_id") != nil {
		t.Fatal("http action params must not be scanned by the messaging pass")
	}
}

func TestMessagingParamsApplyRoundTrip(t *testing.T) {
	script := kafkaScript(dsl.Action{
		Name:   "publish-order",
		Type:   "kafka_produce",
		Target: "orders",
		Params: map[string]any{"correlation_id": "abc-123"},
	})
	result := Suggest(script, Options{EnableDataParams: true})
	corr := findDataParam(result.Suggestions, "params.correlation_id")
	if corr == nil {
		t.Fatal("expected correlation_id data-param suggestion")
	}

	applied, diffs, err := ApplySuggestions(script, result.Suggestions, map[string]bool{corr.ID: true})
	if err != nil {
		t.Fatalf("ApplySuggestions: %v", err)
	}
	got, _ := applied.Actions[0].Params["correlation_id"].(string)
	want := "{{var." + corr.VarName + "}}"
	if got != want {
		t.Fatalf("applied correlation_id = %q, want %q", got, want)
	}
	// Original script must be untouched (Apply clones).
	if orig, _ := script.Actions[0].Params["correlation_id"].(string); orig != "abc-123" {
		t.Fatalf("original script mutated: correlation_id = %q", orig)
	}
	// A value_replaced diff must point at the params path.
	foundDiff := false
	for _, d := range diffs {
		if d.Kind == "value_replaced" && d.Path == "/actions/0/params/correlation_id" {
			foundDiff = true
		}
	}
	if !foundDiff {
		t.Fatalf("expected a value_replaced diff at /actions/0/params/correlation_id, got %+v", diffs)
	}
	// A consolidated data source + parameter must be materialized.
	if len(applied.DataSources) == 0 {
		t.Fatal("expected a data source to be materialized")
	}
	if len(applied.Parameters) == 0 {
		t.Fatal("expected a parameter to be materialized")
	}
}

func TestJMSAndEventHubsParamsRecognized(t *testing.T) {
	for _, tc := range []struct {
		protocol   string
		actionType string
		paramKey   string
	}{
		{"jms-solace", "jms_publish", "correlation_id"},
		{"jms-solace", "jms_publish", "message_id"},
		{"eventhubs", "eventhubs_produce", "partition_key"},
	} {
		script := &dsl.Script{
			Name: "t", Version: "1.0", Protocol: tc.protocol,
			Actions: []dsl.Action{{
				Name: "send", Type: tc.actionType,
				Params: map[string]any{tc.paramKey: "live-value-1"},
			}},
		}
		result := Suggest(script, Options{EnableDataParams: true})
		if findDataParam(result.Suggestions, "params."+tc.paramKey) == nil {
			t.Fatalf("%s/%s: expected data-param for params.%s", tc.protocol, tc.actionType, tc.paramKey)
		}
	}
}
