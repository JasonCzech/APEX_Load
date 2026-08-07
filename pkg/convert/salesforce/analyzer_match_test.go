package salesforce

import (
	"fmt"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// TestMatchHARPinsMatchingSemantics is a characterization test: it pins the exact
// ref -> HAR-entry assignments matchHAR produces so that the URL/body pre-parsing
// index cannot silently change matching results.
func TestMatchHARPinsMatchingSemantics(t *testing.T) {
	tests := []struct {
		name    string
		refs    []actionRef
		entries []harEntry
		want    map[int]int
	}{
		{
			name:    "no refs and no entries",
			refs:    nil,
			entries: nil,
			want:    map[int]int{},
		},
		{
			name: "exact url and method match",
			refs: []actionRef{
				{Method: "GET", URL: "https://example.my.salesforce.com/services/data/v59.0/sobjects/Account"},
			},
			entries: []harEntry{
				{ID: 1, Method: "GET", URL: "https://other.my.salesforce.com/a"},
				{ID: 2, Method: "GET", URL: "https://example.my.salesforce.com/services/data/v59.0/sobjects/Account"},
			},
			want: map[int]int{0: 2},
		},
		{
			name: "method comparison is case-insensitive",
			refs: []actionRef{
				{Method: "post", URL: "https://example.my.salesforce.com/aura"},
			},
			entries: []harEntry{
				{ID: 1, Method: "POST", URL: "https://example.my.salesforce.com/aura"},
			},
			want: map[int]int{0: 1},
		},
		{
			name: "method mismatch leaves the ref unmatched",
			refs: []actionRef{
				{Method: "GET", URL: "https://example.my.salesforce.com/aura"},
			},
			entries: []harEntry{
				{ID: 1, Method: "POST", URL: "https://example.my.salesforce.com/aura"},
			},
			want: map[int]int{},
		},
		{
			name: "scheme host case and default component normalization",
			refs: []actionRef{
				{Method: "GET", URL: "  HTTPS://EXAMPLE.my.Salesforce.com/services/data?q=1  "},
			},
			entries: []harEntry{
				{ID: 7, Method: "GET", URL: "https://example.my.salesforce.com/services/data?q=1"},
			},
			want: map[int]int{0: 7},
		},
		{
			name: "query string difference is not equivalent",
			refs: []actionRef{
				{Method: "GET", URL: "https://example.my.salesforce.com/services/data?q=1"},
			},
			entries: []harEntry{
				{ID: 1, Method: "GET", URL: "https://example.my.salesforce.com/services/data?q=2"},
			},
			want: map[int]int{},
		},
		{
			name: "unparseable urls only match on identical raw text",
			refs: []actionRef{
				{Method: "GET", URL: "https://example.com/%zz"},
				{Method: "GET", URL: "https://example.com/%yy"},
			},
			entries: []harEntry{
				{ID: 1, Method: "GET", URL: "https://example.com/%yy"},
				{ID: 2, Method: "GET", URL: "https://example.com/%zz"},
			},
			want: map[int]int{0: 2, 1: 1},
		},
		{
			name: "identical candidates: body match wins over document order",
			refs: []actionRef{
				{
					Method: "POST",
					URL:    "https://example.my.salesforce.com/aura",
					Action: &dsl.Action{Body: `{"message":"b"}`},
				},
			},
			entries: []harEntry{
				{ID: 1, Method: "POST", URL: "https://example.my.salesforce.com/aura", RequestBody: `{"message":"a"}`},
				{ID: 2, Method: "POST", URL: "https://example.my.salesforce.com/aura", RequestBody: `{ "message" : "b" }`},
			},
			want: map[int]int{0: 2},
		},
		{
			name: "equal scores keep the first candidate in document order",
			refs: []actionRef{
				{Method: "POST", URL: "https://example.my.salesforce.com/aura"},
			},
			entries: []harEntry{
				{ID: 5, Method: "POST", URL: "https://example.my.salesforce.com/aura"},
				{ID: 6, Method: "POST", URL: "https://example.my.salesforce.com/aura"},
			},
			want: map[int]int{0: 5},
		},
		{
			name: "descriptor substring bonus outranks document order",
			refs: []actionRef{
				{
					Method:     "POST",
					URL:        "https://example.my.salesforce.com/aura",
					Descriptor: "ListViewContainer",
				},
			},
			entries: []harEntry{
				{ID: 1, Method: "POST", URL: "https://example.my.salesforce.com/aura", RequestBody: `{"c":"other"}`},
				{ID: 2, Method: "POST", URL: "https://example.my.salesforce.com/aura", RequestBody: `{"c":"listviewcontainer"}`},
			},
			want: map[int]int{0: 2},
		},
		{
			name: "kind bonus outranks document order",
			refs: []actionRef{
				{
					Method: "POST",
					URL:    "https://example.my.salesforce.com/x",
					Kind:   "aura",
				},
			},
			entries: []harEntry{
				// Classifies as "generic" and comes first in document order, so it
				// wins the tie-break unless the kind bonus outranks it.
				{ID: 1, Method: "POST", URL: "https://example.my.salesforce.com/x"},
				// "aura.token" in the body classifies this entry as "aura", matching
				// the ref's kind. The ref carries no action body and no descriptor, so
				// the +10 kind bonus is the only term that separates the two.
				{ID: 2, Method: "POST", URL: "https://example.my.salesforce.com/x", RequestBody: `{"aura.token":"t"}`},
			},
			want: map[int]int{0: 2},
		},
		{
			name: "an entry is consumed by at most one ref",
			refs: []actionRef{
				{Method: "GET", URL: "https://example.my.salesforce.com/a"},
				{Method: "GET", URL: "https://example.my.salesforce.com/a"},
				{Method: "GET", URL: "https://example.my.salesforce.com/a"},
			},
			entries: []harEntry{
				{ID: 1, Method: "GET", URL: "https://example.my.salesforce.com/a"},
				{ID: 2, Method: "GET", URL: "https://example.my.salesforce.com/a"},
			},
			want: map[int]int{0: 1, 1: 2},
		},
		{
			name: "entries with a zero id are never recorded as a match",
			refs: []actionRef{
				{Method: "GET", URL: "https://example.my.salesforce.com/a"},
			},
			entries: []harEntry{
				{ID: 0, Method: "GET", URL: "https://example.my.salesforce.com/a"},
			},
			want: map[int]int{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := matchHAR(tc.refs, tc.entries)
			if len(got) != len(tc.want) {
				t.Fatalf("match count = %d (%v), want %d (%v)", len(got), got, len(tc.want), tc.want)
			}
			for refIndex, wantID := range tc.want {
				if got[refIndex] != wantID {
					t.Fatalf("ref %d matched entry %d, want %d (full map %v)", refIndex, got[refIndex], wantID, got)
				}
			}
		})
	}
}

// TestMatchHARLargeInputRemainsCorrect exercises the lookup index over an input
// large enough that a linear rescan per ref would dominate, and pins that every
// ref still lands on its own distinct entry.
func TestMatchHARLargeInputRemainsCorrect(t *testing.T) {
	const n = 400
	refs := make([]actionRef, 0, n)
	entries := make([]harEntry, 0, n)
	for i := 0; i < n; i++ {
		u := fmt.Sprintf("https://example.my.salesforce.com/services/data/v59.0/sobjects/Account/%d", i)
		refs = append(refs, actionRef{Method: "GET", URL: u})
		// Reverse order so a positional shortcut would not accidentally pass.
		entries = append(entries, harEntry{ID: n - i, Method: "GET", URL: fmt.Sprintf("https://example.my.salesforce.com/services/data/v59.0/sobjects/Account/%d", n-i-1)})
	}
	got := matchHAR(refs, entries)
	if len(got) != n {
		t.Fatalf("matched %d refs, want %d", len(got), n)
	}
	seen := map[int]bool{}
	for refIndex := 0; refIndex < n; refIndex++ {
		id, ok := got[refIndex]
		if !ok {
			t.Fatalf("ref %d unmatched", refIndex)
		}
		if seen[id] {
			t.Fatalf("entry %d matched twice", id)
		}
		seen[id] = true
		// entries[i] carries URL .../(n-i-1) and ID n-i, so the entry whose URL
		// is .../refIndex is the one with ID refIndex+1.
		wantID := refIndex + 1
		if id != wantID {
			t.Fatalf("ref %d matched entry %d, want %d", refIndex, id, wantID)
		}
	}
}

func BenchmarkMatchHAR(b *testing.B) {
	const n = 500
	refs := make([]actionRef, 0, n)
	entries := make([]harEntry, 0, n)
	for i := 0; i < n; i++ {
		u := fmt.Sprintf("https://example.my.salesforce.com/services/data/v59.0/sobjects/Account/%d?fields=Name,Id", i)
		refs = append(refs, actionRef{Method: "POST", URL: u, Action: &dsl.Action{Body: fmt.Sprintf(`{"seq":%d}`, i)}})
		entries = append(entries, harEntry{ID: i + 1, Method: "POST", URL: u, RequestBody: fmt.Sprintf(`{"seq":%d}`, i)})
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = matchHAR(refs, entries)
	}
}
