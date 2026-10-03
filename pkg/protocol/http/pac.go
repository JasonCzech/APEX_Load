package http

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/netpolicy"
	"github.com/jasonczech/apexload/pkg/protocol"
)

const maxPACBytes = 256 << 10

var pacWorkerSlots = make(chan struct{}, 4)
var errPACDNSNotFound = errors.New("PAC DNS name not found")

type pacRequest struct{ Source, URL, Host string }
type pacResponse struct{ DNS, Result, Error string }

// Keep DNS pinning without losing ordinary multi-address/family fallback.
func dialPACIPs(ctx context.Context, network, port string, ips []net.IPAddr, dial func(context.Context, string, string) (net.Conn, error)) (net.Conn, error) {
	var last error
	for _, ip := range ips {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		conn, err := dial(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		last = err
	}
	if last == nil {
		last = fmt.Errorf("PAC DNS lookup returned no addresses")
	}
	return nil, last
}

func pacLookup(ctx context.Context, host string, allowed []string) ([]net.IPAddr, error) {
	if err := protocol.ValidateTargetHost(host, allowed); err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		var dnsErr *net.DNSError
		if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
			return nil, errPACDNSNotFound
		}
		return nil, fmt.Errorf("PAC DNS lookup failed")
	}
	if len(allowed) == 0 {
		for _, ip := range ips {
			if !netpolicy.IsPublicIP(ip.IP) {
				return nil, fmt.Errorf("PAC access to non-public address requires an explicit target policy")
			}
		}
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("PAC DNS lookup returned no addresses")
	}
	return ips, nil
}
func fetchPAC(ctx context.Context, raw string, allowed []string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("PAC requires an absolute HTTP(S) URL without userinfo")
	}
	tr := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := pacLookup(ctx, host, allowed)
		if err != nil {
			return nil, err
		}
		return dialPACIPs(ctx, network, port, ips, (&net.Dialer{Timeout: 5 * time.Second}).DialContext)
	}}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 5 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 5 || req.URL.User != nil {
			return fmt.Errorf("PAC redirect rejected")
		}
		return protocol.ValidateTargetURL(req.URL.String(), allowed)
	}}
	req, err := http.NewRequestWithContext(ctx, "GET", raw, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("PAC download failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("PAC download returned HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxPACBytes+1))
	if err != nil || len(b) > maxPACBytes {
		return "", fmt.Errorf("PAC source exceeds bounds or could not be read")
	}
	return string(b), nil
}
func evaluatePAC(ctx context.Context, source string, u *url.URL, allowed []string) (string, error) {
	if len(source) > maxPACBytes {
		return "", fmt.Errorf("PAC source exceeds bounds")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	select {
	case pacWorkerSlots <- struct{}{}:
		defer func() { <-pacWorkerSlots }()
	case <-ctx.Done():
		return "", ctx.Err()
	}
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, executable, "--apexload-pac-worker")
	cmd.Env = []string{"GOMEMLIMIT=32MiB", "GOMAXPROCS=1"}
	cmd.Dir = os.TempDir()
	cmd.WaitDelay = 100 * time.Millisecond
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return "", err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	if err = cmd.Start(); err != nil {
		return "", fmt.Errorf("PAC worker could not start")
	}
	defer func() { _ = stdin.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	encoder := json.NewEncoder(stdin)
	if err = encoder.Encode(pacRequest{source, u.String(), u.Hostname()}); err != nil {
		return "", err
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 1024), 8192)
	for queries := 0; scanner.Scan(); queries++ {
		if queries > 32 {
			return "", fmt.Errorf("PAC DNS helper budget exceeded")
		}
		var reply pacResponse
		if json.Unmarshal(scanner.Bytes(), &reply) != nil {
			return "", fmt.Errorf("invalid PAC worker output")
		}
		if reply.Error != "" {
			return "", fmt.Errorf("PAC evaluation failed")
		}
		if reply.DNS == "" {
			if len(reply.Result) > 4096 || reply.Result == "" {
				return "", fmt.Errorf("empty or oversized PAC result")
			}
			return reply.Result, nil
		}
		ips, err := pacLookup(ctx, reply.DNS, allowed)
		// A missing DNS name is a helper result, not a policy/evaluator failure.
		// Let an explicit PAC branch decide what to do; all other errors fail closed.
		if errors.Is(err, errPACDNSNotFound) {
			if err = encoder.Encode(""); err != nil {
				return "", err
			}
			continue
		}
		if err != nil {
			return "", err
		}
		if err = encoder.Encode(ips[0].IP.String()); err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("PAC worker failed or exceeded its resource limits")
}
func parsePACRoutes(value string, allowed []string) ([]*url.URL, error) {
	items := strings.Split(value, ";")
	if len(items) > 16 {
		return nil, fmt.Errorf("too many PAC routes")
	}
	routes := make([]*url.URL, 0, len(items))
	for _, item := range items {
		fields := strings.Fields(item)
		if len(fields) == 0 {
			continue
		}
		if len(fields) == 1 && fields[0] == "DIRECT" {
			routes = append(routes, nil)
			continue
		}
		if len(fields) != 2 {
			return nil, fmt.Errorf("invalid PAC route")
		}
		scheme := ""
		switch fields[0] {
		case "PROXY":
			scheme = "http"
		case "HTTPS":
			scheme = "https"
		case "SOCKS5":
			scheme = "socks5"
		default:
			return nil, fmt.Errorf("unsupported PAC proxy type")
		}
		u, err := url.Parse(scheme + "://" + fields[1])
		if err != nil || u.Hostname() == "" || u.Port() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
			return nil, fmt.Errorf("invalid PAC proxy address")
		}
		if err = protocol.ValidateTargetURL(u.String(), allowed); err != nil {
			return nil, err
		}
		routes = append(routes, u)
	}
	if len(routes) == 0 {
		return nil, fmt.Errorf("empty PAC route list")
	}
	return routes, nil
}

type pacRoundTripper struct {
	source   string
	allowed  []string
	base     *http.Transport
	budget   *httpConnectionBudget
	baseDial func(context.Context, string, string) (net.Conn, error)
}

func (p *pacRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// RoundTripper owns the body even when policy or PAC evaluation rejects the
	// request before the underlying transport can take ownership.
	bodyOwned := req.Body != nil
	defer func() {
		if bodyOwned {
			_ = req.Body.Close()
		}
	}()
	if err := protocol.ValidateTargetURL(req.URL.String(), p.allowed); err != nil {
		return nil, err
	}
	value, err := evaluatePAC(req.Context(), p.source, req.URL, p.allowed)
	if err != nil {
		return nil, err
	}
	routes, err := parsePACRoutes(value, p.allowed)
	if err != nil {
		return nil, err
	}
	for i, route := range routes {
		tr := p.base.Clone()
		if p.budget != nil {
			policy := "auto"
			if tr.Protocols != nil && !tr.Protocols.HTTP2() {
				policy = VersionHTTP11
			} else if tr.Protocols != nil && !tr.Protocols.HTTP1() {
				policy = VersionHTTP2
			}
			freezePolicyALPN(tr, policy)
		}
		tr.Proxy = http.ProxyURL(route)
		if route == nil {
			tr.Proxy = nil
		}
		dial := tr.DialContext
		if p.baseDial != nil {
			dial = p.baseDial
		}
		if dial == nil {
			dial = (&net.Dialer{}).DialContext
		}
		tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, e := net.SplitHostPort(address)
			if e != nil {
				return nil, e
			}
			ips, e := pacLookup(ctx, host, p.allowed)
			if e != nil {
				if errors.Is(e, errPACDNSNotFound) {
					return nil, &net.OpError{Op: "dial", Net: network, Err: e}
				}
				return nil, e
			}
			return dialPACIPs(ctx, network, port, ips, dial)
		}
		// Resolve destination under the same policy even when a proxy performs DNS.
		if _, e := pacLookup(req.Context(), req.URL.Hostname(), p.allowed); e != nil {
			return nil, e
		}
		attempt := req.Clone(req.Context())
		if i > 0 && req.Body != nil {
			if req.GetBody == nil {
				return nil, fmt.Errorf("PAC failover requires a replayable request body")
			}
			attempt.Body, err = req.GetBody()
			if err != nil {
				return nil, err
			}
		}
		bodyOwned = false
		var owner *httpConnectionOwner
		var roundTripper http.RoundTripper = tr
		if p.budget != nil {
			owner = p.budget.track(tr, tr.DialContext)
			roundTripper = owner
		}
		resp, e := roundTripper.RoundTrip(attempt)
		if e == nil {
			resp.Body = &pacResponseBody{ReadCloser: resp.Body, transport: tr, owner: owner}
			return resp, nil
		}
		tr.CloseIdleConnections()
		if owner != nil {
			owner.retire()
		}
		err = e
		// Only fail over connection-establishment errors. Never replay a request
		// after an ambiguous server-side write/read failure.
		dialFailure := false
		for cause := e; cause != nil; cause = errors.Unwrap(cause) {
			if op, ok := cause.(*net.OpError); ok && op.Op == "dial" {
				dialFailure = true
				break
			}
		}
		if !dialFailure {
			return nil, e
		}
	}
	return nil, err
}

type pacResponseBody struct {
	io.ReadCloser
	transport *http.Transport
	owner     *httpConnectionOwner
}

func (b *pacResponseBody) Close() error {
	err := b.ReadCloser.Close()
	b.transport.CloseIdleConnections()
	if b.owner != nil {
		b.owner.retire()
	}
	return err
}
