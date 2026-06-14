package api

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

// remoteOutboundAllowPrivateHostsEnv opts deployments that legitimately federate
// to internal/in-cluster or on-prem services back into dialing private addresses.
const remoteOutboundAllowPrivateHostsEnv = "APEXLOAD_LIBRARY_REMOTE_ALLOW_PRIVATE_HOSTS"

// remoteOutboundAllowsPrivateHosts reports whether outbound federation / remote-run
// requests may target private, loopback, or link-local addresses. It is OFF by
// default — the controller is fail-closed against SSRF, since a remote's base URL or
// a redirect target could otherwise point the control plane at internal services or
// cloud metadata (169.254.169.254). Operators that federate to internal hosts opt in
// by setting APEXLOAD_LIBRARY_REMOTE_ALLOW_PRIVATE_HOSTS=true.
func remoteOutboundAllowsPrivateHosts() bool {
	return envBool(remoteOutboundAllowPrivateHostsEnv)
}

// isBlockedOutboundIP reports whether an IP must not be dialed for SSRF safety:
// loopback, RFC1918/ULA private, link-local (which includes the 169.254.169.254
// cloud-metadata endpoint), unspecified, and multicast ranges.
func isBlockedOutboundIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() ||
		ip.IsUnspecified() ||
		ip.IsMulticast()
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
		return fmt.Errorf("remote host %s is a private, loopback, or link-local address; set %s=true to allow internal remotes", host, remoteOutboundAllowPrivateHostsEnv)
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
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
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
					lastErr = fmt.Errorf("blocked outbound connection to %s (%s): private/loopback/link-local address", host, ipAddr.IP)
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
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("stopped after %d redirects", len(via))
			}
			return nil
		},
	}
}
