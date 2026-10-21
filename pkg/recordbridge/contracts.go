// Package recordbridge contains browser recording and replay lifecycles shared
// by the controller and local recording bridge. HTTP adapters provide event and
// persistence ports without coupling the managers to a controller server.
package recordbridge

import (
	"context"
	"io"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
)

// EventSink publishes progress to the owner of a recording or replay session.
type EventSink interface {
	BroadcastScopedJSON(testID, projectID string, value any)
}

// RecordingManagerOptions supplies a recording backend and lifecycle budgets.
// A nil factory uses the native browser recorder; zero budgets use defaults.
type RecordingManagerOptions struct {
	RecorderFactory RecorderFactory
	StopTimeout     time.Duration
	Retention       time.Duration
}

// SessionLease is the durable session-ID ownership boundary. Implementations
// may use controller storage or a local bridge reservation registry.
type SessionLease interface {
	Done() <-chan struct{}
	Fail(error)
	FailureOrExpired(time.Time) error
	RetainForTerminal(time.Duration) error
	ReleaseRetainedID()
	ReleaseAll()
}

type SessionIDReserver func(context.Context, string, string) (string, func(), SessionLease, error)
type ReplayCompletionHook func(context.Context, string, *dsl.Script, *engine.ReplayReport)

// ReplayManagerOptions supplies admission, ownership and completion ports.
// ExecutionContextError preserves the host application's lost-epoch error.
type ReplayManagerOptions struct {
	ValidateStart         func(*dsl.Script) error
	ReserveID             SessionIDReserver
	OnComplete            ReplayCompletionHook
	Retention             *ReplayRetentionConfig
	CloseTimeout          time.Duration
	ExecutionContextError error
}

// TerminalReplayArtifactPublisher is an optional lease-bound persistence port.
// Publish must synchronously join all readers/writers before returning, retain
// the exact lease owner for its final publication CAS, and honor cancellation.
// A nil error means both immutable objects were verified and durably published.
// The input is a detached terminal snapshot and a joined native HAR, never a
// synthetic substitute. Neither may be retained after Publish returns.
type TerminalReplayArtifactPublisher interface {
	Publish(context.Context, *BrowserReplaySession, io.ReadSeeker, int64) error
}

// ReplayArtifactPublicationLease is deliberately separate from SessionLease:
// standalone bridges and older adapters remain local-only. A nil publisher
// also means local-only; implementing the getter alone grants no capability.
type ReplayArtifactPublicationLease interface {
	ReplayArtifactPublisher() TerminalReplayArtifactPublisher
}
