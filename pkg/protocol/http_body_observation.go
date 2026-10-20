package protocol

import (
	"context"
	"sync"
)

// HTTPBodyObservation describes one initially prepared HTTP request body after
// the adapter's configured transformations. It is not a transport byte count or
// evidence of delivery. Redirects, authentication retries and framing are excluded.
// AppliedCompression names only compression performed by this adapter; it does
// not interpret an authored Content-Encoding header or an opaque input format.
type HTTPBodyObservation struct {
	Bytes              int64
	AppliedCompression string
}

type httpBodyObserverKey struct{}

type httpBodyObserver struct {
	once    sync.Once
	observe func(HTTPBodyObservation)
}

// WithHTTPBodyObserver installs an internal, synchronous preparation observer.
// Only the first preparation under this context is observed; nested refreshes
// and subrequests cannot replace the initial observation. A new action installs
// a new observer. An absent callback or refusal before preparation emits no
// sample. A prepared empty body emits an explicit zero. The callback must not
// retain or mutate request state; only the bounded numeric observation is passed.
func WithHTTPBodyObserver(ctx context.Context, observe func(HTTPBodyObservation)) context.Context {
	return context.WithValue(ctx, httpBodyObserverKey{}, &httpBodyObserver{observe: observe})
}

// ObserveHTTPBody publishes only the first valid preparation under the observer
// context, before transport execution. It never carries source bytes.
func ObserveHTTPBody(ctx context.Context, bodyBytes int64, appliedCompression string) {
	if bodyBytes < 0 {
		return
	}
	switch appliedCompression {
	case "none", "gzip", "deflate":
	default:
		return
	}
	if ctx == nil {
		return
	}
	if observer, ok := ctx.Value(httpBodyObserverKey{}).(*httpBodyObserver); ok && observer != nil && observer.observe != nil {
		observer.once.Do(func() {
			observer.observe(HTTPBodyObservation{Bytes: bodyBytes, AppliedCompression: appliedCompression})
		})
	}
}
