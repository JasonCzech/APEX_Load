package correlate

import (
	"errors"
	"testing"
)

func TestRequestSearchMatchesObservationContains(t *testing.T) {
	observations := []Observation{
		{ID: 1, RequestURL: "https://app.example.test/a?token=abc%2Fdef", RequestBody: "name=first+value&id=42"},
		{ID: 2, RequestHeaders: map[string]string{"X-Session": "sess-123"}},
	}
	requests := NewRequestSearch(observations)
	for _, tc := range []struct {
		index int
		value string
	}{
		{0, "abc/def"},     // query-decoded surface
		{0, "first value"}, // parsed form value
		{0, "token=abc%2F"},
		{1, "sess-123"},
		{1, "missing"},
	} {
		if got, want := requests.Contains(tc.index, tc.value), ObservationContains(observations[tc.index], tc.value); got != want {
			t.Errorf("Contains(%d, %q) = %v, want %v", tc.index, tc.value, got, want)
		}
	}
	if requests.SearchBytes(0) <= int64(len(observations[0].RequestURL)) {
		t.Errorf("SearchBytes(0) = %d, want raw plus decoded surfaces", requests.SearchBytes(0))
	}
}

func TestRequestSearchClientSentThrough(t *testing.T) {
	observations := []Observation{
		{ID: 1, RequestHeaders: map[string]string{"Cookie": "sid=cookie-value", "Authorization": "Bearer auth-value"}},
		{ID: 2, RequestURL: "https://app.example.test/b?v=url-value", RequestHeaders: map[string]string{"User-Agent": "agent/18.5.1"}},
		{ID: 3, RequestBody: "later-value"},
	}
	requests := NewRequestSearch(observations)
	unlimited := func(int64) error { return nil }
	for _, tc := range []struct {
		through int
		value   string
		want    bool
	}{
		{2, "cookie-value", false}, // Cookie carries server-issued state
		{2, "auth-value", false},   // so does Authorization
		{1, "url-value", true},
		{1, "18.5.1", true}, // other headers are client-authored
		{0, "url-value", false},
		{1, "later-value", false}, // after the through index
		{2, "later-value", true},  // the producer's own request counts
		{2, "", false},
	} {
		got, err := requests.ClientSentThrough(tc.through, tc.value, unlimited)
		if err != nil || got != tc.want {
			t.Errorf("ClientSentThrough(%d, %q) = %v, %v; want %v", tc.through, tc.value, got, err, tc.want)
		}
	}

	exhausted := errors.New("budget exhausted")
	calls := 0
	_, err := requests.ClientSentThrough(2, "absent", func(int64) error {
		calls++
		if calls == 2 {
			return exhausted
		}
		return nil
	})
	if !errors.Is(err, exhausted) || calls != 2 {
		t.Fatalf("charge error = %v after %d calls, want the charge error to stop the scan", err, calls)
	}
}
