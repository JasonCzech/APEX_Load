package correlate

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func surfaceIndexObservations() []Observation {
	return []Observation{
		{ID: 10, RequestURL: "https://example.test/a?token=abc%2Fdef", RequestHeaders: map[string]string{"Cookie": "sid=cookie-only-value"}},
		{ID: 11}, // no request surfaces at all
		{ID: 12, RequestBody: "message=%7B%22id%22%3A%22rec-123456%22%7D&aura.token=tok-987654"},
		{ID: 13, RequestHeaders: map[string]string{"X-Trace": "abc/def", "Authorization": "Bearer auth-only-value"}},
		{ID: 14, RequestURL: "https://example.test/b", RequestBody: "plain body with rec-123456 inside"},
	}
}

func TestUsedAfterMatchesContainsOnEveryObservation(t *testing.T) {
	observations := surfaceIndexObservations()
	for _, value := range []string{"abc/def", "abc%2Fdef", "rec-123456", "tok-987654", "aura.token", "cookie-only-value", "auth-only-value", "missing", "e"} {
		for index := -1; index < len(observations); index++ {
			want := make([]int, 0)
			linear := NewRequestSearch(observations)
			for later := index + 1; later < len(observations); later++ {
				if linear.Contains(later, value) {
					want = append(want, observations[later].ID)
				}
			}
			got, err := NewRequestSearch(observations).UsedAfter(index, value, func(int64) error { return nil })
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Errorf("UsedAfter(%d, %q) = %v, %v; want %v", index, value, got, err, want)
			}
		}
	}
}

func TestClientSentThroughIndexSkipsCookieAndAuthorization(t *testing.T) {
	requests := NewRequestSearch(surfaceIndexObservations())
	free := func(int64) error { return nil }
	for _, tc := range []struct {
		value   string
		through int
		want    bool
	}{
		{"cookie-only-value", 4, false},
		{"auth-only-value", 4, false},
		{"rec-123456", 1, false},
		{"rec-123456", 2, true},
		{"abc/def", 0, true},
	} {
		if got, err := requests.ClientSentThrough(tc.through, tc.value, free); err != nil || got != tc.want {
			t.Errorf("ClientSentThrough(%d, %q) = %v, %v; want %v", tc.through, tc.value, got, err, tc.want)
		}
	}
}

func TestSurfaceIndexChargesLookupWorkAndFallsBack(t *testing.T) {
	requests := NewRequestSearch(surfaceIndexObservations())
	var charged []int64
	charge := func(cost int64) error { charged = append(charged, cost); return nil }
	if _, err := requests.UsedAfter(-1, "rec-123456", charge); err != nil {
		t.Fatal(err)
	}
	// The value's length, then its matches: observation 12's raw body,
	// decoded body and parsed message value, and observation 14's raw body
	// and the form key that body parses as.
	if !reflect.DeepEqual(charged, []int64{10, 5}) {
		t.Fatalf("charged %v, want the value length then its match count", charged)
	}

	// A value holding the separator byte is searched observation by
	// observation, charging each one's surface bytes.
	charged = nil
	if got, err := requests.UsedAfter(-1, "rec\x00123", charge); err != nil || len(got) != 0 {
		t.Fatalf("separator value = %v, %v", got, err)
	}
	if len(charged) != len(surfaceIndexObservations()) {
		t.Fatalf("fallback charged %d observations, want every one", len(charged))
	}
	stop := fmt.Errorf("budget")
	if _, err := requests.UsedAfter(-1, "rec-123456", func(int64) error { return stop }); err != stop {
		t.Fatalf("charge error = %v, want it returned", err)
	}
}

func TestSurfaceIndexIsSkippedForOversizedSurfaces(t *testing.T) {
	observations := []Observation{{ID: 1, RequestBody: strings.Repeat("x", maxIndexedSurfaceBytes)}}
	if index := NewRequestSearch(observations).indexed(false); index != nil {
		t.Fatal("surfaces above the index limit must not be indexed")
	}
}
