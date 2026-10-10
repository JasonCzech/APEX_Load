package sample

import (
	"slices"
	"strings"
	"testing"
)

func TestShellSplitKeepsEmptyQuotedArguments(t *testing.T) {
	cases := []struct {
		input string
		want  []string
	}{
		{`curl -d "" https://api.example.test/`, []string{"curl", "-d", "", "https://api.example.test/"}},
		{`curl -H '' https://api.example.test/`, []string{"curl", "-H", "", "https://api.example.test/"}},
		{`curl "" ''`, []string{"curl", "", ""}},
		// Quotes inside a word join it, as in a shell.
		{`curl a""b 'c'd`, []string{"curl", "ab", "cd"}},
		{`curl "a b" -H 'X-Note: two words'`, []string{"curl", "a b", "-H", "X-Note: two words"}},
		{"curl \\\n  -d \"\" \\\n  https://api.example.test/", []string{"curl", "-d", "", "https://api.example.test/"}},
		{`curl   https://api.example.test/  `, []string{"curl", "https://api.example.test/"}},
	}
	for _, tc := range cases {
		got, err := shellSplit(tc.input)
		if err != nil || !slices.Equal(got, tc.want) {
			t.Fatalf("shellSplit(%q) = %q, %v; want %q", tc.input, got, err, tc.want)
		}
	}
}

func TestCurlEmptyOptionValuesKeepTheURLAndMatchCurl(t *testing.T) {
	const url = "https://api.example.test/orders"
	convert := func(t *testing.T, command string) (method, body string, headers map[string]string, blockers []string) {
		t.Helper()
		result, err := Convert(ImportRequest{Format: "curl", Content: command})
		if err != nil {
			t.Fatalf("Convert(%s): %v", command, err)
		}
		if len(result.Script.Actions) != 1 {
			t.Fatalf("actions = %d", len(result.Script.Actions))
		}
		action := result.Script.Actions[0]
		if target := result.Script.Config.BaseURL + action.Target; target != url && action.Target != url {
			t.Fatalf("%s: target = %q (base %q), want %s", command, action.Target, result.Script.Config.BaseURL, url)
		}
		method = action.Method
		if method == "" {
			method = action.Type
		}
		return strings.ToUpper(method), action.Body, action.Headers, metadataStrings(action.Metadata, "execution_blockers", "")
	}

	// An empty body is still a POST with an empty body, as curl sends it.
	if method, body, _, _ := convert(t, `curl -d "" `+url); method != "POST" || body != "" {
		t.Fatalf("-d '': method %s body %q", method, body)
	}
	// An empty -X restores the default method.
	if method, _, _, _ := convert(t, `curl -X "" `+url); method != "GET" {
		t.Fatalf("-X '': method %s", method)
	}
	// curl ignores an empty -H; it is not an invalid header.
	if _, _, headers, blockers := convert(t, `curl -H "" `+url); len(headers) != 0 || slices.Contains(blockers, "invalid_curl_header") {
		t.Fatalf("-H '': headers %v blockers %v", headers, blockers)
	}
	// Empty -A, -e and -b send no User-Agent, Referer or Cookie header.
	if _, _, headers, _ := convert(t, `curl -A "" -e '' -b "" `+url); len(headers) != 0 {
		t.Fatalf("empty -A/-e/-b: headers %v", headers)
	}
	// The inline forms follow the same rules.
	if method, _, headers, blockers := convert(t, `curl --request= --header= --user-agent= --referer= --cookie= `+url); method != "GET" || len(headers) != 0 || slices.Contains(blockers, "invalid_curl_header") {
		t.Fatalf("empty inline options: method %s headers %v blockers %v", method, headers, blockers)
	}
	// An empty --url or positional argument names no URL.
	convert(t, `curl --url "" `+url)
	convert(t, `curl --url= `+url)
	convert(t, `curl "" `+url)
}

func TestCurlEmptyProxyIsNoProxyForHTTP3Only(t *testing.T) {
	for _, command := range []string{
		`curl --proxy "" --http3-only https://api.example.test/orders`,
		`curl -x '' --http3-only https://api.example.test/orders`,
		`curl --proxy= --http3-only https://api.example.test/orders`,
	} {
		result, err := Convert(ImportRequest{Format: "curl", Content: command})
		if err != nil {
			t.Fatalf("Convert(%s): %v", command, err)
		}
		action := result.Script.Actions[0]
		if slices.Contains(metadataStrings(action.Metadata, "execution_blockers", ""), http3WithProxy) {
			t.Fatalf("%s: an empty proxy turns the proxy off, so exact HTTP/3 is representable: %v", command, action.Metadata)
		}
		if !action.HTTPVersion.IsPresent() || action.HTTPVersion.Value() != "3" {
			t.Fatalf("%s: leaf version = %+v, want 3", command, action.HTTPVersion)
		}
	}
	if facts := curlTransportFactsFrom([]string{"-x", "http://proxy.example.test:3128"}); !facts.proxy {
		t.Fatal("a proxy value is a proxy route")
	}
	if facts := curlTransportFactsFrom([]string{"--proxy=http://proxy.example.test:3128"}); !facts.proxy {
		t.Fatal("an inline proxy value is a proxy route")
	}
}
