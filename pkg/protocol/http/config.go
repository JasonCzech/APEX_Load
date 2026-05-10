package http

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
	"time"
)

type transportConfig struct {
	maxConnsPerHost  int
	maxIdleConns     int
	connectTimeout   time.Duration
	tlsTimeout       time.Duration
	idleTimeout      time.Duration
	requestTimeout   time.Duration
	keepAlive        time.Duration
	disableKeepAlive bool
}

func parseTransportConfig(config map[string]any) transportConfig {
	settings := transportConfig{
		maxConnsPerHost: 100,
		connectTimeout:  30 * time.Second,
		tlsTimeout:      10 * time.Second,
		idleTimeout:     90 * time.Second,
		requestTimeout:  60 * time.Second,
		keepAlive:       30 * time.Second,
	}
	settings.maxIdleConns = settings.maxConnsPerHost

	if value, ok := config["max_conns_per_host"].(float64); ok {
		settings.maxConnsPerHost = int(value)
		settings.maxIdleConns = int(value)
	}
	if value, ok := config["max_idle_conns"].(float64); ok {
		settings.maxIdleConns = int(value)
	}
	if value, ok := config["connect_timeout_ms"].(float64); ok {
		settings.connectTimeout = time.Duration(value) * time.Millisecond
	}
	if value, ok := config["tls_timeout_ms"].(float64); ok {
		settings.tlsTimeout = time.Duration(value) * time.Millisecond
	}
	if value, ok := config["idle_timeout_ms"].(float64); ok {
		settings.idleTimeout = time.Duration(value) * time.Millisecond
	}
	if value, ok := config["request_timeout_ms"].(float64); ok {
		settings.requestTimeout = time.Duration(value) * time.Millisecond
	}
	if value, ok := config["keep_alive"].(bool); ok && !value {
		settings.disableKeepAlive = true
		settings.keepAlive = 0
	}

	return settings
}

func buildTLSConfig(config map[string]any) (*tls.Config, error) {
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}

	if value, ok := config["tls_min_version"].(string); ok {
		if version, err := parseTLSVersion(value); err == nil {
			tlsCfg.MinVersion = version
		}
	}
	if value, ok := config["tls_max_version"].(string); ok {
		if version, err := parseTLSVersion(value); err == nil {
			tlsCfg.MaxVersion = version
		}
	}
	if value, ok := config["tls_insecure_skip_verify"].(bool); ok {
		tlsCfg.InsecureSkipVerify = value
	}

	certFile, _ := config["tls_client_cert_file"].(string)
	keyFile, _ := config["tls_client_key_file"].(string)
	if certFile != "" && keyFile != "" {
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return nil, fmt.Errorf("load client certificate: %w", err)
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}

	if caFile, ok := config["tls_ca_cert_file"].(string); ok && caFile != "" {
		caCert, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("read CA certificate: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse CA certificate from %s", caFile)
		}
		tlsCfg.RootCAs = pool
	}

	return tlsCfg, nil
}

func newTransport(settings transportConfig, tlsCfg *tls.Config) *http.Transport {
	return &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   settings.connectTimeout,
			KeepAlive: settings.keepAlive,
		}).DialContext,
		MaxIdleConns:        settings.maxIdleConns,
		MaxIdleConnsPerHost: settings.maxConnsPerHost,
		MaxConnsPerHost:     settings.maxConnsPerHost,
		IdleConnTimeout:     settings.idleTimeout,
		TLSHandshakeTimeout: settings.tlsTimeout,
		TLSClientConfig:     tlsCfg,
		DisableKeepAlives:   settings.disableKeepAlive,
	}
}

func applyHTTPVersion(transport *http.Transport, config map[string]any) {
	if value, ok := config["http_version"].(string); ok {
		switch value {
		case "2":
			transport.ForceAttemptHTTP2 = true
		case "1.1", "1.0":
			transport.ForceAttemptHTTP2 = false
			transport.TLSClientConfig.NextProtos = []string{"http/1.1"}
		default:
			transport.ForceAttemptHTTP2 = true
		}
		return
	}

	transport.ForceAttemptHTTP2 = true
}

func applyProxy(transport *http.Transport, config map[string]any) error {
	proxyURL, ok := config["proxy_url"].(string)
	if !ok || proxyURL == "" {
		return nil
	}

	parsedProxy, err := url.Parse(proxyURL)
	if err != nil {
		return fmt.Errorf("parse proxy URL: %w", err)
	}

	proxyUser, _ := config["proxy_username"].(string)
	proxyPass, _ := config["proxy_password"].(string)
	if proxyUser != "" {
		parsedProxy.User = url.UserPassword(proxyUser, proxyPass)
	}

	transport.Proxy = http.ProxyURL(parsedProxy)
	return nil
}

func buildCookieJar(config map[string]any) http.CookieJar {
	cookiesEnabled := true
	if value, ok := config["cookies_enabled"].(bool); ok {
		cookiesEnabled = value
	}
	if !cookiesEnabled {
		return nil
	}

	jar, _ := cookiejar.New(nil)
	return jar
}

func parseMaxRedirects(config map[string]any) int {
	maxRedirects := 10
	if value, ok := config["max_redirects"].(float64); ok {
		maxRedirects = int(value)
	}
	return maxRedirects
}

func applyRedirectPolicy(client *http.Client, maxRedirects int) {
	if maxRedirects == -1 {
		client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		}
		return
	}

	if maxRedirects != 10 {
		client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("stopped after %d redirects", maxRedirects)
			}
			return nil
		}
	}
}

func buildDefaultHeaders(config map[string]any) map[string]string {
	headers := make(map[string]string)
	if value, ok := config["user_agent"].(string); ok && value != "" {
		headers["User-Agent"] = value
	}
	if value, ok := config["accept_encoding"].(string); ok && value != "" {
		headers["Accept-Encoding"] = value
	}
	if value, ok := config["accept_language"].(string); ok && value != "" {
		headers["Accept-Language"] = value
	}
	if values, ok := config["default_headers"].(map[string]string); ok {
		for key, value := range values {
			headers[key] = value
		}
	}
	return headers
}

func buildAuthHeader(config map[string]any) (string, string) {
	authType, _ := config["auth_type"].(string)
	switch strings.ToLower(authType) {
	case "basic":
		user, _ := config["auth_username"].(string)
		pass, _ := config["auth_password"].(string)
		return authType, "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
	case "bearer":
		token, _ := config["auth_token"].(string)
		return authType, "Bearer " + token
	case "ntlm":
		fmt.Println("[WARN] NTLM authentication is not yet fully supported; using basic auth fallback")
		user, _ := config["auth_username"].(string)
		pass, _ := config["auth_password"].(string)
		return authType, "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
	default:
		return authType, ""
	}
}
