package credentialpolicy

import (
	"strings"
	"testing"
)

func provenSet(names ...string) map[string]struct{} {
	proven := make(map[string]struct{}, len(names))
	for _, name := range names {
		proven[name] = struct{}{}
	}
	return proven
}

// TestExecutableSchemeCredentialHeader pins both directions of the
// scheme-preserving rule. The permissive direction keeps a correlated
// "Basic {{var.token}}" working; the restrictive direction is what stops it
// becoming a hole — any authored residue beyond the scheme keyword still
// governs the whole header.
func TestExecutableSchemeCredentialHeader(t *testing.T) {
	proven := provenSet("token", "session")

	tests := []struct {
		name       string
		header     string
		value      string
		executable bool
	}{
		// Executable: scheme keyword plus one already-resolvable span.
		{name: "basic with proven var", header: "Authorization", value: "Basic {{var.token}}", executable: true},
		{name: "bearer with proven var", header: "authorization", value: "Bearer {{var.token}}", executable: true},
		{name: "bearer with secret", header: "Authorization", value: "Bearer {{secret.API_TOKEN}}", executable: true},
		{name: "bearer with env", header: "Authorization", value: "Bearer {{env.API_TOKEN}}", executable: true},
		{name: "proxy authorization", header: "Proxy-Authorization", value: "Digest {{secret.PROXY}}", executable: true},
		{name: "cookie all proven", header: "Cookie", value: "sid={{var.session}}; csrf={{var.token}}", executable: true},
		{name: "cookie mixed governed", header: "Cookie", value: "sid={{var.session}}; api={{secret.KEY}}", executable: true},

		// Governed: an unproven variable is not trusted by syntax alone.
		{name: "bearer with unproven var", header: "Authorization", value: "Bearer {{var.unknown}}"},
		{name: "cookie with one unproven", header: "Cookie", value: "sid={{var.session}}; other={{var.unknown}}"},

		// Governed: authored credential material.
		{name: "bearer literal", header: "Authorization", value: "Bearer sk-live-abc123"},
		{name: "basic literal", header: "Authorization", value: "Basic dXNlcjpwYXNz"},
		// The composed case that motivated narrowing the deferral rule: static
		// credential bytes next to a governed reference must still govern.
		{name: "bearer literal plus secret", header: "Authorization", value: "Bearer sk-live-abc {{secret.EXTRA}}"},
		{name: "cookie literal value", header: "Cookie", value: "sid=raw-session-value"},

		// Governed: unrecognized scheme, or no scheme at all.
		{name: "unknown scheme", header: "Authorization", value: "Weird {{var.token}}"},
		{name: "bare proven var", header: "Authorization", value: "{{var.token}}"},
		{name: "three fields", header: "Authorization", value: "Bearer {{var.token}} extra"},

		// Governed: not a scheme-bearing header.
		{name: "api key header", header: "X-Api-Key", value: "Bearer {{var.token}}"},
		{name: "empty", header: "Authorization", value: ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := executableSchemeCredentialHeader(test.header, test.value, proven)
			if got != test.executable {
				t.Fatalf("executableSchemeCredentialHeader(%q, %q) = %v, want %v",
					test.header, test.value, got, test.executable)
			}
		})
	}
}

// TestProjectHeadersPreservesExecutableSchemeCredentials exercises the rule
// through the real projection entry point, including that a governed header in
// the same request still produces a requirement.
func TestProjectHeadersPreservesExecutableSchemeCredentials(t *testing.T) {
	input := []HeaderOccurrence{
		{Name: "Authorization", Value: "Basic {{var.token}}"},
		{Name: "X-Api-Key", Value: "raw-api-key-value"},
	}
	output, result, err := ProjectHeaders(input, Options{
		Mode:       SecretReference,
		Registry:   NewNameRegistry(),
		NamePrefix: "TEST",
		ProvenVars: provenSet("token"),
	})
	if err != nil {
		t.Fatalf("ProjectHeaders: %v", err)
	}
	if output[0].Value != "Basic {{var.token}}" {
		t.Fatalf("correlated auth header = %q, want it preserved", output[0].Value)
	}
	if !strings.Contains(output[1].Value, "{{secret.") {
		t.Fatalf("authored api key = %q, want it governed", output[1].Value)
	}
	if len(result.Requirements) != 1 {
		t.Fatalf("requirements = %#v, want exactly the authored api key", result.Requirements)
	}
	if result.Requirements[0].Value != "raw-api-key-value" {
		t.Fatalf("requirement value = %q, want the authored api key", result.Requirements[0].Value)
	}
}

// TestProjectHeadersGovernsUnprovenSchemeCredentials is the safety direction:
// without a proven-variable set, a variable-looking token is not trusted.
func TestProjectHeadersGovernsUnprovenSchemeCredentials(t *testing.T) {
	output, result, err := ProjectHeaders(
		[]HeaderOccurrence{{Name: "Authorization", Value: "Basic {{var.token}}"}},
		Options{Mode: SecretReference, Registry: NewNameRegistry(), NamePrefix: "TEST"},
	)
	if err != nil {
		t.Fatalf("ProjectHeaders: %v", err)
	}
	if !strings.Contains(output[0].Value, "{{secret.") {
		t.Fatalf("unproven auth header = %q, want it governed", output[0].Value)
	}
	if len(result.Requirements) != 1 {
		t.Fatalf("requirements = %#v, want the unproven header governed", result.Requirements)
	}
}
