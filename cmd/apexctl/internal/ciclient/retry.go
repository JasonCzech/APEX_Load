// Package ciclient coordinates controller CI requests without command parsing,
// wire DTOs, output files, or server dependencies. A coordinator and its auth
// session belong to one workflow; separate workflows use separate instances.
package ciclient

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

type Failure struct {
	HTTPStatus int
	RetryAfter time.Duration
	Permanent  bool
}

type RetryPolicy struct {
	Attempts  int
	BaseDelay time.Duration
	MaxDelay  time.Duration
	Window    time.Duration
}

type Authentication interface {
	Ensure(context.Context) error
	RefreshUnauthorized() bool
}

type AuthError struct{ Err error }

func (e *AuthError) Error() string {
	if e == nil || e.Err == nil {
		return "controller CI authentication failed"
	}
	return fmt.Sprintf("controller CI authentication failed: %v", e.Err)
}

func (e *AuthError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func Retryable(err error, failure Failure) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || failure.Permanent {
		return false
	}
	return failure.HTTPStatus == 0 || failure.HTTPStatus == http.StatusRequestTimeout || failure.HTTPStatus == http.StatusTooManyRequests || failure.HTTPStatus >= 500
}

func (p RetryPolicy) Delay(attempt int, failure Failure) time.Duration {
	delay := p.BaseDelay
	if delay < 0 {
		delay = 0
	}
	if p.MaxDelay > 0 {
		for step := 0; step < attempt && delay < p.MaxDelay; step++ {
			if delay > p.MaxDelay/2 {
				delay = p.MaxDelay
				break
			}
			delay *= 2
		}
		if delay > p.MaxDelay {
			delay = p.MaxDelay
		}
	} else {
		for step := 0; step < attempt; step++ {
			if delay >= time.Duration(1<<62) {
				delay = time.Duration(1<<63 - 1)
				break
			}
			delay *= 2
		}
	}
	if failure.RetryAfter > delay {
		delay = failure.RetryAfter
	}
	return delay
}

func Wait(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (c *Coordinator[T]) Request(ctx context.Context, operation func() (T, error)) (T, error) {
	var zero T
	if operation == nil {
		return zero, fmt.Errorf("controller CI operation is required")
	}
	attempts := max(1, c.Retry.Attempts)
	started := c.now()
	var lastErr error
	lastFailureWasAuth, refreshedUnauthorized := false, false
	classified := func() error {
		if lastErr != nil && lastFailureWasAuth {
			return &AuthError{Err: lastErr}
		}
		return lastErr
	}
	for attempt := 0; attempt < attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		lastErr = c.ensureAuth(ctx)
		lastFailureWasAuth = lastErr != nil
		if lastErr == nil {
			response, err := operation()
			if err == nil {
				return response, nil
			}
			lastErr = err
			if c.failure(err).HTTPStatus == http.StatusUnauthorized && !refreshedUnauthorized && c.Auth != nil && c.Auth.RefreshUnauthorized() {
				refreshedUnauthorized = true
				continue
			}
		}
		failure := c.failure(lastErr)
		if !Retryable(lastErr, failure) || attempt == attempts-1 {
			return zero, classified()
		}
		delay := c.Retry.Delay(attempt, failure)
		if c.Retry.Window > 0 {
			remaining := c.Retry.Window - c.now().Sub(started)
			if remaining <= 0 {
				return zero, classified()
			}
			if delay > remaining {
				delay = remaining
			}
		}
		if err := c.wait(ctx, delay); err != nil {
			return zero, err
		}
	}
	return zero, classified()
}
