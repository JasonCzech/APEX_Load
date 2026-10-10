package http

import (
	"context"
	"errors"
	"fmt"
	stdhttp "net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol/http/internal/h3transport"
)

// EnvHTTP3Preflight sets the run policy for the scenario HTTP/3 preflight:
// "off" skips it, "report" (the default) records each QUIC handshake check
// with the run, and "block" also refuses to start a run when an exact-3
// origin's check fails. Dynamic targets and auto+h3 origins are only ever
// reported.
const EnvHTTP3Preflight = "APEXLOAD_HTTP3_PREFLIGHT"

// HTTP3 preflight policies.
const (
	HTTP3PreflightOff    = "off"
	HTTP3PreflightReport = "report"
	HTTP3PreflightBlock  = "block"
)

// HTTP3PreflightPolicy reads EnvHTTP3Preflight; anything unrecognized is
// "report", so a typo never blocks runs.
func HTTP3PreflightPolicy(getenv func(string) string) string {
	if getenv == nil {
		getenv = os.Getenv
	}
	switch value := strings.ToLower(strings.TrimSpace(getenv(EnvHTTP3Preflight))); value {
	case HTTP3PreflightOff, HTTP3PreflightBlock:
		return value
	}
	return HTTP3PreflightReport
}

// HTTP3ProbeOutcome is one QUIC handshake check.
type HTTP3ProbeOutcome struct {
	Address    string
	Duration   time.Duration
	ErrorClass string // quic_error_class vocabulary, when the handshake failed
	Err        error
	// Proxied is set, with no check made, when the session's proxy rules
	// (custom, system or PAC, after no_proxy) route the origin through a
	// proxy, which carries TCP only.
	Proxied bool
}

// ProbeHTTP3 completes a QUIC handshake with the origin's host and port and
// closes it without sending a request. It uses this session's TLS settings,
// target policy, resolver, IP version and source address, and its own
// HTTP/3 transport: a probe session must not be a VUser's, so its tickets
// and address tokens are discarded with it.
func (h *HTTP) ProbeHTTP3(ctx context.Context, origin string) HTTP3ProbeOutcome {
	if h.http3 == nil {
		err := h.http3Err
		if err == nil {
			err = ErrHTTP3NotEnabled
		}
		return HTTP3ProbeOutcome{Err: err}
	}
	parsed, err := url.Parse(origin)
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.Hostname() == "" {
		return HTTP3ProbeOutcome{Err: fmt.Errorf("HTTP/3 preflight needs an https origin, got %q", origin)}
	}
	port := 443
	if raw := parsed.Port(); raw != "" {
		if port, err = strconv.Atoi(raw); err != nil {
			return HTTP3ProbeOutcome{Err: fmt.Errorf("invalid port in %q", origin)}
		}
	}
	// The same per-request route rule the HTTP/3 pools apply.
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, "https://"+parsed.Host+"/", nil)
	if err != nil {
		return HTTP3ProbeOutcome{Err: err}
	}
	if err := h.http3Route.directRoute(req); err != nil {
		if errors.Is(err, ErrHTTP3ProxyRoute) {
			return HTTP3ProbeOutcome{Proxied: true}
		}
		return HTTP3ProbeOutcome{Err: err}
	}
	transport, err := h.http3.current()
	if err != nil {
		return HTTP3ProbeOutcome{Err: err}
	}
	result, err := transport.Probe(ctx, parsed.Hostname(), port)
	outcome := HTTP3ProbeOutcome{Address: result.Address, Duration: result.Duration, Err: err}
	var h3Err *h3transport.Error
	if errors.As(err, &h3Err) {
		outcome.ErrorClass = string(h3Err.Class)
	}
	return outcome
}
