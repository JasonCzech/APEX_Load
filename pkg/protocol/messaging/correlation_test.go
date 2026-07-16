package messaging

import "testing"

func TestCorrelationMatches(t *testing.T) {
	cases := []struct {
		expected string
		reply    string
		want     bool
	}{
		{"abc", "abc", true},
		{"abc", "xyz", false},
		{"", "abc", true}, // no expectation -> permissive
		{"abc", "", true}, // reply omits correlation -> permissive
		{"", "", true},    // both empty -> permissive
		{"abc", "abcd", false},
	}
	for _, c := range cases {
		if got := CorrelationMatches(c.expected, c.reply); got != c.want {
			t.Errorf("CorrelationMatches(%q,%q) = %v, want %v", c.expected, c.reply, got, c.want)
		}
	}
}
