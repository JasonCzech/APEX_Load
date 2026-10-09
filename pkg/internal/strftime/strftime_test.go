package strftime

import (
	"testing"
	"time"
)

func TestFormatAndParse(t *testing.T) {
	moment := time.Date(2026, time.March, 7, 14, 5, 9, 0, time.UTC)
	tests := []struct {
		format, want string
	}{
		{"%Y-%m-%d %H:%M:%S", "2026-03-07 14:05:09"},
		{"%d.%m.%Y T1", "07.03.2026 T1"},
		{"%d/%b/%y %I:%M %p", "07/Mar/26 02:05 PM"},
		{"%A, %B %e %Y 100%%", "Saturday, March  7 2026 100%"},
		{"%j", "066"},
	}
	for _, tt := range tests {
		got, err := Format(moment, tt.format)
		if err != nil || got != tt.want {
			t.Errorf("Format(%q) = %q, %v; want %q", tt.format, got, err, tt.want)
			continue
		}
		parsed, err := Parse(got, tt.format, time.UTC)
		if err != nil {
			t.Errorf("Parse(%q, %q): %v", got, tt.format, err)
			continue
		}
		if again, _ := Format(parsed, tt.format); again != got {
			t.Errorf("round trip of %q via %q = %q", got, tt.format, again)
		}
	}
	if _, err := Parse("31/02/2026", "%d/%m/%Y", time.UTC); err == nil {
		t.Error("an impossible date must not parse")
	}
	if _, err := Parse("2026-03-07", "%d/%m/%Y", time.UTC); err == nil {
		t.Error("text that does not match the format must not parse")
	}
	for _, day := range []string{"2026 000", "2026 366", "2026 400"} {
		if _, err := Parse(day, "%Y %j", time.UTC); err == nil {
			t.Errorf("%q: a day outside the year must not parse", day)
		}
	}
	if parsed, err := Parse("2024 366", "%Y %j", time.UTC); err != nil || parsed.Format("2006-01-02") != "2024-12-31" {
		t.Errorf("leap-year day 366 = %v, %v", parsed, err)
	}
	if Supported("%Y-%Q") || !Supported("%Y-%m-%d") {
		t.Error("Supported disagrees with the specifier table")
	}
}
