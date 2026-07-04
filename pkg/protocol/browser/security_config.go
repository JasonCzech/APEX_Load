package browser

import (
	"fmt"
	"net"
	"net/url"
	"strings"

	playwright "github.com/playwright-community/playwright-go"
)

func browserHTTPCredentialsFromConfig(config map[string]any) *playwright.HttpCredentials {
	username := configString(config, "browser_http_credentials_username", "browser_http_username")
	password := configString(config, "browser_http_credentials_password", "browser_http_password")
	origin := configString(config, "browser_http_credentials_origin", "browser_http_origin")
	send := strings.ToLower(configString(config, "browser_http_credentials_send", "browser_http_send"))
	if username == "" && password == "" && origin == "" && send == "" {
		return nil
	}
	credentials := &playwright.HttpCredentials{Username: username, Password: password}
	if origin != "" {
		credentials.Origin = playwright.String(origin)
	}
	if send != "" {
		sendValue := playwright.HttpCredentialsSend(send)
		credentials.Send = &sendValue
	}
	return credentials
}

func browserProxyFromConfig(config map[string]any) *playwright.Proxy {
	server := configString(config, "browser_proxy_server")
	bypass := configStringOrList(config, "browser_proxy_bypass")
	username := configString(config, "browser_proxy_username")
	password := configString(config, "browser_proxy_password")
	if server == "" && bypass == "" && username == "" && password == "" {
		return nil
	}
	proxy := &playwright.Proxy{Server: server}
	if bypass != "" {
		proxy.Bypass = playwright.String(bypass)
	}
	if username != "" {
		proxy.Username = playwright.String(username)
	}
	if password != "" {
		proxy.Password = playwright.String(password)
	}
	return proxy
}

func browserClientCertificatesFromConfig(config map[string]any) []playwright.ClientCertificate {
	if config == nil {
		return nil
	}
	raw, ok := config["browser_client_certificates"]
	if !ok || raw == nil {
		return nil
	}
	switch typed := raw.(type) {
	case []playwright.ClientCertificate:
		return typed
	case []map[string]any:
		certificates := make([]playwright.ClientCertificate, 0, len(typed))
		for _, item := range typed {
			certificates = append(certificates, browserClientCertificateFromMap(item))
		}
		return certificates
	case []any:
		certificates := make([]playwright.ClientCertificate, 0, len(typed))
		for _, item := range typed {
			if mapped, ok := item.(map[string]any); ok {
				certificates = append(certificates, browserClientCertificateFromMap(mapped))
			}
		}
		return certificates
	default:
		return nil
	}
}

func browserClientCertificateFromMap(item map[string]any) playwright.ClientCertificate {
	certificate := playwright.ClientCertificate{
		Origin: mapString(item, "origin"),
	}
	if certPath := mapString(item, "cert_path", "certPath"); certPath != "" {
		certificate.CertPath = playwright.String(certPath)
	}
	if keyPath := mapString(item, "key_path", "keyPath"); keyPath != "" {
		certificate.KeyPath = playwright.String(keyPath)
	}
	if pfxPath := mapString(item, "pfx_path", "pfxPath"); pfxPath != "" {
		certificate.PfxPath = playwright.String(pfxPath)
	}
	if passphrase := mapString(item, "passphrase"); passphrase != "" {
		certificate.Passphrase = playwright.String(passphrase)
	}
	return certificate
}

func validateBrowserHTTPCredentials(credentials *playwright.HttpCredentials) error {
	if credentials == nil {
		return nil
	}
	if strings.TrimSpace(credentials.Username) == "" {
		return fmt.Errorf("browser HTTP credentials require username")
	}
	if strings.TrimSpace(credentials.Password) == "" {
		return fmt.Errorf("browser HTTP credentials require password")
	}
	if credentials.Origin != nil && !validBrowserOrigin(*credentials.Origin, false) {
		return fmt.Errorf("browser HTTP credentials origin must be an HTTP(S) origin")
	}
	if credentials.Send != nil {
		switch strings.ToLower(strings.TrimSpace(string(*credentials.Send))) {
		case "unauthorized", "always":
		default:
			return fmt.Errorf("browser HTTP credentials send must be unauthorized or always")
		}
	}
	return nil
}

func validateBrowserProxy(proxy *playwright.Proxy) error {
	if proxy == nil {
		return nil
	}
	server := strings.TrimSpace(proxy.Server)
	if server == "" {
		return fmt.Errorf("browser proxy requires server")
	}
	if !validProxyServer(server) {
		return fmt.Errorf("browser proxy server must be an HTTP, HTTPS, SOCKS, or host:port proxy")
	}
	if proxy.Password != nil && strings.TrimSpace(*proxy.Password) != "" && (proxy.Username == nil || strings.TrimSpace(*proxy.Username) == "") {
		return fmt.Errorf("browser proxy password requires username")
	}
	return nil
}

func validateBrowserClientCertificates(certificates []playwright.ClientCertificate) error {
	for i, certificate := range certificates {
		label := fmt.Sprintf("browser client certificate %d", i)
		if !validBrowserOrigin(certificate.Origin, true) {
			return fmt.Errorf("%s origin must be an HTTPS origin", label)
		}
		hasPEM := certificate.CertPath != nil || len(certificate.Cert) > 0 || certificate.KeyPath != nil || len(certificate.Key) > 0
		hasPFX := certificate.PfxPath != nil || len(certificate.Pfx) > 0
		if hasPEM && hasPFX {
			return fmt.Errorf("%s must use either cert/key or pfx, not both", label)
		}
		if hasPFX {
			continue
		}
		hasCert := certificate.CertPath != nil || len(certificate.Cert) > 0
		hasKey := certificate.KeyPath != nil || len(certificate.Key) > 0
		if !hasCert || !hasKey {
			return fmt.Errorf("%s requires cert_path and key_path, or pfx_path", label)
		}
	}
	return nil
}

func validBrowserOrigin(value string, requireHTTPS bool) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return false
	}
	if requireHTTPS {
		if parsed.Scheme != "https" {
			return false
		}
	} else if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}
	return parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == "" && (parsed.Path == "" || parsed.Path == "/")
}

func validProxyServer(server string) bool {
	trimmed := strings.TrimSpace(server)
	if trimmed == "" {
		return false
	}
	if strings.Contains(trimmed, "://") {
		parsed, err := url.Parse(trimmed)
		if err != nil || parsed.Host == "" {
			return false
		}
		switch strings.ToLower(parsed.Scheme) {
		case "http", "https", "socks4", "socks5":
			return true
		default:
			return false
		}
	}
	host, port, err := net.SplitHostPort(trimmed)
	return err == nil && strings.TrimSpace(host) != "" && strings.TrimSpace(port) != ""
}

func configString(config map[string]any, keys ...string) string {
	if config == nil {
		return ""
	}
	for _, key := range keys {
		value, ok := config[key]
		if !ok {
			continue
		}
		if typed, ok := value.(string); ok && strings.TrimSpace(typed) != "" {
			return strings.TrimSpace(typed)
		}
	}
	return ""
}

func configStringOrList(config map[string]any, key string) string {
	if config == nil {
		return ""
	}
	switch typed := config[key].(type) {
	case string:
		return strings.TrimSpace(typed)
	case []string:
		return strings.Join(trimStringSlice(typed), ",")
	case []any:
		values := make([]string, 0, len(typed))
		for _, item := range typed {
			if value, ok := item.(string); ok && strings.TrimSpace(value) != "" {
				values = append(values, strings.TrimSpace(value))
			}
		}
		return strings.Join(values, ",")
	default:
		return ""
	}
}

func mapString(item map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := item[key]
		if !ok {
			continue
		}
		if typed, ok := value.(string); ok && strings.TrimSpace(typed) != "" {
			return strings.TrimSpace(typed)
		}
	}
	return ""
}

func trimStringSlice(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
