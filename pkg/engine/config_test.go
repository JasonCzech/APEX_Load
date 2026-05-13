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

func TestBuildProtoConfig_SOAP(t *testing.T) {
	script := &dsl.Script{
		Config: dsl.ScriptConfig{
			SOAP: dsl.SOAPConfig{
				Version:    "1.1",
				Namespaces: map[string]string{"soapenv": "http://schemas.xmlsoap.org/soap/envelope/", "auth": "urn:auth"},
				WSSecurity: &dsl.SOAPWSSecurityConfig{
					UsernameToken: &dsl.SOAPUsernameTokenConfig{
						Username:     "svc-user",
						Password:     "svc-pass",
						PasswordType: "text",
						Nonce:        true,
						Created:      true,
					},
					Timestamp: &dsl.SOAPTimestampConfig{TTLSeconds: 45},
				},
				FaultHandling: dsl.SOAPFaultHandlingConfig{FailOnFault: true},
			},
		},
	}

	cfg := buildProtoConfig(script)

	if cfg["soap_version"] != "1.1" {
		t.Errorf("soap_version = %v, want 1.1", cfg["soap_version"])
	}
	namespaces, ok := cfg["soap_namespaces"].(map[string]string)
	if !ok || namespaces["auth"] != "urn:auth" {
		t.Errorf("soap_namespaces = %v, want auth namespace", cfg["soap_namespaces"])
	}
	if cfg["soap_ws_security_username"] != "svc-user" {
		t.Errorf("soap_ws_security_username = %v, want svc-user", cfg["soap_ws_security_username"])
	}
	if cfg["soap_ws_security_password_type"] != "text" {
		t.Errorf("soap_ws_security_password_type = %v, want text", cfg["soap_ws_security_password_type"])
	}
	if cfg["soap_ws_security_nonce"] != true {
		t.Errorf("soap_ws_security_nonce = %v, want true", cfg["soap_ws_security_nonce"])
	}
	if cfg["soap_ws_security_created"] != true {
		t.Errorf("soap_ws_security_created = %v, want true", cfg["soap_ws_security_created"])
	}
	if cfg["soap_ws_security_timestamp_ttl_seconds"] != float64(45) {
		t.Errorf("soap_ws_security_timestamp_ttl_seconds = %v, want 45", cfg["soap_ws_security_timestamp_ttl_seconds"])
	}
	if cfg["soap_fault_fail_on_fault"] != true {
		t.Errorf("soap_fault_fail_on_fault = %v, want true", cfg["soap_fault_fail_on_fault"])
	}
}

func TestBuildProtoConfig_JavaHTTP(t *testing.T) {
	script := &dsl.Script{
		Config: dsl.ScriptConfig{
			JavaHTTP: dsl.JavaHTTPConfig{
				Codec:                "java_serialized_object_base64",
				HelperMode:           "embedded",
				ContentType:          "application/x-java-serialized-object",
				Accept:               "application/x-java-serialized-object",
				BodyEncoding:         "base64",
				ObjectSchema:         "com.example.AuthRequest",
				RequestMapping:       map[string]string{"username": "principal"},
				ResponseMapping:      map[string]string{"token": "sessionToken"},
				ExtractorType:        "opaque",
				PreserveBinaryResult: true,
			},
		},
	}

	cfg := buildProtoConfig(script)

	if cfg["java_http_codec"] != "java_serialized_object_base64" {
		t.Errorf("java_http_codec = %v, want java_serialized_object_base64", cfg["java_http_codec"])
	}
	if cfg["java_http_helper_mode"] != "embedded" {
		t.Errorf("java_http_helper_mode = %v, want embedded", cfg["java_http_helper_mode"])
	}
	if cfg["java_http_content_type"] != "application/x-java-serialized-object" {
		t.Errorf("java_http_content_type = %v", cfg["java_http_content_type"])
	}
	if cfg["java_http_body_encoding"] != "base64" {
		t.Errorf("java_http_body_encoding = %v, want base64", cfg["java_http_body_encoding"])
	}
	if cfg["java_http_preserve_binary_result"] != true {
		t.Errorf("java_http_preserve_binary_result = %v, want true", cfg["java_http_preserve_binary_result"])
	}
	requestMapping, ok := cfg["java_http_request_mapping"].(map[string]string)
	if !ok || requestMapping["username"] != "principal" {
		t.Errorf("java_http_request_mapping = %v, want username->principal", cfg["java_http_request_mapping"])
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

func TestBuildProtoConfig_Browser(t *testing.T) {
	headless := false
	script := &dsl.Script{
		Config: dsl.ScriptConfig{
			Browser: dsl.BrowserConfig{
				Engine:             "chromium",
				Headless:           &headless,
				SlowMoMs:           125,
				UserAgent:          "StormrunnerBrowser/0.1",
				CaptureScreenshots: "per_action",
				Viewport: dsl.BrowserViewport{
					Width:  1440,
					Height: 900,
				},
			},
		},
	}

	cfg := buildProtoConfig(script)

	if cfg["browser_engine"] != "chromium" {
		t.Errorf("browser_engine = %v, want chromium", cfg["browser_engine"])
	}
	if cfg["browser_headless"] != false {
		t.Errorf("browser_headless = %v, want false", cfg["browser_headless"])
	}
	if cfg["browser_slow_mo_ms"] != float64(125) {
		t.Errorf("browser_slow_mo_ms = %v, want 125", cfg["browser_slow_mo_ms"])
	}
	if cfg["browser_capture_screenshots"] != "per_action" {
		t.Errorf("browser_capture_screenshots = %v, want per_action", cfg["browser_capture_screenshots"])
	}
	if cfg["browser_viewport_width"] != float64(1440) {
		t.Errorf("browser_viewport_width = %v, want 1440", cfg["browser_viewport_width"])
	}
	if cfg["browser_viewport_height"] != float64(900) {
		t.Errorf("browser_viewport_height = %v, want 900", cfg["browser_viewport_height"])
	}
}

func TestBuildProtoConfig_JMS(t *testing.T) {
	script := &dsl.Script{
		Config: dsl.ScriptConfig{
			JMS: dsl.JMSConfig{
				Provider:        "solace",
				BrokerURL:       "tcp://broker:55555",
				MessageVPN:      "perf-vpn",
				Username:        "app",
				Password:        "secret",
				ClientID:        "stormrunner-vu",
				Destination:     "orders.in",
				DestinationType: "queue",
				ReplyTo:         "orders.reply",
				Acknowledgement: "client",
				DeliveryMode:    "persistent",
				TimeoutMs:       3500,
				Properties:      map[string]string{"vpn": "perf"},
				Mock:            true,
			},
		},
	}

	cfg := buildProtoConfig(script)

	if cfg["jms_provider"] != "solace" {
		t.Errorf("jms_provider = %v, want solace", cfg["jms_provider"])
	}
	if cfg["broker_url"] != "tcp://broker:55555" {
		t.Errorf("broker_url = %v, want tcp://broker:55555", cfg["broker_url"])
	}
	if cfg["jms_message_vpn"] != "perf-vpn" {
		t.Errorf("jms_message_vpn = %v, want perf-vpn", cfg["jms_message_vpn"])
	}
	if cfg["jms_destination"] != "orders.in" {
		t.Errorf("jms_destination = %v, want orders.in", cfg["jms_destination"])
	}
	if cfg["jms_reply_to"] != "orders.reply" {
		t.Errorf("jms_reply_to = %v, want orders.reply", cfg["jms_reply_to"])
	}
	if cfg["jms_timeout_ms"] != float64(3500) {
		t.Errorf("jms_timeout_ms = %v, want 3500", cfg["jms_timeout_ms"])
	}
	properties, ok := cfg["jms_properties"].(map[string]string)
	if !ok || properties["vpn"] != "perf" {
		t.Errorf("jms_properties = %v, want vpn=perf", cfg["jms_properties"])
	}
	if cfg["jms_mock"] != true {
		t.Errorf("jms_mock = %v, want true", cfg["jms_mock"])
	}
}
