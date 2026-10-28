package browser

import (
	"strings"
	"testing"
)

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

func TestBrowserTapUsesSyntheticPath(t *testing.T) {
	if !(&Browser{touchMode: false}).shouldUseSyntheticTap() {
		t.Fatal("tap should use synthetic touch events when the context has touch disabled")
	}
	if (&Browser{touchMode: true}).shouldUseSyntheticTap() {
		t.Fatal("tap should use native trusted touch events when the context has touch enabled")
	}
	operation := syntheticTapOperation()
	for _, want := range []string{"dispatchTouch('touchstart'", "dispatchTouch('touchend'", "dispatchClick"} {
		if !strings.Contains(operation, want) {
			t.Fatalf("synthetic tap operation missing %q: %s", want, operation)
		}
	}
}
