package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

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
