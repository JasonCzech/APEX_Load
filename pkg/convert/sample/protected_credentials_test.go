package sample

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/scrub"
)

func TestProtectedCredentialRequirementsAreOneShotAndJSONInvisible(t *testing.T) {
	const value = "sample-protected-canary-7b35360e"
	result, err := Convert(ImportRequest{
		Format: "rest_json",
		Content: fmt.Sprintf(
			`{"request":{"method":"POST","url":"https://api.example.test/login","body":{"password":%q}}}`,
			value,
		),
	})
	if err != nil {
		t.Fatal(err)
	}

	publicJSON, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(publicJSON), value) {
		t.Fatal("converter JSON lost the executable imported credential")
	}
	scriptJSON, err := json.Marshal(result.Script)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(scriptJSON), value) {
		t.Fatal("destination script lost the executable imported credential")
	}

	requirements := result.ProtectedRequirements.Take()
	defer clearSampleProtectedValues(requirements)
	if len(requirements) != 0 {
		t.Fatalf("protected requirements = %#v, preserved credential must not require reentry", requirements)
	}
	if result.ProtectedRequirements.Take() != nil {
		t.Fatal("protected credential requirements were returned more than once")
	}
}

func TestPublicCorrelationSamplesRedactCredentialContext(t *testing.T) {
	correlations := []Correlation{
		{Name: "corr_session_token", Expression: "session_token", SampleValue: "simple-canary-value"},
		{Name: "corr_location_state", Expression: "state", SampleValue: "space token/123456"},
	}
	sanitizePublicCorrelationSamples(correlations)
	if correlations[0].SampleValue != scrub.Redacted {
		t.Fatalf("credential correlation sample = %q", correlations[0].SampleValue)
	}
	if correlations[1].SampleValue != "space token/123456" {
		t.Fatalf("nonsensitive correlation sample = %q", correlations[1].SampleValue)
	}
}

func TestUserinfoSurvivesRelativizationUntilCredentialProjection(t *testing.T) {
	const userinfo = "service:sample%2Dpassword"
	result, err := Convert(ImportRequest{
		Format:  "curl",
		Content: `curl 'https://` + userinfo + `@api.example.test/orders'`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Script.Actions) != 1 {
		t.Fatalf("actions = %#v, want one request", result.Script.Actions)
	}
	action := result.Script.Actions[0]
	if !strings.Contains(action.Target, userinfo) {
		t.Fatalf("target = %q, want imported userinfo preserved", action.Target)
	}
	if blocker := action.Metadata["execution_blocker"]; blocker != nil {
		t.Fatalf("metadata = %#v, preserved userinfo must not block execution", action.Metadata)
	}
	requirements := result.ProtectedRequirements.Take()
	defer clearSampleProtectedValues(requirements)
	if len(requirements) != 0 {
		t.Fatalf("requirements = %#v, preserved userinfo must not require reentry", requirements)
	}
}

func clearSampleProtectedValues(requirements []credentialpolicy.Requirement) {
	for index := range requirements {
		requirements[index].Value = ""
	}
}
