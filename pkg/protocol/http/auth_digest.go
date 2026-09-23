package http

import (
	"bytes"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

const digestAuthNC = "00000001"

type digestChallenge struct {
	Realm     string
	Nonce     string
	Opaque    string
	Algorithm string
	QOP       string
}

func retryWithDigestAuth(client *http.Client, req *http.Request, resp *http.Response, auth *protocol.AuthConfig, body []byte) (*http.Request, *http.Response, bool, error) {
	return retryWithDigestAuthDelay(client, req, resp, auth, body, 0)
}

func retryWithDigestAuthDelay(client *http.Client, req *http.Request, resp *http.Response, auth *protocol.AuthConfig, body []byte, delay time.Duration) (*http.Request, *http.Response, bool, error) {
	if client == nil || req == nil || resp == nil || !isDigestAuth(auth) || resp.StatusCode != http.StatusUnauthorized {
		return req, resp, false, nil
	}
	// client.Do may have followed redirects before the challenge. Authenticate
	// the request that actually received the 401; retrying the original URL
	// hashes the wrong method/request-target and may resend a body after a
	// 301/302/303 changed the challenged request to GET.
	challengedReq := req
	if resp.Request != nil {
		challengedReq = resp.Request
	}
	if !sameDigestRequestOrigin(req.URL, challengedReq.URL) {
		_ = resp.Body.Close()
		return req, resp, false, fmt.Errorf("digest authentication refused a cross-origin redirect challenge")
	}
	challenge, ok, err := digestChallengeFromHeaders(resp.Header.Values("WWW-Authenticate"))
	if err != nil || !ok {
		if err != nil {
			_ = resp.Body.Close()
		}
		return req, resp, false, err
	}
	header, err := buildDigestAuthorizationHeader(auth, challengedReq.Method, challengedReq.URL.RequestURI(), challenge)
	if err != nil {
		_ = resp.Body.Close()
		return req, resp, false, err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	_ = resp.Body.Close()

	if delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-req.Context().Done():
			return req, nil, true, req.Context().Err()
		}
	}
	retryBody := body
	if challengedReq.Body == nil || challengedReq.Body == http.NoBody {
		retryBody = nil
	}
	retryReq, err := http.NewRequestWithContext(
		// net/http's timeout wrapper cancels resp.Request.Context when the
		// challenged response body is closed. The original request retains the
		// caller-owned context and is the correct lifetime for this retry.
		req.Context(),
		challengedReq.Method,
		challengedReq.URL.String(),
		bytes.NewReader(retryBody),
	)
	if err != nil {
		return req, nil, true, err
	}
	retryReq.Header = challengedReq.Header.Clone()
	retryReq.Host = challengedReq.Host
	retryReq.Header.Set("Authorization", header)
	// A Digest Authorization value is bound to the challenged request-target.
	// net/http otherwise copies Authorization across redirects to the same host
	// (and even to subdomains), which both replays an invalid digest and exposes
	// the username/nonce/response material to a target that never challenged
	// this request. Follow the configured redirect policy, but strip the
	// one-request Digest proof before any redirected request reaches transport.
	retryClient := *client
	if retryClient.Jar != nil {
		// resp.Request contains the exact Cookie header that reached the
		// challenged endpoint, including cookies the client's Jar added during
		// the first attempt. Calling Client.Do with that header and the same Jar
		// would append those Jar cookies a second time. Filter exact duplicates
		// from only the Jar's first read; challenge cookies, subsequent redirect
		// hops, and every Set-Cookie update still use the original Jar.
		retryClient.Jar = newDigestRetryCookieJar(retryClient.Jar, retryReq.Cookies())
	}
	baseCheckRedirect := retryClient.CheckRedirect
	retryClient.CheckRedirect = func(redirected *http.Request, via []*http.Request) error {
		redirected.Header.Del("Authorization")
		if baseCheckRedirect != nil {
			err := baseCheckRedirect(redirected, via)
			// A custom policy may mutate headers. The per-target Digest proof
			// must remain stripped regardless of those incidental mutations.
			redirected.Header.Del("Authorization")
			return err
		}
		// Installing a wrapper disables net/http's implicit redirect policy.
		// Reproduce the standard bound so a Digest retry cannot follow an
		// attacker-controlled redirect loop indefinitely.
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	}
	retryResp, err := retryClient.Do(retryReq)
	if err != nil {
		return retryReq, nil, true, err
	}
	return retryReq, retryResp, true, nil
}

type digestRetryCookieJar struct {
	base             http.CookieJar
	firstRead        atomic.Bool
	representedCount map[digestCookieIdentity]int
}

type digestCookieIdentity struct {
	name  string
	value string
}

func newDigestRetryCookieJar(base http.CookieJar, represented []*http.Cookie) *digestRetryCookieJar {
	jar := &digestRetryCookieJar{
		base:             base,
		representedCount: make(map[digestCookieIdentity]int, len(represented)),
	}
	for _, cookie := range represented {
		if cookie == nil {
			continue
		}
		jar.representedCount[digestCookieIdentity{name: cookie.Name, value: cookie.Value}]++
	}
	jar.firstRead.Store(true)
	return jar
}

func (j *digestRetryCookieJar) Cookies(target *url.URL) []*http.Cookie {
	if j == nil || j.base == nil {
		return nil
	}
	cookies := j.base.Cookies(target)
	if !j.firstRead.CompareAndSwap(true, false) {
		return cookies
	}
	// The challenged request already contains the Jar cookies used on the first
	// attempt. Subtract those exact occurrences, but retain cookies the 401
	// response just added to the Jar (Digest deployments commonly bind their
	// nonce to such a session cookie).
	filtered := make([]*http.Cookie, 0, len(cookies))
	for _, cookie := range cookies {
		if cookie == nil {
			continue
		}
		identity := digestCookieIdentity{name: cookie.Name, value: cookie.Value}
		if remaining := j.representedCount[identity]; remaining > 0 {
			j.representedCount[identity] = remaining - 1
			continue
		}
		filtered = append(filtered, cookie)
	}
	return filtered
}

func (j *digestRetryCookieJar) SetCookies(target *url.URL, cookies []*http.Cookie) {
	if j == nil || j.base == nil {
		return
	}
	j.base.SetCookies(target, cookies)
}

func sameDigestRequestOrigin(left, right *url.URL) bool {
	if left == nil || right == nil {
		return false
	}
	origin := func(candidate *url.URL) string {
		scheme := strings.ToLower(candidate.Scheme)
		port := candidate.Port()
		if port == "" {
			switch scheme {
			case "http":
				port = "80"
			case "https":
				port = "443"
			}
		}
		return scheme + "\x00" + strings.ToLower(candidate.Hostname()) + "\x00" + port
	}
	return origin(left) == origin(right)
}

func isDigestAuth(auth *protocol.AuthConfig) bool {
	return auth != nil && strings.EqualFold(strings.TrimSpace(auth.Type), "digest")
}

func digestAuthConfig(auth *protocol.AuthConfig) *protocol.AuthConfig {
	if !isDigestAuth(auth) {
		return nil
	}
	return auth
}

func digestChallengeFromHeaders(headers []string) (digestChallenge, bool, error) {
	var firstFailure error
	for _, header := range headers {
		for _, advertised := range splitAuthenticateHeader(header) {
			if !strings.EqualFold(advertised.scheme, "Digest") {
				continue
			}
			values := parseDigestKeyValues(advertised.payload)
			challenge := digestChallenge{
				Realm:     values["realm"],
				Nonce:     values["nonce"],
				Opaque:    values["opaque"],
				Algorithm: strings.ToUpper(strings.TrimSpace(values["algorithm"])),
				QOP:       values["qop"],
			}
			if challenge.Algorithm == "" {
				challenge.Algorithm = "MD5"
			}
			var failure error
			switch {
			case challenge.Realm == "" || challenge.Nonce == "":
				failure = fmt.Errorf("digest challenge requires realm and nonce")
			case challenge.Algorithm != "MD5":
				failure = fmt.Errorf("unsupported digest algorithm %q", challenge.Algorithm)
			case strings.TrimSpace(challenge.QOP) != "" && selectDigestQOP(challenge.QOP) == "":
				failure = fmt.Errorf("unsupported digest qop")
			}
			if failure != nil {
				if firstFailure == nil {
					firstFailure = failure
				}
				continue
			}
			return challenge, true, nil
		}
	}
	return digestChallenge{}, false, firstFailure
}

type authenticateChallenge struct {
	scheme  string
	payload string
}

// splitAuthenticateHeader separates challenges without splitting their
// comma-delimited auth parameters. A comma starts another challenge only when
// it is followed by a token, required whitespace, and something other than the
// optional whitespace plus '=' accepted by auth-param. Quoted values and
// escapes are skipped.
func splitAuthenticateHeader(header string) []authenticateChallenge {
	var challenges []authenticateChallenge
	start := 0
	for start < len(header) {
		for start < len(header) && (header[start] == ',' || header[start] == ' ' || header[start] == '\t') {
			start++
		}
		schemeStart := start
		for start < len(header) && isHTTPAuthTokenByte(header[start]) {
			start++
		}
		if start == schemeStart {
			break
		}
		scheme := header[schemeStart:start]
		if start >= len(header) || (header[start] != ' ' && header[start] != '\t') {
			// A top-level field must start with an auth scheme followed by
			// whitespace. Skip malformed material disclosure-safely.
			break
		}
		for start < len(header) && (header[start] == ' ' || header[start] == '\t') {
			start++
		}
		payloadStart := start
		end := nextAuthenticateChallengeBoundary(header, payloadStart)
		challenges = append(challenges, authenticateChallenge{
			scheme:  scheme,
			payload: strings.TrimSpace(header[payloadStart:end]),
		})
		if end >= len(header) {
			break
		}
		start = end + 1
	}
	return challenges
}

func nextAuthenticateChallengeBoundary(header string, start int) int {
	inQuote := false
	escaped := false
	for index := start; index < len(header); index++ {
		switch {
		case escaped:
			escaped = false
			continue
		case header[index] == '\\' && inQuote:
			escaped = true
			continue
		case header[index] == '"':
			inQuote = !inQuote
			continue
		case inQuote:
			continue
		}
		if header[index] != ',' {
			continue
		}
		candidate := index + 1
		for candidate < len(header) && (header[candidate] == ' ' || header[candidate] == '\t') {
			candidate++
		}
		tokenEnd := candidate
		for tokenEnd < len(header) && isHTTPAuthTokenByte(header[tokenEnd]) {
			tokenEnd++
		}
		if tokenEnd == candidate || tokenEnd >= len(header) ||
			(header[tokenEnd] != ' ' && header[tokenEnd] != '\t') {
			continue
		}
		afterWhitespace := tokenEnd
		for afterWhitespace < len(header) && (header[afterWhitespace] == ' ' || header[afterWhitespace] == '\t') {
			afterWhitespace++
		}
		if afterWhitespace < len(header) && header[afterWhitespace] == '=' {
			// auth-param permits bad whitespace around '=', so this is still
			// part of the current challenge rather than a new auth scheme.
			continue
		}
		return index
	}
	return len(header)
}

func isHTTPAuthTokenByte(value byte) bool {
	switch {
	case value >= 'a' && value <= 'z',
		value >= 'A' && value <= 'Z',
		value >= '0' && value <= '9':
		return true
	}
	switch value {
	case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
		return true
	default:
		return false
	}
}

func buildDigestAuthorizationHeader(auth *protocol.AuthConfig, method, uri string, challenge digestChallenge) (string, error) {
	username := auth.Username
	password := auth.Password
	if strings.TrimSpace(username) == "" {
		return "", fmt.Errorf("digest auth requires username")
	}
	if password == "" {
		return "", fmt.Errorf("digest auth requires password")
	}
	cnonce, err := randomDigestCNonce()
	if err != nil {
		return "", err
	}
	qop := selectDigestQOP(challenge.QOP)
	if strings.TrimSpace(challenge.QOP) != "" && qop == "" {
		return "", fmt.Errorf("unsupported digest qop")
	}
	ha1 := digestMD5(username + ":" + challenge.Realm + ":" + password)
	ha2 := digestMD5(strings.ToUpper(strings.TrimSpace(method)) + ":" + uri)
	response := ""
	if qop == "" {
		response = digestMD5(ha1 + ":" + challenge.Nonce + ":" + ha2)
	} else {
		response = digestMD5(ha1 + ":" + challenge.Nonce + ":" + digestAuthNC + ":" + cnonce + ":" + qop + ":" + ha2)
	}

	parts := []string{
		`Digest username="` + quoteDigestValue(username) + `"`,
		`realm="` + quoteDigestValue(challenge.Realm) + `"`,
		`nonce="` + quoteDigestValue(challenge.Nonce) + `"`,
		`uri="` + quoteDigestValue(uri) + `"`,
		`response="` + response + `"`,
		`algorithm=MD5`,
	}
	if challenge.Opaque != "" {
		parts = append(parts, `opaque="`+quoteDigestValue(challenge.Opaque)+`"`)
	}
	if qop != "" {
		parts = append(parts, `qop=`+qop, `nc=`+digestAuthNC, `cnonce="`+cnonce+`"`)
	}
	return strings.Join(parts, ", "), nil
}

func parseDigestKeyValues(input string) map[string]string {
	values := map[string]string{}
	for _, part := range splitDigestHeader(input) {
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
			value = unquoteDigestValue(value[1 : len(value)-1])
		}
		if key != "" {
			values[key] = value
		}
	}
	return values
}

func splitDigestHeader(input string) []string {
	var parts []string
	var current strings.Builder
	inQuote := false
	escaped := false
	for _, r := range input {
		switch {
		case escaped:
			current.WriteRune(r)
			escaped = false
		case r == '\\' && inQuote:
			current.WriteRune(r)
			escaped = true
		case r == '"':
			current.WriteRune(r)
			inQuote = !inQuote
		case r == ',' && !inQuote:
			if trimmed := strings.TrimSpace(current.String()); trimmed != "" {
				parts = append(parts, trimmed)
			}
			current.Reset()
		default:
			current.WriteRune(r)
		}
	}
	if trimmed := strings.TrimSpace(current.String()); trimmed != "" {
		parts = append(parts, trimmed)
	}
	return parts
}

func selectDigestQOP(raw string) string {
	for _, item := range strings.Split(raw, ",") {
		if strings.EqualFold(strings.TrimSpace(item), "auth") {
			return "auth"
		}
	}
	return ""
}

func digestMD5(value string) string {
	sum := md5.Sum([]byte(value))
	return hex.EncodeToString(sum[:])
}

func randomDigestCNonce() (string, error) {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", fmt.Errorf("generate digest cnonce: %w", err)
	}
	return hex.EncodeToString(nonce[:]), nil
}

func quoteDigestValue(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	return strings.ReplaceAll(value, `"`, `\"`)
}

func unquoteDigestValue(value string) string {
	var out strings.Builder
	escaped := false
	for _, r := range value {
		if escaped {
			out.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		out.WriteRune(r)
	}
	if escaped {
		out.WriteRune('\\')
	}
	return out.String()
}
