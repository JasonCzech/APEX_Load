package browser

import (
	"testing"

	playwright "github.com/mxschmitt/playwright-go"
)

func TestBrowserContextOptions(t *testing.T) {
	b := &Browser{
		touchMode: true,
		mobile:    true,
		viewportW: 390,
		viewportH: 844,
		userAgent: "CustomUA",
	}

	opts := b.browserContextOptions()
	if opts.HasTouch == nil || !*opts.HasTouch {
		t.Fatalf("HasTouch = %#v, want true", opts.HasTouch)
	}
	if opts.IsMobile == nil || !*opts.IsMobile {
		t.Fatalf("IsMobile = %#v, want true", opts.IsMobile)
	}
	if opts.Viewport == nil || opts.Viewport.Width != 390 || opts.Viewport.Height != 844 {
		t.Fatalf("Viewport = %#v, want 390x844", opts.Viewport)
	}
	if opts.UserAgent == nil || *opts.UserAgent != "CustomUA" {
		t.Fatalf("UserAgent = %#v, want CustomUA", opts.UserAgent)
	}

	b.userAgent = ""
	opts = b.browserContextOptions()
	if opts.UserAgent != nil {
		t.Fatalf("UserAgent = %#v, want nil when unset", opts.UserAgent)
	}
}

func TestRequestDurationMs(t *testing.T) {
	tests := []struct {
		name   string
		timing *playwright.RequestTiming
		want   float64
	}{
		{name: "nil", timing: nil, want: 0},
		{name: "response end", timing: &playwright.RequestTiming{ResponseEnd: 123.5, ResponseStart: 50}, want: 123.5},
		{name: "response start fallback", timing: &playwright.RequestTiming{ResponseEnd: -1, ResponseStart: 50.25}, want: 50.25},
		{name: "unavailable", timing: &playwright.RequestTiming{ResponseEnd: -1, ResponseStart: -1}, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := requestDurationMs(tt.timing); got != tt.want {
				t.Fatalf("requestDurationMs() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDialogMessageMatchesModes(t *testing.T) {
	tests := []struct {
		name     string
		actual   string
		expected string
		mode     string
		want     bool
	}{
		{name: "exact match", actual: "Delete item?", expected: "Delete item?", mode: "exact", want: true},
		{name: "exact mismatch", actual: "Really delete item?", expected: "Delete item?", mode: "exact", want: false},
		{name: "contains", actual: "Really delete item?", expected: "delete item", mode: "contains", want: true},
		{name: "normalized whitespace and case", actual: " Really   DELETE item? ", expected: "really delete item?", mode: "normalized", want: true},
		{name: "empty expected only matches empty message", actual: "anything", expected: "", mode: "exact", want: false},
		{name: "unknown mode falls back to exact", actual: "prefix ok", expected: "ok", mode: "surprise", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := dialogMessageMatches(tt.actual, tt.expected, tt.mode); got != tt.want {
				t.Fatalf("dialogMessageMatches(%q, %q, %q) = %v, want %v", tt.actual, tt.expected, tt.mode, got, tt.want)
			}
		})
	}
}
