package http

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// ManagedRunHeader is an opt-in marker consumed by AppDynamics data collectors.
const ManagedRunHeader = "X-Apex-Test-Run"

// ValidateManagedRunCorrelationOrigins validates controller-owned assignment
// metadata before the generator prepares a protocol runtime.
func ValidateManagedRunCorrelationOrigins(runID string, origins []string) error {
	_, err := newRunCorrelationPolicy(map[string]any{"apexload_run_correlation_id": runID, "apexload_run_correlation_origins": origins})
	return err
}

type runCorrelationPolicy struct {
	runID   string
	origins map[string]struct{}
}

func newRunCorrelationPolicy(config map[string]any) (runCorrelationPolicy, error) {
	if raw, ok := config["apexload_run_correlation_origins"].([]string); ok {
		if len(raw) > 32 {
			return runCorrelationPolicy{}, errors.New("run correlation supports at most 32 approved origins")
		}
		for _, origin := range raw {
			if strings.TrimSpace(origin) == "" {
				return runCorrelationPolicy{}, errors.New("run correlation origin must not be empty")
			}
		}
	}
	origins := protocol.ConfigStringSlice(config, "apexload_run_correlation_origins")
	if len(origins) == 0 {
		return runCorrelationPolicy{}, nil
	}
	if len(origins) > 32 {
		return runCorrelationPolicy{}, errors.New("run correlation supports at most 32 approved origins")
	}
	runID := protocol.ConfigString(config, "apexload_run_correlation_id")
	if runID == "" || len(runID) > 256 || strings.ContainsAny(runID, "\r\n") {
		return runCorrelationPolicy{}, errors.New("run correlation requires a valid managed run identity")
	}
	policy := runCorrelationPolicy{runID: runID, origins: make(map[string]struct{}, len(origins))}
	for _, raw := range origins {
		parsed, err := url.Parse(raw)
		if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Hostname() == "" || parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Opaque != "" {
			return runCorrelationPolicy{}, fmt.Errorf("run correlation origin must be an HTTP or HTTPS origin without credentials, path, query, or fragment")
		}
		policy.origins[correlationOrigin(parsed)] = struct{}{}
	}
	return policy, nil
}

func correlationOrigin(target *url.URL) string {
	port := target.Port()
	if port == "" {
		if target.Scheme == "https" {
			port = "443"
		} else if target.Scheme == "http" {
			port = "80"
		}
	}
	return strings.ToLower(target.Scheme) + "://" + strings.ToLower(target.Hostname()) + ":" + port
}

func (policy runCorrelationPolicy) enabled() bool {
	return policy.runID != "" && len(policy.origins) > 0
}

func (policy runCorrelationPolicy) apply(req *http.Request) error {
	if !policy.enabled() {
		return nil
	}
	if _, approved := policy.origins[correlationOrigin(req.URL)]; !approved {
		// Go copies arbitrary headers across redirects. Remove the marker at
		// every outbound hop, including resource requests and auth retries.
		for name := range req.Header {
			if strings.EqualFold(name, ManagedRunHeader) {
				delete(req.Header, name)
			}
		}
		return nil
	}
	for name, values := range req.Header {
		if !strings.EqualFold(name, ManagedRunHeader) {
			continue
		}
		for _, value := range values {
			if value != policy.runID {
				return errors.New("authored X-Apex-Test-Run conflicts with the managed run identity")
			}
		}
		delete(req.Header, name)
	}
	req.Header.Set(ManagedRunHeader, policy.runID)
	return nil
}

type runCorrelationRoundTripper struct {
	base   http.RoundTripper
	policy runCorrelationPolicy
}

func (transport runCorrelationRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	copy := req.Clone(req.Context())
	if err := transport.policy.apply(copy); err != nil {
		return nil, err
	}
	return transport.base.RoundTrip(copy)
}
