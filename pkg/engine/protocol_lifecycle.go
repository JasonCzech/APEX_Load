package engine

import (
	"context"
	"errors"
	"fmt"
	"github.com/jasonczech/apexload/pkg/protocol"
)

type scrubbedContextError struct {
	message string
	cause   error
}

func (e *scrubbedContextError) Error() string {
	return e.message
}

func (e *scrubbedContextError) Unwrap() error {
	return e.cause
}

// scrubProtocolLifecycleError removes sensitive adapter output while retaining
// only the safe context cancellation class needed by lifecycle callers. Keeping
// the original error as the unwrap target would make its unsanitized text
// reachable through logs and diagnostics.
func scrubProtocolLifecycleError(err error, redaction *RedactionConfig) error {
	scrubbed := scrubSensitiveError(err, redaction)
	if scrubbed == nil {
		return nil
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return &scrubbedContextError{message: scrubbed.Error(), cause: context.DeadlineExceeded}
	case errors.Is(err, context.Canceled):
		return &scrubbedContextError{message: scrubbed.Error(), cause: context.Canceled}
	default:
		return scrubbed
	}
}

// callProtocolInit contains adapter panics at every synchronous Init boundary.
// Adapter panic values and ordinary errors are untrusted because they may echo
// resolved credentials, so both are passed through the active redaction policy.
func callProtocolInit(ctx context.Context, p interface {
	Init(context.Context, map[string]any) error
}, protoConfig map[string]any, redaction *RedactionConfig, unredactedDebug ...bool) (err error) {
	if p == nil {
		return fmt.Errorf("protocol init is unavailable")
	}
	var lease *protocol.BrowserSourceLease
	defer func() {
		if err != nil && lease != nil {
			err = errors.Join(err, lease.Finish(false))
		}
	}()
	defer func() {
		if recovered := recover(); recovered != nil {
			if len(unredactedDebug) > 0 && unredactedDebug[0] {
				err = fmt.Errorf("protocol init panicked: %s", cloneBoundedResultString(fmt.Sprintf("%v", recovered), maxRecoveredPanicLogBytes))
			} else {
				err = fmt.Errorf("protocol init panicked: %s", recoveredPanicLogTextWithConfig(recovered, redaction))
			}
		}
	}()
	if source, ok := protoConfig["browser_document_source_journal"].(protocol.BrowserSourceJournalProvider); ok {
		provider, ok := p.(protocol.BrowserSourceLeaseProvider)
		if !ok {
			return fmt.Errorf("browser source ledger runtime unavailable")
		}
		journal, readErr := source.BrowserDocumentSourceJournal()
		if readErr != nil {
			return readErr
		}
		if journal != nil {
			lease, err = journal.Begin()
			if err != nil {
				return err
			}
			provider.SetBrowserDocumentSourceLease(lease)
		}
	}
	if err := p.Init(ctx, protoConfig); err != nil {
		if len(unredactedDebug) > 0 && unredactedDebug[0] {
			message := cloneBoundedResultString(err.Error(), maxResultErrorBytes)
			switch {
			case errors.Is(err, context.DeadlineExceeded):
				return &scrubbedContextError{message: message, cause: context.DeadlineExceeded}
			case errors.Is(err, context.Canceled):
				return &scrubbedContextError{message: message, cause: context.Canceled}
			default:
				return errors.New(message)
			}
		}
		return scrubProtocolLifecycleError(err, redaction)
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
		return scrubProtocolLifecycleError(err, redaction)
	}
	return nil
}
