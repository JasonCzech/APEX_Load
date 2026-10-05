package dynatrace

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestGrailContinuationBindsOriginatingPrincipalAndCancellation(t *testing.T) {
	start := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	s := Selection{ProjectID: "p", Grail: []GrailQuery{{Name: "logs", Kind: "log", Query: "fetch logs"}}}
	cancels, polls := 0, 0
	c := fixtureClient(t, func(r *http.Request) (*http.Response, error) {
		switch {
		case strings.HasSuffix(r.URL.Path, "query:execute"):
			return response(200, `{"state":"RUNNING","requestToken":"private-token","ttlSeconds":60}`), nil
		case strings.HasSuffix(r.URL.Path, "query:cancel"):
			cancels++
			if r.Method != http.MethodPost || r.URL.Query().Get("request-token") != "private-token" {
				t.Fatal("cancellation contract", r.Method, r.URL)
			}
			return response(202, ""), nil
		default:
			polls++
			return response(200, `{"state":"SUCCEEDED","result":{"records":[],"metadata":{"grail":{"scannedBytes":0}}}}`), nil
		}
	})
	c.credentials.VersionIdentity = "frozen-version"
	c.now = func() time.Time { return start.Add(time.Hour) }
	result, err := c.Collect(t.Context(), s, start, start.Add(time.Minute), "run")
	if err != nil || result.Progress.Cursor == "" || !strings.Contains(result.Progress.Cursor, `"principal"`) {
		t.Fatal(result, err)
	}
	if err = c.Cancel(t.Context(), result.Progress.Cursor); err != nil || cancels != 1 {
		t.Fatal("cancel", err, cancels)
	}
	c.credentials.VersionIdentity = "rotated-version"
	if err = c.Cancel(t.Context(), result.Progress.Cursor); !errors.Is(err, ErrPrincipalChanged) {
		t.Fatal("changed principal cancellation accepted", err)
	}
	s.Cursor, s.QueryOffset = result.Progress.Cursor, result.Progress.NextQuery
	rotated, err := c.Collect(t.Context(), s, start, start.Add(time.Minute), "run")
	if err != nil || rotated.Coverage["grail:logs"].Status != "authentication_denied" || polls != 0 || cancels != 1 {
		t.Fatal("changed principal polled", rotated, err, polls, cancels)
	}
	c.credentials.VersionIdentity = "frozen-version"
	c.cfg.PlatformURL = "https://other.apps.dynatrace.com"
	if err = c.Cancel(t.Context(), result.Progress.Cursor); !errors.Is(err, ErrPrincipalChanged) {
		t.Fatal("changed origin cancellation accepted", err)
	}
}

func TestGrailAbandonedPollIsCancelledWhileNormalPendingSurvives(t *testing.T) {
	start := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	s := Selection{ProjectID: "p", Grail: []GrailQuery{{Name: "logs", Kind: "log", Query: "fetch logs"}}}
	cancelled := 0
	c := fixtureClient(t, func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "query:execute") {
			return response(200, `{"state":"RUNNING","requestToken":"private-token","ttlSeconds":60}`), nil
		}
		if strings.HasSuffix(r.URL.Path, "query:cancel") {
			cancelled++
			return response(202, ""), nil
		}
		return nil, context.Canceled
	})
	c.now = func() time.Time { return start.Add(time.Hour) }
	first, err := c.Collect(t.Context(), s, start, start.Add(time.Minute), "run")
	if err != nil || cancelled != 0 || first.Progress.Cursor == "" {
		t.Fatal("normal pending query abandoned", first, err)
	}
	s.Cursor, s.QueryOffset = first.Progress.Cursor, first.Progress.NextQuery
	second, err := c.Collect(t.Context(), s, start, start.Add(time.Minute), "run")
	if err != nil || cancelled != 1 || second.Coverage["grail:logs"].Status == "complete" {
		t.Fatal("failed poll was not cancelled", second, err, cancelled)
	}
}

func TestSetupDeadlineCancelsPendingGrailQuery(t *testing.T) {
	cancelled := 0
	c := fixtureClient(t, func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "query:cancel") {
			cancelled++
			if r.URL.Query().Get("request-token") != "setup-pending" {
				t.Fatal("wrong abandoned token", r.URL)
			}
			return response(202, ""), nil
		}
		return response(200, `{"state":"RUNNING","requestToken":"setup-pending","ttlSeconds":60}`), nil
	})
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	caps, err := c.TestSelection(ctx, Selection{ProjectID: "p", Grail: []GrailQuery{{Name: "logs", Kind: "log", Query: "fetch logs"}}})
	if err != nil || caps.Logs || cancelled < 1 {
		t.Fatal("setup left an unowned query token", caps, err, cancelled)
	}
}
