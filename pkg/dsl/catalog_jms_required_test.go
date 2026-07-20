package dsl

import (
	"strings"
	"testing"
)

func TestJMSAdvancedCatalogMarksUnconditionalInputsRequired(t *testing.T) {
	tests := []struct {
		actionType string
		fieldName  string
	}{
		{actionType: "jms_settle", fieldName: "params.settlement_token"},
		{actionType: "jms_renew_lock", fieldName: "params.settlement_token"},
		{actionType: "jms_session_state", fieldName: "params.session_token"},
		{actionType: "jms_session_close", fieldName: "params.session_token"},
		{actionType: "jms_receive_deferred", fieldName: "params.sequence_numbers"},
		{actionType: "jms_cancel_scheduled", fieldName: "params.sequence_numbers"},
	}

	for _, test := range tests {
		t.Run(test.actionType+"/"+test.fieldName, func(t *testing.T) {
			spec, ok := SpecForActionType(test.actionType)
			if !ok {
				t.Fatalf("catalog is missing %s", test.actionType)
			}
			field := requireJMSCatalogField(t, spec, test.fieldName)
			if !field.Required {
				t.Fatalf("%s %s is not marked required", test.actionType, test.fieldName)
			}
		})
	}
}

func TestJMSAdvancedCatalogKeepsConditionalInputsOptional(t *testing.T) {
	tests := []struct {
		actionType string
		fieldName  string
	}{
		{actionType: "jms_consume", fieldName: "params.session_token"},
		{actionType: "jms_session_accept", fieldName: "params.session_id"},
		{actionType: "jms_settle", fieldName: "params.disposition"},
	}

	for _, test := range tests {
		t.Run(test.actionType+"/"+test.fieldName, func(t *testing.T) {
			spec, ok := SpecForActionType(test.actionType)
			if !ok {
				t.Fatalf("catalog is missing %s", test.actionType)
			}
			field := requireJMSCatalogField(t, spec, test.fieldName)
			if field.Required {
				t.Fatalf("%s %s is marked required, want conditional/optional", test.actionType, test.fieldName)
			}
		})
	}
}

func requireJMSCatalogField(t *testing.T, spec ActionSpec, fieldName string) FieldSpec {
	t.Helper()
	for _, field := range spec.Fields {
		if field.Name == fieldName {
			return field
		}
	}
	t.Fatalf("%s catalog is missing %s", spec.Type, fieldName)
	return FieldSpec{}
}

func TestJMSAdvancedRequiredMetadataPopulatesSkeletonParams(t *testing.T) {
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
			action := SkeletonAction(test.actionType)
			value, ok := action.Params[test.param]
			if !ok {
				t.Fatalf("SkeletonAction(%q) params = %#v, want %q placeholder", test.actionType, action.Params, test.param)
			}
			if text, ok := value.(string); ok && strings.TrimSpace(text) == "" {
				t.Fatalf("SkeletonAction(%q) %s placeholder is empty", test.actionType, test.param)
			}
		})
	}
}
