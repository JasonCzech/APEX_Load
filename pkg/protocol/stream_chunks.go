package protocol

import "context"

type streamChunkObserverKey struct{}

// WithStreamChunkObserver returns a context whose streaming response reads
// report each chunk (one server-sent event's data) to observe as it arrives,
// before the response completes. Background HTTP conversations use it for
// their on_body_chunk callbacks.
func WithStreamChunkObserver(ctx context.Context, observe func(chunk string)) context.Context {
	return context.WithValue(ctx, streamChunkObserverKey{}, observe)
}

// StreamChunkObserver returns the observer WithStreamChunkObserver set, or
// nil.
func StreamChunkObserver(ctx context.Context) func(chunk string) {
	if ctx == nil {
		return nil
	}
	observe, _ := ctx.Value(streamChunkObserverKey{}).(func(chunk string))
	return observe
}
