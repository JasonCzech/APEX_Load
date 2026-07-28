package enterprisearchive

import (
	"net/url"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func resultWithBaseURL(baseURL string, params []dsl.Parameter) *ConvertResult {
	return &ConvertResult{
		Warnings: []string{},
		Script: &dsl.Script{
			Protocol:   "http",
			Config:     dsl.ScriptConfig{BaseURL: baseURL},
			Parameters: params,
		},
	}
}

func assertBaseURLParseable(t *testing.T, base string) {
	t.Helper()
	if strings.ContainsAny(base, "{}") {
		t.Fatalf("base URL still contains template braces: %q", base)
	}
	parsed, err := url.Parse(base)
	if err != nil {
		t.Fatalf("base URL %q does not parse: %v", base, err)
	}
	if parsed.Host == "" {
		t.Fatalf("base URL %q has no host", base)
	}
}

func TestResolveTemplatedBaseURL(t *testing.T) {
	serverParam := []dsl.Parameter{{Name: "server_url", Type: "list", Values: []string{"acme.my.salesforce.com"}}}

	tests := []struct {
		name        string
		baseURL     string
		params      []dsl.Parameter
		wantBaseURL string
		wantWarning bool
	}{
		{
			name:        "raw param host with path resolves to concrete host",
			baseURL:     "http://{server_url}/mi/",
			params:      serverParam,
			wantBaseURL: "http://acme.my.salesforce.com/mi/",
		},
		{
			name:        "raw param host without path resolves",
			baseURL:     "https://{server_url}",
			params:      serverParam,
			wantBaseURL: "https://acme.my.salesforce.com",
		},
		{
			name:        "templated var host resolves",
			baseURL:     "http://{{var.server_url}}/mi/",
			params:      serverParam,
			wantBaseURL: "http://acme.my.salesforce.com/mi/",
		},
		{
			name:        "param value that is a full URL contributes only scheme+host",
			baseURL:     "http://{server_url}/mi/",
			params:      []dsl.Parameter{{Name: "server_url", Type: "list", Values: []string{"https://host.example.com"}}},
			wantBaseURL: "https://host.example.com/mi/",
		},
		{
			name:        "full URL parameter preserves escaped userinfo bytes",
			baseURL:     "http://{server_url}/mi/",
			params:      []dsl.Parameter{{Name: "server_url", Type: "list", Values: []string{"https://alice:p%2fword@host.example.com/source?ignored=1"}}},
			wantBaseURL: "https://alice:p%2fword@host.example.com/mi/",
		},
		{
			name:        "case-insensitive parameter name match",
			baseURL:     "http://{ServerURL}/mi/",
			params:      []dsl.Parameter{{Name: "ServerURL", Type: "list", Values: []string{"host.example.com"}}},
			wantBaseURL: "http://host.example.com/mi/",
		},
		{
			name:        "already concrete base URL untouched",
			baseURL:     "https://real.example.com/mi/",
			params:      serverParam,
			wantBaseURL: "https://real.example.com/mi/",
		},
		{
			name:        "unresolvable host falls back to a parseable placeholder with a warning",
			baseURL:     "http://{server_url}/mi/",
			params:      nil,
			wantBaseURL: "http://" + unresolvedBaseURLHost + "/mi/",
			wantWarning: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := resultWithBaseURL(tt.baseURL, tt.params)
			resolveTemplatedBaseURL(result)
			if result.Script.Config.BaseURL != tt.wantBaseURL {
				t.Fatalf("base URL = %q, want %q", result.Script.Config.BaseURL, tt.wantBaseURL)
			}
			// The whole point: the engine uses base_url verbatim, so it must parse.
			assertBaseURLParseable(t, result.Script.Config.BaseURL)
			hasWarning := len(result.Warnings) > 0
			if hasWarning != tt.wantWarning {
				t.Fatalf("warnings = %#v, wantWarning = %v", result.Warnings, tt.wantWarning)
			}
		})
	}
}

// TestResolveTemplatedBaseURLViaDataSource covers a host parameter backed by an
// inline data source (the common .prm/.dat import shape) rather than literal
// Values.
func TestResolveTemplatedBaseURLViaDataSource(t *testing.T) {
	result := resultWithBaseURL("http://{server_url}/mi/", []dsl.Parameter{
		{Name: "server_url", Type: "file", DataSource: "hosts", Column: "host"},
	})
	result.Script.DataSources = []dsl.DataSource{
		{Name: "hosts", InlineData: "host\nacme.my.salesforce.com\n", Delimiter: ",", FirstRowHeader: true},
	}
	resolveTemplatedBaseURL(result)
	if result.Script.Config.BaseURL != "http://acme.my.salesforce.com/mi/" {
		t.Fatalf("base URL = %q, want resolved from data source", result.Script.Config.BaseURL)
	}
	assertBaseURLParseable(t, result.Script.Config.BaseURL)
}

func TestSameOriginRelativeTargetPreservesWireSignificantURLComponents(t *testing.T) {
	t.Parallel()

	if got := makeRelative(
		"https://user:password@example.test/orders",
		"https://example.test",
	); got != "https://user:password@example.test/orders" {
		t.Fatalf("userinfo URL = %q, want absolute executable URL retained", got)
	}
	if got := makeRelative(
		"https://EXAMPLE.test/orders/a%2Fb?signature=a%2Bb",
		"https://example.test",
	); got != "/orders/a%2Fb?signature=a%2Bb" {
		t.Fatalf("encoded target = %q, want raw path/query encoding retained", got)
	}
}
