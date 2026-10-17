package http

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

type replayOutcomePolicy struct {
	httpWarnings, failResources, resourceTimeoutWarning, timeoutConfigured, legacyChecks, resourcesConfigured bool
	// acceptRecordedAura accepts Aura actions that end in the same
	// unsuccessful state as in the recording.
	acceptRecordedAura bool
}

// AuraRecordedFailurePolicies are the config.http.aura_recorded_failures
// values; empty means require_success.
var AuraRecordedFailurePolicies = []string{"", "require_success", "accept_recorded"}

func parseReplayOutcomePolicy(config map[string]any) replayOutcomePolicy {
	p := replayOutcomePolicy{legacyChecks: true}
	p.httpWarnings, _ = config["http_errors_as_warnings"].(bool)
	p.failResources, p.resourcesConfigured = config["fail_noncritical_resources"].(bool)
	p.resourceTimeoutWarning, p.timeoutConfigured = config["resource_timeout_as_warning"].(bool)
	if value, ok := config["legacy_html_checks"].(bool); ok {
		p.legacyChecks = value
	}
	policy, _ := config["aura_recorded_failures"].(string)
	p.acceptRecordedAura = strings.EqualFold(strings.TrimSpace(policy), "accept_recorded")
	return p
}

var unresolvedVariablePattern = regexp.MustCompile(`\{\{\s*var\.([A-Za-z_][A-Za-z0-9_.]{0,127})\s*\}\}`)

// noteUnresolvedVariables records variables a request sends as placeholder
// text because nothing produced them. Like LoadRunner, which sends an
// undefined {param} literally and warns that it "is not a parameter", the
// request still goes out; the warning names the variables (script names, never
// values) so a missing producer is visible in run results.
func noteUnresolvedVariables(result *protocol.Result, target string, body []byte, headers protocol.HeaderList) {
	var names []string
	seen := map[string]bool{}
	collect := func(text string) {
		if !strings.Contains(text, "{{") {
			return
		}
		for _, match := range unresolvedVariablePattern.FindAllStringSubmatch(text, -1) {
			if !seen[match[1]] && len(names) < 8 {
				seen[match[1]] = true
				names = append(names, match[1])
			}
		}
	}
	collect(target)
	if bytes.Contains(body, []byte("{{")) {
		collect(string(body))
	}
	for _, header := range headers {
		collect(header.Value)
	}
	if len(names) == 0 {
		return
	}
	addReplayWarning(result, "unresolved_variable_sent")
	result.ProtocolEvidence["unresolved_variables"] = names
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
		eligible := make([]net.IPAddr, 0, len(hit.addresses))
		for _, address := range hit.addresses {
			if network == "tcp4" && address.IP.To4() == nil || network == "tcp6" && address.IP.To4() != nil {
				continue
			}
			eligible = append(eligible, address)
		}
		var last error
		for index, address := range eligible {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			// Numeric-address dials cannot use net.Dialer's hostname fallback.
			// Reserve a share of the remaining deadline for every eligible
			// address so one unreachable address cannot consume the whole dial.
			attemptCtx := ctx
			cancel := func() {}
			if deadline, ok := ctx.Deadline(); ok && index+1 < len(eligible) {
				attemptCtx, cancel = context.WithTimeout(ctx, time.Until(deadline)/time.Duration(len(eligible)-index))
			}
			conn, err := base(attemptCtx, network, net.JoinHostPort(address.String(), port))
			cancel()
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
