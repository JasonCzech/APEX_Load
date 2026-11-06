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

// An allowlisted {{env.*}} name that reads as a credential resolves for the
// request but shows as [REDACTED] in evidence; another name shows its value.
func TestInterpolationRedactsCredentialNamedEnvValues(t *testing.T) {
	t.Setenv(EnvScriptEnvAllowlist, "PW,REGION")
	t.Setenv("PW", "hunter2-recorded")
	t.Setenv("REGION", "eu-west")
	vu := &VUser{Variables: map[string]string{}}
	actual, redacted := vu.interpolateWithSecrets("pw={{env.PW}} region={{env.REGION}}")
	if actual != "pw=hunter2-recorded region=eu-west" {
		t.Fatalf("actual = %q", actual)
	}
	if redacted != "pw=[REDACTED] region=eu-west" {
		t.Fatalf("redacted = %q, want the credential-named value hidden", redacted)
	}
}
