package api

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/netpolicy"
)

// remoteOutboundAllowPrivateHostsEnv opts deployments that legitimately federate
// to internal/in-cluster or on-prem services back into dialing private addresses.
const remoteOutboundAllowPrivateHostsEnv = "APEXLOAD_LIBRARY_REMOTE_ALLOW_PRIVATE_HOSTS"

type guardedHTTPClientCacheKey struct {
	timeout      time.Duration
	allowPrivate bool
}

var guardedHTTPClients sync.Map

// remoteOutboundAllowsPrivateHosts reports whether outbound federation / remote-run
// requests may target private, loopback, or link-local addresses. It is OFF by
// default — the controller is fail-closed against SSRF, since a remote's base URL or
// a redirect target could otherwise point the control plane at internal services or
// cloud metadata (169.254.169.254). Operators that federate to internal hosts opt in
// by setting APEXLOAD_LIBRARY_REMOTE_ALLOW_PRIVATE_HOSTS=true.
func remoteOutboundAllowsPrivateHosts() bool {
	return envBool(remoteOutboundAllowPrivateHostsEnv)
}

// isBlockedOutboundIP reports whether an IP must not be dialed for SSRF safety.
// The shared policy includes private/link-local addresses and IANA special-use
// space that Go's built-in private/global predicates do not classify safely.
func isBlockedOutboundIP(ip net.IP) bool {
	return !netpolicy.IsPublicIP(ip)
}

// validateOutboundRemoteHost rejects a URL host that is a literal blocked IP, unless
// private hosts are explicitly allowed. DNS names are not resolved here (that happens
// at dial time, where every resolved address is re-checked); this is an early, clear
// guard against an operator registering an obviously-internal literal address.
func validateOutboundRemoteHost(host string) error {
	host = strings.TrimSpace(host)
	if host == "" {
		return fmt.Errorf("missing host")
	}
	if remoteOutboundAllowsPrivateHosts() {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && isBlockedOutboundIP(ip) {
		return fmt.Errorf("remote host %s is a non-public or special-use address; set %s=true to allow internal remotes", host, remoteOutboundAllowPrivateHostsEnv)
	}
	return nil
}

// newGuardedHTTPClient returns an *http.Client whose dialer rejects connections to
// blocked addresses at every hop. Resolving and IP-checking inside DialContext (and
// dialing the validated IP directly) defeats both redirect-based SSRF and DNS
// rebinding, because the redirect target's address is re-validated on its own dial.
// Redirects are additionally bounded as defense-in-depth.
func newGuardedHTTPClient(timeout time.Duration) *http.Client {
	allowPrivate := remoteOutboundAllowsPrivateHosts()
	key := guardedHTTPClientCacheKey{timeout: timeout, allowPrivate: allowPrivate}
	if cached, ok := guardedHTTPClients.Load(key); ok {
		return cached.(*http.Client)
	}
	client := buildGuardedHTTPClient(timeout, allowPrivate)
	actual, _ := guardedHTTPClients.LoadOrStore(key, client)
	return actual.(*http.Client)
}

func buildGuardedHTTPClient(timeout time.Duration, allowPrivate bool) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		// Deliberately NOT http.ProxyFromEnvironment: with an HTTP(S)_PROXY set,
		// the transport would dial the proxy and issue CONNECT to the real target,
		// so the proxy — not our IP-checking DialContext — makes the final
		// connection, silently defeating the SSRF guard for DNS-name targets.
		// The sibling GitHub/webhook/external-metric clients likewise set no proxy.
		// (Trade-off: control-plane federation/remote-run egress cannot be routed
		// through an HTTP_PROXY; on-prem egress proxying must be done at the network
		// layer, not via these env vars.)
		Proxy: nil,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			var lastErr error
			for _, ipAddr := range ips {
				if !allowPrivate && isBlockedOutboundIP(ipAddr.IP) {
					lastErr = fmt.Errorf("blocked outbound connection to %s (%s): non-public or special-use address", host, ipAddr.IP)
					continue
				}
				conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(ipAddr.IP.String(), port))
				if dialErr == nil {
					return conn, nil
				}
				lastErr = dialErr
			}
			if lastErr == nil {
				lastErr = fmt.Errorf("no dialable address for %s", host)
			}
			return nil, lastErr
		},
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	return &http.Client{
		Timeout:       timeout,
		Transport:     transport,
		CheckRedirect: sameOriginRedirectPolicy,
	}
}
