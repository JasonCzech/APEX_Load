package ciclient

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"sync"
	"testing"
	"time"
)

type response struct {
	ID           string
	Terminal     bool
	Transactions int
}
type statusError struct {
	code       int
	retryAfter time.Duration
}

func (e *statusError) Error() string { return fmt.Sprintf("HTTP %d", e.code) }
func classify(err error) Failure {
	var status *statusError
	if errors.As(err, &status) {
		return Failure{HTTPStatus: status.code, RetryAfter: status.retryAfter}
	}
	return Failure{}
}

type authStub struct {
	ensure  func(context.Context) error
	refresh func() bool
}

func (a authStub) Ensure(ctx context.Context) error {
	if a.ensure != nil {
		return a.ensure(ctx)
	}
	return nil
}
func (a authStub) RefreshUnauthorized() bool { return a.refresh != nil && a.refresh() }
func coordinator() *Coordinator[response] {
	return &Coordinator[response]{Retry: RetryPolicy{Attempts: 4}, AbortTimeout: time.Second,
		Classify: classify, Transport: Transport[response]{Inspect: func(r response) Observation { return Observation{TestID: r.ID, Terminal: r.Terminal} }}}
}

func TestRunCancellationPreservesEvidenceBeforeIndependentAbort(t *testing.T) {
	for _, stage := range []string{"before create", "create response lost", "poll", "retry wait", "poll wait", "poll authentication"} {
		for _, abort := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/abort=%v", stage, abort), func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				c := coordinator()
				var order []string
				c.Transport.Create = func(ctx context.Context) (response, error) {
					if stage == "create response lost" {
						cancel()
						return response{}, ctx.Err()
					}
					return response{ID: "accepted", Transactions: 7}, nil
				}
				c.Transport.Get = func(ctx context.Context, _ string) (response, error) {
					if stage == "retry wait" {
						return response{}, &statusError{code: 503}
					}
					cancel()
					return response{}, ctx.Err()
				}
				waits := 0
				c.Wait = func(ctx context.Context, _ time.Duration) error {
					waits++
					if stage == "poll wait" || stage == "retry wait" && waits == 2 {
						cancel()
						return ctx.Err()
					}
					return nil
				}
				if stage == "poll authentication" {
					ensures := 0
					c.Auth = authStub{ensure: func(ctx context.Context) error {
						ensures++
						if ensures == 2 {
							cancel()
							return ctx.Err()
						}
						return nil
					}}
				}
				wantID, wantTransactions := "accepted", 7
				if stage == "before create" || stage == "create response lost" {
					wantID, wantTransactions = "deterministic", 0
				}
				if stage == "before create" {
					cancel()
				}
				c.BeforeAbort = func(state State[response], err error) {
					order = append(order, "evidence")
					if state.TestID != wantID || state.Response.Transactions != wantTransactions || !errors.Is(err, context.Canceled) {
						t.Errorf("evidence state=%+v err=%v", state, err)
					}
				}
				c.Transport.Abort = func(ctx context.Context, id, reason string) (bool, error) {
					if ctx.Err() != nil || id != wantID || reason != "cancelled" || !reflect.DeepEqual(order, []string{"evidence"}) {
						t.Errorf("abort ctx=%v id=%s reason=%s order=%v", ctx.Err(), id, reason, order)
					}
					if _, ok := ctx.Deadline(); !ok {
						t.Error("abort has no deadline")
					}
					order = append(order, "abort")
					return true, nil
				}
				state, err := c.Run(ctx, "deterministic", abort)
				if !errors.Is(err, context.Canceled) || state.TestID != wantID || state.Response.Transactions != wantTransactions {
					t.Fatalf("state=%+v err=%v", state, err)
				}
				wantOrder := []string{"evidence"}
				if abort {
					wantOrder = append(wantOrder, "abort")
				}
				if !reflect.DeepEqual(order, wantOrder) {
					t.Fatalf("order=%v want=%v", order, wantOrder)
				}
			})
		}
	}
}

func TestRunDeadlineKeepsPrimaryCauseWhenAbortFails(t *testing.T) {
	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()
	c := coordinator()
	c.Transport.Create = func(context.Context) (response, error) { t.Fatal("create after deadline"); return response{}, nil }
	abortFailure := errors.New("abort refused")
	c.Transport.Abort = func(ctx context.Context, id, reason string) (bool, error) {
		if ctx.Err() != nil || reason != "timed_out" {
			t.Errorf("abort ctx=%v reason=%s", ctx.Err(), reason)
		}
		return false, abortFailure
	}
	c.Classify = func(error) Failure { return Failure{Permanent: true} }
	state, err := c.Run(ctx, "deterministic", true)
	if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, abortFailure) || state.Cancellation != context.DeadlineExceeded {
		t.Fatalf("state=%+v err=%v", state, err)
	}
}

func TestObservationFailureNeverAbortsAndKeepsLastResponse(t *testing.T) {
	c := coordinator()
	c.Retry.Attempts = 1
	c.Transport.Create = func(context.Context) (response, error) { return response{ID: "run", Transactions: 9}, nil }
	failure := &statusError{code: 500}
	c.Transport.Get = func(context.Context, string) (response, error) { return response{}, failure }
	c.Transport.Abort = func(context.Context, string, string) (bool, error) {
		t.Fatal("observation failure aborted")
		return false, nil
	}
	evidence := false
	c.OnObservationFailure = func(state State[response], err error) {
		evidence = true
		if state.Response.Transactions != 9 || err != failure {
			t.Errorf("evidence=%+v err=%v", state, err)
		}
	}
	state, err := c.Run(t.Context(), "initial", true)
	if err != failure || !evidence || state.Cancellation != nil || state.Response.Transactions != 9 {
		t.Fatalf("state=%+v err=%v evidence=%v", state, err, evidence)
	}
}

func TestRunTerminalObservationAndPostRunEvidenceWait(t *testing.T) {
	c := coordinator()
	c.Transport.Create = func(context.Context) (response, error) {
		return response{ID: "run", Terminal: true, Transactions: 1}, nil
	}
	state, err := c.Run(t.Context(), "initial", true)
	if err != nil || !state.Response.Terminal {
		t.Fatalf("state=%+v err=%v", state, err)
	}
	calls := 0
	c.Transport.Get = func(context.Context, string) (response, error) {
		calls++
		if calls == 1 {
			return response{ID: "run", Terminal: true, Transactions: 2}, nil
		}
		return response{}, &statusError{code: 403}
	}
	latest := c.WaitFor(t.Context(), state.Response, time.Minute, func(r response) bool { return r.Transactions < 3 })
	if latest.Transactions != 2 || calls != 2 {
		t.Fatalf("latest=%+v calls=%d", latest, calls)
	}
	c.Transport.Get = func(context.Context, string) (response, error) {
		t.Fatal("settled evidence was polled")
		return response{}, nil
	}
	c.WaitFor(t.Context(), latest, time.Minute, func(response) bool { return false })
}

func TestAbortRetriesFailoverAndRequiresConfirmation(t *testing.T) {
	c := coordinator()
	aborts, polls := 0, 0
	c.Transport.Abort = func(context.Context, string, string) (bool, error) {
		aborts++
		if aborts == 1 {
			return false, &statusError{code: 404}
		}
		if aborts == 2 {
			return false, &statusError{code: 503}
		}
		return false, nil
	}
	c.Transport.Get = func(context.Context, string) (response, error) {
		polls++
		return response{ID: "run", Terminal: polls == 2}, nil
	}
	if err := c.Abort(t.Context(), "run", "cancelled"); err != nil {
		t.Fatal(err)
	}
	if aborts != 4 || polls != 2 {
		t.Fatalf("aborts=%d polls=%d", aborts, polls)
	}
}

func TestRequestRefreshesUnauthorizedAtMostOnce(t *testing.T) {
	for _, refreshable := range []bool{false, true} {
		t.Run(fmt.Sprint(refreshable), func(t *testing.T) {
			c := coordinator()
			refreshes, calls := 0, 0
			c.Auth = authStub{refresh: func() bool {
				if refreshable {
					refreshes++
				}
				return refreshable
			}}
			_, err := c.Request(t.Context(), func() (response, error) { calls++; return response{}, &statusError{code: 401} })
			wantCalls := 1
			if refreshable {
				wantCalls = 2
			}
			if err == nil || calls != wantCalls || refreshes != wantCalls-1 {
				t.Fatalf("calls=%d refreshes=%d err=%v", calls, refreshes, err)
			}
		})
	}
}

func TestRequestClassifiesAuthFailureAndHonorsRetryBudget(t *testing.T) {
	c := coordinator()
	now := time.Now()
	c.Now = func() time.Time { return now }
	c.Retry = RetryPolicy{Attempts: 10, BaseDelay: 20 * time.Millisecond, MaxDelay: time.Second, Window: 30 * time.Millisecond}
	var waits []time.Duration
	c.Wait = func(_ context.Context, d time.Duration) error { waits = append(waits, d); now = now.Add(d); return nil }
	calls := 0
	c.Auth = authStub{ensure: func(context.Context) error { calls++; return &statusError{code: 503} }}
	_, err := c.Request(t.Context(), func() (response, error) { t.Fatal("operation before authentication"); return response{}, nil })
	var authErr *AuthError
	if !errors.As(err, &authErr) || calls != 3 || !reflect.DeepEqual(waits, []time.Duration{20 * time.Millisecond, 10 * time.Millisecond}) {
		t.Fatalf("err=%v calls=%d waits=%v", err, calls, waits)
	}
}

func TestRetryClassificationAndDelay(t *testing.T) {
	for _, code := range []int{400, 401, 403, 404, 409, 422, 408, 429, 500, 503} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			want := code == 408 || code == 429 || code >= 500
			if got := Retryable(&statusError{code: code}, Failure{HTTPStatus: code}); got != want {
				t.Fatalf("retry=%v want=%v", got, want)
			}
		})
	}
	if Retryable(context.Canceled, Failure{}) || Retryable(context.DeadlineExceeded, Failure{}) || Retryable(errors.New("configuration"), Failure{Permanent: true}) {
		t.Fatal("retried permanent failure")
	}
	p := RetryPolicy{BaseDelay: 10 * time.Millisecond, MaxDelay: 40 * time.Millisecond}
	if p.Delay(8, Failure{}) != 40*time.Millisecond || p.Delay(0, Failure{RetryAfter: 75 * time.Millisecond}) != 75*time.Millisecond {
		t.Fatal("delay contract changed")
	}
	if (RetryPolicy{BaseDelay: time.Duration(1 << 62)}).Delay(4, Failure{}) != time.Duration(1<<63-1) {
		t.Fatal("delay overflow")
	}
}

func TestIndependentCoordinatorsHaveNoSharedRetryState(t *testing.T) {
	var workers sync.WaitGroup
	for i := range 20 {
		workers.Go(func() {
			c := coordinator()
			c.Retry.Attempts = i%4 + 1
			calls := 0
			_, err := c.Request(t.Context(), func() (response, error) {
				calls++
				return response{}, &statusError{code: http.StatusServiceUnavailable}
			})
			if err == nil || calls != c.Retry.Attempts {
				t.Errorf("attempts=%d calls=%d err=%v", c.Retry.Attempts, calls, err)
			}
		})
	}
	workers.Wait()
}
