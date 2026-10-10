package engine

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestBuildProtoConfigCarriesControllerRuntimeTargetPolicy(t *testing.T) {
	script := &dsl.Script{Metadata: &dsl.ScriptMetadata{RuntimePolicy: &dsl.ScriptRuntimePolicy{AllowedTargetHosts: []string{"api.example.test", "*.assets.example.test"}}}}
	cfg := buildProtoConfig(script)
	hosts, ok := cfg[protocol.TargetPolicyConfigKey].([]string)
	if !ok || len(hosts) != 2 || hosts[0] != "api.example.test" {
		t.Fatalf("runtime target policy = %#v", cfg[protocol.TargetPolicyConfigKey])
	}
}

func TestBuildProtoConfigForRunMode(t *testing.T) {
	cfg := buildProtoConfigForRunMode(&dsl.Script{}, "load")
	if cfg["apexload_run_mode"] != "load" {
		t.Fatalf("apexload_run_mode = %v, want load", cfg["apexload_run_mode"])
	}

	defaultCfg := buildProtoConfigForRunMode(&dsl.Script{}, " ")
	if _, ok := defaultCfg["apexload_run_mode"]; ok {
		t.Fatalf("blank run mode should not be emitted: %#v", defaultCfg)
	}
}

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

func TestBuildProtoConfig_EventHubs(t *testing.T) {
	script := &dsl.Script{
		Config: dsl.ScriptConfig{
			EventHubs: dsl.EventHubsConfig{
				Namespace:     "ns.servicebus.windows.net",
				EventHub:      "telemetry",
				ConsumerGroup: "probe",
				PartitionKey:  "device-1",
				AuthType:      "aad",
				TimeoutMs:     1500,
				Mock:          true,
			},
		},
	}

	cfg := buildProtoConfig(script)

	if cfg["eventhubs_namespace"] != "ns.servicebus.windows.net" {
		t.Fatalf("eventhubs_namespace = %v", cfg["eventhubs_namespace"])
	}
	if cfg["eventhubs_event_hub"] != "telemetry" {
		t.Fatalf("eventhubs_event_hub = %v", cfg["eventhubs_event_hub"])
	}
	if cfg["eventhubs_consumer_group"] != "probe" {
		t.Fatalf("eventhubs_consumer_group = %v", cfg["eventhubs_consumer_group"])
	}
	if cfg["eventhubs_partition_key"] != "device-1" {
		t.Fatalf("eventhubs_partition_key = %v", cfg["eventhubs_partition_key"])
	}
	if cfg["eventhubs_auth_type"] != "aad" {
		t.Fatalf("eventhubs_auth_type = %v", cfg["eventhubs_auth_type"])
	}
	if cfg["eventhubs_timeout_ms"] != float64(1500) {
		t.Fatalf("eventhubs_timeout_ms = %v, want 1500", cfg["eventhubs_timeout_ms"])
	}
	if cfg["eventhubs_mock"] != true {
		t.Fatalf("eventhubs_mock = %v, want true", cfg["eventhubs_mock"])
	}
}

func TestBuildProtoConfig_EventHubsConnectionStringSecret(t *testing.T) {
	resolver := SecretResolverFunc(func(name string) (string, bool) {
		if name == "eh_conn" {
			return "Endpoint=sb://ns.servicebus.windows.net/;SharedAccessKey=runtime", true
		}
		return "", false
	})

	script := &dsl.Script{
		Config: dsl.ScriptConfig{
			EventHubs: dsl.EventHubsConfig{
				EventHub:               "telemetry",
				ConnectionStringSecret: "{{secret.eh_conn}}",
				Mock:                   true,
			},
		},
	}

	cfg := buildProtoConfigWithSecretResolver(script, resolver)
	got, _ := cfg["eventhubs_connection_string_secret"].(string)
	if got != "Endpoint=sb://ns.servicebus.windows.net/;SharedAccessKey=runtime" {
		t.Fatalf("eventhubs_connection_string_secret = %q, want resolved connection string", got)
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

func TestBuildProtoConfig_ResolvesNamedDedicatedSecrets(t *testing.T) {
	resolver := SecretResolverFunc(func(name string) (string, bool) {
		values := map[string]string{
			"DATABASE_DSN":   "postgres://user:pass@db.example.test/app",
			"KAFKA_PASSWORD": "runtime-kafka-pass",
			"EVENTHUBS_CONN": "Endpoint=sb://ns.servicebus.windows.net/;SharedAccessKey=runtime",
			"JMS_CA":         "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----",
		}
		value, ok := values[name]
		return value, ok
	})
	script := &dsl.Script{Config: dsl.ScriptConfig{
		Database:  dsl.DatabaseConfig{DSNSecret: "DATABASE_DSN"},
		Kafka:     dsl.KafkaConfig{SASLPasswordSecret: "KAFKA_PASSWORD"},
		EventHubs: dsl.EventHubsConfig{ConnectionStringSecret: "EVENTHUBS_CONN"},
		JMS: dsl.JMSConfig{TLS: &dsl.JMSTLSConfig{
			CACertSecret: "JMS_CA",
		}},
	}}

	cfg := buildProtoConfigWithSecretResolver(script, resolver)
	for key, want := range map[string]string{
		"database_dsn_secret":                "postgres://user:pass@db.example.test/app",
		"kafka_sasl_password_secret":         "runtime-kafka-pass",
		"eventhubs_connection_string_secret": "Endpoint=sb://ns.servicebus.windows.net/;SharedAccessKey=runtime",
		"jms_tls_ca_cert_secret":             "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----",
	} {
		if got := cfg[key]; got != want {
			t.Errorf("%s = %q, want resolved governed value", key, got)
		}
	}
}

func TestRuntimeProtocolCompositeValuesIncludeResolvedImportedDSN(t *testing.T) {
	const (
		literalPassword = "literal-dsn-password-9c42"
		runtimePassword = "runtime-dsn-password-7e15"
	)
	unresolved := "postgres://db-user:" + literalPassword +
		"@db.example.test/app?sslpassword={{secret.DB_CERT_PASSWORD}}"
	resolved := "postgres://db-user:" + literalPassword +
		"@db.example.test/app?sslpassword=" + runtimePassword
	script := &dsl.Script{
		Protocol: "database",
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "enterprise_script_archive",
			Preserved: map[string]any{
				dsl.ImportPreservedCredentialPolicyKey: dsl.ImportPreservedCredentialPolicyPreserve,
			},
		}},
		Config: dsl.ScriptConfig{
			Database: dsl.DatabaseConfig{DSN: unresolved},
		},
	}
	resolver := SecretResolverFunc(func(name string) (string, bool) {
		if name == "DB_CERT_PASSWORD" {
			return runtimePassword, true
		}
		return "", false
	})

	values := runtimeProtocolAuthCompositeValues(script, resolver)
	for _, want := range []string{resolved, literalPassword, runtimePassword} {
		if !containsExactString(values, want) {
			t.Fatalf("runtime protocol credential values = %#v, want %q", values, want)
		}
	}
	if containsExactString(values, unresolved) ||
		containsExactString(values, "{{secret.DB_CERT_PASSWORD}}") {
		t.Fatalf("runtime protocol credential values retain unresolved template: %#v", values)
	}
}

// A Basic-auth password written as a literal in the script is script-sourced.
// The operator running the test reads it in the script, so neither it nor any
// composite spelling built from it is registered for redaction.
func TestRuntimeProtocolBasicCompositeValuesSkipScriptLiteralCredentials(t *testing.T) {
	const (
		username = "admin"
		password = "basic-password-4182"
	)
	script := &dsl.Script{Config: dsl.ScriptConfig{HTTP: dsl.HTTPConfig{Auth: dsl.AuthConfig{
		Type: "basic", Username: username, Password: password,
	}}}}
	pair := username + ":" + password
	payload := base64.StdEncoding.EncodeToString([]byte(pair))
	values := runtimeProtocolAuthCompositeValues(script, nil)

	for _, unwanted := range []string{username, password, pair, payload, "Basic " + payload} {
		if containsExactString(values, unwanted) {
			t.Fatalf("runtime protocol credential values = %#v, registered script literal %q", values, unwanted)
		}
	}
}

// The other half of the same line: when the password defers to the project
// secret store the operator does NOT have it, so the resolved value and every
// composite spelling built from it stay protected. The bare username remains a
// non-secret.
func TestRuntimeProtocolBasicCompositeValuesProtectSecretStorePassword(t *testing.T) {
	const (
		username = "admin"
		password = "platform-basic-password-5507"
	)
	script := &dsl.Script{Config: dsl.ScriptConfig{HTTP: dsl.HTTPConfig{Auth: dsl.AuthConfig{
		Type: "basic", Username: username, Password: "{{secret.HTTP_BASIC_PASSWORD}}",
	}}}}
	resolver := SecretResolverFunc(func(name string) (string, bool) {
		if name == "HTTP_BASIC_PASSWORD" {
			return password, true
		}
		return "", false
	})
	pair := username + ":" + password
	payload := base64.StdEncoding.EncodeToString([]byte(pair))
	values := runtimeProtocolAuthCompositeValues(script, resolver)

	if containsExactString(values, username) {
		t.Fatalf("runtime protocol credential values = %#v, included bare username", values)
	}
	for _, expected := range []string{password, pair, payload, "Basic " + payload} {
		if !containsExactString(values, expected) {
			t.Fatalf("runtime protocol credential values = %#v, want %q", values, expected)
		}
	}
}

// The whitespace-tolerant spelling resolves through the secret resolver exactly
// like the compact one, so it must be classified as platform-managed too.
// Getting this wrong would silently declassify a real project secret.
func TestRuntimeProtocolBasicCompositeValuesAcceptSpacedSecretReference(t *testing.T) {
	const password = "spaced-platform-password-6714"
	script := &dsl.Script{Config: dsl.ScriptConfig{HTTP: dsl.HTTPConfig{Auth: dsl.AuthConfig{
		Type: "basic", Username: "admin", Password: "{{ secret.HTTP_BASIC_PASSWORD }}",
	}}}}
	resolver := SecretResolverFunc(func(name string) (string, bool) {
		if name == "HTTP_BASIC_PASSWORD" {
			return password, true
		}
		return "", false
	})

	values := runtimeProtocolAuthCompositeValues(script, resolver)
	if !containsExactString(values, password) {
		t.Fatalf("runtime protocol credential values = %#v, want spaced-reference secret %q", values, password)
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
		case "browser_password":
			return "browser-pass", true
		case "browser_proxy_password":
			return "browser-proxy-pass", true
		case "browser_cert_passphrase":
			return "browser-cert-pass", true
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
			Browser: dsl.BrowserConfig{
				HTTPCredentials: dsl.BrowserHTTPCredentials{
					Username: "browser-user",
					Password: "{{secret.browser_password}}",
					Origin:   "https://secure.example",
					Send:     "always",
				},
				Proxy: dsl.BrowserProxyConfig{
					Server:   "proxy.example:3128",
					Username: "proxy-user",
					Password: "{{secret.browser_proxy_password}}",
				},
				ClientCertificates: []dsl.BrowserClientCertificate{{
					Origin:     "https://mtls.example",
					PfxPath:    "/certs/client.p12",
					Passphrase: "{{secret.browser_cert_passphrase}}",
				}},
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
	if cfg["browser_http_credentials_password"] != "browser-pass" {
		t.Fatalf("browser_http_credentials_password = %v, want browser-pass", cfg["browser_http_credentials_password"])
	}
	if cfg["browser_proxy_password"] != "browser-proxy-pass" {
		t.Fatalf("browser_proxy_password = %v, want browser-proxy-pass", cfg["browser_proxy_password"])
	}
	certificates, ok := cfg["browser_client_certificates"].([]map[string]any)
	if !ok || len(certificates) != 1 {
		t.Fatalf("browser_client_certificates = %#v, want one certificate", cfg["browser_client_certificates"])
	}
	if certificates[0]["passphrase"] != "browser-cert-pass" {
		t.Fatalf("browser client cert passphrase = %#v, want resolved secret", certificates[0]["passphrase"])
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
		tokens:     staticAzureTokenSource("test-token"),
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
	cacheEnabled := true
	traceparentEnabled := true
	traceparentSampled := false

	script := &dsl.Script{
		Config: dsl.ScriptConfig{
			HTTP: dsl.HTTPConfig{
				MaxConnsPerHost:    50,
				MaxIdleConns:       25,
				KeepAlive:          &keepAlive,
				Version:            "2",
				MaxRedirects:       &maxRedirects,
				CookiesEnabled:     &cookiesEnabled,
				CacheEnabled:       &cacheEnabled,
				Traceparent:        &traceparentEnabled,
				TraceparentSampled: &traceparentSampled,
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
	if cfg["http_cache_enabled"] != true {
		t.Errorf("http_cache_enabled = %v", cfg["http_cache_enabled"])
	}
	if cfg["traceparent_enabled"] != true {
		t.Errorf("traceparent_enabled = %v", cfg["traceparent_enabled"])
	}
	if cfg["traceparent_sampled"] != false {
		t.Errorf("traceparent_sampled = %v", cfg["traceparent_sampled"])
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

func TestBuildProtoConfig_JavaVuser(t *testing.T) {
	useXBoot := false
	dedicatedLoader := true
	skipParamEval := true
	script := &dsl.Script{
		Config: dsl.ScriptConfig{
			JavaVuser: dsl.JavaVuserConfig{
				Enabled:                 true,
				RunnerCommand:           "/opt/apexload/bin/java-vuser-runner",
				HarnessJar:              "/opt/apexload/java/apexload-java-vuser-runner.jar",
				Classpath:               []string{"/scripts", "/deps/app.jar"},
				WorkDir:                 "/work/script",
				MaxProcesses:            25,
				StartupTimeoutMs:        3000,
				ActionTimeoutMs:         45000,
				JDKSelection:            "specified",
				JDKHome:                 "/opt/jdks/temurin-21",
				AdditionalVMParameters:  []string{"-Xmx512m", "-Dapp.label=hello world"},
				UseXBootClasspath:       &useXBoot,
				DedicatedClassLoader:    &dedicatedLoader,
				SkipParameterEvaluation: &skipParamEval,
			},
		},
	}

	cfg := buildProtoConfig(script)

	// Enablement and the runner command/harness/working-directory are
	// operator-owned (resolved from APEXLOAD_JAVA_VUSER_* by the protocol).
	// They must NOT be flattened from the uploaded script, otherwise a script
	// author could enable and steer local process execution on the generator.
	for _, key := range []string{
		"java_vuser_enabled",
		"java_vuser_runner_command",
		"java_vuser_harness_jar",
		"java_vuser_work_dir",
		"java_vuser_max_processes",
	} {
		if _, present := cfg[key]; present {
			t.Errorf("%s was flattened from script config (%v); it must be operator-owned and omitted", key, cfg[key])
		}
	}
	if cfg["java_vuser_jdk_selection"] != "specified" || cfg["java_vuser_jdk_home"] != "/opt/jdks/temurin-21" {
		t.Fatalf("java_vuser JDK config = %#v", cfg)
	}
	if got, ok := cfg["java_vuser_additional_vm_parameters"].([]string); !ok || len(got) != 2 || got[1] != "-Dapp.label=hello world" {
		t.Fatalf("java_vuser_additional_vm_parameters = %#v, want exact argv tokens", cfg["java_vuser_additional_vm_parameters"])
	}
	if got, ok := cfg["java_vuser_classpath"].([]string); !ok || len(got) != 2 || got[0] != "/scripts" {
		t.Fatalf("java_vuser_classpath = %#v, want script classpath", cfg["java_vuser_classpath"])
	}
	if cfg["java_vuser_use_xbootclasspath"] != false {
		t.Errorf("java_vuser_use_xbootclasspath = %#v, want explicit false", cfg["java_vuser_use_xbootclasspath"])
	}
	if cfg["java_vuser_dedicated_class_loader"] != true {
		t.Errorf("java_vuser_dedicated_class_loader = %#v, want true", cfg["java_vuser_dedicated_class_loader"])
	}
	if cfg["java_vuser_skip_parameter_evaluation"] != true {
		t.Errorf("java_vuser_skip_parameter_evaluation = %#v, want true", cfg["java_vuser_skip_parameter_evaluation"])
	}
	// Non-privileged per-action timeouts remain script-controlled.
	if cfg["java_vuser_action_timeout_ms"] != float64(45000) {
		t.Errorf("java_vuser_action_timeout_ms = %v, want 45000", cfg["java_vuser_action_timeout_ms"])
	}
	if cfg["java_vuser_startup_timeout_ms"] != float64(3000) {
		t.Errorf("java_vuser_startup_timeout_ms = %v, want 3000", cfg["java_vuser_startup_timeout_ms"])
	}
}

func TestBuildProtoConfigJavaVuserNeverResolvesSecretsIntoVMArguments(t *testing.T) {
	const reference = "-Dservice.password={{secret.java_service_password}}"
	resolutionCalls := 0
	resolver := SecretResolverFunc(func(name string) (string, bool) {
		resolutionCalls++
		if name != "java_service_password" {
			t.Fatalf("unexpected secret name %q", name)
		}
		return "must-not-appear-in-process-argv", true
	})
	script := &dsl.Script{Config: dsl.ScriptConfig{JavaVuser: dsl.JavaVuserConfig{
		AdditionalVMParameters: []string{reference},
	}}}

	cfg := buildProtoConfigWithSecretResolver(script, resolver)

	if resolutionCalls != 0 {
		t.Fatalf("Java VM argument secret resolver calls = %d, want 0", resolutionCalls)
	}
	got, ok := cfg["java_vuser_additional_vm_parameters"].([]string)
	if !ok || len(got) != 1 || got[0] != reference {
		t.Fatalf("Java VM arguments = %#v, want unresolved reference", cfg["java_vuser_additional_vm_parameters"])
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
	geoLat := 47.6062
	geoLon := -122.3321
	geoAccuracy := 15.0
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
				Locale:             "en-GB",
				Timezone:           "Europe/London",
				Geolocation: dsl.BrowserGeolocation{
					Latitude:  &geoLat,
					Longitude: &geoLon,
					Accuracy:  &geoAccuracy,
				},
				DeviceProfile:      "iphone_se",
				TouchMode:          &touchMode,
				Orientation:        "portrait",
				ClientMetrics:      &clientMetrics,
				ExperienceCanary:   true,
				HARCapturePolicy:   "on_sla_breach",
				HARSLAThresholdMs:  1800,
				HARArtifactDir:     "/var/lib/apexload/har",
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
	if cfg["browser_locale"] != "en-GB" {
		t.Errorf("browser_locale = %v, want en-GB", cfg["browser_locale"])
	}
	if cfg["browser_timezone"] != "Europe/London" {
		t.Errorf("browser_timezone = %v, want Europe/London", cfg["browser_timezone"])
	}
	if cfg["browser_geolocation_latitude"] != geoLat || cfg["browser_geolocation_longitude"] != geoLon || cfg["browser_geolocation_accuracy"] != geoAccuracy {
		t.Errorf("browser geolocation config = lat:%v lon:%v acc:%v", cfg["browser_geolocation_latitude"], cfg["browser_geolocation_longitude"], cfg["browser_geolocation_accuracy"])
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
	if cfg["browser_experience_canary"] != true {
		t.Errorf("browser_experience_canary = %v, want true", cfg["browser_experience_canary"])
	}
	if cfg["browser_har_capture_policy"] != "on_sla_breach" {
		t.Errorf("browser_har_capture_policy = %v, want on_sla_breach", cfg["browser_har_capture_policy"])
	}
	if cfg["browser_har_sla_breach_ms"] != float64(1800) {
		t.Errorf("browser_har_sla_breach_ms = %v, want 1800", cfg["browser_har_sla_breach_ms"])
	}
	if cfg["browser_har_dir"] != "/var/lib/apexload/har" {
		t.Errorf("browser_har_dir = %v, want configured retention directory", cfg["browser_har_dir"])
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
				Subscription:    "orders-sub",
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
	if cfg["jms_subscription"] != "orders-sub" {
		t.Errorf("jms_subscription = %v, want orders-sub", cfg["jms_subscription"])
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

func TestBuildGroupProtoConfigUsesManagedScenarioIdentity(t *testing.T) {
	script := &dsl.Script{ID: "script-artifact", Name: "combined-java", Protocol: "java_vuser"}
	cfg := buildGroupProtoConfig(GroupConfig{
		Script:     script,
		ScenarioID: "scenario-run",
	}, "load")
	if got := cfg["scenario_id"]; got != "scenario-run" {
		t.Fatalf("managed scenario_id = %#v, want scenario-run", got)
	}

	fallback := buildGroupProtoConfig(GroupConfig{Script: script}, "load")
	if got := fallback["scenario_id"]; got != "script-artifact" {
		t.Fatalf("direct-run scenario_id = %#v, want script artifact fallback", got)
	}
}

func TestBuildProtoConfig_KafkaConsumerTransactions(t *testing.T) {
	script := &dsl.Script{Protocol: "kafka", Config: dsl.ScriptConfig{Kafka: dsl.KafkaConfig{ConsumerTransactions: true}}}
	cfg := buildProtoConfig(script)
	if cfg["kafka_consumer_transactions"] != true {
		t.Fatal("consumer transaction mode lost during runtime configuration")
	}
}

func TestBuildProtoConfig_EventHubsProcessor(t *testing.T) {
	script := &dsl.Script{Protocol: "eventhubs", Config: dsl.ScriptConfig{EventHubs: dsl.EventHubsConfig{ConsumerMode: "processor", CheckpointContainerURL: "https://account.blob.core.windows.net/checkpoints"}}}
	cfg := buildProtoConfig(script)
	if cfg["eventhubs_consumer_mode"] != "processor" || cfg["eventhubs_checkpoint_container_url"] != script.Config.EventHubs.CheckpointContainerURL {
		t.Fatal("processor settings lost")
	}
}

func TestEventHubsExclusiveOwnerLevelSurvivesRuntimeMapping(t *testing.T) {
	zero := int64(0)
	s := &dsl.Script{Config: dsl.ScriptConfig{EventHubs: dsl.EventHubsConfig{ConsumerMode: "exclusive", OwnerLevel: &zero}}}
	cfg := map[string]any{}
	appendEventHubsConfig(cfg, s)
	if value, ok := cfg["eventhubs_owner_level"].(int64); !ok || value != 0 {
		t.Fatal(cfg)
	}
	zero = 1
	if cfg["eventhubs_owner_level"] != int64(0) {
		t.Fatal("runtime map retained mutable epoch pointer")
	}
}

func TestEventHubsPartitionTailRuntimeConfig(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		cfg := map[string]any{}
		appendEventHubsConfig(cfg, &dsl.Script{Config: dsl.ScriptConfig{EventHubs: dsl.EventHubsConfig{ConsumerMode: "processor", ObservePartitionTail: enabled, TailPollIntervalMs: 10000}}})
		if value, _ := cfg["eventhubs_observe_partition_tail"].(bool); value != enabled {
			t.Fatalf("tail opt-in=%v: %v", enabled, cfg)
		}
		if cfg["eventhubs_tail_poll_interval_ms"] != 10000 {
			t.Fatal("tail interval lost")
		}
	}
}

func TestBuildProtoConfigBrowserQUIC(t *testing.T) {
	script := &dsl.Script{Name: "quic", Protocol: "browser", Config: dsl.ScriptConfig{Browser: dsl.BrowserConfig{QUIC: " off "}}}
	if cfg := buildProtoConfig(script); cfg["browser_quic"] != "off" {
		t.Fatalf("browser_quic = %v, want off", cfg["browser_quic"])
	}
	script.Config.Browser.QUIC = ""
	if _, present := buildProtoConfig(script)["browser_quic"]; present {
		t.Fatal("an unset QUIC mode must stay unset so the runtime default applies")
	}
}

func TestBuildProtoConfigForwardsQUICSettings(t *testing.T) {
	zero := 0
	script := &dsl.Script{Name: "quic", Protocol: "http", Config: dsl.ScriptConfig{HTTP: dsl.HTTPConfig{
		Version: "auto+h3",
		QUIC:    &dsl.HTTPQUICConfig{HandshakeTimeoutMs: 2000, AltSvcCache: "per_iteration", BrokenTTLMs: &zero},
	}}}
	cfg := buildProtoConfig(script)
	if cfg["http_version"] != "auto+h3" || cfg["http_quic_handshake_timeout_ms"] != float64(2000) || cfg["http_quic_alt_svc_cache"] != "per_iteration" || cfg["http_quic_broken_ttl_ms"] != float64(0) {
		t.Fatalf("cfg = %v", cfg)
	}
	for _, key := range []string{"http_quic_max_idle_timeout_ms", "http_quic_keep_alive_ms"} {
		if _, present := cfg[key]; present {
			t.Fatalf("unset %s was forwarded", key)
		}
	}
}
