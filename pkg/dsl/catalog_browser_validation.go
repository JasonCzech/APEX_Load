package dsl

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/credential"
	browserprotocol "github.com/jasonczech/apexload/pkg/protocol/browser"
)

var browserStorageStateSecretPattern = regexp.MustCompile(`^\{\{secret\.` + credential.LogicalNameInputPattern + `\}\}$`)

func validateBrowserConfigIssues(script *Script) []string {
	if script == nil || strings.TrimSpace(script.Protocol) != "browser" {
		return nil
	}

	browser := script.Config.Browser
	var issues []string
	if browser.StorageState != "" && !browserStorageStateSecretPattern.MatchString(browser.StorageState) {
		issues = append(issues, "config.browser.storage_state must be a whole {{secret.NAME}} reference; inline credentials and file paths are not supported")
	}
	trimmedEngine := strings.TrimSpace(browser.Engine)
	trimmedDeviceProfile := strings.TrimSpace(browser.DeviceProfile)
	if trimmedEngine != "" && !isAllowedValue(trimmedEngine, "chromium", "webkit") {
		issues = append(issues, "config.browser.engine must be chromium or webkit")
	}
	if trimmedDeviceProfile != "" {
		profile, ok := browserprotocol.LookupDevice(trimmedDeviceProfile)
		if !ok {
			issues = append(issues, fmt.Sprintf("config.browser.device_profile %q is not recognized", trimmedDeviceProfile))
		} else if trimmedEngine != "" && profile.BrowserFamily != "" && trimmedEngine != profile.BrowserFamily {
			issues = append(issues, fmt.Sprintf("config.browser.engine %q is incompatible with device_profile %q (requires %s)", trimmedEngine, trimmedDeviceProfile, profile.BrowserFamily))
		}
	}
	if browser.Orientation != "" && !isAllowedValue(browser.Orientation, "portrait", "landscape") {
		issues = append(issues, "config.browser.orientation must be portrait or landscape")
	}
	if browser.Viewport.Width < 0 {
		issues = append(issues, "config.browser.viewport.width must be zero or greater")
	} else if browser.Viewport.Width > browserprotocol.MaxConfiguredViewportDimension {
		issues = append(issues, fmt.Sprintf("config.browser.viewport.width must not exceed %d", browserprotocol.MaxConfiguredViewportDimension))
	}
	if browser.Viewport.Height < 0 {
		issues = append(issues, "config.browser.viewport.height must be zero or greater")
	} else if browser.Viewport.Height > browserprotocol.MaxConfiguredViewportDimension {
		issues = append(issues, fmt.Sprintf("config.browser.viewport.height must not exceed %d", browserprotocol.MaxConfiguredViewportDimension))
	}
	if browser.Viewport.Width > 0 && browser.Viewport.Height > 0 && int64(browser.Viewport.Width)*int64(browser.Viewport.Height) > browserprotocol.MaxConfiguredViewportPixels {
		issues = append(issues, fmt.Sprintf("config.browser.viewport must not exceed %d pixels", browserprotocol.MaxConfiguredViewportPixels))
	}
	maxBrowserDelayMS := int(browserprotocol.MaxConfiguredBrowserDelay / time.Millisecond)
	if browser.SlowMoMs < 0 || browser.SlowMoMs > maxBrowserDelayMS {
		issues = append(issues, fmt.Sprintf("config.browser.slow_mo_ms must be between 0 and %d", maxBrowserDelayMS))
	}
	if browser.HARSLAThresholdMs < 0 || browser.HARSLAThresholdMs > maxBrowserDelayMS {
		issues = append(issues, fmt.Sprintf("config.browser.har_sla_threshold_ms must be between 0 and %d", maxBrowserDelayMS))
	}
	if strings.TrimSpace(browser.Timezone) != "" {
		if _, err := time.LoadLocation(strings.TrimSpace(browser.Timezone)); err != nil {
			issues = append(issues, fmt.Sprintf("config.browser.timezone %q is not a valid IANA timezone", browser.Timezone))
		}
	}
	if browser.Geolocation.Latitude != nil || browser.Geolocation.Longitude != nil || browser.Geolocation.Accuracy != nil {
		issues = append(issues, validateBrowserGeolocationConfigIssues(browser.Geolocation)...)
	}
	issues = append(issues, validateBrowserHTTPCredentialsConfigIssues(browser.HTTPCredentials)...)
	issues = append(issues, validateBrowserProxyConfigIssues(browser.Proxy)...)
	issues = append(issues, validateBrowserClientCertificateConfigIssues(browser.ClientCertificates)...)
	if strings.EqualFold(strings.TrimSpace(browser.DownloadResources), "recorded") {
		issues = append(issues, validateRecordedResourceURLMetadata(script)...)
	}
	return issues
}

func validateRecordedResourceURLMetadata(script *Script) []string {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil || script.Metadata.Import.Preserved == nil {
		return nil
	}
	raw, ok := script.Metadata.Import.Preserved["recorded_resource_urls"]
	if !ok {
		return nil
	}
	var urls []string
	switch typed := raw.(type) {
	case []string:
		urls = typed
	case []any:
		for _, value := range typed {
			if text, ok := value.(string); ok {
				urls = append(urls, text)
			}
		}
	}
	for index, candidate := range urls {
		if strings.Contains(candidate, "[REDACTED]") || strings.Contains(candidate, "{{secret.") {
			return []string{fmt.Sprintf("metadata.import.preserved.recorded_resource_urls[%d] contains an unresolved credential and cannot be executed", index)}
		}
	}
	return nil
}

func validateBrowserGeolocationConfigIssues(geo BrowserGeolocation) []string {
	var issues []string
	if geo.Latitude == nil {
		issues = append(issues, "config.browser.geolocation.latitude is required when geolocation is set")
	} else if *geo.Latitude < -90 || *geo.Latitude > 90 {
		issues = append(issues, "config.browser.geolocation.latitude must be between -90 and 90")
	}
	if geo.Longitude == nil {
		issues = append(issues, "config.browser.geolocation.longitude is required when geolocation is set")
	} else if *geo.Longitude < -180 || *geo.Longitude > 180 {
		issues = append(issues, "config.browser.geolocation.longitude must be between -180 and 180")
	}
	if geo.Accuracy != nil && *geo.Accuracy < 0 {
		issues = append(issues, "config.browser.geolocation.accuracy must be zero or greater")
	}
	return issues
}

func validateBrowserHTTPCredentialsConfigIssues(credentials BrowserHTTPCredentials) []string {
	if strings.TrimSpace(credentials.Username) == "" && strings.TrimSpace(credentials.Password) == "" && strings.TrimSpace(credentials.Origin) == "" && strings.TrimSpace(credentials.Send) == "" {
		return nil
	}
	var issues []string
	if strings.TrimSpace(credentials.Username) == "" {
		issues = append(issues, "config.browser.http_credentials.username is required when http_credentials is set")
	}
	if strings.TrimSpace(credentials.Password) == "" {
		issues = append(issues, "config.browser.http_credentials.password is required when http_credentials is set")
	}
	if strings.TrimSpace(credentials.Origin) != "" && !validBrowserOriginConfig(credentials.Origin, false) {
		issues = append(issues, "config.browser.http_credentials.origin must be an HTTP(S) origin")
	}
	if strings.TrimSpace(credentials.Send) != "" && !isAllowedValue(strings.ToLower(strings.TrimSpace(credentials.Send)), "unauthorized", "always") {
		issues = append(issues, "config.browser.http_credentials.send must be unauthorized or always")
	}
	return issues
}

func validateBrowserProxyConfigIssues(proxy BrowserProxyConfig) []string {
	if strings.TrimSpace(proxy.Server) == "" && strings.TrimSpace(proxy.Bypass) == "" && strings.TrimSpace(proxy.Username) == "" && strings.TrimSpace(proxy.Password) == "" {
		return nil
	}
	var issues []string
	if strings.TrimSpace(proxy.Server) == "" {
		issues = append(issues, "config.browser.proxy.server is required when proxy is set")
	} else if !validBrowserProxyServerConfig(proxy.Server) {
		issues = append(issues, "config.browser.proxy.server must be an HTTP, HTTPS, SOCKS, or host:port proxy")
	}
	if strings.TrimSpace(proxy.Password) != "" && strings.TrimSpace(proxy.Username) == "" {
		issues = append(issues, "config.browser.proxy.password requires username")
	}
	return issues
}

func validateBrowserClientCertificateConfigIssues(certificates []BrowserClientCertificate) []string {
	var issues []string
	for i, certificate := range certificates {
		label := fmt.Sprintf("config.browser.client_certificates[%d]", i)
		if !validBrowserOriginConfig(certificate.Origin, true) {
			issues = append(issues, fmt.Sprintf("%s.origin must be an HTTPS origin", label))
		}
		hasPEM := strings.TrimSpace(certificate.CertPath) != "" || strings.TrimSpace(certificate.KeyPath) != ""
		hasPFX := strings.TrimSpace(certificate.PfxPath) != ""
		if hasPEM && hasPFX {
			issues = append(issues, fmt.Sprintf("%s must use either cert_path/key_path or pfx_path, not both", label))
			continue
		}
		if hasPFX {
			continue
		}
		if strings.TrimSpace(certificate.CertPath) == "" || strings.TrimSpace(certificate.KeyPath) == "" {
			issues = append(issues, fmt.Sprintf("%s requires cert_path and key_path, or pfx_path", label))
		}
	}
	return issues
}

func validBrowserOriginConfig(value string, requireHTTPS bool) bool {
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

func validBrowserProxyServerConfig(server string) bool {
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
