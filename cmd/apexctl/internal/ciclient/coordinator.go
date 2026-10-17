package ciclient

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type Observation struct {
	TestID   string
	Terminal bool
}

// Transport functions bind immutable request data and validate wire responses.
// Abort reports whether the controller confirmed a terminal load status.
type Transport[T any] struct {
	Create  func(context.Context) (T, error)
	Get     func(context.Context, string) (T, error)
	Abort   func(context.Context, string, string) (bool, error)
	Inspect func(T) Observation
}

// State retains the last complete observation even when a later read fails.
// Before the create response arrives, TestID is the caller's deterministic ID.
type State[T any] struct {
	Response     T
	Observed     bool
	TestID       string
	Cancellation error
}

type Coordinator[T any] struct {
	Transport    Transport[T]
	Auth         Authentication
	Retry        RetryPolicy
	PollInterval time.Duration
	AbortTimeout time.Duration
	Now          func() time.Time
	Wait         func(context.Context, time.Duration) error
	Classify     func(error) Failure
	// Evidence callbacks run synchronously. On cancellation, evidence is
	// attempted before any abort, including when abort is disabled.
	BeforeAbort          func(State[T], error)
	OnObservationFailure func(State[T], error)
}

func (c *Coordinator[T]) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Coordinator[T]) wait(ctx context.Context, delay time.Duration) error {
	if c.Wait != nil {
		return c.Wait(ctx, delay)
	}
	return Wait(ctx, delay)
}

func (c *Coordinator[T]) failure(err error) Failure {
	if c.Classify != nil {
		return c.Classify(err)
	}
	return Failure{}
}

func (c *Coordinator[T]) ensureAuth(ctx context.Context) error {
	if c.Auth != nil {
		return c.Auth.Ensure(ctx)
	}
	return nil
}

func (c *Coordinator[T]) Run(ctx context.Context, initialTestID string, abortOnCancel bool) (State[T], error) {
	state := State[T]{TestID: initialTestID}
	response, err := c.Request(ctx, func() (T, error) { return c.Transport.Create(ctx) })
	if err != nil {
		if ctx.Err() != nil {
			return c.cancel(ctx, state, err, abortOnCancel)
		}
		return state, err
	}
	for {
		state.Response, state.Observed = response, true
		observed := c.Transport.Inspect(response)
		state.TestID = observed.TestID
		if ctx.Err() != nil {
			return c.cancel(ctx, state, nil, abortOnCancel)
		}
		if observed.Terminal {
			return state, nil
		}
		if err := c.wait(ctx, c.PollInterval); err != nil {
			if ctx.Err() != nil {
				return c.cancel(ctx, state, err, abortOnCancel)
			}
			return state, err
		}
		response, err = c.Request(ctx, func() (T, error) { return c.Transport.Get(ctx, state.TestID) })
		if err != nil {
			if ctx.Err() != nil {
				return c.cancel(ctx, state, err, abortOnCancel)
			}
			if c.OnObservationFailure != nil {
				c.OnObservationFailure(state, err)
			}
			return state, err
		}
	}
}

func (c *Coordinator[T]) cancel(ctx context.Context, state State[T], requestErr error, abort bool) (State[T], error) {
	state.Cancellation = ctx.Err()
	if requestErr == nil {
		requestErr = state.Cancellation
	} else if !errors.Is(requestErr, state.Cancellation) {
		requestErr = errors.Join(requestErr, state.Cancellation)
	}
	if c.BeforeAbort != nil {
		c.BeforeAbort(state, requestErr)
	}
	if abort {
		// Cancellation ends observation; stopping accepted work has a separate,
		// bounded lifetime and must not inherit the expired workflow context.
		if err := c.Abort(context.Background(), state.TestID, AbortReason(state.Cancellation)); err != nil {
			return state, errors.Join(requestErr, fmt.Errorf("controller may still be running the cancelled CI load: %w", err))
		}
	}
	return state, requestErr
}

var ErrAbortConfirmationPending = errors.New("controller has not confirmed CI run abort")

func AbortReason(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "timed_out"
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	return ""
}

func AbortRetryable(err error, failure Failure) bool {
	return errors.Is(err, ErrAbortConfirmationPending) || failure.HTTPStatus == http.StatusNotFound || Retryable(err, failure)
}

func (c *Coordinator[T]) Abort(parent context.Context, testID, reason string) error {
	if strings.TrimSpace(testID) == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(parent, c.AbortTimeout)
	defer cancel()
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := c.ensureAuth(ctx)
		if err == nil {
			var terminal bool
			terminal, err = c.Transport.Abort(ctx, testID, reason)
			if err == nil {
				if terminal {
					return nil
				}
				var response T
				response, err = c.Transport.Get(ctx, testID)
				if err == nil {
					if c.Transport.Inspect(response).Terminal {
						return nil
					}
					err = ErrAbortConfirmationPending
				}
			}
			if c.failure(err).HTTPStatus == http.StatusUnauthorized && c.Auth != nil {
				c.Auth.RefreshUnauthorized()
			}
		}
		failure := c.failure(err)
		if !AbortRetryable(err, failure) {
			return err
		}
		if err := c.wait(ctx, c.Retry.Delay(attempt, failure)); err != nil {
			return err
		}
	}
}

// WaitFor polls optional post-run evidence. It never changes the completed run
// outcome or aborts it; any error/deadline returns the latest complete snapshot.
func (c *Coordinator[T]) WaitFor(ctx context.Context, response T, duration time.Duration, pending func(T) bool) T {
	if !pending(response) {
		return response
	}
	ctx, cancel := context.WithTimeout(ctx, duration)
	defer cancel()
	id := c.Transport.Inspect(response).TestID
	for pending(response) {
		if err := c.wait(ctx, c.PollInterval); err != nil {
			return response
		}
		latest, err := c.Request(ctx, func() (T, error) { return c.Transport.Get(ctx, id) })
		if err != nil {
			return response
		}
		response = latest
	}
	return response
}
