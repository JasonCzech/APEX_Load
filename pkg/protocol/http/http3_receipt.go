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
//
// Under auto+h3 the router reports each hop's route before sending it, so
// connection facts come only from QUIC legs: a TCP hop's dial or reuse never
// reads as a QUIC connection. Hops run one after another, so the current leg
// is unambiguous.
type http3Receipt struct {
	mu           sync.Mutex
	upgrade      bool // auto+h3: the router chooses each hop's transport
	quicLeg      bool // the current hop runs on QUIC
	finalQUIC    bool // the last hop ran, or failed, on QUIC
	tcpHop       bool // some hop ran on TCP, so the action's timings mix transports
	selection    string
	fallback     string
	fellBackFrom error // the QUIC failure a fallback replaced
	attempts     int   // every HTTP attempt in the action
	quicAttempts int   // attempts on QUIC legs, charged to the attempt budget
	dialed       bool
	resolved     bool
	gotConn      bool
	reused       bool
	resumed      bool
	failure      error
}

func newHTTP3Receipt(policy string) *http3Receipt {
	if policy == VersionAutoH3 {
		return &http3Receipt{upgrade: true}
	}
	return &http3Receipt{selection: "exact_policy", quicLeg: true, finalQUIC: true}
}

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
// onGetConn counts an attempt and returns the QUIC attempts so far, which the
// attempt budget limits; TCP attempts are counted but never budgeted, so
// auto+h3 adds no failure mode to a TCP hop.
func (r *http3Receipt) onGetConn() int {
	attempts := 0
	r.lock(func() {
		r.attempts++
		if r.quicLeg {
			r.quicAttempts++
		}
		attempts = r.quicAttempts
	})
	return attempts
}
func (r *http3Receipt) onDNSStart() {
	r.lock(func() { r.resolved = r.resolved || r.quicLeg })
}
func (r *http3Receipt) onConnectStart() {
	r.lock(func() { r.dialed = r.dialed || r.quicLeg })
}
func (r *http3Receipt) onGotConn(reused bool) {
	r.lock(func() {
		if r.quicLeg {
			r.gotConn, r.reused = true, r.reused || reused
		}
	})
}

// routed implements http3RouteObserver: the router's choice for one hop.
func (r *http3Receipt) routed(selection string, quic bool) {
	r.lock(func() {
		r.selection, r.quicLeg, r.finalQUIC = selection, quic, quic
		r.tcpHop = r.tcpHop || !quic
	})
}

// fellBack implements http3RouteObserver: the hop's QUIC attempt ended
// before the target could act on it and the hop now runs on TCP.
func (r *http3Receipt) fellBack(kind string, cause error) {
	r.lock(func() {
		r.fallback, r.quicLeg, r.finalQUIC, r.tcpHop = kind, false, false, true
		if cause != nil {
			r.fellBackFrom = cause
		}
	})
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
		"quic_attempted": attempted,
		"attempt_count":  r.attempts,
		"ttfb_boundary":  "first_byte",
	}
	if r.selection != "" {
		// Empty only when auto+h3 never routed a hop (a fresh cache hit).
		evidence["http_version_selection_reason"] = r.selection
	}
	if r.upgrade {
		evidence["http_version_fallback"] = "none"
		if r.fallback != "" {
			evidence["http_version_fallback"] = r.fallback
		}
	}
	if attempted {
		evidence["quic_connection"] = r.connectionLocked()
		if r.finalQUIC && !r.tcpHop {
			// Explains TLSTime = 0; once a hop ran on TCP the timings are
			// not the QUIC handshake's alone.
			evidence["tls_in_transport_handshake"] = true
		}
	}
	if r.resumed {
		evidence["tls_resumed"] = true
	}
	// The action's own QUIC failure, else the one a fallback replaced.
	var h3Err *h3transport.Error
	if errors.As(r.failure, &h3Err) && h3Err.Class != "" {
		evidence["quic_error_class"] = string(h3Err.Class)
	} else if errors.As(r.fellBackFrom, &h3Err) && h3Err.Class != "" {
		evidence["quic_error_class"] = string(h3Err.Class)
	}
	if !r.finalQUIC || r.tcpHop {
		// The result's timings come from, or include, a TCP hop, which
		// carries no availability reasons.
		return evidence
	}
	availability := map[string]any{"ttfb": "available", "tls": "in_transport_handshake"}
	switch r.connectionLocked() {
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
	return evidence
}

// connectionLocked classifies the QUIC connection the action's QUIC legs
// used. The caller holds r.mu.
func (r *http3Receipt) connectionLocked() string {
	switch {
	case r.dialed && r.gotConn:
		return "new"
	case r.dialed:
		// The action dialed but no connection was ever handed to a request:
		// the handshake failed or never completed.
		return "dial_failed"
	case r.gotConn && r.reused:
		return "reused"
	case r.gotConn:
		// The request waited on another request's handshake: it neither
		// dialed nor reused an established connection.
		return "shared_pending"
	}
	return "not_attempted"
}
