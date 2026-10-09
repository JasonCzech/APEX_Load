package requestimport

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestIntegratedAuthScheme(t *testing.T) {
	for value, want := range map[string]string{
		"NTLM TlRMTVNT": "ntlm", "ntlm abc": "ntlm", "Negotiate YIIG": "negotiate", "Basic abc": "", "Bearer x": "", "": "",
	} {
		if got := IntegratedAuthScheme(value); got != want {
			t.Errorf("IntegratedAuthScheme(%q) = %q, want %q", value, got, want)
		}
	}
	if !IsIntegratedAuthChallenge(401, []string{"Basic realm=x\nNTLM"}) {
		t.Fatal("a newline-joined challenge list containing NTLM is an integrated challenge")
	}
	if IsIntegratedAuthChallenge(401, []string{"Basic realm=x"}) || IsIntegratedAuthChallenge(200, []string{"NTLM"}) {
		t.Fatal("only 401 NTLM/Negotiate challenges qualify")
	}
}

func TestCookieOriginsLeaveJarCookiesToTheJar(t *testing.T) {
	origins := NewCookieOrigins()
	origins.Observe("https://shop.test:443/login", []string{"SESSIONID=a; Path=/", "lang=en; Domain=.shop.test; Path=/"})
	origins.Observe("http://other.test/", []string{"tracking=1"})

	remaining, removed := origins.WithoutJarCookies("https://shop.test/cart", "consent=yes; SESSIONID=a; lang=en; tracking=1")
	if remaining != "consent=yes; tracking=1" || removed != 2 {
		t.Fatalf("remaining = %q removed = %d", remaining, removed)
	}
	remaining, removed = origins.WithoutJarCookies("https://api.shop.test/", "lang=en; SESSIONID=a")
	if remaining != "SESSIONID=a" || removed != 1 {
		t.Fatalf("a Domain= cookie covers subdomains, a host-only cookie does not: remaining = %q removed = %d", remaining, removed)
	}
	remaining, removed = origins.WithoutJarCookies("https://shop.test/", "SESSIONID=a")
	if remaining != "" || removed != 1 {
		t.Fatalf("remaining = %q removed = %d", remaining, removed)
	}
}

// The jar sends a cookie only where RFC 6265 lets it, so only there is the
// recorded value left to it.
func TestCookieOriginsFollowPathSecureAndDeletion(t *testing.T) {
	origins := NewCookieOrigins()
	origins.Observe("https://shop.test/admin/login", []string{"admin=1; Path=/admin", "dir=1", "token=t; Secure; Path=/", "gone=1; Path=/"})
	origins.Observe("https://shop.test/logout", []string{"gone=; Path=/; Max-Age=0"})

	for _, tc := range []struct {
		url, header, want string
	}{
		{"https://shop.test/admin/users", "admin=1", ""},
		{"https://shop.test/administrator", "admin=1", "admin=1"},
		{"https://shop.test/public", "admin=1", "admin=1"},
		// Without Path=, the default path is the directory of /admin/login.
		{"https://shop.test/admin/x", "dir=1", ""},
		{"https://shop.test/other", "dir=1", "dir=1"},
		{"http://shop.test/", "token=t", "token=t"},
		{"https://shop.test/", "token=t", ""},
		// A deleted cookie is not in the jar, so a recorded value stays.
		{"https://shop.test/", "gone=2", "gone=2"},
	} {
		if remaining, _ := origins.WithoutJarCookies(tc.url, tc.header); remaining != tc.want {
			t.Errorf("%s %q: remaining = %q, want %q", tc.url, tc.header, remaining, tc.want)
		}
	}
}

func TestRecordedTrafficFindingGates(t *testing.T) {
	// A truncated body cannot run; an integrated-auth request runs on the
	// script's negotiate_any auth, and promotion waits for review (WP3.4).
	for _, finding := range []struct {
		code string
		gate string
		want string
	}{
		{IntegratedAuthFinding("har", "entry-1", "ntlm").Code, string(IntegratedAuthFinding("har", "entry-1", "ntlm").Gate), "promotion"},
		{RecordedBodyTruncatedFinding("recording", "request-2", 10).Code, string(RecordedBodyTruncatedFinding("recording", "request-2", 10).Gate), "execution"},
	} {
		if finding.gate != finding.want {
			t.Fatalf("%s gate = %q, want %s", finding.code, finding.gate, finding.want)
		}
	}
}

func TestApplyIntegratedAuth(t *testing.T) {
	script := &dsl.Script{}
	if warning := ApplyIntegratedAuth(script, []string{"intranet.test", "sso.test"}); warning != "" {
		t.Fatal(warning)
	}
	auth := script.Config.HTTP.Auth
	if auth.Type != "negotiate_any" || auth.Username != "{{secret.INTEGRATED_AUTH_USERNAME}}" || auth.Password != "{{secret.INTEGRATED_AUTH_PASSWORD}}" || strings.Join(auth.Hosts, ",") != "intranet.test,sso.test" {
		t.Fatalf("auth = %+v", auth)
	}
	if warning := ApplyIntegratedAuth(script, []string{"SSO.test", "other.test"}); warning != "" || strings.Join(script.Config.HTTP.Auth.Hosts, ",") != "intranet.test,sso.test,other.test" {
		t.Fatalf("a second call widens the scope: %+v %q", script.Config.HTTP.Auth, warning)
	}
	basic := &dsl.Script{Config: dsl.ScriptConfig{HTTP: dsl.HTTPConfig{Auth: dsl.AuthConfig{Type: "basic", Username: "u", Password: "p"}}}}
	if warning := ApplyIntegratedAuth(basic, []string{"intranet.test"}); warning == "" || basic.Config.HTTP.Auth.Type != "basic" {
		t.Fatalf("other auth must be kept with a warning: %+v %q", basic.Config.HTTP.Auth, warning)
	}
	if host := IntegratedAuthHost("https://Intranet.Test:8443/x"); host != "intranet.test" {
		t.Fatalf("host = %q", host)
	}
}

func TestRecordedPageTitle(t *testing.T) {
	tests := []struct {
		contentType string
		status      int
		body        string
		dynamic     []string
		want        string
	}{
		{"text/html", 200, "<html><head><TITLE lang=en>\n Orders \n</TITLE>", nil, "Orders"},
		{"application/json", 200, "<title>x</title>", nil, ""},
		{"text/html", 302, "<title>Moved</title>", nil, ""},
		{"text/html", 200, "<title>Order 99172</title>", []string{"99172"}, ""},
		{"text/html", 200, "<title></title>", nil, ""},
		{"text/html", 200, "<title>" + strings.Repeat("x", 201) + "</title>", nil, ""},
	}
	for _, tt := range tests {
		if got := RecordedPageTitle(tt.contentType, tt.status, tt.body, tt.dynamic); got != tt.want {
			t.Errorf("RecordedPageTitle(%q, %d, %q) = %q, want %q", tt.contentType, tt.status, tt.body, got, tt.want)
		}
	}
}
