package http

import (
	"errors"
	"sync"

	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/protocol/http/internal/h3transport"
)

// http3Receipt collects one HTTP/3 action's routing, connection and timing
// facts for the version receipt (evidence version 2). Trace callbacks run on
// transport goroutines, so every method locks.
type http3Receipt struct {
	mu        sync.Mutex
	selection string
	attempts  int
	dialed    bool
	resolved  bool
	gotConn   bool
	reused    bool
	resumed   bool
	failure   error
}

func newHTTP3Receipt() *http3Receipt { return &http3Receipt{selection: "exact_policy"} }

func (r *http3Receipt) lock(update func()) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	update()
}

// Trace notifications. GetConn fires once per HTTP attempt, including
// quic-go's own retry, so it counts attempts (D2).
// onGetConn counts an attempt and returns the action's attempts so far.
func (r *http3Receipt) onGetConn() int {
	attempts := 0
	r.lock(func() { r.attempts++; attempts = r.attempts })
	return attempts
}
func (r *http3Receipt) onDNSStart()     { r.lock(func() { r.resolved = true }) }
func (r *http3Receipt) onConnectStart() { r.lock(func() { r.dialed = true }) }
func (r *http3Receipt) onGotConn(reused bool) {
	r.lock(func() { r.gotConn, r.reused = true, r.reused || reused })
}
func (r *http3Receipt) onDial(info h3transport.DialInfo) {
	r.lock(func() { r.resumed = r.resumed || info.Resumed })
}
func (r *http3Receipt) refuse(selection string) { r.lock(func() { r.selection = selection }) }
func (r *http3Receipt) fail(err error) {
	r.lock(func() {
		r.failure = err
		if errors.Is(err, ErrHTTP3ProxyRoute) {
			r.selection = "proxy_route"
		}
	})
}

// evidence returns the receipt fields. It never reports a fabricated zero: an
// interval this action did not measure carries the reason instead.
func (r *http3Receipt) evidence(result *protocol.Result) map[string]any {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	attempted := r.dialed || r.gotConn
	evidence := map[string]any{
		"http_version_selection_reason": r.selection,
		"quic_attempted":                attempted,
		"attempt_count":                 r.attempts,
		"ttfb_boundary":                 "first_byte",
	}
	connection := "not_attempted"
	switch {
	case r.dialed && r.gotConn:
		connection = "new"
	case r.dialed:
		// The action dialed but no connection was ever handed to a request:
		// the handshake failed or never completed.
		connection = "dial_failed"
	case r.gotConn && r.reused:
		connection = "reused"
	case r.gotConn:
		// The request waited on another request's handshake: it neither
		// dialed nor reused an established connection.
		connection = "shared_pending"
	}
	availability := map[string]any{"ttfb": "available", "tls": "in_transport_handshake"}
	switch connection {
	case "new", "dial_failed":
		availability["connect"] = "available"
		availability["dns"] = "available"
		if !r.resolved {
			availability["dns"] = "ip_literal"
		}
	case "reused":
		availability["connect"], availability["dns"] = "reused_connection", "reused_connection"
	case "shared_pending":
		availability["connect"], availability["dns"] = "shared_pending_connection", "shared_pending_connection"
	default:
		availability["connect"], availability["dns"], availability["tls"] = "not_attempted", "not_attempted", "not_attempted"
	}
	if result == nil || result.TTFB <= 0 {
		availability["ttfb"] = "no_response"
	}
	evidence["timing_availability"] = availability
	if attempted {
		evidence["quic_connection"] = connection
		evidence["tls_in_transport_handshake"] = true
	}
	if r.resumed {
		evidence["tls_resumed"] = true
	}
	var h3Err *h3transport.Error
	if errors.As(r.failure, &h3Err) && h3Err.Class != "" {
		evidence["quic_error_class"] = string(h3Err.Class)
	}
	return evidence
}
