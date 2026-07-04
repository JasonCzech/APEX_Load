package http

import (
	"bytes"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"

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
	if client == nil || req == nil || resp == nil || !isDigestAuth(auth) || resp.StatusCode != http.StatusUnauthorized {
		return req, resp, false, nil
	}
	challenge, ok, err := digestChallengeFromHeaders(resp.Header.Values("WWW-Authenticate"))
	if err != nil || !ok {
		if err != nil {
			_ = resp.Body.Close()
		}
		return req, resp, false, err
	}
	header, err := buildDigestAuthorizationHeader(auth, req.Method, req.URL.RequestURI(), challenge)
	if err != nil {
		_ = resp.Body.Close()
		return req, resp, false, err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	_ = resp.Body.Close()

	retryReq, err := http.NewRequestWithContext(req.Context(), req.Method, req.URL.String(), bytes.NewReader(body))
	if err != nil {
		return req, nil, true, err
	}
	retryReq.Header = req.Header.Clone()
	retryReq.Host = req.Host
	retryReq.Header.Set("Authorization", header)
	retryResp, err := client.Do(retryReq)
	if err != nil {
		return retryReq, nil, true, err
	}
	return retryReq, retryResp, true, nil
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
	for _, header := range headers {
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(header)), "digest") {
			continue
		}
		values := parseDigestKeyValues(strings.TrimSpace(header)[len("Digest"):])
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
		if challenge.Realm == "" || challenge.Nonce == "" {
			return digestChallenge{}, false, fmt.Errorf("digest challenge requires realm and nonce")
		}
		if challenge.Algorithm != "MD5" {
			return digestChallenge{}, false, fmt.Errorf("unsupported digest algorithm %q", challenge.Algorithm)
		}
		return challenge, true, nil
	}
	return digestChallenge{}, false, nil
}

func buildDigestAuthorizationHeader(auth *protocol.AuthConfig, method, uri string, challenge digestChallenge) (string, error) {
	username := strings.TrimSpace(auth.Username)
	password := auth.Password
	if username == "" {
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
