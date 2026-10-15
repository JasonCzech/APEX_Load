package credentialpolicy

import "testing"

// Cookies and session, anti-forgery and tracing headers are state the source
// recorded, not credentials: an import keeps them as recorded instead of
// asking for a project secret, unless the source itself declares one a
// credential.
func TestProjectHeadersKeepsRecordedStateAsImported(t *testing.T) {
	t.Parallel()
	options := projectionOptions()
	options.SensitiveNames = map[string]string{"x-tenant-session": "Tenant session key"}
	input := []HeaderOccurrence{
		{Name: "Cookie", Value: "JSESSIONID=recorded-41"},
		{Name: "X-CSRF-Token", Value: "recorded-csrf-52"},
		{Name: "X-Session-Id", Value: "recorded-session-63"},
		{Name: "traceparent", Value: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"},
		{Name: "X-Tenant-Session", Value: "declared-74"},
		{Name: "Ocp-Apim-Subscription-Key", Value: "subscription-85"},
	}
	output, result, err := ProjectHeaders(input, options)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 4; index++ {
		if output[index].Value != input[index].Value {
			t.Errorf("recorded %s became %q, want it kept as imported", input[index].Name, output[index].Value)
		}
	}
	if output[4].Value != "{{secret.FIXTURE_TENANT_SESSION_KEY}}" {
		t.Errorf("a header the source declares a credential = %q, want it governed", output[4].Value)
	}
	if output[5].Value != "{{secret.FIXTURE_OCP_APIM_SUBSCRIPTION_KEY}}" {
		t.Errorf("subscription key = %q, want it governed", output[5].Value)
	}
	if len(result.Requirements) != 2 {
		t.Fatalf("requirements = %#v, want only the declared credential and the subscription key", result.Requirements)
	}
}
