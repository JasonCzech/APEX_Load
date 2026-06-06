package engine

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestBuildProtoConfig_Kafka(t *testing.T) {
	script := &dsl.Script{
		Config: dsl.ScriptConfig{
			Kafka: dsl.KafkaConfig{
				Brokers:           []string{"broker-1:9092", "broker-2:9092"},
				SecurityProtocol:  "sasl_ssl",
				SASLMechanism:     "scram-sha-512",
				SASLUsername:      "svc",
				ConsumerGroup:     "cg-1",
				Topic:             "orders.events",
				Acks:              "all",
				Compression:       "zstd",
				PartitionStrategy: "key-hash",
				Partitions:        6,
				TimeoutMs:         2500,
				Properties:        map[string]string{"client.dns.lookup": "use_all_dns_ips"},
				Mock:              true,
			},
		},
	}

	cfg := buildProtoConfig(script)

	brokers, ok := cfg["kafka_brokers"].([]string)
	if !ok || len(brokers) != 2 || brokers[0] != "broker-1:9092" {
		t.Fatalf("kafka_brokers = %v, want two brokers", cfg["kafka_brokers"])
	}
	if cfg["kafka_security_protocol"] != "sasl_ssl" {
		t.Fatalf("kafka_security_protocol = %v", cfg["kafka_security_protocol"])
	}
	if cfg["kafka_consumer_group"] != "cg-1" {
		t.Fatalf("kafka_consumer_group = %v", cfg["kafka_consumer_group"])
	}
	if cfg["kafka_topic"] != "orders.events" {
		t.Fatalf("kafka_topic = %v", cfg["kafka_topic"])
	}
	if cfg["kafka_partitions"] != float64(6) {
		t.Fatalf("kafka_partitions = %v, want 6", cfg["kafka_partitions"])
	}
	if cfg["kafka_timeout_ms"] != float64(2500) {
		t.Fatalf("kafka_timeout_ms = %v, want 2500", cfg["kafka_timeout_ms"])
	}
	if cfg["kafka_mock"] != true {
		t.Fatalf("kafka_mock = %v, want true", cfg["kafka_mock"])
	}
}

func TestBuildProtoConfig_KafkaSASLPasswordSecret(t *testing.T) {
	resolver := SecretResolverFunc(func(name string) (string, bool) {
		if name == "kafka_pw" {
			return "runtime-kafka-pass", true
		}
		return "", false
	})

	script := &dsl.Script{
		Config: dsl.ScriptConfig{
			Kafka: dsl.KafkaConfig{
				Brokers:            []string{"broker:9092"},
				SASLPasswordSecret: "{{secret.kafka_pw}}",
				Topic:              "t",
				Mock:               true,
			},
		},
	}

	cfg := buildProtoConfigWithSecretResolver(script, resolver)
	if cfg["kafka_sasl_password_secret"] != "runtime-kafka-pass" {
		t.Fatalf("kafka_sasl_password_secret = %v, want runtime-kafka-pass", cfg["kafka_sasl_password_secret"])
	}
}

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

func TestNetworkProfileLabel(t *testing.T) {
	if got := networkProfileLabel(dsl.NetworkSimulationConfig{Mode: "preset", Preset: "dsl"}); got != "preset:dsl" {
		t.Fatalf("preset label = %q, want preset:dsl", got)
	}
	got := networkProfileLabel(dsl.NetworkSimulationConfig{
		Mode:          "custom",
		BandwidthKbps: 2048,
		LatencyMs:     125,
		JitterMs:      15,
		PacketLossPct: 1.5,
	})
	if got != "custom:bandwidth=2048kbps,latency=125ms,jitter=15ms,loss=1.5%" {
		t.Fatalf("custom label = %q, want stable custom profile label", got)
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
				ProxyMode: "custom",
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

	if cfg["proxy_mode"] != "custom" {
		t.Errorf("proxy_mode = %v", cfg["proxy_mode"])
	}

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

func TestBuildProtoConfig_ResolvesSecretPlaceholders(t *testing.T) {
	resolver := SecretResolverFunc(func(name string) (string, bool) {
		switch name {
		case "proxy_password":
			return "proxy-pass", true
		case "auth_token":
			return "runtime-token", true
		case "ws_password":
			return "soap-pass", true
		default:
			return "", false
		}
	})

	script := &dsl.Script{
		Config: dsl.ScriptConfig{
			HTTP: dsl.HTTPConfig{
				Proxy: dsl.ProxyConfig{Password: "{{secret.proxy_password}}"},
				Auth:  dsl.AuthConfig{Type: "bearer", Token: "{{secret.auth_token}}"},
			},
			SOAP: dsl.SOAPConfig{
				WSSecurity: &dsl.SOAPWSSecurityConfig{
					UsernameToken: &dsl.SOAPUsernameTokenConfig{Password: "{{secret.ws_password}}"},
				},
			},
		},
	}

	cfg := buildProtoConfigWithSecretResolver(script, resolver)

	if cfg["proxy_password"] != "proxy-pass" {
		t.Fatalf("proxy_password = %v, want proxy-pass", cfg["proxy_password"])
	}
	if cfg["auth_token"] != "runtime-token" {
		t.Fatalf("auth_token = %v, want runtime-token", cfg["auth_token"])
	}
	if cfg["soap_ws_security_password"] != "soap-pass" {
		t.Fatalf("soap_ws_security_password = %v, want soap-pass", cfg["soap_ws_security_password"])
	}
}

func TestAzureKeyVaultSecretResolver_Resolve(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("Authorization = %q, want Bearer test-token", got)
		}
		if r.URL.Path != "/secrets/db-password" {
			t.Fatalf("path = %q, want /secrets/db-password", r.URL.Path)
		}
		if r.URL.Query().Get("api-version") != "7.4" {
			t.Fatalf("api-version = %q, want 7.4", r.URL.Query().Get("api-version"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"value":"kv-secret"}`))
	}))
	defer server.Close()

	resolver := &azureKeyVaultSecretResolver{
		baseURL:    server.URL,
		token:      "test-token",
		apiVersion: "7.4",
		client:     server.Client(),
	}

	value, ok := resolver.Resolve("db_password")
	if !ok {
		t.Fatal("Resolve returned ok=false, want true")
	}
	if value != "kv-secret" {
		t.Fatalf("value = %q, want kv-secret", value)
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

func TestBuildProtoConfig_WebContentChecks(t *testing.T) {
	requiredText := false
	script := &dsl.Script{
		Config: dsl.ScriptConfig{
			WebRuntime: dsl.WebRuntimeConfig{
				ContentChecks: []dsl.ContentCheckConfig{{
					Text:        "Access Denied",
					Scope:       "any_response",
					FailIfFound: &requiredText,
				}},
			},
		},
	}

	cfg := buildProtoConfig(script)
	checks, ok := cfg["web_content_checks"].([]map[string]any)
	if !ok || len(checks) != 1 {
		t.Fatalf("web_content_checks = %v, want one flattened content check", cfg["web_content_checks"])
	}
	if checks[0]["text"] != "Access Denied" {
		t.Fatalf("content check text = %v, want Access Denied", checks[0]["text"])
	}
	if checks[0]["scope"] != "any_response" {
		t.Fatalf("content check scope = %v, want any_response", checks[0]["scope"])
	}
	if checks[0]["fail_if_found"] != false {
		t.Fatalf("content check fail_if_found = %v, want false", checks[0]["fail_if_found"])
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

func TestBuildProtoConfig_JavaHTTPSidecarHelperMode(t *testing.T) {
	script := &dsl.Script{
		Config: dsl.ScriptConfig{
			JavaHTTP: dsl.JavaHTTPConfig{
				Codec:        "java_serialized_object_base64",
				HelperMode:   "sidecar",
				BodyEncoding: "base64",
			},
		},
	}

	cfg := buildProtoConfig(script)

	if cfg["java_http_helper_mode"] != "sidecar" {
		t.Errorf("java_http_helper_mode = %v, want sidecar", cfg["java_http_helper_mode"])
	}
}

func TestBuildProtoConfig_Database(t *testing.T) {
	script := &dsl.Script{
		Config: dsl.ScriptConfig{
			Database: dsl.DatabaseConfig{
				Driver:             "sqlite",
				DSN:                ":memory:",
				DSNSecret:          "secret://perf/db-dsn",
				MaxOpenConns:       4,
				MaxIdleConns:       2,
				ConnMaxLifetimeMs:  60000,
				QueryTimeoutMs:     1500,
				MaxRows:            25,
				ReadOnly:           true,
				RollbackOnTeardown: true,
			},
		},
	}

	cfg := buildProtoConfig(script)

	if cfg["database_driver"] != "sqlite" || cfg["database_dsn"] != ":memory:" {
		t.Fatalf("database config = %#v, want driver and dsn", cfg)
	}
	if cfg["database_dsn_secret"] != "secret://perf/db-dsn" {
		t.Fatalf("database_dsn_secret = %#v, want secret binding", cfg["database_dsn_secret"])
	}
	if cfg["database_max_open_conns"] != float64(4) || cfg["database_query_timeout_ms"] != float64(1500) || cfg["database_max_rows"] != float64(25) {
		t.Fatalf("database numeric config = %#v", cfg)
	}
	if cfg["database_read_only"] != true || cfg["database_rollback_on_teardown"] != true {
		t.Fatalf("database boolean config = %#v", cfg)
	}
}

func TestBuildProtoConfig_Network(t *testing.T) {
	script := &dsl.Script{
		Config: dsl.ScriptConfig{
			Network: dsl.NetworkConfig{
				Subprotocol: "smtp",
				Host:        "mail.example.test",
				Port:        2525,
				Address:     "mail.example.test:2525",
				TimeoutMs:   1500,
				TLS:         true,
				Username:    "svc",
				Password:    "secret",
				From:        "load@example.test",
				To:          []string{"ops@example.test"},
				BaseDN:      "dc=example,dc=test",
			},
		},
	}

	cfg := buildProtoConfig(script)

	if cfg["network_subprotocol"] != "smtp" || cfg["network_address"] != "mail.example.test:2525" {
		t.Fatalf("network string config = %#v", cfg)
	}
	if cfg["network_port"] != float64(2525) || cfg["network_timeout_ms"] != float64(1500) {
		t.Fatalf("network numeric config = %#v", cfg)
	}
	if cfg["network_tls"] != true || cfg["network_username"] != "svc" || cfg["network_from"] != "load@example.test" {
		t.Fatalf("network auth/mail config = %#v", cfg)
	}
	if got, ok := cfg["network_to"].([]string); !ok || len(got) != 1 || got[0] != "ops@example.test" {
		t.Fatalf("network_to = %#v, want recipient slice", cfg["network_to"])
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
	cacheEnabled := false
	checkNewerVersions := true
	touchMode := true
	clientMetrics := true
	latencyMs := 75
	script := &dsl.Script{
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{Preserved: map[string]any{
			"recorded_resource_urls_available": true,
			"recorded_resource_urls":           []string{"https://example.com/static/logo.png", "https://cdn.example.com/app/site.css?v=2"},
		}}},
		Config: dsl.ScriptConfig{
			Browser: dsl.BrowserConfig{
				Engine:             "chromium",
				Headless:           &headless,
				CacheEnabled:       &cacheEnabled,
				CacheContentURLs:   &cacheEnabled,
				CheckNewerVersions: &checkNewerVersions,
				DownloadResources:  "recorded",
				SlowMoMs:           125,
				UserAgent:          "APEXLoadBrowser/0.1",
				DeviceProfile:      "iphone_se",
				TouchMode:          &touchMode,
				Orientation:        "portrait",
				ClientMetrics:      &clientMetrics,
				CaptureScreenshots: "per_action",
				Viewport: dsl.BrowserViewport{
					Width:  1440,
					Height: 900,
				},
			},
			WebRuntime: dsl.WebRuntimeConfig{
				DownloadFilters: dsl.DownloadFiltersConfig{
					Mode:  "block_list",
					Hosts: []string{"ads.example.com", "cdn.example.com"},
				},
				DataFormatting: dsl.DataFormattingConfig{
					AutoDetect: true,
					Chain:      []dsl.DFETransform{{Type: "base64"}, {Type: "gzip", Options: map[string]string{"mode": "decode"}}},
				},
				NetworkSimulation: dsl.NetworkSimulationConfig{
					Mode:          "custom",
					BandwidthKbps: 2048,
					LatencyMs:     latencyMs,
					JitterMs:      15,
					PacketLossPct: 1.5,
				},
				Streaming: dsl.StreamingConfig{
					Mode:                "fixed",
					BufferSizeSeconds:   12,
					FixedBitrateKbps:    1800,
					BandwidthEstimation: "instant",
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
	if cfg["browser_cache_enabled"] != false {
		t.Errorf("browser_cache_enabled = %v, want false", cfg["browser_cache_enabled"])
	}
	if cfg["browser_cache_content_urls"] != false {
		t.Errorf("browser_cache_content_urls = %v, want false", cfg["browser_cache_content_urls"])
	}
	if cfg["browser_check_newer_versions"] != true {
		t.Errorf("browser_check_newer_versions = %v, want true", cfg["browser_check_newer_versions"])
	}
	if cfg["browser_download_resources"] != "recorded" {
		t.Errorf("browser_download_resources = %v, want recorded", cfg["browser_download_resources"])
	}
	if cfg["browser_recorded_resources_available"] != true {
		t.Errorf("browser_recorded_resources_available = %v, want true", cfg["browser_recorded_resources_available"])
	}
	recordedResources, ok := cfg["browser_recorded_resources"].([]string)
	if !ok || len(recordedResources) != 2 {
		t.Errorf("browser_recorded_resources = %v, want 2 urls", cfg["browser_recorded_resources"])
	}
	if cfg["browser_slow_mo_ms"] != float64(125) {
		t.Errorf("browser_slow_mo_ms = %v, want 125", cfg["browser_slow_mo_ms"])
	}
	if cfg["browser_capture_screenshots"] != "per_action" {
		t.Errorf("browser_capture_screenshots = %v, want per_action", cfg["browser_capture_screenshots"])
	}
	if cfg["browser_device_profile"] != "iphone_se" {
		t.Errorf("browser_device_profile = %v, want iphone_se", cfg["browser_device_profile"])
	}
	if cfg["browser_touch_mode"] != true {
		t.Errorf("browser_touch_mode = %v, want true", cfg["browser_touch_mode"])
	}
	if cfg["browser_orientation"] != "portrait" {
		t.Errorf("browser_orientation = %v, want portrait", cfg["browser_orientation"])
	}
	if cfg["browser_client_metrics"] != true {
		t.Errorf("browser_client_metrics = %v, want true", cfg["browser_client_metrics"])
	}
	if cfg["browser_viewport_width"] != float64(1440) {
		t.Errorf("browser_viewport_width = %v, want 1440", cfg["browser_viewport_width"])
	}
	if cfg["browser_viewport_height"] != float64(900) {
		t.Errorf("browser_viewport_height = %v, want 900", cfg["browser_viewport_height"])
	}
	if cfg["web_network_simulation_mode"] != "custom" {
		t.Errorf("web_network_simulation_mode = %v, want custom", cfg["web_network_simulation_mode"])
	}
	if cfg["web_download_filters_mode"] != "block_list" {
		t.Errorf("web_download_filters_mode = %v, want block_list", cfg["web_download_filters_mode"])
	}
	downloadFilterHosts, ok := cfg["web_download_filters_hosts"].([]string)
	if !ok || len(downloadFilterHosts) != 2 {
		t.Errorf("web_download_filters_hosts = %v, want 2 hosts", cfg["web_download_filters_hosts"])
	}
	if cfg["web_data_formatting_auto_detect"] != true {
		t.Errorf("web_data_formatting_auto_detect = %v, want true", cfg["web_data_formatting_auto_detect"])
	}
	chain, ok := cfg["web_data_formatting_chain"].([]map[string]any)
	if !ok || len(chain) != 2 || chain[0]["type"] != "base64" || chain[1]["type"] != "gzip" {
		t.Errorf("web_data_formatting_chain = %v, want base64 -> gzip", cfg["web_data_formatting_chain"])
	}
	options, ok := chain[1]["options"].(map[string]string)
	if !ok || options["mode"] != "decode" {
		t.Errorf("web_data_formatting_chain gzip options = %v, want mode=decode", chain[1]["options"])
	}
	if cfg["web_network_simulation_bandwidth_kbps"] != float64(2048) {
		t.Errorf("web_network_simulation_bandwidth_kbps = %v, want 2048", cfg["web_network_simulation_bandwidth_kbps"])
	}
	if cfg["web_network_simulation_latency_ms"] != float64(latencyMs) {
		t.Errorf("web_network_simulation_latency_ms = %v, want %d", cfg["web_network_simulation_latency_ms"], latencyMs)
	}
	if cfg["web_network_simulation_jitter_ms"] != float64(15) {
		t.Errorf("web_network_simulation_jitter_ms = %v, want 15", cfg["web_network_simulation_jitter_ms"])
	}
	if cfg["web_network_simulation_packet_loss_pct"] != 1.5 {
		t.Errorf("web_network_simulation_packet_loss_pct = %v, want 1.5", cfg["web_network_simulation_packet_loss_pct"])
	}
	if cfg["web_streaming_mode"] != "fixed" {
		t.Errorf("web_streaming_mode = %v, want fixed", cfg["web_streaming_mode"])
	}
	if cfg["web_streaming_buffer_size_seconds"] != float64(12) {
		t.Errorf("web_streaming_buffer_size_seconds = %v, want 12", cfg["web_streaming_buffer_size_seconds"])
	}
	if cfg["web_streaming_fixed_bitrate_kbps"] != float64(1800) {
		t.Errorf("web_streaming_fixed_bitrate_kbps = %v, want 1800", cfg["web_streaming_fixed_bitrate_kbps"])
	}
	if cfg["web_streaming_bandwidth_estimation"] != "instant" {
		t.Errorf("web_streaming_bandwidth_estimation = %v, want instant", cfg["web_streaming_bandwidth_estimation"])
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
				ClientID:        "apexload-vu",
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

func TestBuildProtoConfig_WebSocket(t *testing.T) {
	script := &dsl.Script{
		Config: dsl.ScriptConfig{
			BaseURL: "wss://api.example.test",
			WebSocket: dsl.WebSocketConfig{
				ReadTimeoutMs:  5000,
				WriteTimeoutMs: 2500,
				MaxMessageSize: 65536,
				Subprotocol:    "graphql-ws",
				Origin:         "https://app.example.test",
				Compression:    true,
				Reconnect:      true,
			},
		},
	}

	cfg := buildProtoConfig(script)

	if cfg["websocket_read_timeout_ms"] != float64(5000) {
		t.Errorf("websocket_read_timeout_ms = %v, want 5000", cfg["websocket_read_timeout_ms"])
	}
	if cfg["websocket_write_timeout_ms"] != float64(2500) {
		t.Errorf("websocket_write_timeout_ms = %v, want 2500", cfg["websocket_write_timeout_ms"])
	}
	if cfg["websocket_max_message_size"] != float64(65536) {
		t.Errorf("websocket_max_message_size = %v, want 65536", cfg["websocket_max_message_size"])
	}
	if cfg["websocket_subprotocol"] != "graphql-ws" {
		t.Errorf("websocket_subprotocol = %v, want graphql-ws", cfg["websocket_subprotocol"])
	}
	if cfg["websocket_origin"] != "https://app.example.test" {
		t.Errorf("websocket_origin = %v, want https://app.example.test", cfg["websocket_origin"])
	}
	if cfg["websocket_compression"] != true {
		t.Errorf("websocket_compression = %v, want true", cfg["websocket_compression"])
	}
	if cfg["websocket_reconnect"] != true {
		t.Errorf("websocket_reconnect = %v, want true", cfg["websocket_reconnect"])
	}
}

func TestBuildProtoConfig_GRPC(t *testing.T) {
	script := &dsl.Script{
		Config: dsl.ScriptConfig{
			BaseURL: "grpc.example.test:443",
			GRPC: dsl.GRPCConfig{
				ProtoFiles:     []string{"order.proto"},
				UseReflection:  true,
				PlainText:      true,
				MaxRecvMsgSize: 1048576,
				TimeoutMs:      5000,
				Metadata:       map[string]string{"x-env": "perf"},
				Authority:      "orders.example.test",
			},
		},
	}

	cfg := buildProtoConfig(script)

	if cfg["grpc_use_reflection"] != true {
		t.Errorf("grpc_use_reflection = %v, want true", cfg["grpc_use_reflection"])
	}
	if cfg["grpc_plain_text"] != true {
		t.Errorf("grpc_plain_text = %v, want true", cfg["grpc_plain_text"])
	}
	if cfg["grpc_max_recv_msg_size"] != float64(1048576) {
		t.Errorf("grpc_max_recv_msg_size = %v, want 1048576", cfg["grpc_max_recv_msg_size"])
	}
	if cfg["grpc_timeout_ms"] != float64(5000) {
		t.Errorf("grpc_timeout_ms = %v, want 5000", cfg["grpc_timeout_ms"])
	}
	protoFiles, ok := cfg["grpc_proto_files"].([]string)
	if !ok || len(protoFiles) != 1 || protoFiles[0] != "order.proto" {
		t.Errorf("grpc_proto_files = %v, want order.proto", cfg["grpc_proto_files"])
	}
	metadata, ok := cfg["grpc_metadata"].(map[string]string)
	if !ok || metadata["x-env"] != "perf" {
		t.Errorf("grpc_metadata = %v, want x-env=perf", cfg["grpc_metadata"])
	}
	if cfg["grpc_authority"] != "orders.example.test" {
		t.Errorf("grpc_authority = %v, want orders.example.test", cfg["grpc_authority"])
	}
}
