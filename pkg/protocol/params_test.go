package protocol

import (
	"math"
	"testing"
)

func TestConfigStringTrims(t *testing.T) {
	cfg := map[string]any{"host": "  example.com  ", "n": 5}
	if got := ConfigString(cfg, "host"); got != "example.com" {
		t.Fatalf("ConfigString trim: got %q, want %q", got, "example.com")
	}
	if got := ConfigString(cfg, "missing"); got != "" {
		t.Fatalf("ConfigString missing: got %q, want \"\"", got)
	}
	if got := ConfigString(cfg, "n"); got != "" {
		t.Fatalf("ConfigString non-string: got %q, want \"\"", got)
	}
	if got := ConfigString(nil, "host"); got != "" {
		t.Fatalf("ConfigString nil config: got %q, want \"\"", got)
	}
}

func TestConfigIntCoercions(t *testing.T) {
	cases := []struct {
		name string
		val  any
		want int
	}{
		{"int", 7, 7},
		{"int64", int64(8), 8},
		{"float64", float64(9), 9},
		{"float32", float32(10), 10},
		{"string", "11", 0},
		{"nil", nil, 0},
		{"nan", math.NaN(), 0},
		{"positive infinity", math.Inf(1), 0},
		{"negative infinity", math.Inf(-1), 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ConfigInt(map[string]any{"k": tc.val}, "k"); got != tc.want {
				t.Fatalf("ConfigInt(%v): got %d, want %d", tc.val, got, tc.want)
			}
		})
	}
	if got := ConfigInt(nil, "k"); got != 0 {
		t.Fatalf("ConfigInt nil config: got %d, want 0", got)
	}
}

func TestConfigFloatCoercions(t *testing.T) {
	if got := ConfigFloat(map[string]any{"k": 3}, "k"); got != 3 {
		t.Fatalf("ConfigFloat int: got %v, want 3", got)
	}
	if got := ConfigFloat(map[string]any{"k": 2.5}, "k"); got != 2.5 {
		t.Fatalf("ConfigFloat float64: got %v, want 2.5", got)
	}
	if got := ConfigFloat(map[string]any{"k": "x"}, "k"); got != 0 {
		t.Fatalf("ConfigFloat non-numeric: got %v, want 0", got)
	}
	for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if got := ConfigFloat(map[string]any{"k": value}, "k"); got != 0 {
			t.Fatalf("ConfigFloat non-finite %v: got %v, want 0", value, got)
		}
	}
}

func TestFirstNonEmptyTrims(t *testing.T) {
	// The reconciled behavior: blank-after-trim values are skipped, and the
	// returned value is trimmed (the JMS/Kafka copies previously returned the
	// untrimmed value).
	if got := FirstNonEmpty("", "   ", "  topic  ", "other"); got != "topic" {
		t.Fatalf("FirstNonEmpty: got %q, want %q", got, "topic")
	}
	if got := FirstNonEmpty("", "  "); got != "" {
		t.Fatalf("FirstNonEmpty all blank: got %q, want \"\"", got)
	}
}

func TestActionNameNilSafe(t *testing.T) {
	if got := ActionName(nil); got != "" {
		t.Fatalf("ActionName(nil): got %q, want \"\"", got)
	}
	if got := ActionName(&Action{Name: "step"}); got != "step" {
		t.Fatalf("ActionName: got %q, want %q", got, "step")
	}
}

func TestConfigStringSlice(t *testing.T) {
	cfg := map[string]any{
		"a": []string{" x ", "", "  ", "y"},
		"b": []any{"  p  ", "", "q", 3},
		"c": "scalar",
	}
	if got := ConfigStringSlice(cfg, "a"); len(got) != 2 || got[0] != "x" || got[1] != "y" {
		t.Fatalf("ConfigStringSlice []string: got %v", got)
	}
	if got := ConfigStringSlice(cfg, "b"); len(got) != 2 || got[0] != "p" || got[1] != "q" {
		t.Fatalf("ConfigStringSlice []any trim/drop: got %v", got)
	}
	if got := ConfigStringSlice(cfg, "c"); got != nil {
		t.Fatalf("ConfigStringSlice non-slice: got %v, want nil", got)
	}
}
