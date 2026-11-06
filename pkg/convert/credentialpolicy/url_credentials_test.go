package credentialpolicy

import (
	"slices"
	"testing"
)

func TestURLCredentialValues(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want []string
	}{
		{name: "plain address", raw: "https://idp.example.test/oauth2/token", want: nil},
		{name: "empty", raw: "  ", want: nil},
		{
			name: "credential query parameter",
			raw:  "https://idp.example.test/token?client_secret=synthetic-secret-1&scope=read",
			want: []string{"synthetic-secret-1"},
		},
		{
			name: "percent-encoded value is returned as written and decoded",
			raw:  "https://idp.example.test/token?access_token=synthetic%2Btoken%3D",
			want: []string{"synthetic%2Btoken%3D", "synthetic+token="},
		},
		{
			name: "fragment parameter on a templated host",
			raw:  "https://${IDP_HOST}/cb#id_token=synthetic-id-token-1",
			want: []string{"synthetic-id-token-1"},
		},
		{
			name: "userinfo after a scheme",
			raw:  "https://svc-user:synthetic-pass@idp.example.test/token",
			want: []string{"svc-user", "synthetic-pass"},
		},
		{
			name: "schemeless user:password",
			raw:  "svc-user:synthetic-pass@idp.example.test",
			want: []string{"svc-user", "synthetic-pass"},
		},
		{
			// Without "//" a bare name@domain is an address, not userinfo.
			name: "schemeless mailbox is not userinfo",
			raw:  "someone@example.test",
			want: nil,
		},
		{
			name: "variable reference as parameter name fails closed",
			raw:  "https://idp.example.test/token?${PARAM}=synthetic-value",
			want: []string{"https://idp.example.test/token?${PARAM}=synthetic-value"},
		},
		{
			name: "undecodable parameter name fails closed",
			raw:  "https://idp.example.test/token?bad%zz=synthetic-value",
			want: []string{"https://idp.example.test/token?bad%zz=synthetic-value"},
		},
		{
			name: "unsplittable userinfo fails closed",
			raw:  "https://svc user:synthetic=pass@idp.example.test/token",
			want: []string{"https://svc user:synthetic=pass@idp.example.test/token"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := URLCredentialValues(tc.raw)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("URLCredentialValues(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}
