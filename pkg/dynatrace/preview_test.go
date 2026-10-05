package dynatrace

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestPreviewResumesFrozenWindowAndOmitsRunAssociation(t *testing.T) {
	start := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	executed, polled := 0, 0
	c := fixtureClient(t, func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "query:execute") {
			executed++
			return response(200, `{"state":"RUNNING","requestToken":"preview","ttlSeconds":60}`), nil
		}
		if !strings.HasSuffix(r.URL.Path, "query:poll") {
			t.Fatal("finished preview cancelled", r.URL)
		}
		polled++
		return response(200, `{"state":"SUCCEEDED","result":{"records":[{"n":7}],"metadata":{"grail":{"scannedBytes":12}}}}`), nil
	})
	s := Selection{ProjectID: "p", Grail: []GrailQuery{{Name: "n", Kind: "metric", Query: "fetch logs | summarize n=count()", ValueField: "n", Statistic: "sum", Unit: "Count"}}}
	result, err := c.Preview(t.Context(), s, start, start.Add(time.Minute))
	if err != nil || executed != 1 || polled != 1 || !result.Progress.Complete || len(result.Points) != 1 || *result.Points[0].Value != 7 || result.Points[0].RunID != "" || result.Progress.Cursor != "" || len(result.Progress.ScanReservationIDs) != 0 {
		t.Fatal(result, err, executed, polled)
	}
	if err := ValidateResult(s, c.cfg, start, start.Add(time.Minute), "", result); err != nil {
		t.Fatal(err)
	}
}

func TestPreviewDeadlineCancelsAndRemovesPendingHandle(t *testing.T) {
	start := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	cancelled := 0
	c := fixtureClient(t, func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "query:cancel") {
			cancelled++
			return response(202, ""), nil
		}
		return response(200, `{"state":"RUNNING","requestToken":"abandoned-preview","ttlSeconds":60}`), nil
	})
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	result, err := c.Preview(ctx, Selection{ProjectID: "p", Grail: []GrailQuery{{Name: "logs", Kind: "log", Query: "fetch logs"}}}, start, start.Add(time.Minute))
	if !errors.Is(err, context.DeadlineExceeded) || cancelled != 1 || result.Progress.Cursor != "" || result.Progress.CursorExpiresAt != nil || len(result.Progress.ScanReservationIDs) != 0 {
		t.Fatal(result, err, cancelled)
	}
}
