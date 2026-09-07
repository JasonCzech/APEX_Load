package http

import "testing"

func TestSnapshotResponseBodyStates(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		body, want string
	}{
		{"successful body", 200, "ok", "captured"}, {"service unavailable", 503, "retry later", "captured"},
		{"empty response", 204, "", "empty"}, {"network failure", 0, "", "not_captured"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := buildHTTPSnapshot("GET", "https://example.test", nil, nil, "", nil, nil, tc.status, nil, []byte(tc.body))
			if got.ResponseBodyState != tc.want || got.ResponseBody != tc.body {
				t.Fatalf("snapshot = %+v", got)
			}
		})
	}
}
