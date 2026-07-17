package engine

// panic_recovery.go — process-protection backstops for VUser goroutines.
//
// The engine runs one goroutine per VUser. A panic in any goroutine that is not
// recovered propagates to the runtime and crashes the entire generator/
// controller process — taking down every other concurrent VUser and test on the
// pod. Two layers contain this:
//
//   1. safeProtoExecute (vuser_execute.go) turns a panic inside a protocol
//      adapter's Execute() into a normal action error, so the common, recoverable
//      case flows through the existing continue-on-error / abort-to-teardown path.
//   2. recoverVUserGoroutine is the last-resort backstop deferred at every VUser
//      spawn site, catching anything that escaped layer 1 (control-flow/template
//      panics, or a panic in protocol Init/Teardown) so the process survives.

import (
	"fmt"
	"log/slog"
	"runtime/debug"

	"github.com/jasonczech/apexload/pkg/internal/boundedtext"
)

const (
	maxRecoveredPanicLogBytes = 4 << 10
	maxRecoveredStackLogBytes = 64 << 10
)

// recoverVUserGoroutine recovers a panic that escaped per-action recovery and
// logs it with a stack trace. It is meant to be deferred as the first statement
// of a VUser goroutine so it runs last and also contains panics raised by the
// goroutine's other deferred cleanup (e.g. protocol Teardown). The deferred
// wg.Done()/cleanup at the call site still run during unwinding.
func recoverVUserGoroutine(vuserID int, providers ...func() *RedactionConfig) {
	if r := recover(); r != nil {
		slog.Error("vuser goroutine panicked; recovered to protect the process",
			"vuser", vuserID,
			"panic", recoveredPanicLogTextWithConfig(r, currentRedactionConfig(providers...)),
			"stack", boundedtext.TruncateUTF8(string(debug.Stack()), maxRecoveredStackLogBytes))
	}
}

func currentRedactionConfig(providers ...func() *RedactionConfig) (config *RedactionConfig) {
	defer func() {
		if recover() != nil {
			config = nil
		}
	}()
	for _, provider := range providers {
		if provider != nil {
			return provider()
		}
	}
	return nil
}

// recoveredPanicLogText treats panic values as untrusted adapter output. A
// protocol may panic with a URL, response, or error containing resolved
// credentials; emitting that value verbatim would bypass normal result/error
// redaction and persist the secret in generator logs.
func recoveredPanicLogText(value any) string {
	return recoveredPanicLogTextWithConfig(value, nil)
}

func recoveredPanicLogTextWithConfig(value any, config *RedactionConfig) string {
	scrubbed := scrubSensitiveTextWithConfig(fmt.Sprintf("%v", value), config)
	return boundedtext.TruncateUTF8(scrubbed, maxRecoveredPanicLogBytes)
}
