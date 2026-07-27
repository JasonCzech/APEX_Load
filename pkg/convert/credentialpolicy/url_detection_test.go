package credentialpolicy

import "testing"

func TestContainsCredentialURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		text string
		want bool
	}{
		{
			name: "safe endpoint",
			text: "-Dendpoint=https://example.test/service?view=full",
		},
		{
			name: "userinfo",
			text: `"-Dendpoint=https://runner:password@example.test/service"`,
			want: true,
		},
		{
			name: "sensitive query",
			text: "-Dendpoint=HTTPS://example.test/service?api_key=key",
			want: true,
		},
		{
			name: "bracket-delimited URL",
			text: "endpoints=[https://example.test/service?token=key]",
			want: true,
		},
		{
			name: "sensitive fragment",
			text: "-Dendpoint=https://example.test/service#token=short-secret",
			want: true,
		},
		{
			name: "router fragment query",
			text: "-Dendpoint=https://example.test/service#/callback?api_key=short-secret",
			want: true,
		},
		{
			name: "ambiguous bare governed fragment",
			text: "-Dendpoint=https://example.test/service#access_token",
			want: true,
		},
		{
			name: "safe fragment",
			text: "-Dendpoint=https://example.test/service#overview",
		},
		{
			name: "later URL",
			text: "https://example.test/safe https://user:pass@example.test/private",
			want: true,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := ContainsCredentialURL(test.text); got != test.want {
				t.Fatalf("ContainsCredentialURL(%q) = %v, want %v", test.text, got, test.want)
			}
		})
	}
}
