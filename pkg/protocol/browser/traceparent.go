package browser

import (
	"context"
	"crypto/rand"
	"encoding/hex"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// applyActionTraceparent generates a fresh W3C traceparent for the action and
// applies it to every request the page issues until the next action, so slow
// waterfall entries correlate with backend traces (Dynatrace, OTel). The
// traceparent is recorded as the result correlation ID.
//
// Opt-in via browser config `traceparent: true`: adding a custom header makes
// cross-origin requests non-simple and can introduce CORS preflights the real
// user traffic would not have, so fidelity-sensitive runs leave it off.
func (b *Browser) applyActionTraceparent(ctx context.Context, result *protocol.Result) {
	if !b.traceparentEnabled || b.pwPage == nil {
		return
	}
	traceparent := newBrowserTraceparent()
	if traceparent == "" {
		return
	}
	if err := b.pwPage.SetExtraHTTPHeaders(map[string]string{"traceparent": traceparent}); err != nil {
		return
	}
	if result != nil {
		result.CorrelationID = traceparent
	}
	_ = ctx
}

// newBrowserTraceparent builds a W3C trace context header value
// (00-<32 hex trace id>-<16 hex span id>-01), mirroring the HTTP protocol's
// generator.
func newBrowserTraceparent() string {
	traceID := make([]byte, 16)
	if _, err := rand.Read(traceID); err != nil {
		return ""
	}
	spanID := make([]byte, 8)
	if _, err := rand.Read(spanID); err != nil {
		return ""
	}
	return "00-" + hex.EncodeToString(traceID) + "-" + hex.EncodeToString(spanID) + "-01"
}
