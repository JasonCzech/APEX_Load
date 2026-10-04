package engine

import (
	"context"
	"strings"

	"github.com/jasonczech/apexload/pkg/protocol"
	apptelemetry "github.com/jasonczech/apexload/pkg/telemetry"
	"go.opentelemetry.io/otel/trace"
)

// Correlation carriers a run may enable for its controller-approved origins.
// The HTTP protocol enforces them; the engine supplies what they need.
const (
	CorrelationCarrierRunMarker         = "run_marker"
	CorrelationCarrierTraceparent       = "traceparent"
	CorrelationCarrierTransactionMarker = "transaction_marker"
)

// vuserCorrelation is the per-VUser view of the run's correlation carriers.
type vuserCorrelation struct {
	// scope attaches the VUser and transaction names to each action context.
	scope bool
	// traceRoots gives every top-level transaction instance its own trace.
	traceRoots bool
	// sampled is the script's traceparent sampled preference, used for roots
	// that are not exported.
	sampled bool
}

func groupVUserCorrelation(group GroupConfig) vuserCorrelation {
	if len(group.RunCorrelationOrigins) == 0 {
		return vuserCorrelation{}
	}
	var correlation vuserCorrelation
	for _, carrier := range group.CorrelationCarriers {
		switch strings.ToLower(strings.TrimSpace(carrier)) {
		case CorrelationCarrierTraceparent:
			correlation.traceRoots = true
		case CorrelationCarrierTransactionMarker:
			correlation.scope = true
		}
	}
	correlation.sampled = true
	if group.Script != nil && group.Script.Config.HTTP.TraceparentSampled != nil {
		correlation.sampled = *group.Script.Config.HTTP.TraceparentSampled
	}
	return correlation
}

// correlationContext attaches the run's correlation context to one action.
// Requests inside an open source transaction share that transaction's trace;
// an action outside every transaction gets a trace of its own.
func (vu *VUser) correlationContext(ctx context.Context, action *protocol.Action) (context.Context, func()) {
	noop := func() {}
	if vu == nil || ctx == nil || (!vu.correlation.scope && !vu.correlation.traceRoots) {
		return ctx, noop
	}
	// An enclosing logical transaction (an http_sequence or concurrent group)
	// already resolved the context; its children keep it.
	if ctx.Value(correlationResolvedKey{}) != nil {
		return ctx, noop
	}
	ctx = context.WithValue(ctx, correlationResolvedKey{}, struct{}{})
	outermost := vu.outermostSourceTransaction()
	name := ""
	if outermost != nil {
		name = outermost.name
	}
	if strings.TrimSpace(name) == "" && action != nil {
		name = action.Name
	}
	if vu.correlation.scope {
		ctx = protocol.WithCorrelationScope(ctx, protocol.CorrelationScope{VUserID: vu.ID, Transaction: name})
	}
	if !vu.correlation.traceRoots {
		return ctx, noop
	}
	if outermost != nil && outermost.trace != nil && outermost.trace.root.IsValid() {
		return trace.ContextWithSpanContext(ctx, outermost.trace.root), noop
	}
	return apptelemetry.NewTransactionRoot(ctx, name, vu.correlation.sampled)
}

type correlationResolvedKey struct{}

// outermostSourceTransaction returns the earliest-started open source
// transaction, which owns the trace for everything nested inside it.
func (vu *VUser) outermostSourceTransaction() *sourceTransactionRuntimeState {
	var outermost *sourceTransactionRuntimeState
	for _, state := range vu.sourceTransactions {
		if state == nil {
			continue
		}
		if outermost == nil || state.startedAt.Before(outermost.startedAt) ||
			(state.startedAt.Equal(outermost.startedAt) && state.name < outermost.name) {
			outermost = state
		}
	}
	return outermost
}

// startSourceTransactionTraceRoot gives a transaction opened with no other
// transaction open its own trace. Nested transactions share the outer trace.
func (vu *VUser) startSourceTransactionTraceRoot(ctx context.Context, state *sourceTransactionRuntimeState) {
	if vu == nil || state == nil || !vu.correlation.traceRoots || len(vu.sourceTransactions) > 1 {
		return
	}
	rootCtx, end := apptelemetry.NewTransactionRoot(ctx, state.name, vu.correlation.sampled)
	state.trace = &sourceTransactionTrace{root: trace.SpanContextFromContext(rootCtx), end: end}
}

func (state *sourceTransactionRuntimeState) endTrace() {
	if state != nil && state.trace != nil && state.trace.end != nil {
		state.trace.end()
		state.trace.end = nil
	}
}
