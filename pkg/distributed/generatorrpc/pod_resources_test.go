package generatorrpc

import "testing"

func TestParseCPUMax(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		want   float64
		wantOK bool
	}{
		{name: "v2 quota", input: "200000 100000", want: 2, wantOK: true},
		{name: "unlimited", input: "max 100000", wantOK: false},
		{name: "invalid", input: "bad", wantOK: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := parseCPUMax(test.input)
			if ok != test.wantOK {
				t.Fatalf("ok = %v, want %v", ok, test.wantOK)
			}
			if ok && got != test.want {
				t.Fatalf("limit = %v, want %v", got, test.want)
			}
		})
	}
}

func TestParseMemoryLimit(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		want   int64
		wantOK bool
	}{
		{name: "finite", input: "2147483648", want: 2147483648, wantOK: true},
		{name: "unlimited token", input: "max", wantOK: false},
		{name: "huge sentinel", input: "9223372036854771712", wantOK: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := parseMemoryLimit(test.input)
			if ok != test.wantOK {
				t.Fatalf("ok = %v, want %v", ok, test.wantOK)
			}
			if ok && got != test.want {
				t.Fatalf("limit = %d, want %d", got, test.want)
			}
		})
	}
}
