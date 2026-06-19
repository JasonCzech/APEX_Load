package convertutil

import "testing"

func TestFirstNonEmpty(t *testing.T) {
	if got := FirstNonEmpty("", "   ", "  value  ", "other"); got != "value" {
		t.Fatalf("FirstNonEmpty = %q, want %q", got, "value")
	}
	if got := FirstNonEmpty("", "  "); got != "" {
		t.Fatalf("FirstNonEmpty all blank = %q, want \"\"", got)
	}
	if got := FirstNonEmpty(); got != "" {
		t.Fatalf("FirstNonEmpty no args = %q, want \"\"", got)
	}
}

func TestBoolOption(t *testing.T) {
	tru, fls := true, false
	if got := BoolOption(nil, true); got != true {
		t.Fatalf("BoolOption(nil, true) = %v, want true", got)
	}
	if got := BoolOption(nil, false); got != false {
		t.Fatalf("BoolOption(nil, false) = %v, want false", got)
	}
	if got := BoolOption(&fls, true); got != false {
		t.Fatalf("BoolOption(&false, true) = %v, want false", got)
	}
	if got := BoolOption(&tru, false); got != true {
		t.Fatalf("BoolOption(&true, false) = %v, want true", got)
	}
}
