package http

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// authHostScope is one entry of config.http.auth.hosts: a host that receives
// script-level credentials, optionally on one port only.
type authHostScope struct {
	host string
	port string // empty = any port
}

// ValidateAuthHosts reports the first malformed config.http.auth.hosts entry.
// Entries are a host name or IP, optionally with ":port"; schemes, paths and
// wildcards are rejected so a scope can never be broader than intended.
func ValidateAuthHosts(hosts []string) error {
	_, err := parseAuthHostScopes(hosts)
	return err
}

func parseAuthHostScopes(raw any) ([]authHostScope, error) {
	var entries []string
	switch typed := raw.(type) {
	case nil:
		return nil, nil
	case []string:
		entries = typed
	case []any:
		for _, item := range typed {
			value, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("auth hosts must be strings")
			}
			entries = append(entries, value)
		}
	default:
		return nil, fmt.Errorf("auth hosts must be a list of host names")
	}
	scopes := make([]authHostScope, 0, len(entries))
	for _, entry := range entries {
		scope, err := parseAuthHostScope(entry)
		if err != nil {
			return nil, err
		}
		scopes = append(scopes, scope)
	}
	return scopes, nil
}

func parseAuthHostScope(entry string) (authHostScope, error) {
	value := strings.ToLower(strings.TrimSpace(entry))
	if value == "" {
		return authHostScope{}, fmt.Errorf("auth host entries must not be empty")
	}
	if strings.Contains(value, "://") || strings.ContainsAny(value, "/?#*@ ") {
		return authHostScope{}, fmt.Errorf("auth host %q must be a host or host:port, without scheme, path or wildcard", entry)
	}
	host, port := value, ""
	if splitHost, splitPort, err := net.SplitHostPort(value); err == nil {
		number, convErr := strconv.Atoi(splitPort)
		if convErr != nil || number < 1 || number > 65535 {
			return authHostScope{}, fmt.Errorf("auth host %q has an invalid port", entry)
		}
		host, port = splitHost, splitPort
	} else if strings.Count(value, ":") == 1 {
		return authHostScope{}, fmt.Errorf("auth host %q has an invalid port", entry)
	}
	host = strings.Trim(host, "[]")
	if host == "" {
		return authHostScope{}, fmt.Errorf("auth host %q has no host name", entry)
	}
	return authHostScope{host: host, port: port}, nil
}

// authHostScopesAllow reports whether script-level credentials may be sent to
// target. An empty scope list allows every host.
func authHostScopesAllow(scopes []authHostScope, target string) bool {
	if len(scopes) == 0 {
		return true
	}
	parsed, err := url.Parse(target)
	if err != nil || parsed.Hostname() == "" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	port := parsed.Port()
	if port == "" {
		switch strings.ToLower(parsed.Scheme) {
		case "https", "wss":
			port = "443"
		default:
			port = "80"
		}
	}
	for _, scope := range scopes {
		if scope.host != host {
			continue
		}
		if scope.port == "" || scope.port == port {
			return true
		}
	}
	return false
}

// AuthHostScopeAllows reports whether script-level auth scoped to hosts
// applies to target, using the runtime's own matching. It lets validation
// decide before execution exactly as a request would at run time.
func AuthHostScopeAllows(hosts []string, target string) (bool, error) {
	scopes, err := parseAuthHostScopes(hosts)
	if err != nil {
		return false, err
	}
	return authHostScopesAllow(scopes, target), nil
}
