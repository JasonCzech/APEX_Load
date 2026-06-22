package engine

import "testing"

// TestCompositeIntraRequestPlaceholderSurvivesInterpolation locks in the A5
// finding that intra-request composite placeholders (@{refName.field}) are
// resolved server-side and need no correlation handling: the engine template
// resolver only expands {{...}} tokens, so an @{...} reference passes through
// replay untouched.
func TestCompositeIntraRequestPlaceholderSurvivesInterpolation(t *testing.T) {
	vu := &VUser{Variables: map[string]string{"x": "1"}}
	body := `{"compositeRequest":[{"referenceId":"NewAccount","body":{"Name":"Acme"}},{"referenceId":"NewContact","body":{"AccountId":"@{NewAccount.id}"}}]}`
	if got := vu.interpolate(body); got != body {
		t.Fatalf("@{} composite placeholder was altered during interpolation:\n got:  %q\n want: %q", got, body)
	}
}
