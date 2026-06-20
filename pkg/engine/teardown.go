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
	"os"
	"strings"
	"time"
)

// defaultTeardownTimeout bounds how long a VUser/protocol teardown may run before
// it is cancelled. Generous enough for well-behaved network/browser closes.
const defaultTeardownTimeout = 30 * time.Second

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
}) {
	if p == nil {
		return
	}
	ctx, cancel := teardownContext()
	defer cancel()
	_ = p.Teardown(ctx)
}
