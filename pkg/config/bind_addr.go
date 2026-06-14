package config

import (
	"net"
	"net/netip"
	"strings"
)

const (
	BindScopeLoopback = "loopback"
	BindScopeWide     = "wide"
)

// ResolveControllerBindAddr returns the address the controller should listen
// on plus a coarse scope for operator warnings and UI identity.
func ResolveControllerBindAddr(getenv func(string) string) (string, string) {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	if addr := strings.TrimSpace(getenv("APEXLOAD_ADDR")); addr != "" {
		return addr, ClassifyBindScope(addr)
	}
	env := strings.ToLower(strings.TrimSpace(getenv("APEXLOAD_ENV")))
	if env == "production" || env == "prod" {
		return ":8080", BindScopeWide
	}
	return "127.0.0.1:8080", BindScopeLoopback
}

// ClassifyBindScope reports whether addr is loopback-only or reachable beyond
// the local machine. Hostnames other than localhost are treated as wide.
func ClassifyBindScope(addr string) string {
	host := bindHost(addr)
	if host == "" {
		return BindScopeWide
	}
	normalized := strings.ToLower(strings.Trim(host, "[]"))
	if normalized == "localhost" {
		return BindScopeLoopback
	}
	if normalized == "0.0.0.0" || normalized == "::" {
		return BindScopeWide
	}
	if ip, err := netip.ParseAddr(normalized); err == nil {
		if ip.IsLoopback() {
			return BindScopeLoopback
		}
		if ip.IsUnspecified() {
			return BindScopeWide
		}
		return BindScopeWide
	}
	return BindScopeWide
}

func bindHost(addr string) string {
	trimmed := strings.TrimSpace(addr)
	if trimmed == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(trimmed); err == nil {
		return host
	}
	if strings.HasPrefix(trimmed, ":") && strings.Count(trimmed, ":") == 1 {
		return ""
	}
	if strings.HasPrefix(trimmed, "[") && strings.Contains(trimmed, "]") {
		return strings.Trim(trimmed[:strings.Index(trimmed, "]")+1], "[]")
	}
	if idx := strings.LastIndex(trimmed, ":"); idx > 0 && strings.Count(trimmed, ":") == 1 {
		return trimmed[:idx]
	}
	return trimmed
}
