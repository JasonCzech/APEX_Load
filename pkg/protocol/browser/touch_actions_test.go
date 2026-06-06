package browser

import "testing"

func TestNormalizeSwipeDirection(t *testing.T) {
	tests := map[string]string{
		"":        "left",
		"left":    "left",
		" LEFT ":  "left",
		"right":   "right",
		" Right ": "right",
		"UP":      "up",
		"down":    "down",
		"invalid": "left",
	}

	for input, want := range tests {
		if got := normalizeSwipeDirection(input); got != want {
			t.Fatalf("normalizeSwipeDirection(%q) = %q, want %q", input, got, want)
		}
	}
}
