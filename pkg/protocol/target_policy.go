package protocol

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// TargetPolicyConfigKey carries a controller-injected host allowlist through
// engine protocol initialization and distributed generator execution.
const TargetPolicyConfigKey = "apexload_allowed_target_hosts"

// TargetPolicyHosts returns a normalized copy of the runtime host allowlist.
func TargetPolicyHosts(config map[string]any) []string {
	if len(config) == 0 {
		return nil
	}
	var values []string
	switch raw := config[TargetPolicyConfigKey].(type) {
	case []string:
		values = raw
	case []any:
		for _, value := range raw {
			if text, ok := value.(string); ok {
				values = append(values, text)
			}
		}
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(value), "."))
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

// TargetURLAllowed validates the final network host of an outbound URL. Empty
// policies preserve ordinary non-CI execution. Browser-local schemes are
// allowed; blob URLs are checked against their embedded origin.
func TargetURLAllowed(raw string, allowedHosts []string) bool {
	if len(allowedHosts) == 0 {
		return true
	}
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https", "ws", "wss":
		return TargetHostAllowed(parsed.Hostname(), allowedHosts)
	case "about", "data":
		return true
	case "blob":
		return TargetURLAllowed(strings.TrimPrefix(strings.TrimSpace(raw), "blob:"), allowedHosts)
	default:
		return false
	}
}

// TargetHostAllowed matches a concrete host against controller-validated host
// globs. Matching is case-insensitive and never includes ports or URL paths.
func TargetHostAllowed(host string, allowedHosts []string) bool {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if host == "" || len(allowedHosts) == 0 {
		return false
	}
	for _, pattern := range allowedHosts {
		pattern = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(pattern), "."))
		if pattern == host {
			return true
		}
		if matched, err := path.Match(pattern, host); err == nil && matched {
			return true
		}
	}
	return false
}

// ValidateTargetHost enforces a concrete, already-resolved host. An empty
// policy preserves ordinary non-CI execution.
func ValidateTargetHost(host string, allowedHosts []string) error {
	if len(allowedHosts) == 0 {
		return nil
	}
	host = normalizeTargetHost(host)
	if host != "" && TargetHostAllowed(host, allowedHosts) {
		return nil
	}
	if host == "" {
		host = "unresolved"
	}
	return fmt.Errorf("outbound target host %q is not allowed by the CI runtime policy", host)
}

// TargetAddressHost extracts the host from a final URL, gRPC resolver target,
// or host[:port] address. It intentionally returns no userinfo, path, or query
// material so policy failures cannot disclose credentials.
func TargetAddressHost(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("target address is empty")
	}
	if strings.Contains(raw, "{{") || strings.Contains(raw, "}}") {
		return "", fmt.Errorf("target address is unresolved")
	}

	if strings.Contains(raw, "://") {
		parsed, err := url.Parse(raw)
		if err != nil {
			return "", fmt.Errorf("target address is invalid")
		}
		switch strings.ToLower(parsed.Scheme) {
		case "dns", "passthrough":
			// In gRPC resolver targets the actual endpoint is the path; the URL
			// host is resolver authority and must never stand in for the peer.
			if endpoint := strings.TrimLeft(parsed.Path, "/"); endpoint != "" {
				return targetBareAddressHost(endpoint)
			}
			return targetBareAddressHost(parsed.Host)
		case "xds", "unix", "unix-abstract":
			// These schemes can select peers without a concrete policy-checkable
			// network host. CI runs fail closed instead of trusting an authority.
			return "", fmt.Errorf("target address does not contain a concrete network host")
		}
		if host := normalizeTargetHost(parsed.Hostname()); host != "" {
			return host, nil
		}
		return "", fmt.Errorf("target address does not contain a network host")
	}
	return targetBareAddressHost(raw)
}

func targetBareAddressHost(raw string) (string, error) {
	raw = strings.TrimSpace(strings.Trim(raw, "{}"))
	for _, prefix := range []string{"tcp:", "udp:"} {
		if strings.HasPrefix(strings.ToLower(raw), prefix) {
			raw = strings.TrimSpace(raw[len(prefix):])
			break
		}
	}
	if host, _, err := net.SplitHostPort(raw); err == nil {
		if host = normalizeTargetHost(strings.Trim(host, "[]")); host != "" {
			return host, nil
		}
	}
	if index := strings.LastIndex(raw, ","); index > 0 {
		if _, err := strconv.Atoi(strings.TrimSpace(raw[index+1:])); err == nil {
			raw = strings.TrimSpace(raw[:index])
		}
	}
	if ip := net.ParseIP(strings.Trim(raw, "[]")); ip != nil {
		return normalizeTargetHost(ip.String()), nil
	}
	if index := strings.LastIndex(raw, ":"); index > 0 {
		if _, err := strconv.Atoi(raw[index+1:]); err == nil {
			raw = raw[:index]
		}
	}
	if strings.ContainsAny(raw, "/?#@;=() \t\r\n\x00\\") {
		return "", fmt.Errorf("target address is invalid")
	}
	host := normalizeTargetHost(strings.Trim(raw, "[]"))
	if host == "" {
		return "", fmt.Errorf("target address does not contain a network host")
	}
	return host, nil
}

var (
	dsnHostAssignmentPattern = regexp.MustCompile(`(?i)(?:^|[;[:space:]])(?:host|hostname|server|endpoint|data[ _]source|address|addr|network[ _]address)\s*=\s*(?:'([^']*)'|"([^"]*)"|([^;[:space:]]+))`)
	oracleHostPattern        = regexp.MustCompile(`(?i)\(\s*host\s*=\s*([^()[:space:]]+)\s*\)`)
)

// TargetDSNHosts extracts every network endpoint carried by a supported
// database DSN. Multi-host PostgreSQL/ODBC configurations are all returned so
// failover cannot escape the project allowlist.
func TargetDSNHosts(driver, dsn string) ([]string, error) {
	driver = strings.ToLower(strings.TrimSpace(driver))
	dsn = strings.TrimSpace(dsn)
	if driver == "sqlite" || driver == "sqlite3" {
		return nil, nil
	}
	if dsn == "" || strings.Contains(dsn, "{{") || strings.Contains(dsn, "}}") {
		return nil, fmt.Errorf("database DSN target is unresolved")
	}

	set := map[string]struct{}{}
	addAddress := func(address string) error {
		address = strings.TrimSpace(strings.Trim(address, "{}"))
		if address == "" {
			return nil
		}
		for _, candidate := range strings.Split(address, ",") {
			candidate = strings.TrimSpace(candidate)
			if candidate == "" || onlyDecimal(candidate) {
				continue
			}
			host, err := TargetAddressHost(candidate)
			if err != nil {
				return err
			}
			set[host] = struct{}{}
		}
		return nil
	}

	lower := strings.ToLower(dsn)
	if marker := strings.Index(lower, "@tcp("); marker >= 0 {
		start := marker + len("@tcp(")
		if end := strings.Index(dsn[start:], ")"); end >= 0 {
			if err := addAddress(dsn[start : start+end]); err != nil {
				return nil, err
			}
		}
	}
	if strings.Contains(dsn, "://") {
		parsed, err := url.Parse(dsn)
		if err == nil && parsed.Host != "" {
			authority := parsed.Host
			if parsed.User != nil {
				authority = strings.TrimPrefix(authority, parsed.User.String()+"@")
			}
			if err := addAddress(authority); err != nil {
				return nil, err
			}
		}
	}
	for _, match := range dsnHostAssignmentPattern.FindAllStringSubmatch(dsn, -1) {
		value := firstNonEmptyString(match[1:]...)
		if err := addAddress(value); err != nil {
			return nil, err
		}
	}
	for _, match := range oracleHostPattern.FindAllStringSubmatch(dsn, -1) {
		if err := addAddress(match[1]); err != nil {
			return nil, err
		}
	}
	if len(set) == 0 && (driver == "oracle" || strings.Contains(dsn, "@")) {
		address := dsn
		if index := strings.LastIndex(address, "@"); index >= 0 {
			address = address[index+1:]
		}
		address = strings.TrimPrefix(address, "//")
		if slash := strings.Index(address, "/"); slash >= 0 {
			address = address[:slash]
		}
		if err := addAddress(address); err != nil {
			return nil, err
		}
	}
	if len(set) == 0 {
		if err := addAddress(dsn); err != nil {
			return nil, fmt.Errorf("database DSN does not contain a concrete network host")
		}
	}
	hosts := make([]string, 0, len(set))
	for host := range set {
		hosts = append(hosts, host)
	}
	sort.Strings(hosts)
	return hosts, nil
}

func onlyDecimal(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func normalizeTargetHost(host string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
}

// ValidateTargetAddress enforces the final address immediately before a dial.
func ValidateTargetAddress(raw string, allowedHosts []string) error {
	if len(allowedHosts) == 0 {
		return nil
	}
	host, err := TargetAddressHost(raw)
	if err != nil {
		return fmt.Errorf("outbound target host %q is not allowed by the CI runtime policy", "unresolved")
	}
	return ValidateTargetHost(host, allowedHosts)
}

// TargetReferenceHosts accepts either a single address/URL or a structured
// connection string such as an Azure Service Bus Endpoint=... value.
func TargetReferenceHosts(raw string) ([]string, error) {
	if host, err := TargetAddressHost(raw); err == nil {
		return []string{host}, nil
	}
	return TargetDSNHosts("", raw)
}

// ValidateTargetReference enforces an address or structured connection string.
func ValidateTargetReference(raw string, allowedHosts []string) error {
	if len(allowedHosts) == 0 {
		return nil
	}
	hosts, err := TargetReferenceHosts(raw)
	if err != nil || len(hosts) == 0 {
		return fmt.Errorf("outbound target host %q is not allowed by the CI runtime policy", "unresolved")
	}
	for _, host := range hosts {
		if err := ValidateTargetHost(host, allowedHosts); err != nil {
			return err
		}
	}
	return nil
}

// ValidateTargetDSN enforces every host in a potentially multi-host DSN.
func ValidateTargetDSN(driver, dsn string, allowedHosts []string) error {
	if len(allowedHosts) == 0 || strings.EqualFold(strings.TrimSpace(driver), "sqlite") || strings.EqualFold(strings.TrimSpace(driver), "sqlite3") {
		return nil
	}
	hosts, err := TargetDSNHosts(driver, dsn)
	if err != nil || len(hosts) == 0 {
		return fmt.Errorf("outbound target host %q is not allowed by the CI runtime policy", "unresolved")
	}
	for _, host := range hosts {
		if err := ValidateTargetHost(host, allowedHosts); err != nil {
			return err
		}
	}
	return nil
}

// ValidateTargetURL returns an error containing only the rejected host, never a
// query string or userinfo that might carry credentials.
func ValidateTargetURL(raw string, allowedHosts []string) error {
	if TargetURLAllowed(raw, allowedHosts) {
		return nil
	}
	parsed, _ := url.Parse(strings.TrimSpace(raw))
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if host == "" {
		host = "unresolved"
	}
	return fmt.Errorf("outbound target host %q is not allowed by the CI runtime policy", host)
}

// NewTargetPolicyProxy validates the concrete proxy selected for each request,
// including proxies discovered from the runner environment.
func NewTargetPolicyProxy(proxy func(*http.Request) (*url.URL, error), allowedHosts []string) func(*http.Request) (*url.URL, error) {
	if proxy == nil || len(allowedHosts) == 0 {
		return proxy
	}
	return func(request *http.Request) (*url.URL, error) {
		selected, err := proxy(request)
		if err != nil || selected == nil {
			return selected, err
		}
		if err := ValidateTargetAddress(selected.String(), allowedHosts); err != nil {
			return nil, err
		}
		return selected, nil
	}
}

type targetPolicyRoundTripper struct {
	base         http.RoundTripper
	allowedHosts []string
}

// NewTargetPolicyRoundTripper enforces the final request URL immediately before
// every HTTP round trip, including OAuth requests and redirect legs.
func NewTargetPolicyRoundTripper(base http.RoundTripper, allowedHosts []string) http.RoundTripper {
	if len(allowedHosts) == 0 {
		return base
	}
	if base == nil {
		base = http.DefaultTransport
	}
	return &targetPolicyRoundTripper{base: base, allowedHosts: append([]string(nil), allowedHosts...)}
}

func (transport *targetPolicyRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	if request == nil || request.URL == nil {
		return nil, fmt.Errorf("outbound request URL is required by the CI runtime policy")
	}
	if err := ValidateTargetURL(request.URL.String(), transport.allowedHosts); err != nil {
		return nil, err
	}
	return transport.base.RoundTrip(request)
}
