package hostpattern

import "testing"

func TestMatch(t *testing.T) {
	for _, tc := range []struct {
		host, entry string
		want        bool
	}{
		{"t.co", "t.co", true},
		{"x.t.co", "t.co", true},
		{"microsoft.com", "t.co", false},
		{"mint.co", "t.co", false},
		{"ad.doubleclick.net", "doubleclick.net", true},
		{"ad.doubleclick.net", "*.doubleclick.net", true},
		{"doubleclick.net", "*.doubleclick.net", true},
		{"notdoubleclick.net", "*.doubleclick.net", false},
		{"WWW.Example.COM.", "example.com", true},
		{"example.com:443", "example.com", true},
		{"example.com:8443", "example.com:443", false},
		{"localhost:8080", "localhost:8080", true},
		{"[::1]:8080", "::1", true},
		{"cdn.example.com", "https://cdn.example.com/assets/", true},
		{"example.com", "", false},
		{"example.com", "*.", false},
	} {
		if got := Match(tc.host, tc.entry); got != tc.want {
			t.Errorf("Match(%q, %q) = %v, want %v", tc.host, tc.entry, got, tc.want)
		}
	}
	if got := Index("ad.doubleclick.net", []string{"t.co", "doubleclick.net"}); got != 1 {
		t.Errorf("Index = %d, want 1", got)
	}
	if got := Index("example.com", []string{"t.co"}); got != -1 {
		t.Errorf("Index = %d, want -1", got)
	}
}
