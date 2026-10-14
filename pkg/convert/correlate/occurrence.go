package correlate

import (
	"sort"
	"strings"
)

// A correlation rewrites a recorded literal only where the literal is the
// value itself: not inside a longer identifier, and not a URL's scheme or
// host. A host is retargeted through base_url, never through a correlation.

// maxURLAuthorityScanBytes bounds the look-back for a URL's "://": a host
// name is at most 253 bytes, and user info and a port add little.
const maxURLAuthorityScanBytes = 512

// OccurrenceStandalone reports whether text[start:end], an occurrence of a
// correlation literal, stands on its own: it is not continued by a letter or
// digit on a side where it also ends in one. A percent, JSON or backslash
// escape just before it ("%22", """, "\n") is a boundary, not part of a
// longer identifier.
func OccurrenceStandalone(text string, start, end int) bool {
	if start < 0 || start >= end || end > len(text) {
		return false
	}
	if start > 0 && asciiAlphanumeric(text[start]) && asciiAlphanumeric(text[start-1]) && !escapeEndsAt(text, start) {
		return false
	}
	return end == len(text) || !asciiAlphanumeric(text[end-1]) || !asciiAlphanumeric(text[end])
}

// OccurrenceReplaceable reports whether text[start:end], an occurrence of a
// correlation literal, may become its variable: it stands on its own, and it
// is not a URL's scheme or inside its authority.
func OccurrenceReplaceable(text string, start, end int) bool {
	return OccurrenceStandalone(text, start, end) && !urlSchemeAt(text, end) && !insideURLAuthority(text, start)
}

// ContainsReplaceableOccurrence reports whether text holds an occurrence of
// literal that ApplyVars would rewrite.
func ContainsReplaceableOccurrence(text, literal string) bool {
	if literal == "" {
		return false
	}
	for offset := 0; offset+len(literal) <= len(text); {
		index := strings.Index(text[offset:], literal)
		if index < 0 {
			return false
		}
		start := offset + index
		if OccurrenceReplaceable(text, start, start+len(literal)) {
			return true
		}
		offset = start + 1
	}
	return false
}

// HeaderCorrelatable reports whether a request header's value may hold a
// correlation. Host and :authority name the target host.
func HeaderCorrelatable(name string) bool {
	name = strings.TrimSpace(name)
	return !strings.EqualFold(name, "Host") && !strings.EqualFold(name, ":authority")
}

// escapeEndsAt reports a percent ("%22"), JSON (""") or backslash
// ("\n") escape ending just before index.
func escapeEndsAt(text string, index int) bool {
	switch {
	case index >= 3 && text[index-3] == '%' && hexDigit(text[index-2]) && hexDigit(text[index-1]):
		return true
	case index >= 6 && text[index-6] == '\\' && text[index-5] == 'u' &&
		hexDigit(text[index-4]) && hexDigit(text[index-3]) && hexDigit(text[index-2]) && hexDigit(text[index-1]):
		return true
	case index >= 2 && text[index-2] == '\\' && strings.IndexByte("bfnrt", text[index-1]) >= 0:
		return true
	default:
		return false
	}
}

// urlSchemeAt reports a scheme separator ("://", raw, JSON-escaped or
// percent-encoded) starting at index.
func urlSchemeAt(text string, index int) bool {
	rest := text[index:]
	return strings.HasPrefix(rest, "://") || strings.HasPrefix(rest, `:\/\/`) ||
		len(rest) >= 7 && strings.EqualFold(rest[:7], ":%2F%2F")
}

// insideURLAuthority reports whether index falls inside the authority (user
// info, host, port) of an absolute URL in text, written raw
// ("https://host"), JSON-escaped ("https:\/\/host") or percent-encoded
// ("https%3A%2F%2Fhost"): looking back from index, the first path, query or
// field delimiter is the "//" after the scheme. An occurrence that itself
// begins with a path, query or fragment delimiter starts after the authority.
func insideURLAuthority(text string, index int) bool {
	if rest := text[index:]; rest != "" && (strings.IndexByte("/?#", rest[0]) >= 0 || strings.HasPrefix(rest, `\/`) ||
		len(rest) >= 3 && (strings.EqualFold(rest[:3], "%2F") || strings.EqualFold(rest[:3], "%3F") || strings.EqualFold(rest[:3], "%23"))) {
		return false
	}
	for j := index - 1; j >= 0 && j >= index-maxURLAuthorityScanBytes; j-- {
		switch c := text[j]; {
		case c == '/':
			return j >= 2 && text[j-1] == '/' && text[j-2] == ':' ||
				j >= 4 && text[j-1] == '\\' && text[j-2] == '/' && text[j-3] == '\\' && text[j-4] == ':'
		case c == '%' && j+3 <= len(text):
			switch strings.ToUpper(text[j : j+3]) {
			case "%2F":
				return j >= 6 && strings.EqualFold(text[j-6:j], "%3A%2F")
			case "%3A", "%40", "%2E", "%2D", "%5F", "%7E":
				// ":" "@" "." "-" "_" "~" belong to an authority.
			default:
				return false
			}
		case strings.IndexByte("?#&=,;\"'<>\\ \t\r\n(){}|^`", c) >= 0:
			return false
		}
	}
	return false
}

// applyLiteralReplacements rewrites each occurrence of a literal that
// OccurrenceReplaceable allows, longest literal first, in one non-recursive
// pass. A replacement may itself contain another raw correlation value
// (variable names such as corr_access_token make that quite plausible); the
// pass never scans inserted output, so generated {{var.*}} templates remain
// intact.
func applyLiteralReplacements(text string, replacements map[string]string) string {
	if text == "" || len(replacements) == 0 {
		return text
	}
	var byFirstByte [256][]string
	for literal := range replacements {
		if literal != "" {
			byFirstByte[literal[0]] = append(byFirstByte[literal[0]], literal)
		}
	}
	for _, literals := range byFirstByte {
		sort.Slice(literals, func(i, j int) bool {
			if len(literals[i]) != len(literals[j]) {
				return len(literals[i]) > len(literals[j])
			}
			return literals[i] < literals[j]
		})
	}
	var out strings.Builder
	last := 0
	for index := 0; index < len(text); {
		matched := ""
		for _, literal := range byFirstByte[text[index]] {
			end := index + len(literal)
			if end <= len(text) && text[index:end] == literal && OccurrenceReplaceable(text, index, end) {
				matched = literal
				break
			}
		}
		if matched == "" {
			index++
			continue
		}
		out.WriteString(text[last:index])
		out.WriteString(replacements[matched])
		index += len(matched)
		last = index
	}
	if last == 0 {
		return text
	}
	out.WriteString(text[last:])
	return out.String()
}

func asciiAlphanumeric(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func hexDigit(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}
