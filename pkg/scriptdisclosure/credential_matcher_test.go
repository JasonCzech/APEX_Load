package scriptdisclosure

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/credential"
)

func TestCredentialMatcher(t *testing.T) {
	tests := []struct {
		name            string
		secrets, forced []string
		input, want     string
	}{
		{name: "empty", input: "ordinary", want: "ordinary"},
		{name: "unrelated", secrets: []string{"private-value", "another-value"}, input: "GET /status", want: "GET /status"},
		{name: "embedded", secrets: []string{"private-value"}, input: "a private-value z", want: "a [REDACTED] z"},
		{name: "overlap", secrets: []string{"abcd", "cdef", "bcde"}, input: "xabcdefy", want: "x[REDACTED]y"},
		{name: "suffix failure", secrets: []string{"abcdef", "cdef", "efgh"}, input: "xcdefghz", want: "x[REDACTED]z"},
		{name: "repeated", secrets: []string{"abcd"}, input: "abcd,abcd", want: "[REDACTED],[REDACTED]"},
		{name: "short exact", secrets: []string{"t"}, input: "t", want: "[REDACTED]"},
		{name: "short unproven", secrets: []string{"t"}, input: "value=t", want: "value=t"},
		{name: "short proven", secrets: []string{"t"}, forced: []string{"t"}, input: "POST /status value=t", want: "POST /status value=[REDACTED]"},
		{name: "punctuation", secrets: []string{"/"}, forced: []string{"/"}, input: "https://example.test value=/", want: "https://example.test value=[REDACTED]"},
		{name: "whitespace exact", secrets: []string{"    "}, input: "    ", want: "[REDACTED]"},
		{name: "whitespace embedded", secrets: []string{"    "}, input: "a    z", want: "a    z"},
		{name: "unicode boundary", secrets: []string{"é"}, forced: []string{"é"}, input: "café key=é", want: "café key=[REDACTED]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			forced := map[string]struct{}{}
			for _, s := range tt.forced {
				forced[s] = struct{}{}
			}
			m := newCredentialMatcher(tt.secrets, forced)
			if m == nil {
				t.Fatal("matcher unavailable")
			}
			if got := m.replace(tt.input); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLargeCredentialProjectionPreservesOrdinaryValues(t *testing.T) {
	script, secrets := largeCredentialBearingLabelSource()
	projected, err := Project(script)
	if err != nil {
		t.Fatal(err)
	}
	if projected.Actions[0].Target != "/" {
		t.Fatal("ordinary target was classified as a credential")
	}
	if projected.Actions[0].Params["ordinary_blob"] != script.Actions[0].Params["ordinary_blob"] {
		t.Fatal("ordinary payload was classified as a credential")
	}
	for key, value := range projected.Actions[0].Params {
		if strings.HasSuffix(key, "_password") && value != credential.Redacted {
			t.Fatal("credential was not masked")
		}
	}
	if script.Actions[0].Params["field_0_password"] != secrets[0] {
		t.Fatal("source mutated")
	}
}

func TestCredentialMatcherRejectsExcessiveState(t *testing.T) {
	if newCredentialMatcher([]string{strings.Repeat("x", maxCredentialMatcherNodes+1)}, nil) != nil {
		t.Fatal("expected bounded matcher construction")
	}
}

func TestProjectionLimitDoesNotPublishFalseCredentialMasks(t *testing.T) {
	script, _ := largeCredentialBearingLabelSource()
	// Distinct leading bytes prevent the long credentials from sharing trie
	// states. The ordinary payload also exceeds the legacy comparison estimate.
	for i := 0; i < 20; i++ {
		script.Actions[0].Params[fmt.Sprintf("field_%d_password", i)] = string(rune('a'+i)) + strings.Repeat("x", 13_000)
	}
	projected, err := Project(script)
	if err == nil || projected != nil {
		t.Fatal("resource limit must return an error, not blanket credential masks")
	}
}
