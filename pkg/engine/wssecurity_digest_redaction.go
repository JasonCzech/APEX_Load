package engine

import (
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base64"
	"html"
	"regexp"
	"strings"
)

// WS-Security UsernameToken PasswordDigest in authored request evidence.
//
// The SOAP adapter sends <wsse:Password Type="...#PasswordDigest">, which is
// base64(sha1(nonce + created + password)), beside the nonce and created values
// it was computed from. No exact-value matcher can find that keyed hash, so
// with config.log.redact_run_evidence off (authored request content keeps its
// values) a digest made from a platform-managed password would be published
// and could be brute-forced offline or replayed inside the timestamp window.
//
// maskPlatformDerivedWSSecurityDigests therefore recomputes the digest from
// every value the run's exact matcher withholds and masks the digest and its
// nonce when one of them produced it. A digest of a script-literal password
// stays visible, like the rest of what the script authored. A digest already
// replaced by the redaction placeholder is left as is: the decision is by
// origin, not shape, so the SOAP adapter withholds a secret-store digest's
// nonce itself, and an authored "[REDACTED]" digest keeps its script-sourced
// nonce. When the matcher
// is too large to check, or the element is cut off, the digest is masked
// (fail closed).

const maxWSSecurityDigestCandidates = 4096

var (
	wsseDigestPasswordPattern = regexp.MustCompile(`(?is)(<(?:[A-Za-z_][A-Za-z0-9_.-]*:)?Password\b[^>]*\bType\s*=\s*["'][^"']*PasswordDigest["'][^>]*>)([^<]*)(</(?:[A-Za-z_][A-Za-z0-9_.-]*:)?Password\s*>)?`)
	wsseNoncePattern          = regexp.MustCompile(`(?is)(<(?:[A-Za-z_][A-Za-z0-9_.-]*:)?Nonce\b[^>]*>)([^<]*)(</(?:[A-Za-z_][A-Za-z0-9_.-]*:)?Nonce\s*>)?`)
	wsseCreatedPattern        = regexp.MustCompile(`(?is)<(?:[A-Za-z_][A-Za-z0-9_.-]*:)?Created\b[^>]*>([^<]*)<`)
	wsseTokenOpenPattern      = regexp.MustCompile(`(?is)<(?:[A-Za-z_][A-Za-z0-9_.-]*:)?UsernameToken\b[^>]*>`)
	wsseTokenClosePattern     = regexp.MustCompile(`(?is)</(?:[A-Za-z_][A-Za-z0-9_.-]*:)?UsernameToken\s*>`)
)

func maskPlatformDerivedWSSecurityDigests(body string, config *RedactionConfig) string {
	if !strings.Contains(body, "PasswordDigest") || !strings.Contains(body, "Password") {
		return body
	}
	matches := wsseDigestPasswordPattern.FindAllStringSubmatchIndex(body, -1)
	if len(matches) == 0 {
		return body
	}
	var candidates map[string]struct{}
	loaded := false
	type span struct{ start, end int }
	var masks []span
	for _, match := range matches {
		digestStart, digestEnd := match[4], match[5]
		closed := match[6] >= 0
		window := wsseTokenWindow(body, match[0], match[1])
		nonceText, nonceSpan := "", span{-1, -1}
		if nonce := wsseNoncePattern.FindStringSubmatchIndex(body[window.start:window.end]); nonce != nil {
			nonceText = body[window.start+nonce[4] : window.start+nonce[5]]
			nonceSpan = span{window.start + nonce[4], window.start + nonce[5]}
		}
		created := ""
		if found := wsseCreatedPattern.FindStringSubmatch(body[window.start:window.end]); found != nil {
			created = html.UnescapeString(strings.TrimSpace(found[1]))
		}
		digest := strings.TrimSpace(html.UnescapeString(body[digestStart:digestEnd]))
		if digest == "" {
			continue
		}
		mask := !closed
		if !mask {
			if !loaded {
				candidates = allRuntimeSecretValues(config)
				loaded = true
			}
			if len(candidates) > maxWSSecurityDigestCandidates {
				mask = true
			} else if len(candidates) > 0 {
				mask = wsseDigestFromAny(digest, nonceText, created, candidates)
			}
		}
		if !mask {
			continue
		}
		masks = append(masks, span{digestStart, digestEnd})
		if nonceSpan.start >= 0 && strings.TrimSpace(nonceText) != "" {
			masks = append(masks, nonceSpan)
		}
	}
	if len(masks) == 0 {
		return body
	}
	// Apply right to left; spans never overlap (distinct elements), but a
	// nonce shared by two digests in one window may repeat.
	seen := map[span]struct{}{}
	unique := masks[:0]
	for _, m := range masks {
		if _, ok := seen[m]; ok {
			continue
		}
		seen[m] = struct{}{}
		unique = append(unique, m)
	}
	for i := 1; i < len(unique); i++ {
		for j := i; j > 0 && unique[j].start > unique[j-1].start; j-- {
			unique[j], unique[j-1] = unique[j-1], unique[j]
		}
	}
	out := body
	for _, m := range unique {
		out = out[:m.start] + redactedPayloadValue + out[m.end:]
	}
	return out
}

// wsseTokenWindow is the enclosing UsernameToken of the Password element at
// [start, end): from its last opening tag before the element to the next closing
// tag after it (or the end of the text when the body was cut off).
func wsseTokenWindow(body string, start, end int) struct{ start, end int } {
	window := struct{ start, end int }{0, len(body)}
	if opens := wsseTokenOpenPattern.FindAllStringIndex(body[:start], -1); len(opens) > 0 {
		window.start = opens[len(opens)-1][0]
	}
	if closing := wsseTokenClosePattern.FindStringIndex(body[end:]); closing != nil {
		window.end = end + closing[1]
	}
	return window
}

func wsseDigestFromAny(digest, nonceText, created string, candidates map[string]struct{}) bool {
	expected, err := base64.StdEncoding.DecodeString(digest)
	if err != nil || len(expected) != sha1.Size {
		// Not a digest this adapter could have produced; nothing derived from
		// a known value to withhold.
		return false
	}
	var nonce []byte
	if trimmed := strings.TrimSpace(html.UnescapeString(nonceText)); trimmed != "" {
		decoded, err := base64.StdEncoding.DecodeString(trimmed)
		if err != nil {
			return true
		}
		nonce = decoded
	}
	prefix := append(append([]byte(nil), nonce...), created...)
	for value := range candidates {
		sum := sha1.Sum(append(append([]byte(nil), prefix...), value...))
		if subtle.ConstantTimeCompare(sum[:], expected) == 1 {
			return true
		}
	}
	return false
}
