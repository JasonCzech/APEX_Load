package credentialpolicy

import "testing"

func TestProviderKeyHeaderReference(t *testing.T) {
	const ref = "{{secret.llm_api_key}}"
	for _, tc := range []struct {
		name, value, want string
		changed           bool
	}{
		{name: "Authorization", value: "Bearer sk-live", want: "Bearer " + ref, changed: true},
		{name: " api-key ", value: "abc", want: ref, changed: true},
		{name: "X-API-Key", value: "abc", want: ref, changed: true},
		{name: "Ocp-Apim-Subscription-Key", value: "abc", want: ref, changed: true},
		{name: "Authorization", value: "Bearer {{token}}", want: "Bearer {{token}}"},
		{name: "Content-Type", value: "application/json", want: "application/json"},
	} {
		got, changed := ProviderKeyHeaderReference(tc.name, tc.value, ref)
		if got != tc.want || changed != tc.changed {
			t.Errorf("ProviderKeyHeaderReference(%q, %q) = %q, %v; want %q, %v", tc.name, tc.value, got, changed, tc.want, tc.changed)
		}
	}
}
