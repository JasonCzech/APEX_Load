package dsl

import (
	"encoding/json"
	"math"
	"testing"
)

func TestValidateJMSSequenceNumbersMirrorsRuntimeParser(t *testing.T) {
	invalid := []struct {
		name string
		raw  any
		want string
	}{
		{name: "object", raw: map[string]any{"first": 1}, want: "invalid sequence_numbers entry"},
		{name: "nonnumeric string", raw: "one,two", want: "invalid sequence_numbers entry"},
		{name: "nonnumeric array item", raw: []any{float64(1), "two"}, want: "invalid sequence_numbers entry"},
		{name: "object array item", raw: []any{map[string]any{"value": 1}}, want: "invalid sequence_numbers entry"},
		{name: "fractional number", raw: float64(1.5), want: "invalid sequence_numbers entry"},
		{name: "fractional array item", raw: []any{float64(1.5)}, want: "invalid sequence_numbers entry"},
		{name: "positive overflow string", raw: "9223372036854775808", want: "invalid sequence_numbers entry"},
		{name: "negative overflow number", raw: json.Number("-9223372036854775809"), want: "invalid sequence_numbers entry"},
		{name: "nonfinite", raw: math.Inf(1), want: "invalid sequence_numbers entry"},
		{name: "zero scalar", raw: float64(0), want: "must be greater than zero"},
		{name: "negative scalar", raw: int64(-1), want: "must be greater than zero"},
		{name: "zero array item", raw: []any{float64(0)}, want: "must be greater than zero"},
		{name: "negative CSV item", raw: "1,-2", want: "must be greater than zero"},
		{name: "zero int64 array item", raw: []int64{1, 0}, want: "must be greater than zero"},
	}
	for _, actionType := range []string{"jms_receive_deferred", "jms_cancel_scheduled"} {
		for _, test := range invalid {
			t.Run(actionType+"/"+test.name, func(t *testing.T) {
				action := Action{Name: "advanced", Type: actionType, Params: map[string]any{"sequence_numbers": test.raw}}
				issues := ValidateScriptIssues(jmsProviderValidationScript("jms-servicebus", "servicebus", action))
				if !hasIssueContaining(issues, test.want) {
					t.Fatalf("issues = %v, want sequence-number issue containing %q", issues, test.want)
				}
			})
		}
	}
}

func TestValidateJMSSequenceNumbersAcceptsEveryRuntimeForm(t *testing.T) {
	valid := map[string]any{
		"single number":        float64(7),
		"single json number":   json.Number("8"),
		"number array":         []any{float64(1), json.Number("2"), int64(3)},
		"numeric string array": []any{"4", "5"},
		"comma string":         "6, 7,8",
		"int64 array":          []int64{9, 10},
		"single template":      "{{var.sequence_number}}",
		"CSV template":         "1,{{var.sequence_number}},3",
		"array template":       []any{float64(1), "{{var.sequence_number}}"},
	}
	for name, raw := range valid {
		t.Run(name, func(t *testing.T) {
			action := Action{Name: "advanced", Type: "jms_receive_deferred", Params: map[string]any{"sequence_numbers": raw}}
			issues := ValidateScriptIssues(jmsProviderValidationScript("jms-servicebus", "servicebus", action))
			for _, issue := range issues {
				if hasIssueContaining([]string{issue}, "sequence_numbers") {
					t.Fatalf("valid runtime form rejected: %v", issues)
				}
			}
		})
	}
}

func TestValidateJMSSequenceNumberTemplatesStillRejectInvalidStaticEntries(t *testing.T) {
	for name, raw := range map[string]any{
		"CSV":   "not-a-number,{{var.sequence_number}}",
		"array": []any{"not-a-number", "{{var.sequence_number}}"},
	} {
		t.Run(name, func(t *testing.T) {
			action := Action{Name: "advanced", Type: "jms_receive_deferred", Params: map[string]any{"sequence_numbers": raw}}
			issues := ValidateScriptIssues(jmsProviderValidationScript("jms-servicebus", "servicebus", action))
			if !hasIssueContaining(issues, "invalid sequence_numbers entry") {
				t.Fatalf("issues = %v, want invalid static sequence-number issue", issues)
			}
		})
	}
}
