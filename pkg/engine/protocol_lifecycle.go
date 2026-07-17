package engine

import (
	"context"
	"fmt"
)

// callProtocolInit contains adapter panics at every synchronous Init boundary.
// Adapter panic values and ordinary errors are untrusted because they may echo
// resolved credentials, so both are passed through the active redaction policy.
func callProtocolInit(ctx context.Context, p interface {
	Init(context.Context, map[string]any) error
}, protoConfig map[string]any, redaction *RedactionConfig) (err error) {
	if p == nil {
		return fmt.Errorf("protocol init is unavailable")
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("protocol init panicked: %s", recoveredPanicLogTextWithConfig(recovered, redaction))
		}
	}()
	if err := p.Init(ctx, protoConfig); err != nil {
		return scrubSensitiveError(err, redaction)
	}
	return nil
}

// callProtocolHealthCheck is the matching process-protection boundary for
// preflight checks. Health failures remain non-fatal where callers deliberately
// continue, but a faulty adapter can no longer crash the hosting process.
func callProtocolHealthCheck(ctx context.Context, p interface {
	HealthCheck(context.Context) error
}, redaction *RedactionConfig) (err error) {
	if p == nil {
		return fmt.Errorf("protocol health check is unavailable")
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("protocol health check panicked: %s", recoveredPanicLogTextWithConfig(recovered, redaction))
		}
	}()
	if err := p.HealthCheck(ctx); err != nil {
		return scrubSensitiveError(err, redaction)
	}
	return nil
}
