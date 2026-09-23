package http

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

type replayOutcomePolicy struct {
	httpWarnings, failResources, resourceTimeoutWarning, timeoutConfigured, legacyChecks, resourcesConfigured bool
}

func parseReplayOutcomePolicy(config map[string]any) replayOutcomePolicy {
	p := replayOutcomePolicy{legacyChecks: true}
	p.httpWarnings, _ = config["http_errors_as_warnings"].(bool)
	p.failResources, p.resourcesConfigured = config["fail_noncritical_resources"].(bool)
	p.resourceTimeoutWarning, p.timeoutConfigured = config["resource_timeout_as_warning"].(bool)
	if value, ok := config["legacy_html_checks"].(bool); ok {
		p.legacyChecks = value
	}
	return p
}
func addReplayWarning(result *protocol.Result, code string) {
	if result.ProtocolEvidence == nil {
		result.ProtocolEvidence = map[string]any{}
	}
	result.ProtocolEvidence[code] = true
	for _, existing := range result.WarningCodes {
		if existing == code {
			return
		}
	}
	if len(result.WarningCodes) < 8 {
		result.WarningCodes = append(result.WarningCodes, code)
	}
}
func resourceFailureKind(err error) string {
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	var timeout net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &timeout) && timeout.Timeout()) {
		return "timeout"
	}
	return "transport"
}
func (p replayOutcomePolicy) applyResources(ctx context.Context, result *protocol.Result) {
	for _, entry := range result.NetworkEntries {
		if entry.FailureKind == "" {
			continue
		}
		critical := strings.HasPrefix(entry.ResourceType, "document")
		fail := p.failResources || (critical && p.resourcesConfigured)
		switch entry.FailureKind {
		case "http":
			if p.httpWarnings {
				fail = false
			}
		case "timeout":
			if p.timeoutConfigured {
				fail = !p.resourceTimeoutWarning || strings.HasPrefix(entry.ResourceType, "document")
			}
		case "canceled":
			fail = true
		}
		if fail {
			result.Success = false
			if result.ErrorMessage == "" {
				result.ErrorMessage = "embedded resource failed (" + entry.FailureKind + ")"
			}
		} else {
			addReplayWarning(result, "resource_warning")
		}
	}
	if ctx.Err() != nil {
		if context.Cause(ctx) == errPageDeadline && p.resourceTimeoutWarning && result.Success {
			addReplayWarning(result, "resource_timeout_warning")
		} else {
			result.Success = false
			if result.ErrorMessage == "" {
				result.ErrorMessage = ctx.Err().Error()
			}
		}
	}
}

// Each HTTP instance belongs to one VUser. Do not share addresses across users
// or cache failures. The base dialer retains source address, network family and
// connection deadlines; URL/redirect policy still runs above the transport.
func cachedDNSDialer(base func(context.Context, string, string) (net.Conn, error), timeout time.Duration) func(context.Context, string, string) (net.Conn, error) {
	dial := newCachedDNSDialer(base, net.DefaultResolver.LookupIPAddr, time.Now)
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		if timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}
		return dial(ctx, network, address)
	}
}
func newCachedDNSDialer(base func(context.Context, string, string) (net.Conn, error), lookup func(context.Context, string) ([]net.IPAddr, error), now func() time.Time) func(context.Context, string, string) (net.Conn, error) {
	type entry struct {
		addresses []net.IPAddr
		expires   time.Time
	}
	var mu sync.Mutex
	cache := map[string]entry{}
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		host, port, err := net.SplitHostPort(address)
		if err != nil || net.ParseIP(host) != nil || strings.Contains(host, "%") {
			return base(ctx, network, address)
		}
		mu.Lock()
		hit, ok := cache[host]
		mu.Unlock()
		if !ok || !now().Before(hit.expires) {
			addresses, err := lookup(ctx, host)
			if err != nil {
				return nil, err
			}
			if len(addresses) == 0 {
				return nil, fmt.Errorf("DNS returned no addresses")
			}
			if len(addresses) > 16 {
				addresses = addresses[:16]
			}
			copied := make([]net.IPAddr, len(addresses))
			for i, a := range addresses {
				copied[i] = net.IPAddr{IP: append(net.IP(nil), a.IP...), Zone: a.Zone}
			}
			hit = entry{addresses: copied, expires: now().Add(60 * time.Second)}
			mu.Lock()
			if len(cache) >= 256 {
				for key, value := range cache {
					if !now().Before(value.expires) {
						delete(cache, key)
					}
				}
				if len(cache) >= 256 {
					for key := range cache {
						delete(cache, key)
						break
					}
				}
			}
			cache[host] = hit
			mu.Unlock()
		}
		var last error
		for _, address := range hit.addresses {
			if network == "tcp4" && address.IP.To4() == nil || network == "tcp6" && address.IP.To4() != nil {
				continue
			}
			conn, err := base(ctx, network, net.JoinHostPort(address.String(), port))
			if err == nil {
				return conn, nil
			}
			last = err
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
		}
		if last == nil {
			last = fmt.Errorf("DNS returned no addresses for requested IP family")
		}
		return nil, last
	}
}
