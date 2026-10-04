package protocol

import "context"

// CorrelationScope identifies the VUser and business transaction an outbound
// request belongs to. The engine attaches it to the action context, and
// protocols read it to fill controller-approved correlation markers. It holds
// names only, never parameter values.
type CorrelationScope struct {
	VUserID     int
	Transaction string
}

type correlationScopeKey struct{}

// WithCorrelationScope returns ctx carrying scope for outbound requests.
func WithCorrelationScope(ctx context.Context, scope CorrelationScope) context.Context {
	return context.WithValue(ctx, correlationScopeKey{}, scope)
}

// CorrelationScopeFromContext returns the scope the engine attached, if any.
func CorrelationScopeFromContext(ctx context.Context) (CorrelationScope, bool) {
	if ctx == nil {
		return CorrelationScope{}, false
	}
	scope, ok := ctx.Value(correlationScopeKey{}).(CorrelationScope)
	return scope, ok
}
