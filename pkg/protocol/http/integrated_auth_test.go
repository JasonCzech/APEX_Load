package http

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// ntlmChallengeMessage is a minimal NTLM Type 2 message: a server challenge
// and an empty target-info list.
func ntlmChallengeMessage() string {
	var message bytes.Buffer
	message.WriteString("NTLMSSP\x00")
	_ = binary.Write(&message, binary.LittleEndian, uint32(2))
	_ = binary.Write(&message, binary.LittleEndian, [2]uint16{0, 0})
	_ = binary.Write(&message, binary.LittleEndian, uint32(48))
	flags := uint32(1<<0 | 1<<2 | 1<<9 | 1<<19 | 1<<23 | 1<<29 | 1<<31)
	_ = binary.Write(&message, binary.LittleEndian, flags)
	message.Write([]byte{1, 2, 3, 4, 5, 6, 7, 8})
	message.Write(make([]byte, 8))
	_ = binary.Write(&message, binary.LittleEndian, [2]uint16{4, 4})
	_ = binary.Write(&message, binary.LittleEndian, uint32(48))
	message.Write([]byte{0, 0, 0, 0})
	return base64.StdEncoding.EncodeToString(message.Bytes())
}

func ntlmMessageType(header string) (uint32, []byte) {
	token, ok := strings.CutPrefix(header, "NTLM ")
	if !ok {
		return 0, nil
	}
	raw, err := base64.StdEncoding.DecodeString(token)
	if err != nil || len(raw) < 12 || string(raw[:8]) != "NTLMSSP\x00" {
		return 0, nil
	}
	return binary.LittleEndian.Uint32(raw[8:12]), raw
}

func utf16LE(text string) []byte {
	out := make([]byte, 0, 2*len(text))
	for _, r := range text {
		out = append(out, byte(r), byte(r>>8))
	}
	return out
}

func TestIntegratedAuth(t *testing.T) {
	var authenticated atomic.Int32
	ntlmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch kind, raw := ntlmMessageType(r.Header.Get("Authorization")); kind {
		case 1:
			w.Header().Set("WWW-Authenticate", "NTLM "+ntlmChallengeMessage())
			w.WriteHeader(http.StatusUnauthorized)
		case 3:
			if !bytes.Contains(raw, utf16LE("ada")) {
				t.Errorf("NTLM Type 3 does not name the user")
			}
			authenticated.Add(1)
			_, _ = w.Write([]byte("welcome"))
		default:
			if r.Header.Get("Authorization") != "" {
				t.Errorf("unexpected Authorization %q", r.Header.Get("Authorization"))
			}
			w.Header().Set("WWW-Authenticate", "NTLM")
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	defer ntlmServer.Close()
	basicServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "ada" || pass != "pw" {
			w.Header().Set("WWW-Authenticate", `Basic realm="shop"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer basicServer.Close()

	run := func(authType, target string, hosts ...string) *protocol.Result {
		t.Helper()
		config := map[string]any{"auth_type": authType, "auth_username": "ada", "auth_password": "pw", "auth_domain": "CORP"}
		if len(hosts) > 0 {
			config["auth_hosts"] = hosts
		}
		h := initCompatibilityHTTP(t, config)
		result, err := h.Execute(context.Background(), &protocol.Action{Type: "POST", Target: target, Body: []byte("payload")})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	if result := run("ntlm", ntlmServer.URL); !result.Success || authenticated.Load() != 1 {
		t.Fatalf("NTLM: %d %s", result.StatusCode, result.ErrorMessage)
	}
	if result := run("negotiate_any", ntlmServer.URL); !result.Success || authenticated.Load() != 2 {
		t.Fatalf("negotiate_any over NTLM: %d %s", result.StatusCode, result.ErrorMessage)
	}
	if result := run("negotiate_any", basicServer.URL); !result.Success {
		t.Fatalf("negotiate_any must answer a Basic challenge: %d %s", result.StatusCode, result.ErrorMessage)
	}
	if result := run("ntlm", basicServer.URL); result.StatusCode != http.StatusUnauthorized {
		t.Fatalf("ntlm must not send Basic credentials, got %d", result.StatusCode)
	}
	if result := run("negotiate_any", basicServer.URL, "elsewhere.test"); result.StatusCode != http.StatusUnauthorized {
		t.Fatalf("credentials must stay on their hosts, got %d", result.StatusCode)
	}
	if err := validateAuthType("kerberos"); err == nil {
		t.Fatal("kerberos must stay unsupported")
	}
}
