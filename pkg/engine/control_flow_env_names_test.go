package engine

import "testing"

// A condition reading a readable credential env name (API_TKN, as imports now
// write for API_TOKEN) keeps its template text in debug evidence.
func TestConditionOperandTreatsReadableCredentialEnvNamesAsSensitive(t *testing.T) {
	for _, operand := range []string{"{{env.API_TKN}}", "{{env.ACCESS_TKN}}", "{{env.CLIENT_SCRT}}", "{{env.API_PW}}"} {
		if !conditionOperandContainsSensitiveReference(operand) {
			t.Errorf("%s not treated as sensitive", operand)
		}
	}
	if conditionOperandContainsSensitiveReference("{{env.REGION}}") {
		t.Error("{{env.REGION}} treated as sensitive")
	}
}
