package browsercontract

import "testing"

func TestIsBrowserInternalURL(t *testing.T) {
	for _, raw := range []string{"chrome-error://chromewebdata/", "chrome://settings", "devtools://devtools/bundled/inspector.html", "chrome-extension://abc/page.html", " CHROME-ERROR://chromewebdata/ "} {
		if !IsBrowserInternalURL(raw) {
			t.Errorf("IsBrowserInternalURL(%q) = false, want true", raw)
		}
	}
	for _, raw := range []string{"https://app.example.test/", "about:blank", "about:srcdoc", "/relative", "", "data:text/html,hi", "edge:8080/login", "chrome:9000"} {
		if IsBrowserInternalURL(raw) {
			t.Errorf("IsBrowserInternalURL(%q) = true, want false", raw)
		}
	}
}

func TestWindowPageKey(t *testing.T) {
	tests := []struct {
		raw  string
		want string
		ok   bool
	}{
		{"https://sso.example.test/lrr/Controller?TAM_OP=token_login&EXPIRE_SECS=60#state", "https://sso.example.test/lrr/Controller", true},
		{"HTTPS://SSO.example.test/lrr/Controller", "https://sso.example.test/lrr/Controller", true},
		{"https://app.example.test", "https://app.example.test/", true},
		{"https://app.example.test/#/report?id=7", "https://app.example.test/#/report", true},
		{"https://app.example.test/#!/home", "https://app.example.test/#!/home", true},
		{"https://app.example.test/cb#access_token=abc", "https://app.example.test/cb", true},
		{"Checkout", "", false},
		{"1", "", false},
		{"about:blank", "", false},
	}
	for _, tt := range tests {
		got, ok := WindowPageKey(tt.raw)
		if got != tt.want || ok != tt.ok {
			t.Errorf("WindowPageKey(%q) = %q, %v; want %q, %v", tt.raw, got, ok, tt.want, tt.ok)
		}
	}
}
