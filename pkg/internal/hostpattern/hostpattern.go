// Package hostpattern matches request hosts against the host entries of a
// recording's include and exclude filters.
package hostpattern

import (
	"net"
	"net/url"
	"strings"
)

// Match reports whether host is the entry's host or one of its subdomains.
// Matching is on whole labels, so "t.co" matches "t.co" and "x.t.co" but not
// "microsoft.com". An entry may start with "*.", which means the same; one
// with a port matches that port only; a pasted URL stands for its host.
// Case and a trailing dot are ignored.
func Match(host, entry string) bool {
	entryHost, entryPort := split(entry)
	if entryHost == "" {
		return false
	}
	hostName, hostPort := split(host)
	if entryPort != "" && entryPort != hostPort {
		return false
	}
	return hostName == entryHost || strings.HasSuffix(hostName, "."+entryHost)
}

// Index returns the index of the first entry host matches, or -1.
func Index(host string, entries []string) int {
	for index, entry := range entries {
		if Match(host, entry) {
			return index
		}
	}
	return -1
}

// split normalizes a host or entry to its lower-case name and port.
func split(value string) (string, string) {
	value = strings.ToLower(strings.TrimSpace(value))
	if strings.Contains(value, "://") {
		if parsed, err := url.Parse(value); err == nil {
			value = parsed.Host
		}
	}
	value = strings.TrimPrefix(value, "*.")
	port := ""
	if name, p, err := net.SplitHostPort(value); err == nil {
		value, port = name, p
	}
	value = strings.TrimSuffix(strings.Trim(value, "[]"), ".")
	return value, port
}
