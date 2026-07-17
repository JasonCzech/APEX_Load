package engine

// teardown.go — deadline-bounded contexts for teardown work.
//
// Teardown (both DSL teardown actions and protocol.Teardown) deliberately runs
// under a fresh context rather than the cancelled run context, so cleanup always
// executes on stop/abort. Without a deadline, however, a protocol whose Teardown
// blocks (a stuck network or browser close) would hang the VUser goroutine and
// stall wg.Wait() — and thus test completion/shutdown — indefinitely. These
// helpers bound that work while preserving the "always run cleanup" intent.

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"
)

// defaultTeardownTimeout bounds how long a VUser/protocol teardown may run before
// it is cancelled. Generous enough for well-behaved network/browser closes.
const defaultTeardownTimeout = 30 * time.Second

// maxConcurrentProtocolTeardowns bounds the process-wide number of adapter
// cleanup calls that can remain stuck after ignoring cancellation. A generator
// can host thousands of VUsers; without a gate, isolating every teardown in its
// own goroutine would merely turn one shutdown hang into thousands of permanent
// goroutine leaks.
const maxConcurrentProtocolTeardowns = 64

var protocolTeardownSlots = make(chan struct{}, maxConcurrentProtocolTeardowns)

// teardownTimeout resolves the teardown deadline. Operators may override the
// default via APEXLOAD_TEARDOWN_TIMEOUT (a Go duration string, e.g. "10s").
// A non-positive or unparseable value falls back to the default.
func teardownTimeout() time.Duration {
	if raw := strings.TrimSpace(os.Getenv("APEXLOAD_TEARDOWN_TIMEOUT")); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			return d
		}
	}
	return defaultTeardownTimeout
}

// teardownContext returns a fresh, deadline-bounded context for teardown work.
// Callers must call the returned cancel (defer it where convenient).
func teardownContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), teardownTimeout())
}

// runProtocolTeardown closes a protocol under a bounded teardown context. It is
// the single helper every spawn site uses so the timeout policy lives in one
// place.
func runProtocolTeardown(p interface {
	Teardown(context.Context) error
}, providers ...func() *RedactionConfig) {
	if p == nil {
		return
	}
	ctx, cancel := teardownContext()
	defer cancel()
	if err := callProtocolTeardown(ctx, p, currentRedactionConfig(providers...)); err != nil {
		slog.Warn("protocol teardown failed", "error", err)
	}
}

func callProtocolTeardown(ctx context.Context, p interface {
	Teardown(context.Context) error
}, config *RedactionConfig) error {
	return callProtocolTeardownWithSlots(ctx, p, config, protocolTeardownSlots)
}

func callProtocolTeardownWithSlots(ctx context.Context, p interface {
	Teardown(context.Context) error
}, config *RedactionConfig, slots chan struct{}) error {
	if p == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	// Context cancellation only bounds cooperative adapters. Run the adapter in
	// an isolated goroutine as well so an implementation that ignores ctx cannot
	// hold a VUser, wg.Wait, or controller shutdown forever. The buffered result
	// lets a late-returning adapter exit without retaining a blocked sender.
	select {
	case slots <- struct{}{}:
	case <-ctx.Done():
		return scrubSensitiveError(fmt.Errorf("protocol teardown could not start before deadline: %w", ctx.Err()), config)
	}

	result := make(chan error, 1)
	go func() {
		var err error
		defer func() {
			if recovered := recover(); recovered != nil {
				err = fmt.Errorf("protocol teardown panicked: %s", recoveredPanicLogTextWithConfig(recovered, config))
			}
			<-slots
			result <- err
		}()
		if teardownErr := p.Teardown(ctx); teardownErr != nil {
			err = scrubSensitiveError(teardownErr, config)
		}
	}()

	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return scrubSensitiveError(fmt.Errorf("protocol teardown exceeded deadline: %w", ctx.Err()), config)
	}
}
