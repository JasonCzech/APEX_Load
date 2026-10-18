package archiveingest

import (
	"errors"
	"testing"
)

func TestReadZIPClassifiesLimitRefusals(t *testing.T) {
	cases := []struct {
		name    string
		entries []zipFixtureEntry
		limits  Limits
	}{
		{"entry", []zipFixtureEntry{{name: "a", body: "12345"}}, Limits{MaxEntryBytes: 4}},
		{"total", []zipFixtureEntry{{name: "a", body: "123"}, {name: "b", body: "456"}}, Limits{MaxEntryBytes: 4, MaxTotalBytes: 5}},
		{"count", []zipFixtureEntry{{name: "a"}, {name: "b"}}, Limits{MaxEntries: 1}},
		{"depth", []zipFixtureEntry{{name: "a/b/c"}}, Limits{MaxDepth: 2}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ReadZIP(zipFixture(t, tc.entries), Options{Limits: tc.limits})
			if !errors.Is(err, ErrLimitExceeded) {
				t.Fatalf("limit error not classified: %v", err)
			}
		})
	}
	if _, err := ReadZIP([]byte("not a zip"), Options{}); err == nil || errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("malformed archive classified as limit: %v", err)
	}
	if _, err := ReadZIP(zipFixture(t, []zipFixtureEntry{{name: "a", body: "1234"}}), Options{Limits: Limits{MaxEntryBytes: 4}}); err != nil {
		t.Fatalf("exact boundary refused: %v", err)
	}
}
