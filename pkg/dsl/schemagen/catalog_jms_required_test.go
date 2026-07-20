package schemagen

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestJMSAdvancedRequiredFieldsBecomeNestedSchemaRequirements(t *testing.T) {
	tests := []struct {
		actionType string
		param      string
	}{
		{actionType: "jms_settle", param: "settlement_token"},
		{actionType: "jms_renew_lock", param: "settlement_token"},
		{actionType: "jms_session_state", param: "session_token"},
		{actionType: "jms_session_close", param: "session_token"},
		{actionType: "jms_receive_deferred", param: "sequence_numbers"},
		{actionType: "jms_cancel_scheduled", param: "sequence_numbers"},
	}

	for _, test := range tests {
		t.Run(test.actionType+"/"+test.param, func(t *testing.T) {
			spec, ok := dsl.SpecForActionType(test.actionType)
			if !ok {
				t.Fatalf("catalog is missing %s", test.actionType)
			}
			then := actionThenClause(spec)
			if !schemaStringListContains(t, then["required"], "params") {
				t.Fatalf("%s schema required = %#v, want params", test.actionType, then["required"])
			}

			properties, ok := then["properties"].(map[string]any)
			if !ok {
				t.Fatalf("%s schema properties = %#v, want object", test.actionType, then["properties"])
			}
			params, ok := properties["params"].(map[string]any)
			if !ok {
				t.Fatalf("%s params schema = %#v, want object", test.actionType, properties["params"])
			}
			if !schemaStringListContains(t, params["required"], test.param) {
				t.Fatalf("%s params.required = %#v, want %s", test.actionType, params["required"], test.param)
			}
		})
	}
}

func schemaStringListContains(t *testing.T, raw any, want string) bool {
	t.Helper()
	values, ok := raw.([]any)
	if !ok {
		t.Fatalf("schema string list = %#v, want []any", raw)
	}
	for _, value := range values {
		if text, ok := value.(string); ok && text == want {
			return true
		}
	}
	return false
}
