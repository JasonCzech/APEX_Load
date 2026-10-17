package httpcontract

import (
	"strconv"
	"strings"
	"time"
)

// Alt-Svc parsing (RFC 7838). The parser is bounded and lenient: a malformed
// alternative is skipped and counted, never fatal, because a response header
// written by the system under test is untrusted input.

const (
	maxAltSvcFieldBytes = 8 << 10
	maxAltSvcEntries    = 16
	// DefaultAltSvcMaxAge is the RFC 7838 freshness lifetime when "ma" is absent.
	DefaultAltSvcMaxAge = 24 * time.Hour
	// maxAltSvcMaxAge clamps huge advertised ages so a cache entry cannot
	// outlive any realistic test run by orders of magnitude.
	maxAltSvcMaxAge = 30 * 24 * time.Hour
)

// AltSvcEntry is one advertised alternative service.
type AltSvcEntry struct {
	// ProtocolID is the percent-decoded ALPN token, such as "h3" or "h3-29".
	ProtocolID string
	// Host is the alternative host; empty means the origin's own host.
	Host string
	// Port is the alternative port, 1–65535.
	Port int
	// MaxAge is the freshness lifetime, clamped to 30 days.
	MaxAge time.Duration
	// Persist is set by "persist=1": keep the entry across network changes.
	Persist bool
}

// FinalH3 reports whether the entry advertises final (RFC 9114) HTTP/3.
// Draft tokens such as "h3-29" are not final HTTP/3.
func (e AltSvcEntry) FinalH3() bool { return e.ProtocolID == "h3" }

// AltSvc is a parsed Alt-Svc field.
type AltSvc struct {
	// Clear is set by the "clear" value: forget every alternative for the origin.
	Clear bool
	// Entries are the valid alternatives, in field order, at most 16.
	Entries []AltSvcEntry
	// Invalid counts alternatives that were skipped as malformed.
	Invalid int
	// Truncated is set when the field exceeded 8 KiB or 16 alternatives.
	Truncated bool
}

// FinalH3 returns the entries that advertise final HTTP/3.
func (a AltSvc) FinalH3() []AltSvcEntry {
	var out []AltSvcEntry
	for _, entry := range a.Entries {
		if entry.FinalH3() {
			out = append(out, entry)
		}
	}
	return out
}

// AltSvcAdvertisesH3 reports whether the Alt-Svc field values advertise final
// HTTP/3 on any host or port.
func AltSvcAdvertisesH3(values []string) bool {
	return len(ParseAltSvc(values).FinalH3()) > 0
}

// ParseAltSvc parses every Alt-Svc field value of one response.
func ParseAltSvc(values []string) AltSvc {
	var result AltSvc
	field := strings.Join(values, ",")
	if len(field) > maxAltSvcFieldBytes {
		field = field[:maxAltSvcFieldBytes]
		result.Truncated = true
	}
	elements := splitOutsideQuotes(field, ',')
	nonEmpty := 0
	for _, element := range elements {
		element = strings.TrimSpace(element)
		if element == "" {
			continue
		}
		nonEmpty++
		if element == "clear" {
			result.Clear = true
			continue
		}
		if len(result.Entries) >= maxAltSvcEntries {
			result.Truncated = true
			continue
		}
		entry, ok := parseAltValue(element)
		if !ok {
			result.Invalid++
			continue
		}
		result.Entries = append(result.Entries, entry)
	}
	if result.Clear && nonEmpty > 1 {
		// "clear" must be the whole field value. A field that mixes it with
		// alternatives is malformed; honor neither part.
		result.Clear = false
		result.Invalid += len(result.Entries) + 1
		result.Entries = nil
	}
	return result
}

func parseAltValue(element string) (AltSvcEntry, bool) {
	parts := splitOutsideQuotes(element, ';')
	alternative := strings.TrimSpace(parts[0])
	name, authority, found := strings.Cut(alternative, "=")
	if !found {
		return AltSvcEntry{}, false
	}
	protocolID, ok := percentDecodeToken(strings.TrimSpace(name))
	if !ok || protocolID == "" {
		return AltSvcEntry{}, false
	}
	authority, ok = unquote(strings.TrimSpace(authority))
	if !ok {
		return AltSvcEntry{}, false
	}
	host, port, ok := splitAltAuthority(authority)
	if !ok {
		return AltSvcEntry{}, false
	}
	entry := AltSvcEntry{ProtocolID: protocolID, Host: host, Port: port, MaxAge: DefaultAltSvcMaxAge}
	seen := map[string]bool{}
	for _, raw := range parts[1:] {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		key, value, found := strings.Cut(raw, "=")
		if !found {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		if seen[key] {
			continue // first occurrence wins
		}
		seen[key] = true
		value = strings.TrimSpace(value)
		if strings.HasPrefix(value, `"`) {
			if value, found = unquote(value); !found {
				return AltSvcEntry{}, false
			}
		}
		switch key {
		case "ma":
			seconds, err := strconv.ParseUint(value, 10, 64)
			if err != nil {
				return AltSvcEntry{}, false
			}
			if seconds > uint64(maxAltSvcMaxAge/time.Second) {
				entry.MaxAge = maxAltSvcMaxAge
			} else {
				entry.MaxAge = time.Duration(seconds) * time.Second
			}
		case "persist":
			entry.Persist = value == "1"
		}
	}
	return entry, true
}

// splitAltAuthority splits "host:port", "[ipv6]:port" or ":port".
func splitAltAuthority(authority string) (string, int, bool) {
	idx := strings.LastIndexByte(authority, ':')
	if idx < 0 {
		return "", 0, false
	}
	host, rawPort := authority[:idx], authority[idx+1:]
	port, err := strconv.Atoi(rawPort)
	if err != nil || port < 1 || port > 65535 || strings.ContainsAny(rawPort, "+-") {
		return "", 0, false
	}
	if strings.HasPrefix(host, "[") {
		if !strings.HasSuffix(host, "]") || len(host) < 3 {
			return "", 0, false
		}
		host = host[1 : len(host)-1]
	} else if strings.ContainsAny(host, "[]:") {
		return "", 0, false
	}
	if strings.ContainsAny(host, " \t/?#@") {
		return "", 0, false
	}
	return strings.ToLower(host), port, true
}

// splitOutsideQuotes splits s on sep, ignoring separators inside quoted
// strings and honoring backslash escapes there.
func splitOutsideQuotes(s string, sep byte) []string {
	var parts []string
	start, quoted, escaped := 0, false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case escaped:
			escaped = false
		case quoted && c == '\\':
			escaped = true
		case c == '"':
			quoted = !quoted
		case !quoted && c == sep:
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	return append(parts, s[start:])
}

func unquote(s string) (string, bool) {
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return "", false
	}
	var b strings.Builder
	for i := 1; i < len(s)-1; i++ {
		c := s[i]
		if c == '\\' {
			i++
			if i >= len(s)-1 {
				return "", false
			}
			c = s[i]
		} else if c == '"' {
			return "", false
		}
		b.WriteByte(c)
	}
	return b.String(), true
}

func percentDecodeToken(s string) (string, bool) {
	if !strings.Contains(s, "%") {
		return s, isToken(s)
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '%' {
			if !isTokenChar(c) {
				return "", false
			}
			b.WriteByte(c)
			continue
		}
		if i+2 >= len(s) {
			return "", false
		}
		value, err := strconv.ParseUint(s[i+1:i+3], 16, 8)
		if err != nil {
			return "", false
		}
		b.WriteByte(byte(value))
		i += 2
	}
	return b.String(), true
}

func isToken(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isTokenChar(s[i]) {
			return false
		}
	}
	return true
}

func isTokenChar(c byte) bool {
	if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
		return true
	}
	return strings.IndexByte("!#$&'*+-.^_`|~", c) >= 0
}
