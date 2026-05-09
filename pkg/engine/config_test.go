package engine

import (
	"testing"

	"github.com/jasonczech/stormrunner/pkg/dsl"
)

func TestBuildProtoConfig_BaseURL(t *testing.T) {
	script := &dsl.Script{
		Config: dsl.ScriptConfig{
			BaseURL: "https://api.example.com",
		},
	}

	cfg := buildProtoConfig(script)
	if cfg["base_url"] != "https://api.example.com" {
		t.Errorf("base_url = %v, want %q", cfg["base_url"], "https://api.example.com")
	}
}

func TestBuildProtoConfig_Timeouts(t *testing.T) {
	script := &dsl.Script{
		Config: dsl.ScriptConfig{
			HTTP: dsl.HTTPConfig{
				ConnectTimeoutMs: 5000,
				RequestTimeoutMs: 30000,
				TLSTimeoutMs:     10000,
				IdleTimeoutMs:    90000,
			},
		},
	}

	cfg := buildProtoConfig(script)

	tests := []struct {
		key  string
		want float64
	}{
		{"connect_timeout_ms", 5000},
		{"request_timeout_ms", 30000},
		{"tls_timeout_ms", 10000},
		{"idle_timeout_ms", 90000},
	}

	for _, tt := range tests {
		v, ok := cfg[tt.key].(float64)
		if !ok {
			t.Errorf("%s not set", tt.key)
			continue
		}
		if v != tt.want {
			t.Errorf("%s = %v, want %v", tt.key, v, tt.want)
		}
	}
}

func TestBuildProtoConfig_TLS(t *testing.T) {
	script := &dsl.Script{
		Config: dsl.ScriptConfig{
			HTTP: dsl.HTTPConfig{
				TLS: dsl.TLSConfig{
					MinVersion:         "1.2",
					MaxVersion:         "1.3",
					InsecureSkipVerify: true,
					ClientCertFile:     "/certs/client.pem",
					ClientKeyFile:      "/certs/client.key",
					CACertFile:         "/certs/ca.pem",
				},
			},
		},
	}

	cfg := buildProtoConfig(script)

	if cfg["tls_min_version"] != "1.2" {
		t.Errorf("tls_min_version = %v, want %q", cfg["tls_min_version"], "1.2")
	}
	if cfg["tls_max_version"] != "1.3" {
		t.Errorf("tls_max_version = %v, want %q", cfg["tls_max_version"], "1.3")
	}
	if cfg["tls_insecure_skip_verify"] != true {
		t.Error("tls_insecure_skip_verify should be true")
	}
	if cfg["tls_client_cert_file"] != "/certs/client.pem" {
		t.Errorf("tls_client_cert_file = %v", cfg["tls_client_cert_file"])
	}
}

func TestBuildProtoConfig_Proxy(t *testing.T) {
	script := &dsl.Script{
		Config: dsl.ScriptConfig{
			HTTP: dsl.HTTPConfig{
				Proxy: dsl.ProxyConfig{
					URL:      "http://proxy:8080",
					Username: "user",
					Password: "pass",
					NoProxy:  []string{"localhost", "10.0.0.0/8"},
				},
			},
		},
	}

	cfg := buildProtoConfig(script)

	if cfg["proxy_url"] != "http://proxy:8080" {
		t.Errorf("proxy_url = %v", cfg["proxy_url"])
	}
	if cfg["proxy_username"] != "user" {
		t.Errorf("proxy_username = %v", cfg["proxy_username"])
	}
	if cfg["proxy_password"] != "pass" {
		t.Errorf("proxy_password = %v", cfg["proxy_password"])
	}
	noProxy, ok := cfg["proxy_no_proxy"].([]string)
	if !ok || len(noProxy) != 2 {
		t.Errorf("proxy_no_proxy = %v", cfg["proxy_no_proxy"])
	}
}

func TestBuildProtoConfig_Auth(t *testing.T) {
	script := &dsl.Script{
		Config: dsl.ScriptConfig{
			HTTP: dsl.HTTPConfig{
				Auth: dsl.AuthConfig{
					Type:     "ntlm",
					Username: "admin",
					Password: "secret",
					Domain:   "CORP",
				},
			},
		},
	}

	cfg := buildProtoConfig(script)

	if cfg["auth_type"] != "ntlm" {
		t.Errorf("auth_type = %v", cfg["auth_type"])
	}
	if cfg["auth_username"] != "admin" {
		t.Errorf("auth_username = %v", cfg["auth_username"])
	}
	if cfg["auth_domain"] != "CORP" {
		t.Errorf("auth_domain = %v", cfg["auth_domain"])
	}
}

func TestBuildProtoConfig_BearerAuth(t *testing.T) {
	script := &dsl.Script{
		Config: dsl.ScriptConfig{
			HTTP: dsl.HTTPConfig{
				Auth: dsl.AuthConfig{
					Type:  "bearer",
					Token: "my-jwt-token",
				},
			},
		},
	}

	cfg := buildProtoConfig(script)

	if cfg["auth_type"] != "bearer" {
		t.Errorf("auth_type = %v", cfg["auth_type"])
	}
	if cfg["auth_token"] != "my-jwt-token" {
		t.Errorf("auth_token = %v", cfg["auth_token"])
	}
}

func TestBuildProtoConfig_Connection(t *testing.T) {
	keepAlive := true
	maxRedirects := 5
	cookiesEnabled := false

	script := &dsl.Script{
		Config: dsl.ScriptConfig{
			HTTP: dsl.HTTPConfig{
				MaxConnsPerHost: 50,
				MaxIdleConns:    25,
				KeepAlive:       &keepAlive,
				Version:         "2",
				MaxRedirects:    &maxRedirects,
				CookiesEnabled:  &cookiesEnabled,
			},
		},
	}

	cfg := buildProtoConfig(script)

	if cfg["max_conns_per_host"] != float64(50) {
		t.Errorf("max_conns_per_host = %v", cfg["max_conns_per_host"])
	}
	if cfg["keep_alive"] != true {
		t.Errorf("keep_alive = %v", cfg["keep_alive"])
	}
	if cfg["http_version"] != "2" {
		t.Errorf("http_version = %v", cfg["http_version"])
	}
	if cfg["max_redirects"] != float64(5) {
		t.Errorf("max_redirects = %v", cfg["max_redirects"])
	}
	if cfg["cookies_enabled"] != false {
		t.Errorf("cookies_enabled = %v", cfg["cookies_enabled"])
	}
}

func TestBuildProtoConfig_BrowserEmulation(t *testing.T) {
	script := &dsl.Script{
		Config: dsl.ScriptConfig{
			HTTP: dsl.HTTPConfig{
				UserAgent:      "Mozilla/5.0",
				AcceptEncoding: "gzip, deflate, br",
				AcceptLanguage: "en-US,en;q=0.9",
				DefaultHeaders: map[string]string{"X-Custom": "value"},
			},
		},
	}

	cfg := buildProtoConfig(script)

	if cfg["user_agent"] != "Mozilla/5.0" {
		t.Errorf("user_agent = %v", cfg["user_agent"])
	}
	if cfg["accept_encoding"] != "gzip, deflate, br" {
		t.Errorf("accept_encoding = %v", cfg["accept_encoding"])
	}
	headers, ok := cfg["default_headers"].(map[string]string)
	if !ok || headers["X-Custom"] != "value" {
		t.Errorf("default_headers = %v", cfg["default_headers"])
	}
}

func TestBuildProtoConfig_EmptyConfig(t *testing.T) {
	script := &dsl.Script{}
	cfg := buildProtoConfig(script)

	if cfg["base_url"] != "" {
		t.Errorf("base_url should be empty, got %v", cfg["base_url"])
	}
	// Zeroed fields should not appear in the config
	for _, key := range []string{
		"connect_timeout_ms", "proxy_url", "auth_type",
		"tls_min_version", "user_agent", "http_version",
	} {
		if _, ok := cfg[key]; ok {
			t.Errorf("key %q should not be set for empty config", key)
		}
	}
}
