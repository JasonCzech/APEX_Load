package engine

// teardown.go — deadline-bounded contexts for teardown work.
//
// Teardown (both DSL teardown actions and protocol.Teardown) deliberately runs
// under a fresh context rather than the cancelled run context, so cleanup always
// executes on stop/abort. The deadline bounds cooperative adapters. Direct and
// replay tooling additionally isolates a context-ignoring adapter; generator
// processes deliberately keep it on the owning VUser so exact quiescence stays
// open and the process-level safety watchdog can terminate the pod.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
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
var strictProtocolTeardown atomic.Bool

// EnableStrictProtocolTeardown keeps adapter teardown on the owning execution
// goroutine even after its context deadline. Generator processes enable this
// once at startup so an adapter that ignores cancellation remains represented
// by groupExecution.done and is handled by the process-level safety watchdog.
// Direct/replay tooling retains isolated teardown behavior by default.
func EnableStrictProtocolTeardown() {
	strictProtocolTeardown.Store(true)
}

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
	return callProtocolTeardownWithMode(ctx, p, config, slots, strictProtocolTeardown.Load())
}

func callProtocolTeardownWithMode(ctx context.Context, p interface {
	Teardown(context.Context) error
}, config *RedactionConfig, slots chan struct{}, strict bool) error {
	if p == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	select {
	case slots <- struct{}{}:
	case <-ctx.Done():
		return scrubSensitiveError(fmt.Errorf("protocol teardown could not start before deadline: %w", ctx.Err()), config)
	}
	if strict {
		return callProtocolTeardownStrict(ctx, p, config, slots)
	}

	// Tooling mode isolates a context-ignoring adapter so it cannot hold a local
	// replay forever. The buffered result lets a late return exit without keeping
	// a blocked sender.
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
			if errors.Is(teardownErr, protocol.ErrContainmentUnproven) {
				// Preserve only the stable, token-free sentinel. The adapter's
				// wrapped detail may contain resolved runtime configuration.
				err = protocol.ErrContainmentUnproven
			} else {
				err = scrubSensitiveError(teardownErr, config)
			}
		}
	}()

	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return scrubSensitiveError(fmt.Errorf("protocol teardown exceeded deadline: %w", ctx.Err()), config)
	}
}

func callProtocolTeardownStrict(ctx context.Context, p interface {
	Teardown(context.Context) error
}, config *RedactionConfig, slots chan struct{}) (err error) {
	defer func() {
		<-slots
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("protocol teardown panicked: %s", recoveredPanicLogTextWithConfig(recovered, config))
		}
	}()
	if teardownErr := p.Teardown(ctx); teardownErr != nil {
		if errors.Is(teardownErr, protocol.ErrContainmentUnproven) {
			// Returning would let the owning VUser and groupExecution.done close,
			// falsely advertising quiescence while an unowned descendant may still
			// generate load. Keep this exact barrier (and its teardown slot) open;
			// the generator service's bounded drain requests process termination.
			slog.Error("protocol teardown could not prove child-process containment; retaining generator execution barrier", "event", "protocol_containment_unproven")
			select {}
		}
		return scrubSensitiveError(teardownErr, config)
	}
	return nil
}
