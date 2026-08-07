package kubeops

import "testing"

func TestParseCPUMillis(t *testing.T) {
	cases := []struct {
		raw     string
		millis  int64
		present bool
		wantErr bool
	}{
		{raw: "", present: false},
		{raw: "  ", present: false},
		{raw: "100m", millis: 100, present: true},
		{raw: "1", millis: 1000, present: true},
		{raw: "2", millis: 2000, present: true},
		{raw: "0.5", millis: 500, present: true},
		{raw: "0", millis: 0, present: true},
		{raw: "1.5", millis: 1500, present: true},
		// Fractional millicores must be rejected, not rounded away.
		{raw: "1.5m", wantErr: true},
		{raw: "0.0005", wantErr: true},
		{raw: "-1", wantErr: true},
		{raw: "-100m", wantErr: true},
		{raw: "2c", wantErr: true},
		{raw: "abc", wantErr: true},
	}
	for _, tc := range cases {
		millis, present, err := ParseCPUMillis(tc.raw)
		if tc.wantErr {
			if err == nil {
				t.Errorf("ParseCPUMillis(%q) error = nil, want an error", tc.raw)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseCPUMillis(%q) error = %v", tc.raw, err)
			continue
		}
		if millis != tc.millis || present != tc.present {
			t.Errorf("ParseCPUMillis(%q) = (%d, %v), want (%d, %v)", tc.raw, millis, present, tc.millis, tc.present)
		}
	}
}

func TestParseMemoryBytes(t *testing.T) {
	cases := []struct {
		raw     string
		bytes   int64
		present bool
		wantErr bool
	}{
		{raw: "", present: false},
		{raw: "512Mi", bytes: 536870912, present: true},
		{raw: "1Gi", bytes: 1073741824, present: true},
		{raw: "4Gi", bytes: 4294967296, present: true},
		{raw: "1Ki", bytes: 1024, present: true},
		// Decimal and binary suffixes must not be conflated.
		{raw: "1M", bytes: 1000000, present: true},
		{raw: "1G", bytes: 1000000000, present: true},
		{raw: "1024", bytes: 1024, present: true},
		{raw: "0", bytes: 0, present: true},
		{raw: "-1Gi", wantErr: true},
		{raw: "512mi", wantErr: true},
		{raw: "4gi", wantErr: true},
		{raw: "abc", wantErr: true},
	}
	for _, tc := range cases {
		bytes, present, err := ParseMemoryBytes(tc.raw)
		if tc.wantErr {
			if err == nil {
				t.Errorf("ParseMemoryBytes(%q) error = nil, want an error", tc.raw)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseMemoryBytes(%q) error = %v", tc.raw, err)
			continue
		}
		if bytes != tc.bytes || present != tc.present {
			t.Errorf("ParseMemoryBytes(%q) = (%d, %v), want (%d, %v)", tc.raw, bytes, present, tc.bytes, tc.present)
		}
	}
}

func TestMemoryDecimalAndBinarySuffixesDiffer(t *testing.T) {
	decimal, _, err := ParseMemoryBytes("1M")
	if err != nil {
		t.Fatalf("ParseMemoryBytes(1M): %v", err)
	}
	binary, _, err := ParseMemoryBytes("1Mi")
	if err != nil {
		t.Fatalf("ParseMemoryBytes(1Mi): %v", err)
	}
	if decimal == binary {
		t.Fatalf("1M and 1Mi both parsed to %d; decimal and binary suffixes must differ", decimal)
	}
}

func TestQuantityFormattersRoundTrip(t *testing.T) {
	for _, raw := range []string{"100m", "1", "2", "500m", "1500m"} {
		millis, _, err := ParseCPUMillis(raw)
		if err != nil {
			t.Fatalf("ParseCPUMillis(%q): %v", raw, err)
		}
		reparsed, _, err := ParseCPUMillis(FormatCPUMillis(millis))
		if err != nil {
			t.Fatalf("reparse CPU %q: %v", raw, err)
		}
		if reparsed != millis {
			t.Errorf("CPU round-trip of %q = %d, want %d", raw, reparsed, millis)
		}
	}
	for _, raw := range []string{"512Mi", "1Gi", "4Gi", "1024", "0"} {
		bytes, _, err := ParseMemoryBytes(raw)
		if err != nil {
			t.Fatalf("ParseMemoryBytes(%q): %v", raw, err)
		}
		reparsed, _, err := ParseMemoryBytes(FormatMemoryBytes(bytes))
		if err != nil {
			t.Fatalf("reparse memory %q: %v", raw, err)
		}
		if reparsed != bytes {
			t.Errorf("memory round-trip of %q = %d, want %d", raw, reparsed, bytes)
		}
	}
}
