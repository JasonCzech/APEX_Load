package sample

import (
	"testing"
)

func TestCurlHTTPVersionFlags(t *testing.T) {
	cases := []struct {
		name, command   string
		wantURL         string
		wantVersion     string
		wantBlocker     string
		wantNoLeafValue bool
	}{
		{name: "http1.1", command: `curl --http1.1 https://api.example.test/orders`, wantVersion: "1.1"},
		{name: "prior knowledge cleartext", command: `curl --http2-prior-knowledge http://api.example.test/orders`, wantVersion: "2"},
		{name: "prior knowledge over tls still negotiates", command: `curl --http2-prior-knowledge https://api.example.test/orders`, wantNoLeafValue: true},
		{name: "http2 over tls is automatic", command: `curl --http2 https://api.example.test/orders`, wantNoLeafValue: true},
		{name: "http2 cleartext upgrade gated", command: `curl --http2 http://api.example.test/orders`, wantBlocker: "curl_http2_upgrade_not_modeled"},
		{name: "http1.0 gated", command: `curl --http1.0 https://api.example.test/orders`, wantBlocker: "curl_http10_not_supported"},
		{name: "short -0 does not swallow the URL", command: `curl -0 https://api.example.test/orders`, wantURL: "https://api.example.test/orders", wantBlocker: "curl_http10_not_supported"},
		{name: "http3 gated", command: `curl --http3 https://api.example.test/orders`, wantBlocker: "curl_http3_not_supported"},
		{name: "http3 only gated", command: `curl --http3-only https://api.example.test/orders`, wantBlocker: "curl_http3_not_supported"},
		{name: "last flag wins toward exact", command: `curl --http3 --http1.1 https://api.example.test/orders`, wantVersion: "1.1"},
		{name: "last flag wins toward gate", command: `curl --http1.1 --http3 https://api.example.test/orders`, wantBlocker: "curl_http3_not_supported"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := Convert(ImportRequest{Format: "curl", Content: tc.command})
			if err != nil {
				t.Fatalf("Convert: %v", err)
			}
			if len(result.Script.Actions) != 1 {
				t.Fatalf("actions = %d", len(result.Script.Actions))
			}
			action := result.Script.Actions[0]
			if tc.wantURL != "" && action.Target != tc.wantURL && result.Script.Config.BaseURL+action.Target != tc.wantURL {
				t.Fatalf("target = %q base = %q, want %q", action.Target, result.Script.Config.BaseURL, tc.wantURL)
			}
			blocker, _ := action.Metadata["execution_blocker"].(string)
			if blocker != tc.wantBlocker {
				t.Fatalf("execution blocker = %q, want %q", blocker, tc.wantBlocker)
			}
			if tc.wantVersion != "" && (!action.HTTPVersion.IsPresent() || action.HTTPVersion.Value() != tc.wantVersion) {
				t.Fatalf("leaf version = %+v, want %s", action.HTTPVersion, tc.wantVersion)
			}
			if tc.wantNoLeafValue && action.HTTPVersion.IsPresent() && action.HTTPVersion.Value() != "auto" {
				t.Fatalf("leaf version = %q, want automatic negotiation", action.HTTPVersion.Value())
			}
		})
	}
}
