package http

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

type digestRoundTripFunc func(*http.Request) (*http.Response, error)

func (roundTrip digestRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

func TestBuildDigestAuthorizationHeader(t *testing.T) {
	challenge := digestChallenge{
		Realm:     "apex",
		Nonce:     "nonce-1",
		Opaque:    "opaque-1",
		Algorithm: "MD5",
		QOP:       "auth,auth-int",
	}
	header, err := buildDigestAuthorizationHeader(&protocol.AuthConfig{
		Type:     "digest",
		Username: `al"ice`,
		Password: "secret",
	}, "GET", "/orders", challenge)
	if err != nil {
		t.Fatalf("buildDigestAuthorizationHeader returned error: %v", err)
	}
	if !strings.HasPrefix(header, `Digest username="al\"ice"`) {
		t.Fatalf("header = %q, want quoted username", header)
	}
	values := parseDigestKeyValues(strings.TrimSpace(strings.TrimPrefix(header, "Digest")))
	if values["realm"] != "apex" || values["nonce"] != "nonce-1" || values["uri"] != "/orders" || values["qop"] != "auth" || values["nc"] != digestAuthNC {
		t.Fatalf("digest values = %#v", values)
	}
	ha1 := digestMD5(`al"ice:apex:secret`)
	ha2 := digestMD5("GET:/orders")
	want := digestMD5(ha1 + ":nonce-1:" + digestAuthNC + ":" + values["cnonce"] + ":auth:" + ha2)
	if values["response"] != want {
		t.Fatalf("response = %q, want %q", values["response"], want)
	}
}

func TestBuildDigestAuthorizationHeaderPreservesUsernameWhitespace(t *testing.T) {
	challenge := digestChallenge{
		Realm:     "apex",
		Nonce:     "nonce-1",
		Algorithm: "MD5",
	}
	header, err := buildDigestAuthorizationHeader(&protocol.AuthConfig{
		Type:     "digest",
		Username: " alice ",
		Password: "secret",
	}, "GET", "/orders", challenge)
	if err != nil {
		t.Fatalf("buildDigestAuthorizationHeader returned error: %v", err)
	}
	values := parseDigestKeyValues(strings.TrimSpace(strings.TrimPrefix(header, "Digest")))
	if values["username"] != " alice " {
		t.Fatalf("username = %q, want source-exact whitespace", values["username"])
	}
	ha1 := digestMD5(" alice :apex:secret")
	ha2 := digestMD5("GET:/orders")
	want := digestMD5(ha1 + ":nonce-1:" + ha2)
	if values["response"] != want {
		t.Fatalf("response = %q, want %q", values["response"], want)
	}

	_, err = buildDigestAuthorizationHeader(&protocol.AuthConfig{
		Type:     "digest",
		Username: " \t ",
		Password: "secret",
	}, "GET", "/orders", challenge)
	if err == nil {
		t.Fatal("all-whitespace digest username was accepted")
	}
}

func TestBuildDigestAuthorizationHeaderRejectsUnsupportedQOP(t *testing.T) {
	_, err := buildDigestAuthorizationHeader(&protocol.AuthConfig{
		Type:     "digest",
		Username: "alice",
		Password: "secret",
	}, "POST", "/orders", digestChallenge{
		Realm:     "apex",
		Nonce:     "nonce-1",
		Algorithm: "MD5",
		QOP:       "auth-int",
	})
	if err == nil || err.Error() != "unsupported digest qop" {
		t.Fatalf("buildDigestAuthorizationHeader() error = %v, want unsupported qop", err)
	}
}

func TestDigestChallengeFromCombinedAuthenticateHeader(t *testing.T) {
	challenge, ok, err := digestChallengeFromHeaders([]string{
		`Basic realm="legacy", Digest realm="apex", nonce="nonce-1", qop="auth", algorithm=MD5`,
	})
	if err != nil {
		t.Fatalf("digestChallengeFromHeaders returned error: %v", err)
	}
	if !ok {
		t.Fatal("digestChallengeFromHeaders did not find the combined Digest challenge")
	}
	if challenge.Realm != "apex" || challenge.Nonce != "nonce-1" || challenge.QOP != "auth" || challenge.Algorithm != "MD5" {
		t.Fatalf("challenge = %#v", challenge)
	}
}

func TestDigestChallengeDoesNotMatchQuotedSchemeName(t *testing.T) {
	_, ok, err := digestChallengeFromHeaders([]string{`Basic realm="Digest realm=\"decoy\", nonce=\"decoy\""`})
	if err != nil {
		t.Fatalf("digestChallengeFromHeaders returned error: %v", err)
	}
	if ok {
		t.Fatal("digestChallengeFromHeaders treated a quoted realm value as a Digest auth scheme")
	}
}

func TestDigestChallengeSelectsSupportedAlternativeWithoutMixingParameters(t *testing.T) {
	challenge, ok, err := digestChallengeFromHeaders([]string{
		`Digest realm="sha-realm", nonce="sha-nonce", algorithm=SHA-256, qop="auth", Digest realm="md5-realm", nonce="md5-nonce", algorithm=MD5, qop="auth"`,
	})
	if err != nil {
		t.Fatalf("digestChallengeFromHeaders returned error: %v", err)
	}
	if !ok {
		t.Fatal("digestChallengeFromHeaders did not select the supported alternative")
	}
	if challenge.Realm != "md5-realm" || challenge.Nonce != "md5-nonce" ||
		challenge.Algorithm != "MD5" || challenge.QOP != "auth" {
		t.Fatalf("challenge = %#v, want the complete MD5 alternative", challenge)
	}
}

func TestDigestChallengeSkipsUnsupportedQOPAlternative(t *testing.T) {
	challenge, ok, err := digestChallengeFromHeaders([]string{
		`Digest realm="int-realm", nonce="int-nonce", algorithm=MD5, qop="auth-int", Basic realm = "legacy", Digest realm="auth-realm", nonce="auth-nonce", algorithm=MD5, qop="auth"`,
	})
	if err != nil {
		t.Fatalf("digestChallengeFromHeaders returned error: %v", err)
	}
	if !ok || challenge.Realm != "auth-realm" || challenge.Nonce != "auth-nonce" {
		t.Fatalf("challenge = %#v, ok=%t; want supported auth QOP alternative", challenge, ok)
	}
}

func TestDigestChallengeReturnsFirstFailureWhenNoAlternativeIsSupported(t *testing.T) {
	_, ok, err := digestChallengeFromHeaders([]string{
		`Basic realm="legacy", Digest realm="apex", nonce="nonce-1", algorithm=SHA-256`,
	})
	if ok || err == nil || !strings.Contains(err.Error(), "unsupported digest algorithm") {
		t.Fatalf("result = ok:%t err:%v, want disclosure-safe unsupported-algorithm failure", ok, err)
	}
}

func TestHTTPDigestRetryTransportFailureSnapshotRedactsAuthorization(t *testing.T) {
	const usernameCanary = "digest-user-canary"
	runtime := initHTTP(t, map[string]any{
		"base_url":      "https://example.test",
		"auth_type":     "digest",
		"auth_username": usernameCanary,
		"auth_password": "digest-password-canary",
	})
	attempts := 0
	runtime.client.Transport = digestRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		attempts++
		if attempts == 1 {
			headers := make(http.Header)
			headers.Set("WWW-Authenticate", `Digest realm="apex", nonce="nonce-1", qop="auth", algorithm=MD5`)
			return &http.Response{
				StatusCode: http.StatusUnauthorized,
				Header:     headers,
				Body:       io.NopCloser(strings.NewReader("auth required")),
				Request:    request,
			}, nil
		}
		if !strings.HasPrefix(request.Header.Get("Authorization"), "Digest ") {
			t.Fatalf("retry Authorization = %q, want Digest header", request.Header.Get("Authorization"))
		}
		return nil, errors.New("retry transport failed")
	})

	result, err := runtime.Execute(context.Background(), &protocol.Action{
		Name:   "digest failure",
		Method: http.MethodGet,
		Target: "/private",
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want challenge plus authenticated retry", attempts)
	}
	if result.Snapshot == nil {
		t.Fatal("transport failure did not produce a snapshot")
	}
	if got := result.Snapshot.RequestHeaders["Authorization"]; got != "Digest [REDACTED]" {
		t.Fatalf("snapshot Authorization = %q, want redacted Digest header", got)
	}
	if strings.Contains(fmt.Sprintf("%#v", result.Snapshot), usernameCanary) {
		t.Fatal("failure snapshot disclosed the Digest username")
	}
}

func TestHTTPExecuteDigestAuthDefaultConfig(t *testing.T) {
	server, attempts := newDigestAuthServer(t)
	defer server.Close()

	h := initHTTP(t, map[string]any{
		"base_url":      server.URL,
		"auth_type":     "digest",
		"auth_username": "alice",
		"auth_password": "secret",
	})
	result, err := h.Execute(context.Background(), &protocol.Action{Name: "digest", Method: "GET", Target: "/"})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !result.Success || result.ResponseBody != "ok" {
		t.Fatalf("result = %#v, want success body ok", result)
	}
	if *attempts != 2 {
		t.Fatalf("attempts = %d, want initial 401 plus digest retry", *attempts)
	}
}

func TestHTTPExecuteDigestAuthActionOverride(t *testing.T) {
	server, attempts := newDigestAuthServer(t)
	defer server.Close()

	h := initHTTP(t, map[string]any{"base_url": server.URL})
	result, err := h.Execute(context.Background(), &protocol.Action{
		Name:   "digest",
		Method: "GET",
		Target: "/",
		Auth:   &protocol.AuthConfig{Type: "digest", Username: "alice", Password: "secret"},
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !result.Success || result.ResponseBody != "ok" {
		t.Fatalf("result = %#v, want success body ok", result)
	}
	if *attempts != 2 {
		t.Fatalf("attempts = %d, want initial 401 plus digest retry", *attempts)
	}
}

func TestRetryWithDigestAuthUsesRedirectedChallengeRequest(t *testing.T) {
	original, err := http.NewRequest(http.MethodPost, "https://example.test/start", strings.NewReader("original-body"))
	if err != nil {
		t.Fatalf("NewRequest(original): %v", err)
	}
	challengedURL, err := url.Parse("https://example.test/protected?view=full")
	if err != nil {
		t.Fatalf("Parse(challenged): %v", err)
	}
	challenged := original.Clone(context.Background())
	challenged.Method = http.MethodGet
	challenged.URL = challengedURL
	challenged.Body = http.NoBody
	challenged.GetBody = nil
	challenged.Header.Set("X-Redirected", "kept")
	challengeHeaders := make(http.Header)
	challengeHeaders.Set("WWW-Authenticate", `Digest realm="apex", nonce="nonce-1", qop="auth", algorithm=MD5`)
	response := &http.Response{
		StatusCode: http.StatusUnauthorized,
		Header:     challengeHeaders,
		Body:       io.NopCloser(strings.NewReader("auth required")),
		Request:    challenged,
	}

	var retryRequest *http.Request
	client := &http.Client{Transport: digestRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		retryRequest = request
		return &http.Response{
			StatusCode: http.StatusNoContent,
			Header:     make(http.Header),
			Body:       http.NoBody,
			Request:    request,
		}, nil
	})}
	gotReq, gotResp, retried, err := retryWithDigestAuth(
		client,
		original,
		response,
		&protocol.AuthConfig{Type: "digest", Username: "alice", Password: "secret"},
		[]byte("original-body"),
	)
	if err != nil {
		t.Fatalf("retryWithDigestAuth(): %v", err)
	}
	if !retried || gotReq != retryRequest || gotResp == nil {
		t.Fatalf("retry result = (%p, %#v, %t), want captured retry and response", gotReq, gotResp, retried)
	}
	if retryRequest.Method != http.MethodGet || retryRequest.URL.String() != challengedURL.String() {
		t.Fatalf("retry request = %s %s, want redirected GET", retryRequest.Method, retryRequest.URL)
	}
	if retryRequest.Body != nil && retryRequest.Body != http.NoBody {
		retryBody, readErr := io.ReadAll(retryRequest.Body)
		if readErr != nil {
			t.Fatalf("read retry body: %v", readErr)
		}
		if len(retryBody) != 0 {
			t.Fatalf("retry body = %q, want empty redirected GET body", retryBody)
		}
	}
	if retryRequest.Header.Get("X-Redirected") != "kept" {
		t.Fatalf("redirected request headers = %#v", retryRequest.Header)
	}
	digest := retryRequest.Header.Get("Authorization")
	values := parseDigestKeyValues(strings.TrimSpace(strings.TrimPrefix(digest, "Digest")))
	if values["uri"] != challengedURL.RequestURI() {
		t.Fatalf("digest uri = %q, want %q", values["uri"], challengedURL.RequestURI())
	}
}

func TestRetryWithDigestAuthRejectsCrossOriginRedirectChallenge(t *testing.T) {
	original, err := http.NewRequest(http.MethodGet, "https://api.example.test/start", nil)
	if err != nil {
		t.Fatalf("NewRequest(original): %v", err)
	}
	challenged, err := http.NewRequest(http.MethodGet, "https://attacker.example.test/protected", nil)
	if err != nil {
		t.Fatalf("NewRequest(challenged): %v", err)
	}
	headers := make(http.Header)
	headers.Set("WWW-Authenticate", `Digest realm="apex", nonce="nonce-1", qop="auth", algorithm=MD5`)
	response := &http.Response{
		StatusCode: http.StatusUnauthorized,
		Header:     headers,
		Body:       io.NopCloser(strings.NewReader("auth required")),
		Request:    challenged,
	}
	var attempts int
	client := &http.Client{Transport: digestRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		attempts++
		return nil, errors.New("must not send digest credentials cross-origin")
	})}

	_, _, retried, err := retryWithDigestAuth(
		client,
		original,
		response,
		&protocol.AuthConfig{Type: "digest", Username: "alice", Password: "secret"},
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), "cross-origin") {
		t.Fatalf("retryWithDigestAuth() error = %v, want cross-origin rejection", err)
	}
	if retried || attempts != 0 {
		t.Fatalf("retry state = (%t, %d attempts), want no credential-bearing retry", retried, attempts)
	}
}

func TestRetryWithDigestAuthDoesNotForwardDigestProofAcrossRedirect(t *testing.T) {
	original, err := http.NewRequest(http.MethodGet, "https://api.example.test/protected", nil)
	if err != nil {
		t.Fatalf("NewRequest(original): %v", err)
	}
	headers := make(http.Header)
	headers.Set("WWW-Authenticate", `Digest realm="apex", nonce="nonce-1", qop="auth", algorithm=MD5`)
	challengeResponse := &http.Response{
		StatusCode: http.StatusUnauthorized,
		Header:     headers,
		Body:       io.NopCloser(strings.NewReader("auth required")),
		Request:    original,
	}

	var attempts int
	var redirectedAuthorization string
	client := &http.Client{Transport: digestRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		attempts++
		switch attempts {
		case 1:
			if got := request.Header.Get("Authorization"); !strings.HasPrefix(got, "Digest ") {
				t.Fatalf("authenticated retry Authorization = %q, want Digest proof", got)
			}
			return &http.Response{
				StatusCode: http.StatusFound,
				Header:     http.Header{"Location": []string{"https://child.api.example.test/next"}},
				Body:       http.NoBody,
				Request:    request,
			}, nil
		case 2:
			redirectedAuthorization = request.Header.Get("Authorization")
			return &http.Response{
				StatusCode: http.StatusNoContent,
				Header:     make(http.Header),
				Body:       http.NoBody,
				Request:    request,
			}, nil
		default:
			t.Fatalf("unexpected transport attempt %d", attempts)
			return nil, nil
		}
	})}

	_, response, retried, err := retryWithDigestAuth(
		client,
		original,
		challengeResponse,
		&protocol.AuthConfig{Type: "digest", Username: "alice", Password: "secret"},
		nil,
	)
	if err != nil {
		t.Fatalf("retryWithDigestAuth(): %v", err)
	}
	if !retried || response == nil || response.StatusCode != http.StatusNoContent {
		t.Fatalf("retry result = (%#v, %t), want followed redirect response", response, retried)
	}
	if attempts != 2 {
		t.Fatalf("transport attempts = %d, want authenticated retry plus redirect", attempts)
	}
	if redirectedAuthorization != "" {
		t.Fatalf("redirected Authorization = %q, want Digest proof removed", redirectedAuthorization)
	}
}

func TestRetryWithDigestAuthRetainsDefaultRedirectLimit(t *testing.T) {
	original, err := http.NewRequest(http.MethodGet, "https://api.example.test/protected", nil)
	if err != nil {
		t.Fatalf("NewRequest(original): %v", err)
	}
	headers := make(http.Header)
	headers.Set("WWW-Authenticate", `Digest realm="apex", nonce="nonce-1", qop="auth", algorithm=MD5`)
	challengeResponse := &http.Response{
		StatusCode: http.StatusUnauthorized,
		Header:     headers,
		Body:       io.NopCloser(strings.NewReader("auth required")),
		Request:    original,
	}

	var attempts int
	client := &http.Client{Transport: digestRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		attempts++
		if attempts == 1 && !strings.HasPrefix(request.Header.Get("Authorization"), "Digest ") {
			t.Fatalf("authenticated retry Authorization = %q, want Digest proof", request.Header.Get("Authorization"))
		}
		return &http.Response{
			StatusCode: http.StatusFound,
			Header: http.Header{
				"Location": []string{fmt.Sprintf("/redirect/%d", attempts)},
			},
			Body:    http.NoBody,
			Request: request,
		}, nil
	})}

	_, _, retried, err := retryWithDigestAuth(
		client,
		original,
		challengeResponse,
		&protocol.AuthConfig{Type: "digest", Username: "alice", Password: "secret"},
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), "stopped after 10 redirects") {
		t.Fatalf("retryWithDigestAuth() error = %v, want default redirect-limit rejection", err)
	}
	if !retried || attempts != 10 {
		t.Fatalf("retry state = (%t, %d attempts), want bounded authenticated redirect chain", retried, attempts)
	}
}

func TestHTTPDigestRetryDeduplicatesJarCookiesAndRetainsChallengeCookie(t *testing.T) {
	runtime := initHTTP(t, map[string]any{
		"base_url":      "https://example.test",
		"auth_type":     "digest",
		"auth_username": "alice",
		"auth_password": "secret",
	})
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New(): %v", err)
	}
	target, err := url.Parse("https://example.test/private")
	if err != nil {
		t.Fatalf("url.Parse(): %v", err)
	}
	jar.SetCookies(target, []*http.Cookie{{Name: "jar_session", Value: "three"}})
	runtime.client.Jar = jar

	var attempts int
	runtime.client.Transport = digestRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		attempts++
		wantCookie := "authored=one; jar_session=three"
		if attempts == 2 {
			wantCookie += "; digest_session=nonce-bound"
		}
		if got := request.Header.Get("Cookie"); got != wantCookie {
			t.Fatalf("attempt %d Cookie = %q, want %q", attempts, got, wantCookie)
		}
		if attempts == 1 {
			headers := make(http.Header)
			headers.Set("WWW-Authenticate", `Digest realm="apex", nonce="nonce-1", qop="auth", algorithm=MD5`)
			headers.Set("Set-Cookie", "digest_session=nonce-bound; Path=/")
			return &http.Response{
				StatusCode: http.StatusUnauthorized,
				Header:     headers,
				Body:       io.NopCloser(strings.NewReader("auth required")),
				Request:    request,
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusNoContent,
			Header:     make(http.Header),
			Body:       http.NoBody,
			Request:    request,
		}, nil
	})

	result, err := runtime.Execute(context.Background(), &protocol.Action{
		Name:   "digest cookie retry",
		Method: http.MethodGet,
		Target: "/private",
		HeaderList: protocol.HeaderList{
			{Name: "Cookie", Value: "authored=one"},
		},
	})
	if err != nil {
		t.Fatalf("Execute(): %v", err)
	}
	if attempts != 2 || !result.Success {
		t.Fatalf("attempts = %d, result = %#v; want successful authenticated retry", attempts, result)
	}
}

func TestHTTPExecuteAuthoredAuthorizationSuppressesDigestRetry(t *testing.T) {
	const authorizationCanary = "Custom opaque-auth-canary"
	h := initHTTP(t, map[string]any{
		"base_url":      "https://example.test",
		"auth_type":     "digest",
		"auth_username": "alice",
		"auth_password": "secret",
	})
	var attempts int
	h.client.Transport = digestRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		attempts++
		if got := request.Header.Get("Authorization"); got != authorizationCanary {
			t.Fatalf("Authorization = %q, want authored value", got)
		}
		headers := make(http.Header)
		headers.Set("WWW-Authenticate", `Digest realm="apex", nonce="nonce-1", qop="auth", algorithm=MD5`)
		return &http.Response{
			StatusCode: http.StatusUnauthorized,
			Header:     headers,
			Body:       io.NopCloser(bytes.NewReader(nil)),
			Request:    request,
		}, nil
	})

	result, err := h.Execute(context.Background(), &protocol.Action{
		Name:   "authored authorization",
		Method: http.MethodGet,
		Target: "/protected",
		HeaderList: protocol.HeaderList{
			{Name: "Authorization", Value: authorizationCanary},
		},
	})
	if err != nil {
		t.Fatalf("Execute(): %v", err)
	}
	if attempts != 1 {
		t.Fatalf("transport attempts = %d, want no digest retry", attempts)
	}
	if result.StatusCode != http.StatusUnauthorized {
		t.Fatalf("result = %#v, want authored 401 response", result)
	}
	if result.Snapshot == nil {
		t.Fatal("401 response did not produce a failure snapshot")
	}
	if got := result.Snapshot.RequestHeaders["Authorization"]; got != "[REDACTED]" {
		t.Fatalf("snapshot Authorization = %q, want disclosure-safe value", got)
	}
	if strings.Contains(fmt.Sprintf("%#v", result.Snapshot), authorizationCanary) {
		t.Fatal("failure snapshot disclosed the authored Authorization value")
	}
}

func newDigestAuthServer(t *testing.T) (*httptest.Server, *int) {
	t.Helper()
	const realm = "apex"
	const nonce = "nonce-1"
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		auth := r.Header.Get("Authorization")
		if auth == "" {
			w.Header().Set("WWW-Authenticate", `Digest realm="`+realm+`", nonce="`+nonce+`", qop="auth", algorithm=MD5`)
			http.Error(w, "auth required", http.StatusUnauthorized)
			return
		}
		if !strings.HasPrefix(auth, "Digest ") {
			t.Fatalf("Authorization = %q, want Digest header", auth)
		}
		values := parseDigestKeyValues(strings.TrimSpace(strings.TrimPrefix(auth, "Digest")))
		if values["username"] != "alice" || values["realm"] != realm || values["nonce"] != nonce || values["uri"] != r.URL.RequestURI() || values["qop"] != "auth" || values["nc"] != digestAuthNC {
			t.Fatalf("digest values = %#v", values)
		}
		ha1 := digestMD5("alice:" + realm + ":secret")
		ha2 := digestMD5(r.Method + ":" + r.URL.RequestURI())
		want := digestMD5(ha1 + ":" + nonce + ":" + digestAuthNC + ":" + values["cnonce"] + ":auth:" + ha2)
		if values["response"] != want {
			t.Fatalf("digest response = %q, want %q", values["response"], want)
		}
		_, _ = w.Write([]byte("ok"))
	}))
	return server, &attempts
}
