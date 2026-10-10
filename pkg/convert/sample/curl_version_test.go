package sample

import (
	"slices"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestCurlHTTPVersionFlags(t *testing.T) {
	cases := []struct {
		name, command   string
		wantURL         string
		wantVersion     string
		wantBlocker     string
		wantBlockers    []string
		wantNoLeafValue bool
	}{
		{name: "http1.1", command: `curl --http1.1 https://api.example.test/orders`, wantVersion: "1.1"},
		{name: "prior knowledge cleartext", command: `curl --http2-prior-knowledge http://api.example.test/orders`, wantVersion: "2"},
		{name: "prior knowledge over tls still negotiates", command: `curl --http2-prior-knowledge https://api.example.test/orders`, wantNoLeafValue: true},
		{name: "http2 over tls is automatic", command: `curl --http2 https://api.example.test/orders`, wantNoLeafValue: true},
		{name: "http2 cleartext upgrade gated", command: `curl --http2 http://api.example.test/orders`, wantBlocker: "curl_http2_upgrade_not_modeled"},
		{name: "http1.0 gated", command: `curl --http1.0 https://api.example.test/orders`, wantBlocker: "curl_http10_not_supported"},
		{name: "short -0 does not swallow the URL", command: `curl -0 https://api.example.test/orders`, wantURL: "https://api.example.test/orders", wantBlocker: "curl_http10_not_supported"},
		{name: "http3 race gated", command: `curl --http3 https://api.example.test/orders`, wantBlocker: "curl_http3_race_not_modeled"},
		{name: "http3 on http is a no-op", command: `curl --http3 http://api.example.test/orders`, wantNoLeafValue: true},
		{name: "http3 only is exact 3", command: `curl --http3-only https://api.example.test/orders`, wantVersion: "3"},
		// --tls-max itself is not modeled, so its generic gate stays.
		{name: "http3 only with tls 1.3", command: `curl --tls-max 1.3 --http3-only https://api.example.test/orders`, wantVersion: "3", wantBlocker: "unsupported_curl_option"},
		{name: "http3 only on http", command: `curl --http3-only http://api.example.test/orders`, wantBlocker: "http3_requires_https"},
		{name: "http3 only below tls 1.3", command: `curl --http3-only --tls-max=1.2 https://api.example.test/orders`, wantBlocker: "http3_requires_tls13", wantBlockers: []string{"http3_requires_tls13", "unsupported_curl_option"}},
		{name: "http3 only through a proxy", command: `curl -x http://proxy.example.test:8080 --http3-only https://api.example.test/orders`, wantBlocker: "http3_with_proxy", wantBlockers: []string{"http3_with_proxy", "unsupported_curl_option"}},
		{name: "http3 only through socks", command: `curl --socks5 127.0.0.1:1080 --http3-only https://api.example.test/orders`, wantBlocker: "http3_with_proxy", wantBlockers: []string{"http3_with_proxy", "unsupported_curl_option"}},
		{name: "last flag wins toward exact", command: `curl --http3 --http1.1 https://api.example.test/orders`, wantVersion: "1.1"},
		{name: "last flag wins toward gate", command: `curl --http1.1 --http3 https://api.example.test/orders`, wantBlocker: "curl_http3_race_not_modeled"},
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
			if tc.wantBlockers != nil {
				got := metadataStrings(action.Metadata, "execution_blockers", "")
				for _, want := range tc.wantBlockers {
					if !slices.Contains(got, want) {
						t.Fatalf("execution blockers = %v, want %s", got, want)
					}
				}
			}
			if tc.wantBlocker != "" && tc.wantVersion == "" && action.HTTPVersion.IsPresent() && action.HTTPVersion.Value() != "auto" {
				t.Fatalf("a gated HTTP/3 source kept leaf %q", action.HTTPVersion.Value())
			}
			if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
				t.Fatalf("invalid draft: %v", issues)
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

func TestCurlHTTP3RaceProposesAnApproximateRewrite(t *testing.T) {
	result, err := Convert(ImportRequest{Format: "curl", Content: `curl --http3 https://api.example.test/orders`})
	if err != nil {
		t.Fatal(err)
	}
	action := result.Script.Actions[0]
	proposal, _ := action.Metadata[HTTPVersionRewriteProposalKey].(map[string]any)
	if proposal["policy"] != "auto+h3" || proposal["approximate"] != true || proposal["reason_code"] != "curl_http3_race_not_modeled" {
		t.Fatalf("proposal = %v", proposal)
	}
	// The proposal is not applied: the gate and the inherited policy stay
	// until the author accepts it.
	if action.HTTPVersion.IsPresent() || action.Metadata["execution_blocker"] != "curl_http3_race_not_modeled" {
		t.Fatalf("leaf=%v blocker=%v", action.HTTPVersion, action.Metadata["execution_blocker"])
	}
}

func TestRawHTTP3RequestLineStaysAnObservation(t *testing.T) {
	for _, content := range []string{
		"GET /items HTTP/3.0\nHost: api.example.test\n\n",
		"GET http://api.example.test/items HTTP/3.0\n\n",
	} {
		result, err := Convert(ImportRequest{Format: "raw_http", Content: content})
		if err != nil {
			t.Fatalf("%q: %v", content, err)
		}
		action := result.Script.Actions[0]
		if action.HTTPVersion.IsPresent() || result.Script.Config.HTTP.Version != "" {
			t.Fatalf("%q: an HTTP/3 request line became policy leaf=%v config=%q", content, action.HTTPVersion, result.Script.Config.HTTP.Version)
		}
		if blockers := metadataStrings(action.Metadata, "execution_blockers", ""); !slices.Contains(blockers, "raw_http_version_review_required") {
			t.Fatalf("%q: blockers = %v", content, blockers)
		}
		if action.Metadata[rawHTTP3RequestLineKey] != "HTTP/3.0" {
			t.Fatalf("%q: observation lost: %v", content, action.Metadata)
		}
	}
}
