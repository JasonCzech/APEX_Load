package dsl

import "testing"

func TestIsSAPGUISessionBehavior(t *testing.T) {
	for _, value := range []string{"attach", "new_session", "reuse", "", "token", "attach-secret"} {
		want := value == "attach" || value == "new_session" || value == "reuse"
		if IsSAPGUISessionBehavior(value) != want {
			t.Fatalf("unexpected classification: %q", value)
		}
	}
}
