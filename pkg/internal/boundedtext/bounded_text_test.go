package boundedtext

import (
	"reflect"
	"testing"
	"unicode/utf8"
)

func TestTruncateUTF8(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		limit int
		want  string
	}{
		{name: "ascii", value: "abcdef", limit: 3, want: "abc"},
		{name: "rune boundary", value: "a€b", limit: 3, want: "a"},
		{name: "exact rune", value: "a€b", limit: 4, want: "a€"},
		{name: "invalid replaced", value: "a\xffb", limit: 5, want: "a�b"},
		{name: "invalid replacement does not fit", value: "a\xffb", limit: 3, want: "a"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := TruncateUTF8(tc.value, tc.limit)
			if got != tc.want || len(got) > tc.limit || !utf8.ValidString(got) {
				t.Fatalf("TruncateUTF8(%q, %d) = %q", tc.value, tc.limit, got)
			}
		})
	}
}

func TestSmallestStringKeysIsDeterministicAndBounded(t *testing.T) {
	values := map[string]int{"z": 1, "b": 2, "d": 3, "a": 4, "c": 5}
	want := []string{"a", "b", "c"}
	for i := 0; i < 10; i++ {
		if got := SmallestStringKeys(values, 3); !reflect.DeepEqual(got, want) {
			t.Fatalf("SmallestStringKeys = %v, want %v", got, want)
		}
	}
}
