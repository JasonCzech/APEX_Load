package http

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// ManagedRunHeader is an opt-in marker consumed by AppDynamics data collectors.
const ManagedRunHeader = "X-Apex-Test-Run"

// TransactionMarkerHeader is Dynatrace's load-testing request marker. APEX Load
// fills it with names only (VUser, transaction, script, test), never values.
const TransactionMarkerHeader = "X-Dynatrace-Test"

// Correlation carriers name what a run may add to requests sent to its
// controller-approved origins. A policy without explicit carriers carries only
// the run marker, which is the behaviour that predates carriers.
const (
	CorrelationCarrierRunMarker         = "run_marker"
	CorrelationCarrierTraceparent       = "traceparent"
	CorrelationCarrierTransactionMarker = "transaction_marker"
)

const (
	maxCorrelationOrigins          = 32
	maxTransactionMarkerFieldBytes = 64
	maxTransactionMarkerBytes      = 256
)

// ValidateManagedRunCorrelationOrigins validates controller-owned assignment
// metadata before the generator prepares a protocol runtime.
func ValidateManagedRunCorrelationOrigins(runID string, origins []string) error {
	return ValidateManagedRunCorrelation(runID, origins, nil)
}

// ValidateManagedRunCorrelation validates origins together with the carriers
// the controller enabled for them.
func ValidateManagedRunCorrelation(runID string, origins []string, carriers []string) error {
	_, err := newRunCorrelationPolicy(map[string]any{
		"apexload_run_correlation_id":      runID,
		"apexload_run_correlation_origins": origins,
		"apexload_correlation_carriers":    carriers,
	})
	return err
}

type runCorrelationPolicy struct {
	runID             string
	testName          string
	scriptName        string
	origins           map[string]struct{}
	runMarker         bool
	traceparent       bool
	transactionMarker bool
}

func newRunCorrelationPolicy(config map[string]any) (runCorrelationPolicy, error) {
	if raw, ok := config["apexload_run_correlation_origins"].([]string); ok {
		if len(raw) > maxCorrelationOrigins {
			return runCorrelationPolicy{}, errors.New("run correlation supports at most 32 approved origins")
		}
		for _, origin := range raw {
			if strings.TrimSpace(origin) == "" {
				return runCorrelationPolicy{}, errors.New("run correlation origin must not be empty")
			}
		}
	}
	carriers := protocol.ConfigStringSlice(config, "apexload_correlation_carriers")
	origins := protocol.ConfigStringSlice(config, "apexload_run_correlation_origins")
	if len(origins) == 0 {
		if len(carriers) > 0 {
			return runCorrelationPolicy{}, errors.New("correlation carriers require at least one approved origin")
		}
		return runCorrelationPolicy{}, nil
	}
	if len(origins) > maxCorrelationOrigins {
		return runCorrelationPolicy{}, errors.New("run correlation supports at most 32 approved origins")
	}
	runID := protocol.ConfigString(config, "apexload_run_correlation_id")
	if runID == "" || len(runID) > 256 || strings.ContainsAny(runID, "\r\n") {
		return runCorrelationPolicy{}, errors.New("run correlation requires a valid managed run identity")
	}
	policy := runCorrelationPolicy{
		runID:      runID,
		testName:   protocol.ConfigString(config, "apexload_correlation_test_name"),
		scriptName: protocol.ConfigString(config, "apexload_correlation_script_name"),
		origins:    make(map[string]struct{}, len(origins)),
	}
	if len(carriers) == 0 {
		policy.runMarker = true
	}
	for _, carrier := range carriers {
		switch strings.ToLower(strings.TrimSpace(carrier)) {
		case CorrelationCarrierRunMarker:
			policy.runMarker = true
		case CorrelationCarrierTraceparent:
			policy.traceparent = true
		case CorrelationCarrierTransactionMarker:
			policy.transactionMarker = true
		default:
			return runCorrelationPolicy{}, fmt.Errorf("unsupported correlation carrier %q", carrier)
		}
	}
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

func (policy runCorrelationPolicy) approved(target *url.URL) bool {
	if !policy.enabled() || target == nil {
		return false
	}
	_, approved := policy.origins[correlationOrigin(target)]
	return approved
}

// scopesTraceparent reports whether the run limits generated trace context to
// its approved origins. Scripts' own traceparent settings govern otherwise.
func (policy runCorrelationPolicy) scopesTraceparent() bool {
	return policy.enabled() && policy.traceparent
}

// correlationAuthorship records which correlation headers the script authored
// for one logical request. Authored headers are script content and are never
// rewritten or stripped; only platform-generated ones follow the policy.
type correlationAuthorship struct {
	traceparent       bool
	transactionMarker bool
}

type correlationAuthorshipKey struct{}

func withCorrelationAuthorship(ctx context.Context, authorship correlationAuthorship) context.Context {
	return context.WithValue(ctx, correlationAuthorshipKey{}, authorship)
}

func correlationAuthorshipFromContext(ctx context.Context) (correlationAuthorship, bool) {
	authorship, ok := ctx.Value(correlationAuthorshipKey{}).(correlationAuthorship)
	return authorship, ok
}

// markCorrelationAuthorship captures authored correlation headers before the
// platform adds any, so every later hop can tell the two apart.
func (policy runCorrelationPolicy) markCorrelationAuthorship(req *http.Request) *http.Request {
	if !policy.enabled() || (!policy.traceparent && !policy.transactionMarker) {
		return req
	}
	return req.WithContext(withCorrelationAuthorship(req.Context(), correlationAuthorship{
		traceparent:       requestHeaderKeyPresent(req.Header, "Traceparent"),
		transactionMarker: requestHeaderKeyPresent(req.Header, TransactionMarkerHeader),
	}))
}

// apply enforces the policy on one outbound hop. Go copies arbitrary headers
// across redirects, so platform headers are removed at every hop that is not
// an approved origin, including resource requests and auth retries.
func (policy runCorrelationPolicy) apply(req *http.Request) error {
	if !policy.enabled() {
		return nil
	}
	approved := policy.approved(req.URL)
	if policy.runMarker {
		if err := policy.applyRunMarker(req, approved); err != nil {
			return err
		}
	}
	authorship, known := correlationAuthorshipFromContext(req.Context())
	if !known {
		return nil
	}
	if policy.transactionMarker && !authorship.transactionMarker {
		deleteHeaderFold(req.Header, TransactionMarkerHeader)
		if approved {
			if marker := policy.transactionMarkerValue(req.Context()); marker != "" {
				req.Header.Set(TransactionMarkerHeader, marker)
			}
		}
	}
	if policy.traceparent && !authorship.traceparent && !approved {
		deleteHeaderFold(req.Header, "Traceparent")
		deleteHeaderFold(req.Header, "Tracestate")
	}
	return nil
}

func (policy runCorrelationPolicy) applyRunMarker(req *http.Request, approved bool) error {
	if !approved {
		deleteHeaderFold(req.Header, ManagedRunHeader)
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

// transactionMarkerValue builds Dynatrace's load-test marker from names the
// controller and engine supplied. Values are bounded and stripped of the
// marker's own delimiters so no authored text can add or forge a field.
func (policy runCorrelationPolicy) transactionMarkerValue(ctx context.Context) string {
	scope, _ := protocol.CorrelationScopeFromContext(ctx)
	fields := []string{
		"VU=" + strconv.Itoa(scope.VUserID),
		"SI=APEXLoad",
	}
	if name := sanitizeTransactionMarkerField(scope.Transaction); name != "" {
		fields = append(fields, "TSN="+name)
	}
	if name := sanitizeTransactionMarkerField(policy.scriptName); name != "" {
		fields = append(fields, "LSN="+name)
	}
	testName := policy.testName
	if strings.TrimSpace(testName) == "" {
		testName = policy.runID
	}
	if name := sanitizeTransactionMarkerField(testName); name != "" {
		fields = append(fields, "LTN="+name)
	}
	marker := ""
	for _, field := range fields {
		next := field
		if marker != "" {
			next = marker + ";" + field
		}
		if len(next) > maxTransactionMarkerBytes {
			break
		}
		marker = next
	}
	return marker
}

func sanitizeTransactionMarkerField(value string) string {
	var builder strings.Builder
	for _, r := range strings.TrimSpace(value) {
		if r == ';' || r == '=' || r < 0x20 || r == 0x7f || r > 0x7e {
			continue
		}
		if builder.Len()+1 > maxTransactionMarkerFieldBytes {
			break
		}
		builder.WriteRune(r)
	}
	return strings.TrimSpace(builder.String())
}

func deleteHeaderFold(header http.Header, name string) {
	for key := range header {
		if strings.EqualFold(key, name) {
			delete(header, key)
		}
	}
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
