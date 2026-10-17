package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func withRCAWaitPoll(t *testing.T, interval time.Duration) {
	t.Helper()
	oldPoll := rcaWaitPoll
	rcaWaitPoll = interval
	t.Cleanup(func() { rcaWaitPoll = oldPoll })
}

// runningThen answers the first rca request with a running analysis and every
// later poll with next.
func runningThen(next http.HandlerFunc) (*httptest.Server, *atomic.Int32) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"analysis_id":"a1","state":"running"}`)
			return
		}
		next(w, r)
	}))
	return server, &calls
}

func TestRCAWaitReportsAFailedPollAsAControllerFailure(t *testing.T) {
	withRCAWaitPoll(t, time.Millisecond)
	for _, tc := range []struct {
		name string
		poll http.HandlerFunc
		want string
	}{
		{"server error", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"error":"archive store unavailable"}`)
		}, "archive store unavailable"},
		{"unreadable response", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, "<html>gateway error</html>")
		}, "unreadable response"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, calls := runningThen(tc.poll)
			defer server.Close()
			var output bytes.Buffer
			err := executeWorkflowCommand(context.Background(), "rca", []string{"analyze", "--id", "run", "--wait", "1m", "--api", server.URL}, &output)
			var verdict *verdictError
			if !errors.As(err, &verdict) || verdict.code != exitCodeControllerOrTransport || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want a controller or transport failure naming %q", err, tc.want)
			}
			if output.Len() != 0 || calls.Load() != 2 {
				t.Fatalf("output %q after %d requests; want no stale state printed", output.String(), calls.Load())
			}
		})
	}
}

func TestRCAWaitReturnsCancellation(t *testing.T) {
	t.Run("during a poll", func(t *testing.T) {
		withRCAWaitPoll(t, time.Millisecond)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		server, _ := runningThen(func(_ http.ResponseWriter, r *http.Request) {
			cancel()
			<-r.Context().Done()
		})
		defer server.Close()
		var output bytes.Buffer
		err := executeWorkflowCommand(ctx, "rca", []string{"analyze", "--id", "run", "--wait", "1m", "--api", server.URL}, &output)
		var verdict *verdictError
		if !errors.Is(err, context.Canceled) || errors.As(err, &verdict) || output.Len() != 0 {
			t.Fatalf("error = %v, output %q; want the cancellation itself", err, output.String())
		}
	})
	t.Run("between polls", func(t *testing.T) {
		withRCAWaitPoll(t, time.Minute)
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		server, calls := runningThen(func(http.ResponseWriter, *http.Request) {})
		defer server.Close()
		var output bytes.Buffer
		err := executeWorkflowCommand(ctx, "rca", []string{"analyze", "--id", "run", "--wait", "1m", "--api", server.URL}, &output)
		if !errors.Is(err, context.DeadlineExceeded) || output.Len() != 0 || calls.Load() != 1 {
			t.Fatalf("error = %v, output %q after %d requests; want the deadline itself", err, output.String(), calls.Load())
		}
	})
}
