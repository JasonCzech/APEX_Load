package prometheus

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRetryAfterIsBoundedAndAcceptsHTTPDates(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		header string
		want   time.Duration
	}{
		{"", 0}, {"0", 0}, {"2", 2 * time.Second}, {" 15 ", 15 * time.Second},
		{"120", 30 * time.Second}, {"99999999999999999999999999999", 30 * time.Second},
		{"-1", 0}, {"1.5", 0}, {"invalid", 0},
		{now.Add(12 * time.Second).Format(http.TimeFormat), 12 * time.Second},
		{now.Add(time.Hour).Format(http.TimeFormat), 30 * time.Second},
		{now.Add(-time.Second).Format(http.TimeFormat), 0},
	} {
		if got := retryAfter(tc.header, now); got != tc.want {
			t.Errorf("Retry-After %q = %v, want %v", tc.header, got, tc.want)
		}
	}
}

func TestSharedThrottleBackoffAndSuccessReset(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	rate := &rateState{inflight: make(chan struct{}, 1), skipWait: true}
	for _, want := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second, 30 * time.Second} {
		rate.deferResponse(http.StatusTooManyRequests, "", now)
		if got := rate.next.Sub(now); got != want {
			t.Fatalf("consecutive 429 backoff = %v, want %v", got, want)
		}
		now = now.Add(time.Minute)
	}
	// Server delay can increase backoff, but cannot remove a reserved quota slot.
	rate.resetThrottling()
	rate.next = now.Add(25 * time.Second)
	rate.deferResponse(http.StatusTooManyRequests, "12", now)
	if !rate.next.Equal(now.Add(25 * time.Second)) {
		t.Fatal("throttling shortened an existing rate reservation")
	}
	now = now.Add(time.Minute)
	rate.deferResponse(http.StatusServiceUnavailable, now.Add(18*time.Second).Format(http.TimeFormat), now)
	if !rate.next.Equal(now.Add(18*time.Second)) || rate.throttles != 1 {
		t.Fatal("503 Retry-After was not honored independently of 429 backoff")
	}
	before := rate.next
	for _, status := range []int{http.StatusBadRequest, http.StatusUnprocessableEntity, http.StatusUnauthorized} {
		rate.deferResponse(status, "30", now)
		if !rate.next.Equal(before) || rate.throttles != 1 {
			t.Fatal("query-budget/authentication rejection entered transient backoff")
		}
	}
	c := fixture(t, Config{}, func(*http.Request) (*http.Response, error) {
		return success(map[string]any{"resultType": "vector", "result": []any{}}), nil
	})
	c.rate = rate
	if _, err := c.query(context.Background(), "/api/v1/query", url.Values{"query": {"vector(1)"}}); err != nil {
		t.Fatal(err)
	}
	if rate.throttles != 0 || !rate.next.Equal(before) {
		t.Fatal("successful response must reset consecutive throttling without clearing quota reservations")
	}
	now = now.Add(time.Minute)
	rate.deferResponse(http.StatusTooManyRequests, "", now)
	if !rate.next.Equal(now.Add(time.Second)) {
		t.Fatal("successful query did not reset exponential backoff")
	}
}

func TestThrottleDeadlineSharedAcrossClientsAndIdentityIsolated(t *testing.T) {
	cfg := Config{Endpoint: "https://retry-after-fixture.example", AuthType: AuthBearer}.Defaults()
	transport := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusTooManyRequests, Header: http.Header{"Retry-After": {"120"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	makeClient := func(cred Credentials) *Client {
		t.Helper()
		c, err := NewClientWithHTTPClient(cfg, cred, transport)
		if err != nil {
			t.Fatal(err)
		}
		c.rate = sharedRate(cfg, cred)
		return c
	}
	c1, c2, other := makeClient(Credentials{Token: "identity-a"}), makeClient(Credentials{Token: "identity-a"}), makeClient(Credentials{Token: "identity-b"})
	if c1.rate != c2.rate || c1.rate == other.rate {
		t.Fatal("quota identity does not share/isolate rate state correctly")
	}
	_, err := c1.query(context.Background(), "/api/v1/query", url.Values{"query": {"up"}})
	var upstream *HTTPError
	if !errors.As(err, &upstream) || upstream.Status != 429 || upstream.RetryAfter != 30*time.Second {
		t.Fatalf("bounded throttling error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := c2.permit(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second client ignored shared Retry-After: %v", err)
	}
	if len(c2.rate.inflight) != 0 {
		t.Fatal("deadline rejection leaked the shared concurrency permit")
	}
	release, err := other.permit(ctx)
	if err != nil {
		t.Fatalf("unrelated identity inherited throttle: %v", err)
	}
	release()
	otherEndpoint := cfg
	otherEndpoint.Endpoint = "https://retry-after-other.example"
	if sharedRate(otherEndpoint, Credentials{Token: "identity-a"}) == c1.rate {
		t.Fatal("unrelated endpoint inherited throttle")
	}
}

func TestThrottleGateCancellationAndConcurrentUpdates(t *testing.T) {
	r := &rateState{inflight: make(chan struct{}, 1)}
	c := &Client{cfg: Config{}.Defaults(), rate: r}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.permit(ctx); !errors.Is(err, context.Canceled) || len(r.inflight) != 0 {
		t.Fatalf("canceled request acquired a permit: %v", err)
	}
	// Cancellation also interrupts a request blocked behind another in-flight
	// request, without removing the other request's permit.
	r.inflight <- struct{}{}
	ctx, cancel = context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { _, err := c.permit(ctx); result <- err }()
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) || len(r.inflight) != 1 {
		t.Fatalf("in-flight wait cancellation = %v", err)
	}
	<-r.inflight
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); r.deferResponse(429, "", now) }()
	}
	wg.Wait()
	if !r.next.Equal(now.Add(30*time.Second)) || r.throttles != 6 {
		t.Fatalf("shared concurrent throttle was not bounded: %+v", r)
	}
}

func TestSampleBudgetErrorDoesNotThrottle(t *testing.T) {
	c := fixture(t, Config{}, func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 422, Header: http.Header{"Retry-After": {"30"}}, Body: io.NopCloser(strings.NewReader("maximum sample limit exceeded"))}, nil
	})
	_, err := c.query(context.Background(), "/api/v1/query", url.Values{"query": {"up"}})
	var upstream *HTTPError
	if !errors.As(err, &upstream) || upstream.Kind != "sample budget exceeded" || !c.rate.next.IsZero() || c.rate.throttles != 0 {
		t.Fatalf("sample-budget rejection became transient throttling: %v", err)
	}
}
